// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"ankra.io/terraform-provider-ankra/internal/client"
)

// clusterSettingsResolvePlacement stores a ci_placement write the way the
// platform does: null clears
// it; node group "ci" contributes its selector and the toleration of its
// taint; the mode defaults to required and a toleration's operator to Equal
// with a value and Exists without.
func clusterSettingsResolvePlacement(t *testing.T, raw json.RawMessage) any {
	if string(raw) == "null" {
		return nil
	}
	var placement client.ClusterAgentCIPlacement
	if err := json.Unmarshal(raw, &placement); err != nil {
		t.Errorf("decoding ci_placement %s: %v", raw, err)
		return nil
	}
	selector := map[string]string{}
	for key, value := range placement.NodeSelector {
		selector[strings.TrimSpace(key)] = strings.TrimSpace(value)
	}
	tolerations := []client.ClusterAgentCIPlacementToleration{}
	for _, toleration := range placement.Tolerations {
		tolerations = append(tolerations, clusterAgentCIPlacementNormaliseToleration(toleration))
	}
	placement.NodeGroup = strings.TrimSpace(placement.NodeGroup)
	if placement.NodeGroup == "ci" {
		selector["ankra.io/node-group"] = "ci"
		tolerations = append(tolerations, client.ClusterAgentCIPlacementToleration{Key: "dedicated", Operator: "Equal", Value: "ci", Effect: "NoSchedule"})
	}
	sort.Slice(tolerations, func(left, right int) bool { return tolerations[left].Key < tolerations[right].Key })
	mode := placement.Mode
	if mode == "" {
		mode = "required"
	}
	return map[string]any{"node_group": placement.NodeGroup, "node_selector": selector, "tolerations": tolerations, "mode": mode}
}

// clusterSettingsFollowupState is what the follow-up routes store.
type clusterSettingsFollowupState struct {
	selfHeal  bool
	resources map[string]string
	labels    map[string]string
}

// addFollowupRoutes registers the control-plane self-heal, agent resources
// and labels routes, answering as the platform
// does: the agent sizing write canonicalises quantities ("2048Mi" is stored
// as "2Gi") and refuses a body naming nothing; the labels PATCH refuses an
// empty change and the reserved environment key.
func (fake *clusterSettingsFake) addFollowupRoutes(t *testing.T) *clusterSettingsFollowupState {
	state := &clusterSettingsFollowupState{selfHeal: true, resources: map[string]string{}, labels: map[string]string{}}
	prefix := "/api/v1/org/clusters/" + clusterSettingsTestCluster
	selfHealAnswer := func(writer http.ResponseWriter) {
		writeJSON(t, writer, http.StatusOK, map[string]any{"cluster_id": clusterSettingsTestCluster, "enabled": state.selfHeal, "latest_incident": nil})
	}
	fake.handle("GET "+prefix+"/control-plane-self-heal", func(writer http.ResponseWriter, _ *http.Request) {
		if !fake.answerHidden(t, writer) {
			selfHealAnswer(writer)
		}
	})
	fake.handle("PUT "+prefix+"/control-plane-self-heal", func(writer http.ResponseWriter, request *http.Request) {
		var body map[string]any
		decodeJSON(t, request, &body)
		enabled, isBool := body["enabled"].(bool)
		if !isBool {
			writeJSON(t, writer, http.StatusUnprocessableEntity, map[string]any{"detail": "enabled is required"})
			return
		}
		state.selfHeal = enabled
		selfHealAnswer(writer)
	})
	resourcesAnswer := func(writer http.ResponseWriter) {
		answer := map[string]any{"apply_state": "applied", "message": "", "updated_at": nil}
		for _, name := range []string{"memory_limit", "memory_request", "cpu_limit", "cpu_request"} {
			if value, present := state.resources[name]; present && value != "" {
				answer[name] = value
			} else {
				answer[name] = nil
			}
		}
		writeJSON(t, writer, http.StatusOK, answer)
	}
	fake.handle("GET "+prefix+"/agent/resources", func(writer http.ResponseWriter, _ *http.Request) {
		if !fake.answerHidden(t, writer) {
			resourcesAnswer(writer)
		}
	})
	fake.handle("PUT "+prefix+"/agent/resources", func(writer http.ResponseWriter, request *http.Request) {
		var body map[string]*string
		decodeJSON(t, request, &body)
		named := false
		for name, value := range body {
			if value == nil {
				continue
			}
			named = true
			canonical := *value
			if canonical == "2048Mi" {
				canonical = "2Gi"
			}
			state.resources[name] = canonical
		}
		if !named {
			writeJSON(t, writer, http.StatusUnprocessableEntity, map[string]any{"detail": "Name at least one of memory_limit, memory_request, cpu_limit or cpu_request."})
			return
		}
		resourcesAnswer(writer)
	})
	labelsAnswer := func(writer http.ResponseWriter) {
		labels := map[string]string{}
		for key, value := range state.labels {
			labels[key] = value
		}
		writeJSON(t, writer, http.StatusOK, map[string]any{"cluster_id": clusterSettingsTestCluster, "labels": labels, "environment": nil, "reserved_keys": []string{"environment"}})
	}
	fake.handle("GET "+prefix+"/labels", func(writer http.ResponseWriter, _ *http.Request) {
		if !fake.answerHidden(t, writer) {
			labelsAnswer(writer)
		}
	})
	fake.handle("PATCH "+prefix+"/labels", func(writer http.ResponseWriter, request *http.Request) {
		var change client.ClusterLabelsChange
		decodeJSON(t, request, &change)
		if len(change.Set) == 0 && len(change.Remove) == 0 {
			writeJSON(t, writer, http.StatusUnprocessableEntity, map[string]any{"detail": "The change sets and removes nothing."})
			return
		}
		if _, reserved := change.Set["environment"]; reserved {
			writeJSON(t, writer, http.StatusUnprocessableEntity, map[string]any{"detail": "The \"environment\" label is reserved."})
			return
		}
		for _, key := range change.Remove {
			delete(state.labels, key)
		}
		for key, value := range change.Set {
			state.labels[key] = value
		}
		labelsAnswer(writer)
	})
	return state
}

