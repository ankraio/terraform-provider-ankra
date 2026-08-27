// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package client

import (
	"encoding/json"
	"os"
	"testing"
)

// testdata/cluster_list_response.json is a frozen capture of
// ClusterListResponseContract from the platform's committed openapi.json
// (servers: https://platform.ankra.app). It is the contract of record for
// GET /api/v1/clusters and is maintained by hand alongside the client.
//
// It exists because the client previously decoded the listing from a
// "clusters" key that the platform never sends. Every fake in this package
// agreed with that mistake, so the suite was green while the provider could
// not see a single cluster. Fixtures now come from the contract, not from the
// client's assumptions.
func TestClusterListFixtureMatchesPlatformContract(t *testing.T) {
	fixture, readError := os.ReadFile("testdata/cluster_list_response.json")
	if readError != nil {
		t.Fatalf("reading fixture: %v", readError)
	}

	var response ClusterListResponse
	if unmarshalError := json.Unmarshal(fixture, &response); unmarshalError != nil {
		t.Fatalf("decoding fixture: %v", unmarshalError)
	}

	if len(response.Result) != 1 {
		t.Fatalf("result rows = %d, want 1 - the client is decoding the wrong key", len(response.Result))
	}
	cluster := response.Result[0]
	if cluster.ID != "3f2b1a90-0c4d-4e5f-8a7b-1c2d3e4f5a6b" {
		t.Errorf("id = %q", cluster.ID)
	}
	if cluster.Name != "production" {
		t.Errorf("name = %q", cluster.Name)
	}
	if cluster.Kind != "hetzner" {
		t.Errorf("kind = %q", cluster.Kind)
	}
	if cluster.State != "running" {
		t.Errorf("state = %q", cluster.State)
	}
	if response.Pagination.TotalPages != 1 || response.Pagination.PageSize != 25 {
		t.Errorf("pagination = %+v", response.Pagination)
	}
}

// TestClusterListRejectsRetiredEnvelope pins the regression directly: the
// pre-fix envelope must not decode into anything.
func TestClusterListRejectsRetiredEnvelope(t *testing.T) {
	var response ClusterListResponse
	retired := []byte(`{"clusters":[{"id":"1","name":"a"}]}`)
	if unmarshalError := json.Unmarshal(retired, &response); unmarshalError != nil {
		t.Fatalf("decoding: %v", unmarshalError)
	}
	if len(response.Result) != 0 {
		t.Fatal("the retired \"clusters\" envelope must not populate Result")
	}
}
