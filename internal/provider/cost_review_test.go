// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"fmt"
	"regexp"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

// A token pinned to one project may not list the whole organisation, and
// its fallback listing hides an environment budget. That budget must not be
// dropped from state (every re-create would be a 409), and the data source
// must say that its listing is partial.
func TestAccCostBudgetPartialListing(t *testing.T) {
	fake := newCostFake(t)
	configuration := fake.providerBlock() + `
resource "ankra_cost_budget" "production" {
  name          = "Production"
  scope_kind    = "environment"
  scope_id      = "prod"
  monthly_cents = 500000
  currency      = "eur"
}
`
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{Config: configuration},
			{
				PreConfig:   func() { fake.isTokenPinned = true },
				Config:      configuration,
				ExpectError: regexp.MustCompile("Cost budget not visible to this token"),
			},
			{
				PreConfig: func() { fake.isTokenPinned = false },
				Config: configuration + `
data "ankra_cost_budgets" "visible" {}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.ankra_cost_budgets.visible", "is_partial", "false"),
					resource.TestCheckResourceAttr("data.ankra_cost_budgets.visible", "budgets.#", "1"),
					func(_ *terraform.State) error {
						if len(fake.budgets) != 1 {
							return fmt.Errorf("budget duplicated or lost: %v", fake.budgets)
						}
						return nil
					},
				),
			},
		},
	})
}

// An override that ran out as configured disappears from the platform's
// read. Re-creating it would be refused for its past expiry and lock every
// apply, so it stays in state with no planned change.
func TestAccCostAutopilotOverrideExpired(t *testing.T) {
	fake := newCostFake(t)
	clusterID := "11111111-1111-1111-1111-111111111111"
	configuration := fake.providerBlock() + fmt.Sprintf(`
resource "ankra_cost_autopilot_override" "prod" {
  cluster_id = %q
  tier       = "scheduled"
  reason     = "Quarter-end freeze"
  expires_at = "2030-01-01T00:00:00Z"
}
`, clusterID)
	realClock := clusterSettingsNow
	t.Cleanup(func() { clusterSettingsNow = realClock })

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{Config: configuration},
			{
				PreConfig: func() {
					clusterSettingsNow = func() time.Time { return time.Date(2030, 6, 1, 0, 0, 0, 0, time.UTC) }
					delete(fake.overrides, clusterID)
				},
				Config:   configuration,
				PlanOnly: true,
			},
			{
				// Before its expiry, a vanished override is re-created.
				PreConfig: func() {
					clusterSettingsNow = realClock
				},
				Config: configuration,
				Check: func(_ *terraform.State) error {
					if fake.overrides[clusterID] == nil {
						return fmt.Errorf("override not re-created")
					}
					return nil
				},
			},
		},
	})
}

// Budgets are listed across every project (project_id=all) and deleted by
// organisation, so a project switch does not hide one; moving the provider
// to another organisation does. That must neither plan a duplicate nor let
// destroy report success while the budget lives on.
func TestAccCostBudgetScopeMove(t *testing.T) {
	fake := newCostFake(t)
	const firstOrganisation = "11111111-aaaa-4aaa-8aaa-111111111111"
	const otherOrganisation = "22222222-bbbb-4bbb-8bbb-222222222222"
	inOrganisation := func(organisation string) string {
		return fmt.Sprintf(`
provider "ankra" {
  token           = "test-token"
  base_url        = %q
  organisation_id = %q
}

resource "ankra_cost_budget" "production" {
  name          = "Production"
  scope_kind    = "environment"
  scope_id      = "prod"
  monthly_cents = 500000
  currency      = "eur"
}
`, fake.server.URL, organisation)
	}
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{Config: inOrganisation(firstOrganisation)},
			{
				Config:      inOrganisation(otherOrganisation),
				ExpectError: regexp.MustCompile(`Object not visible in the provider's current scope`),
			},
			{
				Config:      inOrganisation(otherOrganisation),
				Destroy:     true,
				ExpectError: regexp.MustCompile(`Object not visible in the provider's current scope`),
			},
			{
				Config: inOrganisation(firstOrganisation),
				Check: func(_ *terraform.State) error {
					if len(fake.budgets) != 1 {
						return fmt.Errorf("budget lost or duplicated: %v", fake.budgets)
					}
					return nil
				},
			},
		},
	})
}

func TestAccCostBudgetsDataSourcePartial(t *testing.T) {
	fake := newCostFake(t)
	fake.isTokenPinned = true
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config: fake.providerBlock() + `
data "ankra_cost_budgets" "visible" {}
`,
			Check: resource.TestCheckResourceAttr("data.ankra_cost_budgets.visible", "is_partial", "true"),
		}},
	})
}
