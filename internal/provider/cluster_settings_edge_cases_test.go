// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"ankra.io/terraform-provider-ankra/internal/client"
)

// clusterSettingsFreezeClock moves the provider's plan-time clock to at for
// the rest of the test.
func clusterSettingsFreezeClock(t *testing.T, at time.Time) {
	t.Cleanup(func() { clusterSettingsNow = time.Now })
	clusterSettingsNow = func() time.Time { return at }
}

func TestAccClusterAccessGrantReasonRemovalAndExpiry(t *testing.T) {
	fake := newClusterSettingsFake(t)
	withReason := fake.providerBlock() + fmt.Sprintf(`
resource "ankra_cluster_access_grant" "ops" {
  cluster_id = %q
  user_email = "ops@example.com"
  role       = "edit"
  expires_at = "2030-01-01T00:00:00Z"
  reason     = "migration night"
}
`, clusterSettingsTestCluster)
	withoutReason := fake.providerBlock() + fmt.Sprintf(`
resource "ankra_cluster_access_grant" "ops" {
  cluster_id = %q
  user_email = "ops@example.com"
  role       = "edit"
  expires_at = "2030-01-01T00:00:00Z"
}
`, clusterSettingsTestCluster)
	var firstID string
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: withReason,
				Check: func(state *terraform.State) error {
					firstID = state.RootModule().Resources["ankra_cluster_access_grant.ops"].Primary.ID
					return nil
				},
			},
			{
				// The platform keeps the old reason on a re-time without one, so
				// removing the reason replaces the grant.
				Config: withoutReason,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckNoResourceAttr("ankra_cluster_access_grant.ops", "reason"),
					func(state *terraform.State) error {
						if state.RootModule().Resources["ankra_cluster_access_grant.ops"].Primary.ID == firstID {
							return fmt.Errorf("removing the reason did not replace the grant")
						}
						if len(fake.grants) != 1 || fake.grants[0]["reason"] != nil {
							return fmt.Errorf("platform holds %v", fake.grants)
						}
						return nil
					},
				),
			},
			{
				// Past its expiry and retired by the platform: kept quietly, and
				// destroy tolerates the 404.
				PreConfig: func() {
					clusterSettingsFreezeClock(t, time.Date(2031, 1, 1, 0, 0, 0, 0, time.UTC))
					fake.grants = nil
				},
				Config:             withoutReason,
				PlanOnly:           true,
				ExpectNonEmptyPlan: false,
			},
		},
	})
}

func TestAccClusterAccessGrantRejectsInvalidExpiry(t *testing.T) {
	fake := newClusterSettingsFake(t)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config: fake.providerBlock() + fmt.Sprintf(`
resource "ankra_cluster_access_grant" "ops" {
  cluster_id = %q
  user_email = "ops@example.com"
  role       = "edit"
  expires_at = "next tuesday"
}
`, clusterSettingsTestCluster),
			ExpectError: regexp.MustCompile(`RFC 3339`),
		}},
	})
}

func TestAccClusterAccessGrantRefusesTakeover(t *testing.T) {
	fake := newClusterSettingsFake(t)
	fake.grants = []map[string]any{{
		"id": "grant-manual", "cluster_id": clusterSettingsTestCluster, "ankra_user_id": clusterSettingsOtherMember,
		"user_email": "ops@example.com", "scope": "cluster", "namespace": nil, "role": "edit", "reconcile_status": "applied",
		"expires_at": "2030-06-01T00:00:00Z", "reason": "given in the portal", "source": "manual",
	}}
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config: fake.providerBlock() + fmt.Sprintf(`
resource "ankra_cluster_access_grant" "ops" {
  cluster_id = %q
  user_email = "OPS@example.com"
  role       = "edit"
  expires_at = "2030-01-01T00:00:00Z"
}
`, clusterSettingsTestCluster),
			ExpectError: regexp.MustCompile(`terraform import <resource address>\s+` + clusterSettingsTestCluster + `/grant-manual`),
		}},
	})
	if fake.grants[0]["expires_at"] != "2030-06-01T00:00:00Z" {
		t.Errorf("the existing grant was re-timed: %v", fake.grants[0])
	}
}

