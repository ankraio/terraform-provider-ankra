// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

// The organisation-wide singletons never take over values someone set
// outside Terraform, and their destroy resets only what the configuration
// set, so a destroy cannot undo a portal change to another part.

func TestAccCostSettingsSingleton(t *testing.T) {
	fake := newCostFake(t)
	fake.currency = "usd"
	configuration := fake.providerBlock() + `
resource "ankra_cost_settings" "pricing" {
  currency = "sek"
}
`
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:      configuration,
				ExpectError: regexp.MustCompile("Cost settings already customised"),
			},
			{
				PreConfig: func() { fake.currency = "eur" },
				Config:    configuration,
			},
			{
				// A setting the configuration leaves out is changed in the
				// portal; destroy must leave it.
				PreConfig: func() { fake.discount = 15 },
				Config:    configuration,
				Destroy:   true,
			},
		},
		CheckDestroy: func(_ *terraform.State) error {
			if fake.currency != "eur" || fake.discount != 15 {
				return fmt.Errorf("destroy left currency %q and discount %v; want eur and the portal's 15", fake.currency, fake.discount)
			}
			return nil
		},
	})
}

func TestAccCostAutopilotSingleton(t *testing.T) {
	fake := newCostFake(t)
	fake.autopilotDefaults["staging"] = "scheduled"
	configuration := fake.providerBlock() + `
resource "ankra_cost_autopilot" "policy" {
  defaults = {
    production = "managed"
  }
}
`
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:      configuration,
				ExpectError: regexp.MustCompile("Cost autopilot policy already set"),
			},
			{
				PreConfig: func() { fake.autopilotDefaults["staging"] = "hands-off" },
				Config:    configuration,
			},
			{
				PreConfig: func() { fake.notificationRouteID = "route-from-portal" },
				Config:    configuration,
				Destroy:   true,
			},
		},
		CheckDestroy: func(_ *terraform.State) error {
			if fake.autopilotDefaults["production"] != "hands-off" || fake.notificationRouteID != "route-from-portal" {
				return fmt.Errorf("destroy left production %q and route %v; want hands-off and the portal's route",
					fake.autopilotDefaults["production"], fake.notificationRouteID)
			}
			return nil
		},
	})
}

func TestAccAIProviderRefusesTakeover(t *testing.T) {
	fake := newAIFake(t)
	fake.provider = "openrouter"
	fake.openRouterKey = "sk-or-portal-key-0001"
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config: fake.providerBlock() + `
resource "ankra_ai_provider" "active" {
  name = "ankra"
}
`,
			ExpectError: regexp.MustCompile("AI provider already chosen"),
		}},
	})
	if fake.provider != "openrouter" {
		t.Errorf("provider changed to %q", fake.provider)
	}
}

func TestAccSecurityComplianceFrameworkRefusesTakeover(t *testing.T) {
	fake := newSecurityFake(t)
	fake.frameworks["soc2"]["enabled"] = true
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config: fake.providerBlock() + `
resource "ankra_security_compliance_framework" "soc2" {
  key = "soc2"
}
`,
			ExpectError: regexp.MustCompile("Compliance framework already enabled"),
		}},
	})
	if fake.frameworks["soc2"]["enabled"] != true {
		t.Errorf("framework enabled in the portal was disabled")
	}
}