func TestAccClusterAgentCIPlacementAndAdmission(t *testing.T) {
	fake := newClusterSettingsFake(t)
	config := func(admitting int, placement string) string {
		return fake.providerBlock() + fmt.Sprintf(`
resource "ankra_cluster_agent_ci_settings" "prod" {
  cluster_id            = %q
  ci_max_admitting_runs = %d
  ci_reservation_sizing = "measured"
  ci_placement          = %s
}
`, clusterSettingsTestCluster, admitting, placement)
	}
	byGroup := `{
    node_group  = " ci"
    tolerations = [{ key = "gpu ", effect = "NoSchedule" }]
  }`
	bySelector := `{
    node_selector = { "pool" = "builders" }
    mode          = "preferred"
  }`
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:      config(64, bySelector),
				PlanOnly:    true,
				ExpectError: regexp.MustCompile(`between 1 and 32`),
			},
			{
				// The platform merges the group's selector and toleration into
				// the stored placement; the configured shape stays in state.
				Config: config(4, byGroup),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ankra_cluster_agent_ci_settings.prod", "ci_max_admitting_runs", "4"),
					resource.TestCheckResourceAttr("ankra_cluster_agent_ci_settings.prod", "ci_reservation_sizing", "measured"),
					resource.TestCheckResourceAttr("ankra_cluster_agent_ci_settings.prod", "ci_placement.node_group", " ci"),
					resource.TestCheckNoResourceAttr("ankra_cluster_agent_ci_settings.prod", "ci_placement.node_selector.%"),
					resource.TestCheckResourceAttr("ankra_cluster_agent_ci_settings.prod", "ci_placement.tolerations.#", "1"),
					func(*terraform.State) error {
						stored, _ := fake.ciSettings["ci_placement"].(map[string]any)
						if stored == nil || stored["mode"] != "required" || fmt.Sprint(fake.ciSettings["ci_max_admitting_runs"]) != "4" ||
							fake.ciSettings["ci_reservation_sizing"] != "measured" {
							return fmt.Errorf("platform holds %v", fake.ciSettings)
						}
						return nil
					},
				),
			},
			// No diff after the platform resolved the node group.
			{Config: config(4, byGroup), PlanOnly: true},
			{
				Config: config(8, bySelector),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ankra_cluster_agent_ci_settings.prod", "ci_placement.mode", "preferred"),
					resource.TestCheckResourceAttr("ankra_cluster_agent_ci_settings.prod", "ci_placement.node_selector.pool", "builders"),
					resource.TestCheckNoResourceAttr("ankra_cluster_agent_ci_settings.prod", "ci_placement.node_group"),
				),
			},
			{
				// The placement changed outside Terraform: the next apply restores it.
				PreConfig: func() {
					fake.ciSettings["ci_placement"] = map[string]any{"node_selector": map[string]string{"pool": "other"}, "tolerations": []any{}, "mode": "required"}
				},
				Config: config(8, bySelector),
				Check: func(*terraform.State) error {
					stored, _ := fake.ciSettings["ci_placement"].(map[string]any)
					if stored == nil || stored["mode"] != "preferred" {
						return fmt.Errorf("drift not corrected: %v", fake.ciSettings["ci_placement"])
					}
					return nil
				},
			},
		},
	})
	if fake.ciSettings["ci_placement"] != nil || fmt.Sprint(fake.ciSettings["ci_max_admitting_runs"]) != "2" || fake.ciSettings["ci_reservation_sizing"] != "declared" {
		t.Errorf("destroy did not restore the defaults: %v", fake.ciSettings)
	}
}

