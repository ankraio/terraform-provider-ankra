// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework-timeouts/resource/timeouts"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	testresource "github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
)

const poolReplaceClusterID = "7d3c1a2b-0000-4000-8000-000000000001"

// poolReplaceFake answers the managed node-pool routes the way the platform
// does (cluster go/internal/managedk8sapi, usecase/managedk8s/replacepool.go):
// a replace answers 202 with the operation id and the replacement's name; the
// operation runs for one read, then the replacement is listed and the
// replaced pool deleted; a repeat while it runs is a 409 "already in
// progress"; a failed attempt may be retried with the same name and size.
type poolReplaceFake struct {
	*routeFake
	pools        map[string]map[string]any
	replaceCalls []string
	events       []string
	deletes      []string
	operations   map[string]*poolReplaceOperation
	// failNext makes the next accepted replacement fail.
	failNext bool
	// stalled keeps every operation running.
	stalled bool
	// answerInProgress makes the next replace a 409 while the operation
	// already accepted keeps running.
	answerInProgress bool
}

type poolReplaceOperation struct {
	old, replacement, size string
	readsLeft              int
	status                 string
}

func newPoolReplaceFake(t *testing.T) *poolReplaceFake {
	fake := &poolReplaceFake{routeFake: newRouteFake(t), pools: map[string]map[string]any{}, operations: map[string]*poolReplaceOperation{}}
	base := "/api/v1/clusters/managed/doks/" + poolReplaceClusterID + "/node-pools"
	fake.handle("GET "+base, func(writer http.ResponseWriter, _ *http.Request) {
		fake.advance()
		names := make([]string, 0, len(fake.pools))
		for name := range fake.pools {
			names = append(names, name)
		}
		sort.Strings(names)
		rows := make([]any, 0, len(names))
		for _, name := range names {
			rows = append(rows, fake.pools[name])
		}
		writeJSON(t, writer, http.StatusOK, map[string]any{"node_pools": rows})
	})
	fake.handle("POST "+base, func(writer http.ResponseWriter, request *http.Request) {
		var body map[string]any
		decodeJSON(t, request, &body)
		name := fmt.Sprint(body["name"])
		fake.pools[name] = map[string]any{"name": name, "size": body["size"], "count": body["count"], "labels": map[string]any{}, "taints": []any{}}
		writeJSON(t, writer, http.StatusOK, map[string]any{"cluster_id": poolReplaceClusterID, "node_pool_name": name})
	})
	fake.handle("PATCH "+base+"/{pool}", func(writer http.ResponseWriter, request *http.Request) {
		row := fake.pools[request.PathValue("pool")]
		if row == nil {
			writeJSON(t, writer, http.StatusNotFound, map[string]any{"detail": "Cluster or node pool not found"})
			return
		}
		var body map[string]any
		decodeJSON(t, request, &body)
		if count, present := body["count"]; present {
			row["count"] = count
		}
		fake.events = append(fake.events, fmt.Sprintf("patch %s count=%v", request.PathValue("pool"), body["count"]))
		writeJSON(t, writer, http.StatusOK, map[string]any{"node_pool_name": request.PathValue("pool")})
	})
	fake.handle("DELETE "+base+"/{pool}", func(writer http.ResponseWriter, request *http.Request) {
		if fake.pools[request.PathValue("pool")] == nil {
			writeJSON(t, writer, http.StatusNotFound, map[string]any{"detail": "Cluster or node pool not found"})
			return
		}
		fake.deletes = append(fake.deletes, request.PathValue("pool"))
		delete(fake.pools, request.PathValue("pool"))
		writeJSON(t, writer, http.StatusOK, map[string]any{"node_pool_name": request.PathValue("pool")})
	})
	fake.handle("POST "+base+"/{pool}/replace", func(writer http.ResponseWriter, request *http.Request) {
		var body map[string]any
		decodeJSON(t, request, &body)
		old := request.PathValue("pool")
		replacement := fmt.Sprint(body["replacement_pool_name"])
		size := fmt.Sprint(body["size"])
		fake.replaceCalls = append(fake.replaceCalls, fmt.Sprintf("%s -> %s (%s)", old, replacement, size))
		fake.events = append(fake.events, "replace "+old)
		if fake.pools[old] == nil {
			writeJSON(t, writer, http.StatusNotFound, map[string]any{"detail": "Cluster or node pool not found"})
			return
		}
		if fake.answerInProgress {
			fake.answerInProgress = false
			writeJSON(t, writer, http.StatusConflict, map[string]any{"detail": "Node pool replacement is already in progress"})
			return
		}
		if _, taken := fake.pools[replacement]; taken {
			writeJSON(t, writer, http.StatusConflict, map[string]any{"detail": "Node pool already exists"})
			return
		}
		operationID := fmt.Sprintf("00000000-0000-4000-8000-%012d", len(fake.operations)+1)
		status := "running"
		if fake.failNext {
			fake.failNext = false
			status = "failed"
		}
		fake.operations[operationID] = &poolReplaceOperation{old: old, replacement: replacement, size: size, readsLeft: 1, status: status}
		writeJSON(t, writer, http.StatusAccepted, map[string]any{
			"operation_id": operationID, "cluster_id": poolReplaceClusterID, "node_pool_name": old,
			"replacement_pool_name": replacement, "old_size": fake.pools[old]["size"], "new_size": size,
		})
	})
	fake.handle("GET /api/v1/org/executions/{id}", func(writer http.ResponseWriter, request *http.Request) {
		operation := fake.operations[request.PathValue("id")]
		if operation == nil {
			writeJSON(t, writer, http.StatusNotFound, map[string]any{"detail": "Execution not found"})
			return
		}
		fake.advance()
		execution := map[string]any{"id": request.PathValue("id"), "name": "managed_k8s_replace_node_pool", "status": operation.status}
		if operation.status == "failed" {
			execution["error_excerpt"] = "replacement pool did not become ready"
		}
		writeJSON(t, writer, http.StatusOK, map[string]any{"execution": execution, "steps": []any{}})
	})
	return fake
}