func TestAccClusterPowerScheduleSpentOnce(t *testing.T) {
	fake := newClusterSettingsFake(t)
	config := func(preserveState bool) string {
		return fake.providerBlock() + fmt.Sprintf(`
resource "ankra_cluster_power_schedule" "launch" {
  cluster_id     = %q
  action         = "start"
  schedule_kind  = "once"
  run_at         = "2030-01-01T09:00:00Z"
  preserve_state = %t
}
`, clusterSettingsTestCluster, preserveState)
	}
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{Config: config(true)},
			{
				// It fired: the platform disabled it and cleared next_run_at.
				PreConfig: func() {
					clusterSettingsFreezeClock(t, time.Date(2030, 1, 2, 0, 0, 0, 0, time.UTC))
					fake.schedules[0]["enabled"] = false
					fake.schedules[0]["next_run_at"] = nil
					fake.schedules[0]["last_run_at"] = "2030-01-01T09:00:10Z"
					fake.schedules[0]["last_run_status"] = "completed"
				},
				Config:             config(true),
				PlanOnly:           true,
				ExpectNonEmptyPlan: false,
			},
			{
				Config:      config(false),
				ExpectError: regexp.MustCompile(`run_at is in the past`),
			},
		},
	})
}

// TestAccClusterPowerScheduleCreatedID pins that a create tracks the
// schedule the platform names in created_id, even when an identical schedule
// landed in the same listing, and that it lists nothing before creating.
func TestAccClusterPowerScheduleCreatedID(t *testing.T) {
	fake := newClusterSettingsFake(t)
	fake.duplicateCreate = true
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config: fake.providerBlock() + fmt.Sprintf(`
resource "ankra_cluster_power_schedule" "nightly" {
  cluster_id      = %q
  action          = "stop"
  schedule_kind   = "cron"
  cron_expression = "0 19 * * *"
}
`, clusterSettingsTestCluster),
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("ankra_cluster_power_schedule.nightly", "id", "schedule-1"),
				func(*terraform.State) error {
					served := fake.served()
					for index, request := range served {
						if request == "POST /api/v1/org/clusters/imported/"+clusterSettingsTestCluster+"/power-schedules" {
							for _, earlier := range served[:index] {
								if strings.HasSuffix(earlier, "/power-schedules") {
									return fmt.Errorf("the create listed the schedules first: %v", served)
								}
							}
							return nil
						}
					}
					return fmt.Errorf("no create was sent: %v", served)
				},
			),
		}},
	})
}

// TestAccClusterPowerScheduleCreateListingLags pins that a create whose
// listing does not show the new schedule yet still tracks created_id, takes
// the rest from the configuration, and plans nothing once reads catch up.
func TestAccClusterPowerScheduleCreateListingLags(t *testing.T) {
	fake := newClusterSettingsFake(t)
	fake.createListingLags = true
	config := fake.providerBlock() + fmt.Sprintf(`
resource "ankra_cluster_power_schedule" "nightly" {
  cluster_id      = %q
  action          = "stop"
  schedule_kind   = "cron"
  cron_expression = "0 19 * * *"
}
`, clusterSettingsTestCluster)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: config,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ankra_cluster_power_schedule.nightly", "id", "schedule-1"),
					resource.TestCheckResourceAttr("ankra_cluster_power_schedule.nightly", "cron_expression", "0 19 * * *"),
					resource.TestCheckResourceAttr("ankra_cluster_power_schedule.nightly", "timezone", "UTC"),
					func(*terraform.State) error {
						if len(fake.schedules) != 1 {
							return fmt.Errorf("expected exactly one schedule, got %v", fake.schedules)
						}
						return nil
					},
				),
			},
			{Config: config, PlanOnly: true},
		},
	})
}