func TestClusterAgentCIPlacementCovers(t *testing.T) {
	stored := client.ClusterAgentCIPlacement{
		NodeGroup:    "ci",
		NodeSelector: map[string]string{"ankra.io/node-group": "ci"},
		Tolerations:  []client.ClusterAgentCIPlacementToleration{{Key: "dedicated", Operator: "Equal", Value: "ci", Effect: "NoSchedule"}, {Key: "gpu", Operator: "Exists", Effect: "NoSchedule"}},
		Mode:         "required",
	}
	cases := []struct {
		name      string
		requested client.ClusterAgentCIPlacement
		covers    bool
	}{
		{"group only", client.ClusterAgentCIPlacement{NodeGroup: "ci"}, true},
		{"defaulted operator", client.ClusterAgentCIPlacement{NodeGroup: "ci", Tolerations: []client.ClusterAgentCIPlacementToleration{{Key: "gpu", Effect: "NoSchedule"}}}, true},
		{"other group", client.ClusterAgentCIPlacement{NodeGroup: "build"}, false},
		{"other mode", client.ClusterAgentCIPlacement{NodeGroup: "ci", Mode: "preferred"}, false},
		{"missing selector", client.ClusterAgentCIPlacement{NodeGroup: "ci", NodeSelector: map[string]string{"pool": "x"}}, false},
		{"missing toleration", client.ClusterAgentCIPlacement{NodeGroup: "ci", Tolerations: []client.ClusterAgentCIPlacementToleration{{Key: "spot", Effect: "NoSchedule"}}}, false},
	}
	for _, testCase := range cases {
		if covers := clusterAgentCIPlacementCovers(stored, testCase.requested); covers != testCase.covers {
			t.Errorf("%s: covers = %v, want %v", testCase.name, covers, testCase.covers)
		}
	}
}

