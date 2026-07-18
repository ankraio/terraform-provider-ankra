// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package client

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestImportClusterSuccess(t *testing.T) {
	var receivedAuthorization string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.Path != "/api/v1/clusters/import" {
			t.Errorf("unexpected request: %s %s", request.Method, request.URL.Path)
		}
		receivedAuthorization = request.Header.Get("Authorization")
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"cluster_id":"abc-123","import_command":"helm install ..."}`))
	}))
	defer server.Close()

	apiClient := NewClient(server.URL, "secret-token", "test-agent")
	response, err := apiClient.ImportCluster(context.Background(), ImportClusterRequest{Name: "dev"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if response.ClusterID != "abc-123" {
		t.Errorf("expected cluster id abc-123, got %q", response.ClusterID)
	}
	if response.ImportCommand != "helm install ..." {
		t.Errorf("unexpected import command: %q", response.ImportCommand)
	}
	if receivedAuthorization != "Bearer secret-token" {
		t.Errorf("expected bearer token header, got %q", receivedAuthorization)
	}
}

func TestImportClusterErrorSurfacesStatusAndBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusUnauthorized)
		_, _ = writer.Write([]byte(`{"error":"invalid token"}`))
	}))
	defer server.Close()

	apiClient := NewClient(server.URL, "bad", "test-agent")
	_, err := apiClient.ImportCluster(context.Background(), ImportClusterRequest{Name: "dev"})
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	var apiError *APIError
	if !errors.As(err, &apiError) {
		t.Fatalf("expected *APIError, got %T", err)
	}
	if apiError.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected status 401, got %d", apiError.StatusCode)
	}
	if apiError.Body == "" {
		t.Error("expected response body to be captured in the error")
	}
}

func TestListClustersParsesPayload(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte(`{"clusters":[{"id":"1","name":"a"},{"id":"2","name":"b"}]}`))
	}))
	defer server.Close()

	apiClient := NewClient(server.URL, "token", "test-agent")
	clusters, err := apiClient.ListClusters(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(clusters) != 2 || clusters[0].Name != "a" || clusters[1].ID != "2" {
		t.Errorf("unexpected clusters: %+v", clusters)
	}
}

func TestGetClusterByIDMissingReturnsNil(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte(`{"clusters":[{"id":"1","name":"a"}]}`))
	}))
	defer server.Close()

	apiClient := NewClient(server.URL, "token", "test-agent")
	cluster, err := apiClient.GetClusterByID(context.Background(), "does-not-exist")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cluster != nil {
		t.Errorf("expected nil cluster, got %+v", cluster)
	}
}

func TestDeleteClusterTreats404AsSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodDelete {
			t.Errorf("expected DELETE, got %s", request.Method)
		}
		writer.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	apiClient := NewClient(server.URL, "token", "test-agent")
	if err := apiClient.DeleteCluster(context.Background(), "dev"); err != nil {
		t.Fatalf("expected 404 to be treated as success, got %v", err)
	}
}

func TestDeleteClusterErrorsOnServerFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	apiClient := NewClient(server.URL, "token", "test-agent")
	if err := apiClient.DeleteCluster(context.Background(), "dev"); err == nil {
		t.Fatal("expected an error on 500, got nil")
	}
}

func TestNewClientDefaultsBaseURL(t *testing.T) {
	apiClient := NewClient("", "token", "agent")
	if apiClient.BaseURL != DefaultBaseURL {
		t.Errorf("expected default base url, got %q", apiClient.BaseURL)
	}
}
