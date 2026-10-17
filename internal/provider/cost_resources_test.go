// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"encoding/json"
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

func costRawString(raw json.RawMessage) string {
	return string(raw)
}

func TestAccCostBudgetLifecycle(t *testing.T) {
	fake := newCostFake(t)

	withOwner := fake.providerBlock() + `
resource "ankra_cost_budget" "production" {
  name          = "Production"
  scope_kind    = "environment"
  scope_id      = "Prod"
  monthly_cents = 500000
  currency      = "EUR"
  owner_user_id = "33333333-3333-3333-3333-333333333333"
}

data "ankra_cost_budgets" "all" {
  depends_on = [ankra_cost_budget.production]
}
`
	withoutOwner := fake.providerBlock() + `
resource "ankra_cost_budget" "production" {
  name          = "Production"
  scope_kind    = "environment"
  scope_id      = "Prod"
  monthly_cents = 750000
  currency      = "EUR"
  notify_at_pct = 90
}
`

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy: func(_ *terraform.State) error {
			if len(fake.budgets) != 0 {
				return fmt.Errorf("budgets left after destroy: %v", fake.budgets)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: withOwner,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("ankra_cost_budget.production", "id"),
					resource.TestCheckResourceAttr("ankra_cost_budget.production", "notify_at_pct", "80"),
					// The platform lower-cases both; the configured spelling stays.
					resource.TestCheckResourceAttr("ankra_cost_budget.production", "scope_id", "Prod"),
					resource.TestCheckResourceAttr("ankra_cost_budget.production", "currency", "EUR"),
					resource.TestCheckResourceAttr("ankra_cost_budget.production", "scope_name", "scope prod"),
					resource.TestCheckResourceAttr("data.ankra_cost_budgets.all", "budgets.#", "1"),
					resource.TestCheckResourceAttr("data.ankra_cost_budgets.all", "budgets.0.scope_id", "prod"),
					resource.TestCheckResourceAttr("data.ankra_cost_budgets.all", "budgets.0.status", "unknown"),
					resource.TestCheckNoResourceAttr("data.ankra_cost_budgets.all", "budgets.0.month_to_date_cents"),
				),
			},
			{
				Config: withoutOwner,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ankra_cost_budget.production", "monthly_cents", "750000"),
					resource.TestCheckResourceAttr("ankra_cost_budget.production", "notify_at_pct", "90"),
					resource.TestCheckNoResourceAttr("ankra_cost_budget.production", "owner_user_id"),
					func(_ *terraform.State) error {
						if len(fake.budgetUpdates) == 0 {
							return fmt.Errorf("no budget update was sent")
						}
						body := fake.budgetUpdates[len(fake.budgetUpdates)-1]
						if raw, present := body["owner_user_id"]; !present || !costIsNull(raw) {
							return fmt.Errorf("update must send owner_user_id null to clear the owner, sent %q", costRawString(raw))
						}
						for _, field := range []string{"scope_kind", "scope_id"} {
							if _, present := body[field]; present {
								return fmt.Errorf("update sent %s, which the platform refuses", field)
							}
						}
						for _, budget := range fake.budgets {
							if budget["owner_user_id"] != nil {
								return fmt.Errorf("platform still holds owner %v", budget["owner_user_id"])
							}
						}
						return nil
					},
				),
			},
			{
				ResourceName:            "ankra_cost_budget.production",
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"scope_id", "currency"},
			},
			{
				PreConfig: func() {
					for identifier := range fake.budgets {
						delete(fake.budgets, identifier)
					}
				},
				Config: withoutOwner,
				Check: func(_ *terraform.State) error {
					if len(fake.budgets) != 1 {
						return fmt.Errorf("budget deleted outside Terraform was not recreated")
					}
					return nil
				},
			},
		},
	})
}