func TestAccClusterControlPlaneSelfHealAgentResourcesAndLabels(t *testing.T) {
	fake := newClusterSettingsFake(t)
	state := fake.addFollowupRoutes(t)
	state.labels["team"] = "portal-set"
	config := func(selfHeal bool, memory string, labels string) string {
		return fake.providerBlock() + fmt.Sprintf(`
resource "ankra_cluster_control_plane_self_heal" "prod" {
  cluster_id = %q
  enabled    = %t
}

resource "ankra_cluster_agent_resources" "prod" {
  cluster_id   = %q
  memory_limit = %q
  cpu_request  = "100m"
}

resource "ankra_cluster_labels" "prod" {
  cluster_id = %q
  labels     = %s
}
`, clusterSettingsTestCluster, selfHeal, clusterSettingsTestCluster, memory, clusterSettingsTestCluster, labels)
	}
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:      config(true, "1Gi", `{ environment = "prod" }`),
				PlanOnly:    true,
				ExpectError: regexp.MustCompile(`Reserved label key`),
			},
			{
				Config:      config(true, "1Gi", `{ "Upper" = "x" }`),
				PlanOnly:    true,
				ExpectError: regexp.MustCompile(`Invalid label key`),
			},
			{
				// A label added in the portal is not removed silently on create.
				Config:      config(false, "2048Mi", `{ tier = "gold", region = "eu" }`),
				ExpectError: regexp.MustCompile(`Cluster already has labels`),
			},
			{
				PreConfig: func() { delete(state.labels, "team"); state.labels["tier"] = "bronze" },
				Config:    config(false, "2048Mi", `{ tier = "gold", region = "eu" }`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ankra_cluster_control_plane_self_heal.prod", "enabled", "false"),
					// The platform stores 2Gi; the configured spelling is kept.
					resource.TestCheckResourceAttr("ankra_cluster_agent_resources.prod", "memory_limit", "2048Mi"),
					resource.TestCheckResourceAttr("ankra_cluster_agent_resources.prod", "cpu_request", "100m"),
					resource.TestCheckResourceAttr("ankra_cluster_agent_resources.prod", "memory_request", ""),
					resource.TestCheckResourceAttr("ankra_cluster_labels.prod", "labels.%", "2"),
					func(*terraform.State) error {
						if state.selfHeal || state.resources["memory_limit"] != "2Gi" {
							return fmt.Errorf("platform holds self-heal %v resources %v", state.selfHeal, state.resources)
						}
						if _, kept := state.labels["team"]; kept || state.labels["tier"] != "gold" {
							return fmt.Errorf("labels not converged: %v", state.labels)
						}
						return nil
					},
				),
			},
			{Config: config(false, "2048Mi", `{ tier = "gold", region = "eu" }`), PlanOnly: true},
			{
				Config: config(true, "1Gi", `{ tier = "silver" }`),
				Check: func(*terraform.State) error {
					if !state.selfHeal || state.resources["memory_limit"] != "1Gi" || len(state.labels) != 1 || state.labels["tier"] != "silver" {
						return fmt.Errorf("platform holds self-heal %v resources %v labels %v", state.selfHeal, state.resources, state.labels)
					}
					return nil
				},
			},
			{ResourceName: "ankra_cluster_control_plane_self_heal.prod", ImportState: true, ImportStateId: clusterSettingsTestCluster, ImportStateVerify: true},
			{ResourceName: "ankra_cluster_labels.prod", ImportState: true, ImportStateId: clusterSettingsTestCluster, ImportStateVerify: true},
			{
				// Changed outside Terraform: the next apply restores the configuration.
				PreConfig: func() {
					state.selfHeal = false
					state.resources["memory_limit"] = "4Gi"
					state.labels["added"] = "outside"
				},
				Config: config(true, "1Gi", `{ tier = "silver" }`),
				Check: func(*terraform.State) error {
					if !state.selfHeal || state.resources["memory_limit"] != "1Gi" || len(state.labels) != 1 {
						return fmt.Errorf("drift not corrected: self-heal %v resources %v labels %v", state.selfHeal, state.resources, state.labels)
					}
					return nil
				},
			},
		},
	})
	if !state.selfHeal || state.resources["memory_limit"] != "" || state.resources["cpu_request"] != "" || len(state.labels) != 0 {
		t.Errorf("destroy did not restore the defaults: self-heal %v resources %v labels %v", state.selfHeal, state.resources, state.labels)
	}
}

func TestAccClusterFollowupSettingsOutOfScope(t *testing.T) {
	fake := newClusterSettingsFake(t)
	fake.addFollowupRoutes(t)
	fake.clusterHidden = true
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: fake.providerBlock() + fmt.Sprintf(`
resource "ankra_cluster_labels" "prod" {
  cluster_id = %q
  labels     = { tier = "gold" }
}
`, clusterSettingsTestCluster),
				ExpectError: regexp.MustCompile(`not visible in the provider's\s+organisation\s+and\s+project\s+scope`),
			},
		},
	})
}

// stackProtectionFake models the stack protect, unprotect and backup
// posture routes: the
// policy names the vault by name whether the write named it by name or id,
// unprotect needs the stack name as confirmation, and the posture answers
// unprotected/no_policy for a stack without a policy.
type stackProtectionFake struct {
	*routeFake
	vaultName  string
	schedule   string
	protects   []map[string]any
	unprotects int
	// policyUnreadable makes the posture answer unknown/policy_unreadable,
	// with no vault or schedule.
	policyUnreadable bool
}