// finish completes a replacement the way the platform job does: the new
// pool takes the old one's count, and the old pool is deleted.
func (fake *poolReplaceFake) finish(operation *poolReplaceOperation) {
	old := fake.pools[operation.old]
	fake.pools[operation.replacement] = map[string]any{
		"name": operation.replacement, "size": strings.ToUpper(operation.size), "count": old["count"],
		"labels": map[string]any{}, "taints": []any{},
	}
	delete(fake.pools, operation.old)
	operation.status = "success"
	fake.events = append(fake.events, "finished "+operation.replacement)
}

// advance moves each running replacement on by one read, unless the fake
// is stalled.
func (fake *poolReplaceFake) advance() {
	if fake.stalled {
		return
	}
	for _, operation := range fake.operations {
		if operation.status != "running" {
			continue
		}
		if operation.readsLeft > 0 {
			operation.readsLeft--
			continue
		}
		fake.finish(operation)
	}
}

// running returns the one operation still running.
func (fake *poolReplaceFake) running() *poolReplaceOperation {
	for _, operation := range fake.operations {
		if operation.status == "running" {
			return operation
		}
	}
	return nil
}

func poolReplaceConfig(fake *poolReplaceFake, size string, count int, extra string) string {
	return fake.providerBlock() + fmt.Sprintf(`
resource "ankra_managed_node_pool" "workers" {
  provider_name = "doks"
  cluster_id    = %q
  name          = "workers"
  size          = %q
  node_count    = %d
%s
}
`, poolReplaceClusterID, size, count, extra)
}

