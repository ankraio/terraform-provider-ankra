// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

const (
	osPatchFakeClusterID         = "0b6f1d2e-1111-4222-8333-944445555001"
	osPatchFakeImportedClusterID = "0b6f1d2e-1111-4222-8333-944445555002"
)

// osPatchFake mirrors the OS patch lane's status and policy routes
// (go/internal/providerapi/ospatch.go): the status read carries the policy,
// and the PATCH merges the members it names onto the stored policy and
// answers the merged policy.
type osPatchFake struct {
	*routeFake
	enabled              bool
	days                 []string
	startHour            int
	durationHours        int
	cadenceDays          int
	includeStatefulNodes bool
	securityLagDays      int
	stored               bool
	patches              []map[string]any
	runInFlight          bool
}

func newOSPatchFake(t *testing.T) *osPatchFake {
	fake := &osPatchFake{routeFake: newRouteFake(t), days: []string{"mon", "tue", "wed", "thu"}, startHour: 2, durationHours: 4,
		cadenceDays: 7, securityLagDays: 14}
	kinds := map[string]string{osPatchFakeClusterID: "hetzner", osPatchFakeImportedClusterID: "imported"}
	fake.handle("GET /api/v1/clusters", func(writer http.ResponseWriter, request *http.Request) {
		wanted := request.URL.Query().Get("cluster_id")
		rows := []map[string]any{}
		for id, kind := range kinds {
			if wanted == "" || wanted == id {
				rows = append(rows, map[string]any{"id": id, "name": "cluster-" + kind, "kind": kind, "state": "running"})
			}
		}
		writeJSON(t, writer, http.StatusOK, map[string]any{
			"result":     rows,
			"pagination": map[string]any{"total_count": len(rows), "total_pages": 1, "page": 1, "page_size": 100},
		})
	})
	lookup := func(writer http.ResponseWriter, request *http.Request) bool {
		if request.PathValue("lane") != "hetzner" || request.PathValue("id") != osPatchFakeClusterID {
			writeJSON(t, writer, http.StatusNotFound, map[string]any{"detail": "Cluster not found"})
			return false
		}
		return true
	}
	fake.handle("GET /api/v1/clusters/{lane}/{id}/os-patches", func(writer http.ResponseWriter, request *http.Request) {
		if lookup(writer, request) {
			writeJSON(t, writer, http.StatusOK, map[string]any{"policy": fake.policy(), "security_lag": map[string]any{"lagging": false}})
		}
	})
	fake.handle("PATCH /api/v1/clusters/{lane}/{id}/os-patches/policy", func(writer http.ResponseWriter, request *http.Request) {
		if !lookup(writer, request) {
			return
		}
		var body map[string]any
		decodeJSON(t, request, &body)
		fake.patches = append(fake.patches, body)
		if enabled, set := body["enabled"].(bool); set {
			if fake.enabled && !enabled && fake.runInFlight {
				writeJSON(t, writer, http.StatusConflict, map[string]any{"detail": "The OS patch policy was not changed: an OS patch run is in progress."})
				return
			}
			fake.enabled = enabled
		}
		if days, set := body["window_days"].([]any); set {
			fake.days = []string{}
			for _, day := range days {
				fake.days = append(fake.days, day.(string))
			}
			slices.SortFunc(fake.days, func(left, right string) int {
				return slices.Index(osPatchWindowDays, left) - slices.Index(osPatchWindowDays, right)
			})
		}
		for name, target := range map[string]*int{
			"window_start_hour_utc": &fake.startHour, "window_duration_hours": &fake.durationHours,
			"cadence_days": &fake.cadenceDays, "security_lag_days": &fake.securityLagDays,
		} {
			if value, set := body[name].(float64); set {
				*target = int(value)
			}
		}
		if include, set := body["include_stateful_nodes"].(bool); set {
			fake.includeStatefulNodes = include
		}
		fake.stored = true
		writeJSON(t, writer, http.StatusOK, fake.policy())
	})
	return fake
}

func (fake *osPatchFake) policy() map[string]any {
	return map[string]any{
		"enabled":                fake.enabled,
		"window":                 map[string]any{"days": fake.days, "start_hour_utc": fake.startHour, "duration_hours": fake.durationHours},
		"cadence_days":           fake.cadenceDays,
		"include_stateful_nodes": fake.includeStatefulNodes,
		"security_lag_days":      fake.securityLagDays,
		"stored":                 fake.stored,
		"window_description":     fmt.Sprintf("%v from %02d:00 UTC for %d hours", fake.days, fake.startHour, fake.durationHours),
		"window_open_now":        false,
	}
}