func newStackProtectionFake(t *testing.T) *stackProtectionFake {
	fake := &stackProtectionFake{routeFake: newRouteFake(t)}
	prefix := "/api/v1/org/clusters/imported/" + clusterSettingsTestCluster + "/stacks/"
	fake.handle("POST "+prefix+"{stack}/protect", func(writer http.ResponseWriter, request *http.Request) {
		if request.PathValue("stack") != "shop" {
			writeJSON(t, writer, http.StatusNotFound, map[string]any{"detail": "Stack not found on this cluster"})
			return
		}
		var body map[string]any
		decodeJSON(t, request, &body)
		fake.protects = append(fake.protects, body)
		vault, _ := body["vault_id"].(string)
		if vault == "" {
			writeJSON(t, writer, http.StatusUnprocessableEntity, map[string]any{"detail": "vault_id is required"})
			return
		}
		if vault == "6f0e7f6a-0000-4000-8000-000000000001" {
			vault = "production-backups"
		}
		fake.vaultName = vault
		fake.schedule, _ = body["schedule"].(string)
		writeJSON(t, writer, http.StatusOK, map[string]any{"stack_name": "shop", "policy": map[string]any{"enabled": true, "vault": vault, "schedule": "0 1 * * *"},
			"backup_stack": "installing", "warnings": []string{}})
	})
	fake.handle("DELETE "+prefix+"{stack}/protect", func(writer http.ResponseWriter, request *http.Request) {
		var body map[string]string
		decodeJSON(t, request, &body)
		if body["confirm"] != request.PathValue("stack") {
			writeJSON(t, writer, http.StatusUnprocessableEntity, map[string]any{"detail": "Type the stack name to confirm"})
			return
		}
		if request.PathValue("stack") != "shop" {
			writeJSON(t, writer, http.StatusNotFound, map[string]any{"detail": "Stack not found on this cluster"})
			return
		}
		fake.unprotects++
		fake.vaultName = ""
		fake.schedule = ""
		writeJSON(t, writer, http.StatusOK, map[string]any{"stack_name": "shop", "policy": map[string]any{"enabled": false}, "warnings": []string{}})
	})
	fake.handle("GET "+prefix+"{stack}/backup-posture", func(writer http.ResponseWriter, request *http.Request) {
		if request.PathValue("stack") != "shop" {
			writeJSON(t, writer, http.StatusNotFound, map[string]any{"detail": "Stack not found"})
			return
		}
		posture := map[string]any{"cluster_id": clusterSettingsTestCluster, "stack_name": "shop", "stateful": true}
		if fake.policyUnreadable {
			posture["protection_state"] = "unknown"
			posture["unknown_reason"] = "policy_unreadable"
		} else if fake.vaultName == "" {
			posture["protection_state"] = "unprotected"
			posture["unprotected_reason"] = "no_policy"
		} else {
			posture["protection_state"] = "protected"
			posture["vault_name"] = fake.vaultName
			// The platform reports its default for a policy written without one.
			posture["schedule"] = "daily"
			if fake.schedule != "" {
				posture["schedule"] = fake.schedule
			}
		}
		writeJSON(t, writer, http.StatusOK, posture)
	})
	return fake
}

