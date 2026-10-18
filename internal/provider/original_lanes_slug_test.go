// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/diag"

	"ankra.io/terraform-provider-ankra/internal/client"
)

// recordedPrivateState is a private-state reader holding recorded keys.
type recordedPrivateState map[string][]byte

func (private recordedPrivateState) GetKey(_ context.Context, key string) ([]byte, diag.Diagnostics) {
	return private[key], nil
}

func defaultTimeout(_ context.Context, fallback time.Duration) (time.Duration, diag.Diagnostics) {
	return fallback, nil
}

// TestDeprovisionResolvesProjectSlugBeforeScopeCheck pins that a destroy run
// whose first request is the deprovision itself (no refresh) resolves a slug
// project_id before comparing it with the recorded scope, instead of reading
// the unresolved slug as a scope move and refusing the destroy.
func TestDeprovisionResolvesProjectSlugBeforeScopeCheck(t *testing.T) {
	lanesFastPolling(t)
	fake := newLanesFake(t)
	listings := 0
	registerProjects(fake.routeFake, map[string]string{"platform": lanesTestProject}, &listings)
	cluster := fake.addCluster(client.LaneHetzner, "tf-hetzner", "running")
	cluster.project = lanesTestProject

	apiClient := client.NewClient(fake.server.URL, "test-token", "test")
	apiClient.ProjectSlug = "platform"
	recorded, err := json.Marshal(recordedScope{ProjectID: lanesTestProject})
	if err != nil {
		t.Fatal(err)
	}
	var diagnostics diag.Diagnostics
	deprovisionLaneClusterAndWait(context.Background(), apiClient, laneDeprovision{
		lane:          client.LaneHetzner,
		displayName:   "Hetzner",
		clusterID:     cluster.id,
		options:       client.LaneDeprovisionOptions{Force: true},
		deleteTimeout: defaultTimeout,
	}, recordedPrivateState{scopePrivateKey: recorded}, &diagnostics)
	if diagnostics.HasError() {
		t.Fatalf("deprovision failed: %v", diagnostics)
	}
	if err := lanesExpect("deprovision queries", fake.deprovisionQuery, []string{"force=true"}); err != nil {
		t.Error(err)
	}
	if len(fake.clusters) != 0 {
		t.Error("the deprovision returned before the cluster left the listing")
	}
	if outside := fake.requestsOutsideProject(lanesTestProject); len(outside) != 0 {
		t.Errorf("requests outside the resolved project: %v", outside)
	}
}

// TestRequireUnmovedScopeResolvesProjectSlug pins the scope check every
// destroy shares (node groups, managed clusters and the playground call it
// directly): a fresh client with a slug project_id compares the recorded
// project with the slug's ID, not with an unresolved "", and still refuses a
// real move.
func TestRequireUnmovedScopeResolvesProjectSlug(t *testing.T) {
	fake := newRouteFake(t)
	listings := 0
	registerProjects(fake, map[string]string{"platform": lanesTestProject, "other": lanesOtherTestProject}, &listings)
	recorded, err := json.Marshal(recordedScope{ProjectID: lanesTestProject})
	if err != nil {
		t.Fatal(err)
	}
	private := recordedPrivateState{scopePrivateKey: recorded}

	sameProject := client.NewClient(fake.server.URL, "test-token", "test")
	sameProject.ProjectSlug = "platform"
	var diagnostics diag.Diagnostics
	if !requireUnmovedScope(context.Background(), sameProject, private, &diagnostics) || diagnostics.HasError() {
		t.Fatalf("the slug of the recorded project read as a scope move: %v", diagnostics)
	}

	otherProject := client.NewClient(fake.server.URL, "test-token", "test")
	otherProject.ProjectSlug = "other"
	diagnostics = nil
	if requireUnmovedScope(context.Background(), otherProject, private, &diagnostics) {
		t.Fatal("a slug of another project did not read as a scope move")
	}

	unknownProject := client.NewClient(fake.server.URL, "test-token", "test")
	unknownProject.ProjectSlug = "missing"
	diagnostics = nil
	if requireUnmovedScope(context.Background(), unknownProject, private, &diagnostics) || !diagnostics.HasError() {
		t.Fatal("an unknown slug passed the scope check")
	}
}

// TestDeprovisionRefusesUnknownProjectSlug pins that a slug that resolves to
// no project fails the destroy before anything is deleted.
func TestDeprovisionRefusesUnknownProjectSlug(t *testing.T) {
	fake := newLanesFake(t)
	listings := 0
	registerProjects(fake.routeFake, map[string]string{"platform": lanesTestProject}, &listings)
	cluster := fake.addCluster(client.LaneHetzner, "tf-hetzner", "running")

	apiClient := client.NewClient(fake.server.URL, "test-token", "test")
	apiClient.ProjectSlug = "missing"
	var diagnostics diag.Diagnostics
	deprovisionLaneClusterAndWait(context.Background(), apiClient, laneDeprovision{
		lane:          client.LaneHetzner,
		displayName:   "Hetzner",
		clusterID:     cluster.id,
		deleteTimeout: defaultTimeout,
	}, recordedPrivateState{}, &diagnostics)
	if !diagnostics.HasError() {
		t.Fatal("an unknown slug did not fail the destroy")
	}
	if len(fake.deprovisionQuery) != 0 {
		t.Errorf("a deprovision was sent: %v", fake.deprovisionQuery)
	}
}