// TestAccManagedNodePoolSizeChangeReplacesPoolInPlace pins the tracked
// replace: a size change updates the resource (no destroy and create), sends
// the replacement name the provider derives from name and size, waits for
// the platform to finish, and records the new pool as current_pool_name,
// known at plan time. A failed replacement keeps the old pool recorded, and
// the next apply asks for the same replacement again.
func TestAccManagedNodePoolSizeChangeReplacesPoolInPlace(t *testing.T) {
	lanesFastPolling(t)
	fake := newPoolReplaceFake(t)
	medium := managedReplacementPoolName("workers", "s-4vcpu-8gb")
	large := managedReplacementPoolName("workers", "s-8vcpu-16gb")
	address := "ankra_managed_node_pool.workers"
	testresource.Test(t, testresource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []testresource.TestStep{
			{
				Config: poolReplaceConfig(fake, "s-2vcpu-4gb", 2, ""),
				Check: testresource.ComposeAggregateTestCheckFunc(
					testresource.TestCheckResourceAttr(address, "current_pool_name", "workers"),
					testresource.TestCheckResourceAttr(address, "id", "doks/"+poolReplaceClusterID+"/workers"),
				),
			},
			{Config: poolReplaceConfig(fake, "s-2vcpu-4gb", 2, ""), PlanOnly: true},
			{
				Config: poolReplaceConfig(fake, "s-4vcpu-8gb", 3, ""),
				ConfigPlanChecks: testresource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction(address, plancheck.ResourceActionUpdate),
					plancheck.ExpectKnownValue(address, tfjsonpath.New("current_pool_name"), knownvalue.StringExact(medium)),
				}},
				Check: testresource.ComposeAggregateTestCheckFunc(
					testresource.TestCheckResourceAttr(address, "current_pool_name", medium),
					testresource.TestCheckResourceAttr(address, "name", "workers"),
					testresource.TestCheckResourceAttr(address, "id", "doks/"+poolReplaceClusterID+"/workers"),
					testresource.TestCheckResourceAttr(address, "size", "s-4vcpu-8gb"),
					testresource.TestCheckResourceAttr(address, "node_count", "3"),
					lanesCheck(func() error {
						return lanesExpect("events", fake.events, []string{"patch workers count=3", "replace workers", "finished " + medium})
					}),
				),
			},
			{
				// The platform reports the size upper-cased; the configured
				// spelling is kept and nothing is planned.
				Config:   poolReplaceConfig(fake, "s-4vcpu-8gb", 3, ""),
				PlanOnly: true,
			},
			{
				PreConfig:   func() { fake.failNext = true },
				Config:      poolReplaceConfig(fake, "s-8vcpu-16gb", 3, ""),
				ExpectError: regexp.MustCompile(`(?s)Unable to change the managed node pool's size.*ended\s+failed`),
			},
			{
				// The old pool is still recorded, with its old size, so the
				// retry plans the same replacement and the platform resumes it.
				Config: poolReplaceConfig(fake, "s-8vcpu-16gb", 3, ""),
				ConfigPlanChecks: testresource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction(address, plancheck.ResourceActionUpdate),
					plancheck.ExpectKnownValue(address, tfjsonpath.New("current_pool_name"), knownvalue.StringExact(large)),
				}},
				Check: testresource.ComposeAggregateTestCheckFunc(
					testresource.TestCheckResourceAttr(address, "current_pool_name", large),
					lanesCheck(func() error {
						want := fmt.Sprintf("%s -> %s (s-8vcpu-16gb)", medium, large)
						return lanesExpect("replace calls", fake.replaceCalls, []string{
							"workers -> " + medium + " (s-4vcpu-8gb)", want, want,
						})
					}),
				),
			},
			{
				// A pool resized by this resource imports by the name it was
				// created with.
				ResourceName:      address,
				ImportState:       true,
				ImportStateId:     "doks/" + poolReplaceClusterID + "/workers",
				ImportStateVerify: true,
				// The import records the provider's spelling of the size.
				ImportStateVerifyIgnore: []string{"size"},
			},
		},
		CheckDestroy: func(*terraform.State) error {
			return lanesExpect("deletes", fake.deletes, []string{large})
		},
	})
}