func TestAccStackBackupProtectionLifecycle(t *testing.T) {
	fake := newStackProtectionFake(t)
	config := func(vault, schedule string) string {
		return fake.providerBlock() + fmt.Sprintf(`
resource "ankra_stack_backup_protection" "shop" {
  cluster_id = %q
  stack_name = "shop"
  vault      = %q
  schedule   = %q
  retention = {
    daily  = 7
    weekly = 4
  }
  selection = {
    persistent_volume_claims = ["shop/uploads"]
  }
  backup_now = true
}
`, clusterSettingsTestCluster, vault, schedule)
	}
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: config("6f0e7f6a-0000-4000-8000-000000000001", "daily"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ankra_stack_backup_protection.shop", "id", clusterSettingsTestCluster+"/shop"),
					resource.TestCheckResourceAttr("ankra_stack_backup_protection.shop", "vault_name", "production-backups"),
					resource.TestCheckResourceAttr("ankra_stack_backup_protection.shop", "protection_state", "protected"),
					func(*terraform.State) error {
						if len(fake.protects) != 1 || fake.protects[0]["backup_now"] != true {
							return fmt.Errorf("protect requests = %v, want one taking a restore point now", fake.protects)
						}
						retention, _ := fake.protects[0]["retention"].(map[string]any)
						selection, _ := fake.protects[0]["selection"].(map[string]any)
						if fmt.Sprint(retention["daily"]) != "7" || fmt.Sprint(selection["persistent_volume_claims"]) != "[shop/uploads]" {
							return fmt.Errorf("protect body = %v", fake.protects[0])
						}
						return nil
					},
				),
			},
			// The vault named by id reads back by name: no diff.
			{Config: config("6f0e7f6a-0000-4000-8000-000000000001", "daily"), PlanOnly: true},
			{
				Config: config("production-backups", "weekly"),
				Check: func(*terraform.State) error {
					if len(fake.protects) != 2 || fake.protects[1]["backup_now"] != nil || fake.schedule != "weekly" {
						return fmt.Errorf("update protect = %v, want the policy rewritten without a new restore point", fake.protects)
					}
					return nil
				},
			},
			{
				ResourceName:            "ankra_stack_backup_protection.shop",
				ImportState:             true,
				ImportStateId:           clusterSettingsTestCluster + "/shop",
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"retention", "selection", "backup_now"},
			},
			{
				// Unprotected outside Terraform: the next apply protects it again.
				PreConfig: func() { fake.vaultName = "" },
				Config:    config("production-backups", "weekly"),
				Check: func(*terraform.State) error {
					if fake.vaultName != "production-backups" || len(fake.protects) != 3 {
						return fmt.Errorf("not protected again: vault %q protects %d", fake.vaultName, len(fake.protects))
					}
					return nil
				},
			},
			{
				// The vault changed outside Terraform: the next apply restores it.
				PreConfig: func() { fake.vaultName = "other-vault" },
				Config:    config("production-backups", "weekly"),
				Check: func(*terraform.State) error {
					if fake.vaultName != "production-backups" {
						return fmt.Errorf("vault drift not corrected: %q", fake.vaultName)
					}
					return nil
				},
			},
		},
	})
	if fake.unprotects != 1 || fake.vaultName != "" {
		t.Errorf("destroy unprotected %d times, vault %q; want once", fake.unprotects, fake.vaultName)
	}
}

// TestAccStackBackupProtectionDefaultScheduleAndUnreadablePolicy: an unset
// schedule reads back as the platform default without a diff, and a policy
// the platform cannot read keeps the resource instead of re-protecting.
func TestAccStackBackupProtectionDefaultScheduleAndUnreadablePolicy(t *testing.T) {
	fake := newStackProtectionFake(t)
	config := fake.providerBlock() + fmt.Sprintf(`
resource "ankra_stack_backup_protection" "shop" {
  cluster_id = %q
  stack_name = "shop"
  vault      = "production-backups"
}
`, clusterSettingsTestCluster)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: config,
				Check:  resource.TestCheckNoResourceAttr("ankra_stack_backup_protection.shop", "schedule"),
			},
			{Config: config, PlanOnly: true},
			{
				PreConfig: func() { fake.policyUnreadable = true },
				Config:    config,
				PlanOnly:  true,
			},
			{
				Config: config,
				Check: func(*terraform.State) error {
					if len(fake.protects) != 1 {
						return fmt.Errorf("protected %d times; an unreadable policy must not be overwritten", len(fake.protects))
					}
					fake.policyUnreadable = false
					return nil
				},
			},
		},
	})
}

func TestAccStackBackupProtectionMissingStack(t *testing.T) {
	fake := newStackProtectionFake(t)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: fake.providerBlock() + fmt.Sprintf(`
resource "ankra_stack_backup_protection" "missing" {
  cluster_id = %q
  stack_name = "missing"
  vault      = "production-backups"
}
`, clusterSettingsTestCluster),
				ExpectError: regexp.MustCompile(`Stack not found on this cluster`),
			},
			{
				Config: fake.providerBlock() + fmt.Sprintf(`
resource "ankra_stack_backup_protection" "slash" {
  cluster_id = %q
  stack_name = "a/b"
  vault      = "production-backups"
}
`, clusterSettingsTestCluster),
				PlanOnly:    true,
				ExpectError: regexp.MustCompile(`cannot contain "/"`),
			},
		},
	})
}

