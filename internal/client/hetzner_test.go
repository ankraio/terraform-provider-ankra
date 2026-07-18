// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package client

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCreateHetznerClusterSendsPayload(t *testing.T) {
	var received map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.Path != "/api/v1/clusters/hetzner" {
			t.Errorf("unexpected request: %s %s", request.Method, request.URL.Path)
		}
		body, _ := io.ReadAll(request.Body)
		_ = json.Unmarshal(body, &received)
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"cluster_id":"hz-1","name":"tf-hz"}`))
	}))
	defer server.Close()

	apiClient := NewClient(server.URL, "token", "agent")
	version := "v1.35.6+k3s1"
	response, err := apiClient.CreateHetznerCluster(context.Background(), HetznerClusterRequest{
		Name:              "tf-hz",
		CredentialID:      "cred-1",
		Location:          "fsn1",
		Distribution:      "k3s",
		ControlPlaneCount: 1,
		WorkerCount:       2,
		KubernetesVersion: &version,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if response.ClusterID != "hz-1" {
		t.Errorf("expected cluster id hz-1, got %q", response.ClusterID)
	}
	if received["credential_id"] != "cred-1" || received["location"] != "fsn1" {
		t.Errorf("required fields not sent: %+v", received)
	}
	workerCount, ok := received["worker_count"].(float64)
	if !ok || workerCount != 2 {
		t.Errorf("worker_count not sent correctly: %v", received["worker_count"])
	}
	if received["kubernetes_version"] != version {
		t.Errorf("kubernetes_version not sent: %v", received["kubernetes_version"])
	}
	// A nil optional pointer must be omitted entirely.
	if _, present := received["description"]; present {
		t.Errorf("empty description should have been omitted, got %v", received["description"])
	}
}

func TestDeleteHetznerClusterUsesForceAndTreats404AsSuccess(t *testing.T) {
	var receivedQuery string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodDelete {
			t.Errorf("expected DELETE, got %s", request.Method)
		}
		receivedQuery = request.URL.RawQuery
		writer.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	apiClient := NewClient(server.URL, "token", "agent")
	if err := apiClient.DeleteHetznerCluster(context.Background(), "hz-1", true); err != nil {
		t.Fatalf("expected 404 to be treated as success, got %v", err)
	}
	if receivedQuery != "force=true" {
		t.Errorf("expected force=true query, got %q", receivedQuery)
	}
}
