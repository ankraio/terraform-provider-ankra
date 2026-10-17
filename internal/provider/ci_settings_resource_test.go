// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

const (
	ciTestCluster      = "12121212-0000-0000-0000-000000000001"
	ciTestOtherCluster = "12121212-0000-0000-0000-000000000002"
)

// ciFake holds the CI settings and pool the way the token surface does
// (go/internal/appsapi/cisettings.go, cipool.go): every settings member is
// tri-state (absent keeps, a null ci_cluster_id clears, a null anywhere else
// or a platform_builds_enabled member is a 422), an empty body is a 422, a
// cluster outside the organisation is a 422; a pool PUT answers 201 for a
// newly listed cluster and 200 for a re-weight, and DELETE answers the
// remaining pool or 404 for a cluster that is not listed.
type ciFake struct {
	*routeFake
	settings map[string]any
	pool     map[string]int64
	writes   []map[string]json.RawMessage
}

func ciFakeDefaults() map[string]any {
	return map[string]any{
		"ci_cluster_id": nil, "ci_build_fallback": "platform_builders", "ci_max_parallel_runs": float64(16),
		"ci_max_parallel_steps": float64(8), "ci_allowed_image_prefixes": []any{}, "ci_artifact_retention_days": float64(30),
		"ci_cache_retention_days": float64(14), "ci_run_retention_days": float64(90), "ci_image_gate": "app",
		"ci_ignore_unfixed": true, "ci_egress_allowed_cidrs": []any{}, "ci_always_run_stages": []any{},
		"ci_full_run_on_default_branch": false, "ci_skip_markers": "except_default_branch",
	}
}

func newCIFake(t *testing.T) *ciFake {
	fake := &ciFake{routeFake: newRouteFake(t), settings: ciFakeDefaults(), pool: map[string]int64{}}
	clusters := map[string]string{ciTestCluster: "builder", ciTestOtherCluster: "spare"}

	render := func() map[string]any {
		row := map[string]any{"platform_builds_enabled": true, "updated_at": nil, "ci_cluster_name": nil}
		for key, value := range fake.settings {
			row[key] = value
		}
		if identifier, isSet := fake.settings["ci_cluster_id"].(string); isSet {
			row["ci_cluster_name"] = clusters[identifier]
		}
		row["is_default"] = reflect.DeepEqual(fake.settings, ciFakeDefaults())
		return row
	}
	renderPool := func() map[string]any {
		members := []map[string]any{}
		for _, identifier := range []string{ciTestCluster, ciTestOtherCluster} {
			if weight, listed := fake.pool[identifier]; listed {
				members = append(members, map[string]any{
					"cluster_id": identifier, "cluster_name": clusters[identifier], "weight": weight,
					"is_primary": fake.settings["ci_cluster_id"] == identifier, "is_listed": true, "created_at": nil,
				})
			}
		}
		return map[string]any{"is_pooled": len(members) > 0, "members": members}
	}

	fake.handle("GET /api/v1/org/ci-settings", func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(t, writer, http.StatusOK, render())
	})
	fake.handle("PUT /api/v1/org/ci-settings", func(writer http.ResponseWriter, request *http.Request) {
		var body map[string]json.RawMessage
		decodeJSON(t, request, &body)
		fake.writes = append(fake.writes, body)
		if len(body) == 0 {
			writeJSON(t, writer, http.StatusUnprocessableEntity, map[string]any{"detail": []map[string]any{{"msg": "at least one CI setting must be named"}}})
			return
		}
		if _, present := body["platform_builds_enabled"]; present {
			writeJSON(t, writer, http.StatusUnprocessableEntity, map[string]any{"detail": []map[string]any{{"msg": "platform_builds_enabled is Ankra's grant"}}})
			return
		}
		for key, raw := range body {
			var value any
			_ = json.Unmarshal(raw, &value)
			if value == nil && key != "ci_cluster_id" {
				writeJSON(t, writer, http.StatusUnprocessableEntity, map[string]any{"detail": []map[string]any{{"msg": key + " must not be null"}}})
				return
			}
			if key == "ci_cluster_id" && value != nil {
				if _, owned := clusters[fmt.Sprint(value)]; !owned {
					writeJSON(t, writer, http.StatusUnprocessableEntity, map[string]any{"detail": []map[string]any{{"msg": "Cluster not found in this organisation."}}})
					return
				}
			}
		}
		for key, raw := range body {
			var value any
			_ = json.Unmarshal(raw, &value)
			fake.settings[key] = value
		}
		writeJSON(t, writer, http.StatusOK, render())
	})
	fake.handle("GET /api/v1/org/ci-settings/pool", func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(t, writer, http.StatusOK, renderPool())
	})
	fake.handle("PUT /api/v1/org/ci-settings/pool/{cluster_id}", func(writer http.ResponseWriter, request *http.Request) {
		identifier := request.PathValue("cluster_id")
		if _, owned := clusters[identifier]; !owned {
			writeJSON(t, writer, http.StatusNotFound, map[string]any{"detail": "Cluster not found in this organisation."})
			return
		}
		var body map[string]any
		decodeJSON(t, request, &body)
		weight := int64(100)
		if value, present := body["weight"].(float64); present {
			weight = int64(value)
		}
		_, wasListed := fake.pool[identifier]
		fake.pool[identifier] = weight
		status := http.StatusCreated
		if wasListed {
			status = http.StatusOK
		}
		writeJSON(t, writer, status, renderPool())
	})
	fake.handle("DELETE /api/v1/org/ci-settings/pool/{cluster_id}", func(writer http.ResponseWriter, request *http.Request) {
		identifier := request.PathValue("cluster_id")
		if _, listed := fake.pool[identifier]; !listed {
			writeJSON(t, writer, http.StatusNotFound, map[string]any{"detail": "That cluster is not listed in the CI pool."})
			return
		}
		delete(fake.pool, identifier)
		writeJSON(t, writer, http.StatusOK, renderPool())
	})
	return fake
}