func TestAddonSettingsToAPI(t *testing.T) {
	members := memberKinds{}
	var diagnostics diag.Diagnostics
	addon := addonModel{
		Settings: &addonSettingsModel{
			RevisionHistoryLimit: types.Int64Value(3),
			SyncPolicy: &addonSyncPolicyModel{
				SelfHeal:    types.BoolValue(false),
				SyncOptions: listFromStrings([]string{}),
			},
			RetryPolicy: &addonRetryPolicyModel{Limit: types.Int64Value(2), BackoffDuration: types.StringValue("10s")},
			IgnoreDifferences: []addonIgnoreDifferenceModel{{
				Kind:         types.StringValue("Deployment"),
				JSONPointers: listFromStrings([]string{"/spec/replicas"}),
			}},
		},
	}
	apiAddon := addonToAPI(addon, members, path.Root("addons"), &diagnostics)
	encoded, err := json.Marshal(apiAddon.Settings)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"sync_policy":{"self_heal":false,"sync_options":[]},"ignore_differences":[{"kind":"Deployment","json_pointers":["/spec/replicas"]}],` +
		`"retry_policy":{"limit":2,"backoff":{"duration":"10s"}},"revision_history_limit":3}`
	if string(encoded) != want {
		t.Errorf("settings = %s\nwant       %s", encoded, want)
	}
	withoutSettings := addonToAPI(addonModel{}, members, path.Root("addons"), &diagnostics)
	encoded, _ = json.Marshal(withoutSettings)
	if strings.Contains(string(encoded), "settings") {
		t.Errorf("an addon without a settings block sent %s; the platform must keep its stored settings", encoded)
	}
}

// TestAccClusterFollowupResourcesResolveProjectSlug: with a provider scoped
// by project slug, every request of the new resources carries the project's
// ID, and a destroy without refresh does not read the slug as a scope move.
func TestAccClusterFollowupResourcesResolveProjectSlug(t *testing.T) {
	const projectID = "7d3f0c1e-2222-4333-8444-955556666777"
	fake := newClusterSettingsFake(t)
	fake.addFollowupRoutes(t)
	listings := 0
	registerProjects(fake.routeFake, map[string]string{"platform": projectID}, &listings)
	protection := newStackProtectionFake(t)
	registerProjects(protection.routeFake, map[string]string{"platform": projectID}, &listings)
	settingsConfig := slugProviderBlock(fake.routeFake, "platform") + fmt.Sprintf(`
resource "ankra_cluster_control_plane_self_heal" "prod" {
  cluster_id = %q
  enabled    = false
}

resource "ankra_cluster_labels" "prod" {
  cluster_id = %q
  labels     = { tier = "gold" }
}

resource "ankra_cluster_agent_resources" "prod" {
  cluster_id  = %q
  cpu_request = "100m"
}
`, clusterSettingsTestCluster, clusterSettingsTestCluster, clusterSettingsTestCluster)
	protectionConfig := slugProviderBlock(protection.routeFake, "platform") + fmt.Sprintf(`
resource "ankra_stack_backup_protection" "shop" {
  cluster_id = %q
  stack_name = "shop"
  vault      = "production-backups"
}
`, clusterSettingsTestCluster)
	for _, testCase := range []struct {
		fake   *routeFake
		config string
	}{{fake.routeFake, settingsConfig}, {protection.routeFake, protectionConfig}} {
		resource.Test(t, resource.TestCase{
			ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{Config: testCase.config},
				{Config: testCase.config, Destroy: true, PreventPostDestroyRefresh: true},
			},
		})
		if outside := testCase.fake.requestsOutsideProject(projectID); len(outside) != 0 {
			t.Errorf("requests not scoped to the resolved project: %v", outside)
		}
	}
	if listings == 0 {
		t.Error("the slug was never resolved against the project listing")
	}
	if protection.unprotects != 1 {
		t.Errorf("destroy unprotected %d times, want once", protection.unprotects)
	}
}