// TestAccManagedNodePoolSizeChangeOutlastingTimeout pins the recovery of a
// replacement that outlasts the update timeout: the apply fails and keeps the
// old pool; the next apply follows the replacement still running (a 409
// "already in progress") to its end; and a replacement that finished after
// its apply gave up is found by the next refresh, planning nothing.
func TestAccManagedNodePoolSizeChangeOutlastingTimeout(t *testing.T) {
	lanesFastPolling(t)
	fake := newPoolReplaceFake(t)
	medium := managedReplacementPoolName("workers", "s-4vcpu-8gb")
	large := managedReplacementPoolName("workers", "s-8vcpu-16gb")
	address := "ankra_managed_node_pool.workers"
	shortTimeoutBlock := `  timeouts { update = "300ms" }`
	testresource.Test(t, testresource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []testresource.TestStep{
			{Config: poolReplaceConfig(fake, "s-2vcpu-4gb", 1, "")},
			{
				PreConfig:   func() { fake.stalled = true },
				Config:      poolReplaceConfig(fake, "s-4vcpu-8gb", 1, shortTimeoutBlock),
				ExpectError: regexp.MustCompile(`(?s)had\s+not\s+finished\s+before\s+the\s+update\s+timeout`),
			},
			{
				PreConfig: func() { fake.stalled, fake.answerInProgress = false, true },
				Config:    poolReplaceConfig(fake, "s-4vcpu-8gb", 1, ""),
				Check: testresource.ComposeAggregateTestCheckFunc(
					testresource.TestCheckResourceAttr(address, "current_pool_name", medium),
					lanesCheck(func() error { return lanesExpect("pools", len(fake.pools), 1) }),
				),
			},
			{
				// This replacement outlasts the apply.
				PreConfig: func() {
					fake.stalled = true
					fake.answerInProgress = false
				},
				Config:      poolReplaceConfig(fake, "S-8VCPU-16GB", 1, shortTimeoutBlock),
				ExpectError: regexp.MustCompile(`(?s)had\s+not\s+finished`),
			},
			{
				// It finishes at the platform afterwards; the refresh finds
				// the new pool by its derived name.
				PreConfig:    func() { fake.finish(fake.running()) },
				RefreshState: true,
			},
			{
				// The platform reports the size in its own case, which the
				// configuration uses here, so the adopted pool plans nothing.
				Config:   poolReplaceConfig(fake, "S-8VCPU-16GB", 1, shortTimeoutBlock),
				PlanOnly: true,
			},
			{
				Config: poolReplaceConfig(fake, "S-8VCPU-16GB", 1, shortTimeoutBlock),
				Check:  testresource.TestCheckResourceAttr(address, "current_pool_name", large),
			},
		},
	})
}

// TestManagedReplacementPoolName pins the derived name: deterministic in the
// name and the size (whatever its case), short and plain enough for every
// provider's pool-name rule, and never the configured name itself.
func TestManagedReplacementPoolName(t *testing.T) {
	first := managedReplacementPoolName("workers", "Standard_D4s_v5")
	if first != managedReplacementPoolName("workers", "standard_d4s_v5") {
		t.Errorf("the size's case changed the name")
	}
	if first == managedReplacementPoolName("workers", "Standard_D8s_v5") {
		t.Errorf("two sizes gave one name")
	}
	pattern := regexp.MustCompile(`^[a-z][a-z0-9]{0,11}$`)
	for _, name := range []string{"workers", "GPU-pool-01", "1-system", "a-very-long-pool-name-for-batch-jobs", "---"} {
		derived := managedReplacementPoolName(name, "s-4vcpu-8gb")
		if !pattern.MatchString(derived) {
			t.Errorf("%q derived %q, not a lowercase name of at most 12 characters", name, derived)
		}
		if derived == name {
			t.Errorf("%q derived itself", name)
		}
	}
}