func TestAccCISettingsLifecycle(t *testing.T) {
	fake := newCIFake(t)
	// Set in the portal before Terraform: a pipeline cluster, an image
	// policy and a scheduler share. None of it is in the configuration.
	fake.settings["ci_cluster_id"] = ciTestCluster
	fake.settings["ci_allowed_image_prefixes"] = []any{"ghcr.io/portal/"}
	fake.settings["ci_max_parallel_steps"] = float64(32)
	gated := fake.providerBlock() + fmt.Sprintf(`
resource "ankra_ci_settings" "this" {
  ci_max_parallel_runs = 4
  ci_image_gate        = "all"
}

resource "ankra_ci_pool_member" "spare" {
  cluster_id = %q
  weight     = 50
}
`, ciTestOtherCluster)
	runsOnly := fake.providerBlock() + fmt.Sprintf(`
resource "ankra_ci_settings" "this" {
  ci_max_parallel_runs = 4
}

resource "ankra_ci_pool_member" "spare" {
  cluster_id = %q
  weight     = 70
}
`, ciTestOtherCluster)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy: func(_ *terraform.State) error {
			if fake.settings["ci_max_parallel_runs"] != float64(16) {
				return fmt.Errorf("destroy did not reset the managed ci_max_parallel_runs: %v", fake.settings["ci_max_parallel_runs"])
			}
			if fake.settings["ci_cluster_id"] != ciTestCluster || fake.settings["ci_max_parallel_steps"] != float64(32) ||
				!reflect.DeepEqual(fake.settings["ci_allowed_image_prefixes"], []any{"ghcr.io/portal/"}) || fake.settings["ci_image_gate"] != "all" {
				return fmt.Errorf("destroy touched settings Terraform did not manage: %v", fake.settings)
			}
			if len(fake.pool) != 0 {
				return fmt.Errorf("pool members left after destroy: %v", fake.pool)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: gated,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ankra_ci_settings.this", "ci_cluster_id", ciTestCluster),
					resource.TestCheckResourceAttr("ankra_ci_settings.this", "ci_cluster_name", "builder"),
					resource.TestCheckResourceAttr("ankra_ci_settings.this", "ci_allowed_image_prefixes.0", "ghcr.io/portal/"),
					resource.TestCheckResourceAttr("ankra_ci_settings.this", "ci_max_parallel_steps", "32"),
					resource.TestCheckResourceAttr("ankra_ci_settings.this", "platform_builds_enabled", "true"),
					resource.TestCheckResourceAttr("ankra_ci_pool_member.spare", "weight", "50"),
					resource.TestCheckResourceAttr("ankra_ci_pool_member.spare", "cluster_name", "spare"),
					func(_ *terraform.State) error {
						write := fake.writes[0]
						if len(write) != 2 || string(write["ci_image_gate"]) != `"all"` || string(write["ci_max_parallel_runs"]) != "4" {
							return fmt.Errorf("the settings write must name only the configured settings, got %v", write)
						}
						return nil
					},
				),
			},
			{
				Config: runsOnly,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ankra_ci_settings.this", "ci_image_gate", "all"),
					resource.TestCheckResourceAttr("ankra_ci_settings.this", "ci_cluster_id", ciTestCluster),
					resource.TestCheckResourceAttr("ankra_ci_pool_member.spare", "weight", "70"),
					func(_ *terraform.State) error {
						write := fake.writes[len(fake.writes)-1]
						if len(write) != 1 {
							return fmt.Errorf("an attribute removed from configuration must stop being written, got %v", write)
						}
						return nil
					},
				),
			},
			{
				ResourceName:      "ankra_ci_settings.this",
				ImportState:       true,
				ImportStateId:     "anything",
				ImportStateVerify: true,
			},
			{
				ResourceName:      "ankra_ci_pool_member.spare",
				ImportState:       true,
				ImportStateId:     ciTestOtherCluster,
				ImportStateVerify: true,
			},
			{
				PreConfig: func() {
					fake.settings["ci_max_parallel_runs"] = float64(32)
					delete(fake.pool, ciTestOtherCluster)
				},
				Config: runsOnly,
				Check: func(_ *terraform.State) error {
					if fake.settings["ci_max_parallel_runs"] != float64(4) || fake.pool[ciTestOtherCluster] != 70 {
						return fmt.Errorf("drift was not corrected: settings %v, pool %v", fake.settings, fake.pool)
					}
					return nil
				},
			},
		},
	})
}
