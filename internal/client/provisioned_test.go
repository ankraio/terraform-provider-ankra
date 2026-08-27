// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// waitTestClusterID is the cluster every wait test polls for.
const waitTestClusterID = "c-1"

// statefulClusterServer serves a cluster whose state walks the given sequence,
// one step per request, holding on the final value.
func statefulClusterServer(t *testing.T, states []string, requests *int32) *httptest.Server {
	t.Helper()
	clusterID := waitTestClusterID
	return httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		index := int(atomic.AddInt32(requests, 1)) - 1
		if index >= len(states) {
			index = len(states) - 1
		}
		_, _ = writer.Write([]byte(clusterListBody(1, 1, 1,
			`{"id":"`+clusterID+`","name":"prod","kind":"hetzner","state":"`+states[index]+`"}`)))
	}))
}

func TestWaitForProvisionedClusterPollsUntilRunning(t *testing.T) {
	var requests int32
	server := statefulClusterServer(t, []string{"creating", "provider_access", "running"}, &requests)
	defer server.Close()

	apiClient := NewClient(server.URL, "token", "agent")
	cluster, err := apiClient.WaitForProvisionedCluster(context.Background(), "c-1", time.Millisecond)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cluster.State != ClusterReadyState {
		t.Errorf("state = %q, want %q", cluster.State, ClusterReadyState)
	}
	if got := atomic.LoadInt32(&requests); got != 3 {
		t.Errorf("expected 3 polls, got %d", got)
	}
}

func TestWaitForProvisionedClusterFailsOnUnexpectedState(t *testing.T) {
	var requests int32
	server := statefulClusterServer(t, []string{"creating", "stopped"}, &requests)
	defer server.Close()

	apiClient := NewClient(server.URL, "token", "agent")
	_, err := apiClient.WaitForProvisionedCluster(context.Background(), "c-1", time.Millisecond)
	if err == nil {
		t.Fatal("expected an error when the cluster stops while provisioning")
	}
	if !strings.Contains(err.Error(), "stopped") {
		t.Errorf("error should name the observed state, got %v", err)
	}
}

// TestWaitForClusterStateHonoursDeadline is what makes the resource's
// configurable timeout meaningful: the wait must end when the caller's context
// expires rather than polling forever.
func TestWaitForClusterStateHonoursDeadline(t *testing.T) {
	var requests int32
	server := statefulClusterServer(t, []string{"creating"}, &requests)
	defer server.Close()

	apiClient := NewClient(server.URL, "token", "agent")
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()

	_, err := apiClient.WaitForProvisionedCluster(ctx, "c-1", time.Millisecond)
	if err == nil {
		t.Fatal("expected a timeout error")
	}
	if !strings.Contains(err.Error(), "timed out") || !strings.Contains(err.Error(), "creating") {
		t.Errorf("error should report the timeout and the last observed state, got %v", err)
	}
}

// TestWaitTreatsUnlistedClusterAsProvisioning covers the lag between a create
// response and the cluster appearing in the listing: an absent row means "not
// yet", not "gone".
func TestWaitTreatsUnlistedClusterAsProvisioning(t *testing.T) {
	var requests int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		if atomic.AddInt32(&requests, 1) < 3 {
			_, _ = writer.Write([]byte(clusterListBody(1, 1, 0)))
			return
		}
		_, _ = writer.Write([]byte(clusterListBody(1, 1, 1, clusterRow("c-1", "prod"))))
	}))
	defer server.Close()

	apiClient := NewClient(server.URL, "token", "agent")
	cluster, err := apiClient.WaitForProvisionedCluster(context.Background(), "c-1", time.Millisecond)
	if err != nil {
		t.Fatalf("an unlisted cluster must not end the wait: %v", err)
	}
	if cluster.ID != "c-1" {
		t.Errorf("unexpected cluster %+v", cluster)
	}
}

func TestWaitForImportedClusterWaitsForOnline(t *testing.T) {
	var requests int32
	server := statefulClusterServer(t, []string{"offline", "online"}, &requests)
	defer server.Close()

	apiClient := NewClient(server.URL, "token", "agent")
	cluster, err := apiClient.WaitForImportedCluster(context.Background(), "c-1", time.Millisecond)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cluster.State != ImportedClusterReadyState {
		t.Errorf("state = %q, want %q", cluster.State, ImportedClusterReadyState)
	}
}