func TestAccClusterOSPatchPolicyLifecycle(t *testing.T) {
	fake := newOSPatchFake(t)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy: func(_ *terraform.State) error {
			if fake.enabled || !fake.stored {
				return fmt.Errorf("destroy must leave the stored policy as it is: enabled %t, stored %t", fake.enabled, fake.stored)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: fake.providerBlock() + fmt.Sprintf(`
resource "ankra_cluster_os_patch_policy" "patching" {
  cluster_id  = %q
  enabled     = true
  window_days = ["sat", "sun"]
}
`, osPatchFakeClusterID),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ankra_cluster_os_patch_policy.patching", "id", osPatchFakeClusterID),
					resource.TestCheckResourceAttr("ankra_cluster_os_patch_policy.patching", "cluster_kind", "hetzner"),
					resource.TestCheckResourceAttr("ankra_cluster_os_patch_policy.patching", "enabled", "true"),
					resource.TestCheckResourceAttr("ankra_cluster_os_patch_policy.patching", "window_days.#", "2"),
					resource.TestCheckResourceAttr("ankra_cluster_os_patch_policy.patching", "window_start_hour_utc", "2"),
					resource.TestCheckResourceAttr("ankra_cluster_os_patch_policy.patching", "cadence_days", "7"),
					resource.TestCheckResourceAttr("ankra_cluster_os_patch_policy.patching", "security_lag_days", "14"),
					func(_ *terraform.State) error {
						if _, sent := fake.patches[0]["cadence_days"]; sent {
							return fmt.Errorf("an unset attribute must not be written: %v", fake.patches[0])
						}
						return nil
					},
				),
			},
			{
				Config: fake.providerBlock() + fmt.Sprintf(`
resource "ankra_cluster_os_patch_policy" "patching" {
  cluster_id             = %q
  enabled                = false
  window_days            = ["sat", "sun"]
  window_start_hour_utc  = 22
  window_duration_hours  = 6
  cadence_days           = 14
  include_stateful_nodes = true
  security_lag_days      = 30
}
`, osPatchFakeClusterID),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ankra_cluster_os_patch_policy.patching", "enabled", "false"),
					resource.TestCheckResourceAttr("ankra_cluster_os_patch_policy.patching", "window_start_hour_utc", "22"),
					resource.TestCheckResourceAttr("ankra_cluster_os_patch_policy.patching", "window_duration_hours", "6"),
					resource.TestCheckResourceAttr("ankra_cluster_os_patch_policy.patching", "include_stateful_nodes", "true"),
					resource.TestCheckResourceAttr("ankra_cluster_os_patch_policy.patching", "security_lag_days", "30"),
				),
			},
			{
				ResourceName:      "ankra_cluster_os_patch_policy.patching",
				ImportState:       true,
				ImportStateId:     osPatchFakeClusterID,
				ImportStateVerify: true,
			},
			{
				PreConfig: func() { fake.cadenceDays = 3 },
				Config: fake.providerBlock() + fmt.Sprintf(`
resource "ankra_cluster_os_patch_policy" "patching" {
  cluster_id             = %q
  enabled                = false
  window_days            = ["sat", "sun"]
  window_start_hour_utc  = 22
  window_duration_hours  = 6
  cadence_days           = 14
  include_stateful_nodes = true
  security_lag_days      = 30
}
`, osPatchFakeClusterID),
				Check: func(_ *terraform.State) error {
					if fake.cadenceDays != 14 {
						return fmt.Errorf("a cadence changed outside Terraform must be set back, got %d", fake.cadenceDays)
					}
					return nil
				},
			},
		},
	})
}

func TestAccClusterOSPatchPolicyRefusals(t *testing.T) {
	fake := newOSPatchFake(t)
	fake.enabled = true
	fake.runInFlight = true
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: fake.providerBlock() + fmt.Sprintf(`
resource "ankra_cluster_os_patch_policy" "patching" {
  cluster_id = %q
  enabled    = true
}
`, osPatchFakeImportedClusterID),
				ExpectError: regexp.MustCompile(`Cluster has no OS patch policy`),
			},
			{
				Config: fake.providerBlock() + fmt.Sprintf(`
resource "ankra_cluster_os_patch_policy" "patching" {
  cluster_id  = %q
  window_days = ["Saturday"]
}
`, osPatchFakeClusterID),
				ExpectError: regexp.MustCompile(`Invalid window day`),
			},
			{
				Config: fake.providerBlock() + fmt.Sprintf(`
resource "ankra_cluster_os_patch_policy" "patching" {
  cluster_id            = %q
  window_duration_hours = 24
}
`, osPatchFakeClusterID),
				ExpectError: regexp.MustCompile(`Value out of range`),
			},
			{
				Config: fake.providerBlock() + fmt.Sprintf(`
resource "ankra_cluster_os_patch_policy" "patching" {
  cluster_id = %q
  enabled    = false
}
`, osPatchFakeClusterID),
				ExpectError: regexp.MustCompile(`(?s)Unable to set the OS patch policy.*progress`),
			},
		},
	})
}
