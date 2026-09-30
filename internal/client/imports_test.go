// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package client

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// importResultBody is CreateOrUpdateImportClusterResult as the platform
// renders it (cluster go/internal/importedapi/importparse.go,
// ImportClusterResultPayload).
const importResultBody = `{"name":"dev","cluster_id":"5b0c7a3e-1f7e-4a4e-9a55-1d2f3c4b5a69",` +
	`"organisation_id":"0f6d2a51-7a3c-4b8e-9c1d-2e3f4a5b6c7d","import_command":"helm install ankra-agent ...",` +
	`"errors":[],"commit_sha":"abc123","commit_url":"https://github.com/o/r/commit/abc123",` +
	`"warnings":[{"kind":"addon","name":"podinfo","key":"encrypted_paths","message":"matches nothing","category":"over_match"}]}`

func TestImportClusterWaitsForTheResult(t *testing.T) {
	var query string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.Path != "/api/v1/clusters/import" {
			t.Errorf("unexpected request: %s %s", request.Method, request.URL.Path)
		}
		query = request.URL.RawQuery
		_, _ = writer.Write([]byte(importResultBody))
	}))
	defer server.Close()

	response, err := NewClient(server.URL, "token", "test").ImportCluster(context.Background(), ImportClusterRequest{Name: "dev"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if query != "wait=true" {
		t.Errorf("query = %q, want wait=true: without it the platform answers 202 with no cluster_id", query)
	}
	if response.ClusterID != "5b0c7a3e-1f7e-4a4e-9a55-1d2f3c4b5a69" || response.ImportCommand != "helm install ankra-agent ..." {
		t.Errorf("unexpected response: %+v", response)
	}
	if len(response.Warnings) != 1 || response.Warnings[0].Key != "encrypted_paths" {
		t.Errorf("warnings = %+v", response.Warnings)
	}
	if response.CommitSHA == nil || *response.CommitSHA != "abc123" {
		t.Errorf("commit_sha = %v", response.CommitSHA)
	}
}

func TestImportClusterAcceptedWithoutResult(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusAccepted)
		_, _ = writer.Write([]byte(`{"status":"accepted"}`))
	}))
	defer server.Close()

	response, err := NewClient(server.URL, "token", "test").ImportCluster(context.Background(), ImportClusterRequest{Name: "dev"})
	if err != nil {
		t.Fatalf("a 202 is not an error, the caller resolves the cluster by name: %v", err)
	}
	if response.Status != ImportStatusAccepted || response.ClusterID != "" {
		t.Errorf("unexpected response: %+v", response)
	}
}

func TestImportClusterRejectedSpec(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		// The platform answers 200 with resource errors and an unpersisted id.
		_, _ = writer.Write([]byte(`{"name":"dev","cluster_id":"5b0c7a3e-1f7e-4a4e-9a55-1d2f3c4b5a69",` +
			`"organisation_id":"0f6d2a51-7a3c-4b8e-9c1d-2e3f4a5b6c7d","import_command":"",` +
			`"errors":[{"name":"podinfo","kind":"addon","errors":[{"key":"registry_name",` +
			`"message":"Helm registry 'podinfo' is not connected to this organisation."}]}],` +
			`"commit_sha":null,"commit_url":null,"warnings":[]}`))
	}))
	defer server.Close()

	_, err := NewClient(server.URL, "token", "test").ImportCluster(context.Background(), ImportClusterRequest{Name: "dev"})
	var rejected *ImportRejectedError
	if !errors.As(err, &rejected) {
		t.Fatalf("expected *ImportRejectedError, got %T: %v", err, err)
	}
	if !strings.Contains(err.Error(), `addon "podinfo": registry_name: Helm registry 'podinfo' is not connected`) {
		t.Errorf("error does not name the member and field: %v", err)
	}
}

// TestImportClusterOutlivesTheAttemptTimeout: a synchronous import commits and
// pushes to Git before it answers, so the per-attempt timeout must not cut it.
func TestImportClusterOutlivesTheAttemptTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		time.Sleep(300 * time.Millisecond)
		_, _ = writer.Write([]byte(importResultBody))
	}))
	defer server.Close()

	apiClient := NewClient(server.URL, "token", "test")
	apiClient.HTTPClient.Timeout = 50 * time.Millisecond
	if _, err := apiClient.ImportCluster(context.Background(), ImportClusterRequest{Name: "dev"}); err != nil {
		t.Fatalf("import was cut by the per-attempt timeout: %v", err)
	}
	if apiClient.HTTPClient.Timeout != 50*time.Millisecond {
		t.Error("ImportCluster must not mutate the shared HTTP client")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := apiClient.ImportCluster(ctx, ImportClusterRequest{Name: "dev"}); err == nil {
		t.Error("the caller's deadline must still bound the import")
	}
}

func TestImportClusterIsNotRetriedOnGatewayErrors(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		writer.WriteHeader(http.StatusBadGateway)
	}))
	defer server.Close()

	if _, err := NewClient(server.URL, "token", "test").ImportCluster(context.Background(), ImportClusterRequest{Name: "dev"}); err == nil {
		t.Fatal("expected an error")
	}
	if calls.Load() != 1 {
		t.Errorf("import attempted %d times; it may already have run, so it must not be retried", calls.Load())
	}
}

func TestWaitForClusterByName(t *testing.T) {
	var polls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Query().Get("cluster_name") != "dev" {
			t.Errorf("unexpected query %q", request.URL.RawQuery)
		}
		if polls.Add(1) < 3 {
			_, _ = writer.Write([]byte(clusterListBody(1, 1, 0)))
			return
		}
		_, _ = writer.Write([]byte(clusterListBody(1, 1, 1, clusterRow("id-1", "dev"))))
	}))
	defer server.Close()

	cluster, err := NewClient(server.URL, "token", "test").WaitForClusterByName(context.Background(), "dev", time.Millisecond)
	if err != nil || cluster == nil || cluster.ID != "id-1" {
		t.Fatalf("got %+v, %v", cluster, err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	missing := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte(clusterListBody(1, 1, 0)))
	}))
	defer missing.Close()
	if _, err := NewClient(missing.URL, "token", "test").WaitForClusterByName(ctx, "dev", time.Millisecond); err == nil {
		t.Error("expected a timeout")
	}
}