func TestAccCostAutopilotLifecycle(t *testing.T) {
	fake := newCostFake(t)

	withQuietHours := fake.providerBlock() + `
resource "ankra_cost_autopilot" "policy" {
  defaults = {
    development = "managed"
  }
  quiet_hours = {
    start    = "22:00"
    end      = "06:00"
    timezone = "Europe/Stockholm"
  }
}
`
	withoutQuietHours := fake.providerBlock() + `
resource "ankra_cost_autopilot" "policy" {
  defaults = {
    development = "managed"
    staging     = "scheduled"
  }
  notification_route_id = "44444444-4444-4444-4444-444444444444"
}
`

	lastWrite := func() map[string]json.RawMessage {
		return fake.autopilotWrites[len(fake.autopilotWrites)-1]
	}

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy: func(_ *terraform.State) error {
			for kind, tier := range fake.autopilotDefaults {
				if tier != "hands-off" {
					return fmt.Errorf("destroy left %s at %s", kind, tier)
				}
			}
			if fake.quietHours != nil || fake.notificationRouteID != nil {
				return fmt.Errorf("destroy left quiet hours %v and route %v", fake.quietHours, fake.notificationRouteID)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: withQuietHours,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ankra_cost_autopilot.policy", "id", "cost_autopilot"),
					resource.TestCheckResourceAttr("ankra_cost_autopilot.policy", "defaults.%", "1"),
					resource.TestCheckResourceAttr("ankra_cost_autopilot.policy", "effective_defaults.%", "5"),
					resource.TestCheckResourceAttr("ankra_cost_autopilot.policy", "effective_defaults.production", "hands-off"),
					resource.TestCheckResourceAttr("ankra_cost_autopilot.policy", "quiet_hours.timezone", "Europe/Stockholm"),
					func(_ *terraform.State) error {
						body := lastWrite()
						var defaults map[string]string
						if err := json.Unmarshal(body["defaults"], &defaults); err != nil {
							return err
						}
						if len(defaults) != 5 || defaults["development"] != "managed" || defaults["preview"] != "hands-off" {
							return fmt.Errorf("write must name every kind, unnamed ones hands-off: %v", defaults)
						}
						if raw, present := body["notification_route_id"]; !present || !costIsNull(raw) {
							return fmt.Errorf("write must send notification_route_id null, sent %q", costRawString(raw))
						}
						return nil
					},
				),
			},
			{
				Config: withoutQuietHours,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckNoResourceAttr("ankra_cost_autopilot.policy", "quiet_hours"),
					resource.TestCheckResourceAttr("ankra_cost_autopilot.policy", "effective_defaults.staging", "scheduled"),
					func(_ *terraform.State) error {
						if raw, present := lastWrite()["quiet_hours"]; !present || !costIsNull(raw) {
							return fmt.Errorf("write must send quiet_hours null to clear them, sent %q", costRawString(raw))
						}
						if fake.quietHours != nil {
							return fmt.Errorf("platform still holds quiet hours %v", fake.quietHours)
						}
						return nil
					},
				),
			},
			{
				ResourceName:      "ankra_cost_autopilot.policy",
				ImportState:       true,
				ImportStateId:     "cost_autopilot",
				ImportStateVerify: true,
			},
			{
				// A kind changed outside Terraform is drift and is put back.
				PreConfig: func() { fake.autopilotDefaults["preview"] = "ephemeral" },
				Config:    withoutQuietHours,
				Check: func(_ *terraform.State) error {
					if fake.autopilotDefaults["preview"] != "hands-off" {
						return fmt.Errorf("drift on preview was not corrected: %s", fake.autopilotDefaults["preview"])
					}
					return nil
				},
			},
		},
	})
}

func TestAccCostAutopilotOverrideLifecycle(t *testing.T) {
	fake := newCostFake(t)
	clusterID := "11111111-1111-1111-1111-111111111111"

	withExpiry := fake.providerBlock() + fmt.Sprintf(`
resource "ankra_cost_autopilot_override" "prod" {
  cluster_id = %q
  tier       = "scheduled"
  reason     = "Quarter-end freeze"
  expires_at = "2030-01-01T00:00:00+02:00"
}
`, clusterID)
	withoutExpiry := fake.providerBlock() + fmt.Sprintf(`
resource "ankra_cost_autopilot_override" "prod" {
  cluster_id = %q
  tier       = "managed"
  reason     = "Quarter-end freeze"
}
`, clusterID)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy: func(_ *terraform.State) error {
			if len(fake.overrides) != 0 {
				return fmt.Errorf("overrides left after destroy: %v", fake.overrides)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				// The platform answers the instant in UTC; the configured
				// spelling stays, so the next plan is empty.
				Config: withExpiry,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ankra_cost_autopilot_override.prod", "id", clusterID),
					resource.TestCheckResourceAttr("ankra_cost_autopilot_override.prod", "expires_at", "2030-01-01T00:00:00+02:00"),
					func(_ *terraform.State) error {
						if expiresAt := fake.overrides[clusterID]["expires_at"]; expiresAt != "2029-12-31T22:00:00Z" {
							return fmt.Errorf("platform holds expires_at %v", expiresAt)
						}
						return nil
					},
				),
			},
			{
				Config: withoutExpiry,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ankra_cost_autopilot_override.prod", "tier", "managed"),
					resource.TestCheckNoResourceAttr("ankra_cost_autopilot_override.prod", "expires_at"),
					func(_ *terraform.State) error {
						body := fake.overrideWrites[len(fake.overrideWrites)-1]
						if _, present := body["expires_at"]; present {
							return fmt.Errorf("an override without expiry must omit expires_at, sent %q", costRawString(body["expires_at"]))
						}
						if fake.overrides[clusterID]["expires_at"] != nil {
							return fmt.Errorf("platform still holds expiry %v", fake.overrides[clusterID]["expires_at"])
						}
						return nil
					},
				),
			},
			{
				ResourceName:      "ankra_cost_autopilot_override.prod",
				ImportState:       true,
				ImportStateId:     clusterID,
				ImportStateVerify: true,
			},
			{
				PreConfig: func() { delete(fake.overrides, clusterID) },
				Config:    withoutExpiry,
				Check: func(_ *terraform.State) error {
					if _, exists := fake.overrides[clusterID]; !exists {
						return fmt.Errorf("override cleared outside Terraform was not recreated")
					}
					return nil
				},
			},
		},
	})
}