// TestProvisionedLanePathsMatchContract pins each lane's create and delete
// paths to the platform's OpenAPI contract.
func TestProvisionedLanePathsMatchContract(t *testing.T) {
	lanes := []struct {
		lane string
		want string
	}{
		{LaneHetzner, "/api/v1/clusters/hetzner"},
		{LaneDigitalOcean, "/api/v1/clusters/digitalocean"},
		{LaneOVH, "/api/v1/clusters/ovh"},
		{LaneScaleway, "/api/v1/clusters/scaleway"},
		{LaneUpCloud, "/api/v1/clusters/upcloud"},
	}
	for _, testCase := range lanes {
		t.Run(testCase.lane, func(subtest *testing.T) {
			var createPath, deletePath, deleteQuery string
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				switch request.Method {
				case http.MethodPost:
					createPath = request.URL.Path
					_, _ = writer.Write([]byte(`{"cluster_id":"c-1"}`))
				case http.MethodDelete:
					deletePath = request.URL.Path
					deleteQuery = request.URL.RawQuery
					writer.WriteHeader(http.StatusOK)
				}
			}))
			defer server.Close()

			apiClient := NewClient(server.URL, "token", "agent")
			if _, err := apiClient.CreateProvisionedCluster(context.Background(), testCase.lane, map[string]any{}); err != nil {
				subtest.Fatalf("create: %v", err)
			}
			if createPath != testCase.want {
				subtest.Errorf("create path = %q, want %q", createPath, testCase.want)
			}
			if err := apiClient.DeleteProvisionedCluster(context.Background(), testCase.lane, "c-1", false); err != nil {
				subtest.Fatalf("delete: %v", err)
			}
			if deletePath != testCase.want+"/c-1" {
				subtest.Errorf("delete path = %q, want %q", deletePath, testCase.want+"/c-1")
			}
			if deleteQuery != "force=false" {
				subtest.Errorf("delete query = %q, want force=false", deleteQuery)
			}
		})
	}
}

// TestLaneRequestsMatchContractFieldNames guards the JSON field names each
// lane sends, since a silent rename would be accepted by the platform's
// defaults and produce a differently-shaped cluster.
func TestLaneRequestsMatchContractFieldNames(t *testing.T) {
	cases := []struct {
		name    string
		request any
		want    []string
	}{
		{"digitalocean", DigitalOceanClusterRequest{}, []string{
			"region", "ssh_key_credential_id", "control_plane_size", "worker_size", "bastion_size", "etcd_size"}},
		{"upcloud", UpCloudClusterRequest{}, []string{
			"zone", "ssh_key_credential_id", "control_plane_plan", "worker_plan", "bastion_plan", "etcd_plan"}},
		{"ovh", OVHClusterRequest{}, []string{
			"region", "control_plane_flavor_id", "worker_flavor_id", "etcd_flavor_id", "gateway_flavor_id",
			"network_vlan_id", "subnet_cidr", "dhcp_start", "dhcp_end"}},
		{"scaleway", ScalewayClusterRequest{}, []string{
			"region", "zone", "control_plane_type", "worker_type", "etcd_type", "gateway_type",
			"bastion_port", "include_dns", "retention_policy"}},
		{"hetzner", HetznerClusterRequest{}, []string{
			"location", "control_plane_server_type", "worker_server_type", "bastion_server_type",
			"network_ip_range", "subnet_range", "etcd_server_type"}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(subtest *testing.T) {
			encoded, marshalError := json.Marshal(testCase.request)
			if marshalError != nil {
				subtest.Fatalf("marshalling: %v", marshalError)
			}
			var decoded map[string]any
			if unmarshalError := json.Unmarshal(encoded, &decoded); unmarshalError != nil {
				subtest.Fatalf("decoding: %v", unmarshalError)
			}
			for _, field := range testCase.want {
				if _, present := decoded[field]; !present {
					subtest.Errorf("missing contract field %q in %s", field, testCase.name)
				}
			}
		})
	}
}