// TestAccClusterPowerScheduleCreateWithoutCreatedID pins that a create the
// platform answers without created_id fails with the import instructions
// instead of guessing which listed schedule is the new one.
func TestAccClusterPowerScheduleCreateWithoutCreatedID(t *testing.T) {
	fake := newClusterSettingsFake(t)
	fake.omitCreatedID = true
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config: fake.providerBlock() + fmt.Sprintf(`
resource "ankra_cluster_power_schedule" "nightly" {
  cluster_id      = %q
  action          = "stop"
  schedule_kind   = "cron"
  cron_expression = "0 19 * * *"
}
`, clusterSettingsTestCluster),
			ExpectError: regexp.MustCompile(`(?s)did\s+not\s+answer\s+the\s+created\s+schedule.s\s+id.*terraform\s+import`),
		}},
	})
}

func TestAccClusterSettingsOutOfScopeCluster(t *testing.T) {
	fake := newClusterSettingsFake(t)
	config := fake.providerBlock() + fmt.Sprintf(`
resource "ankra_cluster_power_schedule" "nightly" {
  cluster_id      = %q
  action          = "stop"
  schedule_kind   = "cron"
  cron_expression = "0 19 * * *"
}

resource "ankra_cluster_log_shipping" "prod" {
  cluster_id = %q
  enabled    = true
}
`, clusterSettingsTestCluster, clusterSettingsTestCluster)
	scopeError := regexp.MustCompile(`not visible in the provider's\s+organisation\s+and\s+project\s+scope`)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{Config: config},
			{
				// Out of the provider's project scope: an error, never a silent drop.
				PreConfig:   func() { fake.clusterHidden = true },
				Config:      config,
				ExpectError: scopeError,
			},
			{
				PreConfig: func() { fake.clusterHidden = false },
				Config:    config,
				Check: func(*terraform.State) error {
					if len(fake.schedules) != 1 || !fake.shipping {
						return fmt.Errorf("the schedule or switch was lost while out of scope: %v %v", fake.schedules, fake.shipping)
					}
					return nil
				},
			},
		},
	})
}

func TestAccClusterMeshMemberJoinFailsAfterCommit(t *testing.T) {
	fake := newClusterSettingsFake(t)
	config := fake.providerBlock() + fmt.Sprintf(`
resource "ankra_cluster_mesh" "edge" {
  name = "edge"
}

resource "ankra_cluster_mesh_member" "prod" {
  cluster_id = %q
  mesh_id    = ankra_cluster_mesh.edge.id
}
`, clusterSettingsTestCluster)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				PreConfig:   func() { fake.joinFailsAfterCommit = true },
				Config:      config,
				ExpectError: regexp.MustCompile(`peer secret failed`),
			},
			{
				// The committed membership was tracked (tainted), so the next
				// apply replaces it instead of joining again into a 409.
				PreConfig: func() { fake.joinFailsAfterCommit = false },
				Config:    config,
				Check:     resource.TestCheckResourceAttr("ankra_cluster_mesh_member.prod", "cilium_cluster_id", "1"),
			},
		},
	})
}

func TestClusterSettingsScopeError(t *testing.T) {
	cases := map[string]bool{
		`{"detail":"Cluster not found"}`:                          true,
		`{"detail":"Not Found"}`:                                  true,
		`{"detail":"Power schedule not found"}`:                   false,
		`{"detail":"Custom DNS zone not found for this cluster"}`: false,
		`not json`: false,
	}
	for body, isScope := range cases {
		err := &client.APIError{StatusCode: http.StatusNotFound, Body: body}
		if got := clusterSettingsScopeError(err, clusterSettingsTestCluster) != nil; got != isScope {
			t.Errorf("%s: scope error %v, want %v", body, got, isScope)
		}
		if treatedAsDone := clusterSettingsDeleteError(err, clusterSettingsTestCluster) == nil; treatedAsDone == isScope {
			t.Errorf("%s: delete treated as done %v", body, treatedAsDone)
		}
	}
}