func TestAccCostSettingsLifecycle(t *testing.T) {
	fake := newCostFake(t)

	withLimit := fake.providerBlock() + `
resource "ankra_cost_settings" "pricing" {
  currency                        = "usd"
  effective_discount_pct          = 12.5
  include_network_egress_estimate = true
  analysed_cluster_limit          = 20
}
`
	withoutLimit := fake.providerBlock() + `
resource "ankra_cost_settings" "pricing" {
  currency = "usd"
}
`

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy: func(_ *terraform.State) error {
			if fake.currency != "eur" || fake.discount != 0 || fake.includeEgress || fake.analysedClusterLimit != nil {
				return fmt.Errorf("destroy did not restore the defaults: %v", fake.settings())
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: withLimit,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ankra_cost_settings.pricing", "analysed_cluster_limit", "20"),
					resource.TestCheckResourceAttr("ankra_cost_settings.pricing", "effective_discount_pct", "12.5"),
				),
			},
			{
				Config: withoutLimit,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckNoResourceAttr("ankra_cost_settings.pricing", "analysed_cluster_limit"),
					resource.TestCheckResourceAttr("ankra_cost_settings.pricing", "effective_discount_pct", "0"),
					resource.TestCheckResourceAttr("ankra_cost_settings.pricing", "include_network_egress_estimate", "false"),
					func(_ *terraform.State) error {
						body := fake.settingsWrites[len(fake.settingsWrites)-1]
						if raw, present := body["analysed_cluster_limit"]; !present || !costIsNull(raw) {
							return fmt.Errorf("write must send analysed_cluster_limit null to restore the default, sent %q", costRawString(raw))
						}
						return nil
					},
				),
			},
			{
				ResourceName:      "ankra_cost_settings.pricing",
				ImportState:       true,
				ImportStateId:     "cost_settings",
				ImportStateVerify: true,
			},
			{
				// A limit set outside Terraform is drift and is returned to the default.
				PreConfig: func() { fake.analysedClusterLimit = 30 },
				Config:    withoutLimit,
				Check: func(_ *terraform.State) error {
					if fake.analysedClusterLimit != nil {
						return fmt.Errorf("drifted limit %v was not cleared", fake.analysedClusterLimit)
					}
					return nil
				},
			},
		},
	})
}

func TestAccCostPlanTimeValidation(t *testing.T) {
	fake := newCostFake(t)
	cases := map[string]string{
		"Unexpected scope_id": `
resource "ankra_cost_budget" "organisation" {
  name          = "Organisation"
  scope_kind    = "organisation"
  scope_id      = "prod"
  monthly_cents = 100
  currency      = "eur"
}
`,
		"Missing scope_id": `
resource "ankra_cost_budget" "cluster" {
  name          = "Cluster"
  scope_kind    = "cluster"
  monthly_cents = 100
  currency      = "eur"
}
`,
		"is not accepted": `
resource "ankra_cost_autopilot_override" "prod" {
  cluster_id = "11111111-1111-1111-1111-111111111111"
  tier       = "aggressive"
  reason     = "Quarter-end freeze"
}
`,
		"Unknown environment kind": `
resource "ankra_cost_autopilot" "policy" {
  defaults = { qa = "managed" }
}
`,
		"Value out of range": `
resource "ankra_cost_settings" "pricing" {
  analysed_cluster_limit = 51
}
`,
	}
	for expected, config := range cases {
		resource.Test(t, resource.TestCase{
			ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
			Steps: []resource.TestStep{{
				Config:      fake.providerBlock() + config,
				ExpectError: regexp.MustCompile(expected),
			}},
		})
	}
	if served := fake.served(); len(served) != 0 {
		t.Errorf("plan-time refusals still sent %v", served)
	}
}
