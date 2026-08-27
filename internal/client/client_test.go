// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package client

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// clusterRow renders one row of the platform's ClusterListItemContract.
func clusterRow(id, name string) string {
	return fmt.Sprintf(`{"id":%q,"name":%q,"kind":"hetzner","state":"running"}`, id, name)
}

// clusterListBody renders the platform's ClusterListResponseContract. Every
// fake in this package builds its listing here so no test can quietly invent
// an envelope the platform does not send.
func clusterListBody(page, totalPages, totalCount int, rows ...string) string {
	return fmt.Sprintf(
		`{"result":[%s],"pagination":{"total_count":%d,"total_pages":%d,"page":%d,"page_size":%d},"metrics":{}}`,
		strings.Join(rows, ","), totalCount, totalPages, page, listPageSize,
	)
}

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

func TestListClustersReadsResultEnvelope(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte(clusterListBody(1, 1, 2, clusterRow("1", "a"), clusterRow("2", "b"))))
	}))
	defer server.Close()

	apiClient := NewClient(server.URL, "token", "test-agent")
	clusters, err := apiClient.ListClusters(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(clusters) != 2 || clusters[0].Name != "a" || clusters[1].ID != "2" {
		t.Fatalf("unexpected clusters: %+v", clusters)
	}
	if clusters[0].Kind != "hetzner" || clusters[0].State != "running" {
		t.Errorf("kind/state not decoded: %+v", clusters[0])
	}
}

// TestListClustersFollowsPagination guards the second half of the regression:
// an unpaged listing stopped at the platform's default page size, so any
// cluster past it read as deleted.
func TestListClustersFollowsPagination(t *testing.T) {
	var requestedPages []string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		page := request.URL.Query().Get("page")
		requestedPages = append(requestedPages, page)
		if request.URL.Query().Get("page_size") != fmt.Sprintf("%d", listPageSize) {
			t.Errorf("expected page_size=%d, got %q", listPageSize, request.URL.Query().Get("page_size"))
		}
		switch page {
		case "1":
			_, _ = writer.Write([]byte(clusterListBody(1, 2, 3, clusterRow("1", "a"), clusterRow("2", "b"))))
		case "2":
			_, _ = writer.Write([]byte(clusterListBody(2, 2, 3, clusterRow("3", "c"))))
		default:
			t.Errorf("unexpected page %q", page)
		}
	}))
	defer server.Close()

	apiClient := NewClient(server.URL, "token", "test-agent")
	clusters, err := apiClient.ListClusters(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(clusters) != 3 {
		t.Fatalf("expected 3 clusters across 2 pages, got %d: %+v", len(clusters), clusters)
	}
	if len(requestedPages) != 2 || requestedPages[0] != "1" || requestedPages[1] != "2" {
		t.Errorf("expected pages 1 and 2 to be requested, got %v", requestedPages)
	}
}

// TestListClustersStopsOnShortPage covers a platform response with no usable
// total_pages, where a short page is the only end-of-listing signal.
func TestListClustersStopsOnShortPage(t *testing.T) {
	var requests int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&requests, 1)
		_, _ = writer.Write([]byte(`{"result":[` + clusterRow("1", "a") + `],"pagination":{}}`))
	}))
	defer server.Close()

	apiClient := NewClient(server.URL, "token", "test-agent")
	clusters, err := apiClient.ListClusters(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(clusters) != 1 {
		t.Errorf("expected 1 cluster, got %d", len(clusters))
	}
	if got := atomic.LoadInt32(&requests); got != 1 {
		t.Errorf("expected the short page to end paging after 1 request, got %d", got)
	}
}

// TestGetClusterByIDFiltersServerSide asserts the lookup asks the platform for
// one row instead of listing the whole estate on every Terraform read.
func TestGetClusterByIDFiltersServerSide(t *testing.T) {
	var requests int32
	var receivedQuery string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		atomic.AddInt32(&requests, 1)
		receivedQuery = request.URL.RawQuery
		_, _ = writer.Write([]byte(clusterListBody(1, 1, 1, clusterRow("cluster-9", "prod"))))
	}))
	defer server.Close()

	apiClient := NewClient(server.URL, "token", "test-agent")
	cluster, err := apiClient.GetClusterByID(context.Background(), "cluster-9")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cluster == nil {
		t.Fatal("expected the cluster to be found")
	}
	if cluster.Name != "prod" || cluster.State != "running" {
		t.Errorf("unexpected cluster: %+v", cluster)
	}
	if !strings.Contains(receivedQuery, "cluster_id=cluster-9") {
		t.Errorf("expected a server-side cluster_id filter, got %q", receivedQuery)
	}
	if got := atomic.LoadInt32(&requests); got != 1 {
		t.Errorf("expected exactly 1 request, got %d", got)
	}
}