// TestManagedPoolCurrentNamePlan pins the plan of current_pool_name, in
// particular for state written before the attribute existed (null), which
// must plan the configured name rather than an unknown value.
func TestManagedPoolCurrentNamePlan(t *testing.T) {
	ctx := context.Background()
	var schemaResponse resource.SchemaResponse
	NewManagedNodePoolResource().Schema(ctx, resource.SchemaRequest{}, &schemaResponse)
	resourceSchema := schemaResponse.Schema
	model := func(size string, current types.String) managedNodePoolModel {
		return managedNodePoolModel{
			ID: types.StringValue("doks/c/workers"), Provider: types.StringValue("doks"), ClusterID: types.StringValue("c"),
			Name: types.StringValue("workers"), CurrentPoolName: current,
			Timeouts: timeoutsNull(t, resourceSchema),
			managedNodePoolFields: managedNodePoolFields{
				Size: types.StringValue(size), Count: types.Int64Value(1), Labels: types.MapNull(types.StringType),
				Zone: types.StringNull(), RootVolumeType: types.StringNull(), RootVolumeSizeGB: types.Int64Null(),
			},
		}
	}
	plan := func(prior *managedNodePoolModel, planned managedNodePoolModel) types.String {
		t.Helper()
		planState := tfsdk.Plan{Schema: resourceSchema, Raw: tftypes.NewValue(resourceSchema.Type().TerraformType(ctx), nil)}
		if diagnostics := planState.Set(ctx, &planned); diagnostics.HasError() {
			t.Fatalf("plan: %v", diagnostics)
		}
		priorState := tfsdk.State{Schema: resourceSchema, Raw: tftypes.NewValue(resourceSchema.Type().TerraformType(ctx), nil)}
		stateValue := types.StringNull()
		if prior != nil {
			if diagnostics := priorState.Set(ctx, prior); diagnostics.HasError() {
				t.Fatalf("state: %v", diagnostics)
			}
			stateValue = prior.CurrentPoolName
		}
		response := &planmodifier.StringResponse{PlanValue: types.StringUnknown()}
		managedPoolCurrentNamePlanModifier{}.PlanModifyString(ctx, planmodifier.StringRequest{
			Plan: planState, State: priorState, StateValue: stateValue, PlanValue: types.StringUnknown(),
		}, response)
		if response.Diagnostics.HasError() {
			t.Fatalf("modifier: %v", response.Diagnostics)
		}
		return response.PlanValue
	}
	legacy := model("s-2vcpu-4gb", types.StringNull())
	resized := model("s-2vcpu-4gb", types.StringValue("workersab12"))
	cases := []struct {
		name  string
		prior *managedNodePoolModel
		size  string
		want  types.String
	}{
		{"create", nil, "s-2vcpu-4gb", types.StringValue("workers")},
		{"legacy state, unchanged", &legacy, "s-2vcpu-4gb", types.StringValue("workers")},
		{"legacy state, case only", &legacy, "S-2VCPU-4GB", types.StringValue("workers")},
		{"resized, unchanged", &resized, "s-2vcpu-4gb", types.StringValue("workersab12")},
		{"size change", &resized, "s-4vcpu-8gb", types.StringValue(managedReplacementPoolName("workers", "s-4vcpu-8gb"))},
	}
	for _, testCase := range cases {
		if got := plan(testCase.prior, model(testCase.size, types.StringUnknown())); !got.Equal(testCase.want) {
			t.Errorf("%s: planned %s, want %s", testCase.name, got, testCase.want)
		}
	}
}

// timeoutsNull is a null timeouts block of the schema.
func timeoutsNull(t *testing.T, resourceSchema schema.Schema) timeouts.Value {
	t.Helper()
	timeoutsType, isTimeouts := resourceSchema.Blocks["timeouts"].Type().(timeouts.Type)
	if !isTimeouts {
		t.Fatalf("timeouts block has type %T", resourceSchema.Blocks["timeouts"].Type())
	}
	return timeouts.Value{Object: types.ObjectNull(timeoutsType.AttrTypes)}
}
