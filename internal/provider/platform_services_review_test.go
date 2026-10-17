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

// An expired join token is dropped on refresh and minted again.
func TestAccDeployHostJoinTokenExpiry(t *testing.T) {
	fake := newDeployServiceFake(t)
	configuration := fake.providerBlock() + `
resource "ankra_host_join_token" "edge" {
  environment = "edge"
}
`
	realClock := clusterSettingsNow
	t.Cleanup(func() { clusterSettingsNow = realClock })

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{Config: configuration},
			{
				// Not expired yet: nothing to do.
				Config:   configuration,
				PlanOnly: true,
			},
			{
				PreConfig: func() {
					clusterSettingsNow = func() time.Time { return time.Date(2030, 1, 1, 11, 30, 0, 0, time.UTC) }
				},
				Config: configuration,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ankra_host_join_token.edge", "join_token", "ahjt_secret_2"),
					func(_ *terraform.State) error {
						if fake.minted != 2 {
							return fmt.Errorf("minted %d tokens, want 2", fake.minted)
						}
						return nil
					},
				),
			},
		},
	})
}

// A key replaced in the portal no longer has the preview of the key in
// state, so the next plan stores the configured key again.
func TestAccAIKeyReplacedOutside(t *testing.T) {
	fake := newAIFake(t)
	configuration := fake.providerBlock() + `
resource "ankra_ai_anthropic_key" "own" {
  api_key = "sk-ant-configured-0001"
}

resource "ankra_ai_endpoint" "litellm" {
  name     = "litellm"
  base_url = "https://litellm.example.com/v1"
  api_key  = "sk-endpoint-0001"
}
`
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{Config: configuration},
			{
				Config:   configuration,
				PlanOnly: true,
			},
			{
				PreConfig: func() {
					fake.anthropicKey = "sk-ant-from-portal-9999"
					fake.endpointKeys[platformServicesText(fake.endpoints[0]["id"])] = "sk-portal-endpoint-9999"
					fake.endpoints[0]["key_preview"] = aiFakeMask("sk-portal-endpoint-9999", 3, 8)
				},
				Config: configuration,
				Check: func(_ *terraform.State) error {
					endpointKey := fake.endpointKeys[platformServicesText(fake.endpoints[0]["id"])]
					if fake.anthropicKey != "sk-ant-configured-0001" || endpointKey != "sk-endpoint-0001" {
						return fmt.Errorf("keys not restored: anthropic %q, endpoint %q", fake.anthropicKey, endpointKey)
					}
					return nil
				},
			},
		},
	})
}

// Moving the provider to another organisation hides an AI endpoint. That
// must neither plan a duplicate nor let destroy report success.
func TestAccAIEndpointOrganisationMove(t *testing.T) {
	fake := newAIFake(t)
	inOrganisation := func(organisation string) string {
		return fmt.Sprintf(`
provider "ankra" {
  token           = "test-token"
  base_url        = %q
  organisation_id = %q
}

resource "ankra_ai_endpoint" "litellm" {
  name     = "litellm"
  base_url = "https://litellm.example.com/v1"
  api_key  = "sk-endpoint-0001"
}
`, fake.server.URL, organisation)
	}
	const firstOrganisation = "11111111-aaaa-4aaa-8aaa-111111111111"
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{Config: inOrganisation(firstOrganisation)},
			{
				Config:      inOrganisation(aiFakeOtherOrganisation),
				ExpectError: regexp.MustCompile(`Object not visible in the provider's current scope`),
			},
			{
				Config:      inOrganisation(aiFakeOtherOrganisation),
				Destroy:     true,
				ExpectError: regexp.MustCompile(`Object not visible in the provider's current scope`),
			},
			{
				Config: inOrganisation(firstOrganisation),
				Check: func(_ *terraform.State) error {
					if len(fake.endpoints) != 1 {
						return fmt.Errorf("endpoint lost or duplicated: %v", fake.endpoints)
					}
					return nil
				},
			},
		},
	})
}

// Values the platform trims are refused with surrounding whitespace.
func TestAccPlatformServicesTrimmedValues(t *testing.T) {
	aiFake := newAIFake(t)
	costFake := newCostFake(t)
	for name, configuration := range map[string]string{
		"endpoint name": aiFake.providerBlock() + `
resource "ankra_ai_endpoint" "bad" {
  name     = "litellm "
  base_url = "https://litellm.example.com/v1"
  api_key  = "sk-endpoint-0001"
}
`,
		"compatible model": aiFake.providerBlock() + `
resource "ankra_ai_openai_compatible" "bad" {
  base_url = "https://llm.example.com/v1"
  model    = " gpt-4o"
  api_key  = "sk-legacy-123456"
}
`,
		"budget scope": costFake.providerBlock() + `
resource "ankra_cost_budget" "bad" {
  name          = "Production"
  scope_kind    = "environment"
  scope_id      = "prod "
  monthly_cents = 500000
  currency      = "eur"
}
`,
		"override reason": costFake.providerBlock() + `
resource "ankra_cost_autopilot_override" "bad" {
  cluster_id = "11111111-1111-1111-1111-111111111111"
  tier       = "managed"
  reason     = "Quarter-end freeze "
}
`,
	} {
		t.Run(name, func(t *testing.T) {
			resource.Test(t, resource.TestCase{
				ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
				Steps: []resource.TestStep{{
					Config:      configuration,
					PlanOnly:    true,
					ExpectError: regexp.MustCompile("starts or ends with whitespace"),
				}},
			})
		})
	}
	if served := append(aiFake.served(), costFake.served()...); len(served) != 0 {
		t.Errorf("a refused plan sent %v", served)
	}
}