func TestGetClusterByIDMissingReturnsNil(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte(clusterListBody(1, 1, 0)))
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

func TestGetClusterByNameFiltersServerSide(t *testing.T) {
	var receivedQuery string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		receivedQuery = request.URL.RawQuery
		_, _ = writer.Write([]byte(clusterListBody(1, 1, 1, clusterRow("cluster-9", "prod"))))
	}))
	defer server.Close()

	apiClient := NewClient(server.URL, "token", "test-agent")
	cluster, err := apiClient.GetClusterByName(context.Background(), "prod")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cluster == nil || cluster.ID != "cluster-9" {
		t.Fatalf("unexpected cluster: %+v", cluster)
	}
	if !strings.Contains(receivedQuery, "cluster_name=prod") {
		t.Errorf("expected a server-side cluster_name filter, got %q", receivedQuery)
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
	apiClient.MaxRetries = 1
	if err := apiClient.DeleteCluster(context.Background(), "dev"); err == nil {
		t.Fatal("expected an error on 500, got nil")
	}
}

func TestNewClientDefaultsBaseURL(t *testing.T) {
	apiClient := NewClient("", "token", "agent")
	if apiClient.BaseURL != DefaultBaseURL {
		t.Errorf("expected default base url, got %q", apiClient.BaseURL)
	}
	if apiClient.MaxRetries != DefaultMaxRetries {
		t.Errorf("expected default retries, got %d", apiClient.MaxRetries)
	}
}

func TestReadRetriesOnGatewayFailure(t *testing.T) {
	var attempts int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		if atomic.AddInt32(&attempts, 1) == 1 {
			writer.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = writer.Write([]byte(clusterListBody(1, 1, 1, clusterRow("1", "a"))))
	}))
	defer server.Close()

	apiClient := NewClient(server.URL, "token", "agent")
	cluster, err := apiClient.GetClusterByID(context.Background(), "1")
	if err != nil {
		t.Fatalf("expected the retry to succeed, got %v", err)
	}
	if cluster == nil {
		t.Fatal("expected the cluster to be found after a retry")
	}
	if got := atomic.LoadInt32(&attempts); got != 2 {
		t.Errorf("expected 2 attempts, got %d", got)
	}
}

// TestCreateIsNotRetriedOnServerError is a safety property, not a nicety: the
// platform may have provisioned the cluster before the failure surfaced, so a
// repeated POST risks a second live cluster.
func TestCreateIsNotRetriedOnServerError(t *testing.T) {
	var attempts int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&attempts, 1)
		writer.WriteHeader(http.StatusBadGateway)
	}))
	defer server.Close()

	apiClient := NewClient(server.URL, "token", "agent")
	_, err := apiClient.CreateHetznerCluster(context.Background(), HetznerClusterRequest{Name: "dev"})
	if err == nil {
		t.Fatal("expected an error")
	}
	if got := atomic.LoadInt32(&attempts); got != 1 {
		t.Fatalf("a create must not be retried on a server error, got %d attempts", got)
	}
}

// TestCreateIsRetriedOnRateLimit complements the above: a 429 means the
// request was rejected unprocessed, so repeating it cannot double-provision.
func TestCreateIsRetriedOnRateLimit(t *testing.T) {
	var attempts int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		if atomic.AddInt32(&attempts, 1) == 1 {
			writer.WriteHeader(http.StatusTooManyRequests)
			_, _ = writer.Write([]byte(`{"retry_after":0.01}`))
			return
		}
		_, _ = writer.Write([]byte(`{"cluster_id":"abc","import_command":"helm install"}`))
	}))
	defer server.Close()

	apiClient := NewClient(server.URL, "token", "agent")
	created, err := apiClient.CreateHetznerCluster(context.Background(), HetznerClusterRequest{Name: "dev"})
	if err != nil {
		t.Fatalf("expected the rate-limited create to succeed on retry, got %v", err)
	}
	if created.ClusterID != "abc" {
		t.Errorf("unexpected cluster id %q", created.ClusterID)
	}
	if got := atomic.LoadInt32(&attempts); got != 2 {
		t.Errorf("expected 2 attempts, got %d", got)
	}
}

func TestRetryExhaustionSurfacesAPIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	apiClient := NewClient(server.URL, "token", "agent")
	apiClient.MaxRetries = 2
	_, err := apiClient.GetClusterByID(context.Background(), "1")
	var apiError *APIError
	if !errors.As(err, &apiError) || apiError.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("expected a 503 APIError, got %v", err)
	}
}
