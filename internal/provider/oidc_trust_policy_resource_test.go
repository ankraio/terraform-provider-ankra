// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

// oidcTrustFake holds trust policies the way the bearer routes do
// (go/internal/oidctrustapi, usecase/oidctrust): create refuses unknown body
// members, defaults ttl_seconds 0 to 900, answers 201 with the policy and
// 409 for a name already taken; the listing carries live policies oldest
// first with scopes as an empty list when unset; delete answers 404 for a
// policy that is gone. There is no update route, so a provider that tried one
// would fail the test on an unexpected request.
type oidcTrustFake struct {
	*routeFake
	policies     map[string]map[string]any
	order        []string
	createBodies []map[string]json.RawMessage
	deletes      []string
	nextID       int
}

var oidcTrustCreateMembers = map[string]bool{
	"name": true, "repository_owner": true, "repository_name": true, "repository_owner_id": true, "repository_id": true,
	"ref": true, "environment": true, "job_workflow_ref": true, "allow_pull_request": true, "role_slug": true, "scopes": true,
	"ttl_seconds": true,
}

func newOIDCTrustFake(t *testing.T) *oidcTrustFake {
	fake := &oidcTrustFake{routeFake: newRouteFake(t), policies: map[string]map[string]any{}}
	fake.handle("GET /api/v1/org/organisation/oidc-trust-policies", func(writer http.ResponseWriter, _ *http.Request) {
		listing := []map[string]any{}
		for _, identifier := range fake.order {
			if row, live := fake.policies[identifier]; live {
				listing = append(listing, row)
			}
		}
		writeJSON(t, writer, http.StatusOK, map[string]any{"policies": listing})
	})
	fake.handle("POST /api/v1/org/organisation/oidc-trust-policies", func(writer http.ResponseWriter, request *http.Request) {
		var raw map[string]json.RawMessage
		decodeJSON(t, request, &raw)
		fake.createBodies = append(fake.createBodies, raw)
		for member := range raw {
			if !oidcTrustCreateMembers[member] {
				writeJSON(t, writer, http.StatusBadRequest, map[string]any{"detail": "invalid JSON body: unknown field " + member})
				return
			}
		}
		row := map[string]any{}
		for member, value := range raw {
			var decoded any
			_ = json.Unmarshal(value, &decoded)
			row[member] = decoded
		}
		for _, existing := range fake.policies {
			if existing["name"] == row["name"] {
				writeJSON(t, writer, http.StatusConflict, map[string]any{"detail": "A trust policy with this name already exists"})
				return
			}
		}
		if ttl, _ := row["ttl_seconds"].(float64); ttl == 0 {
			row["ttl_seconds"] = float64(900)
		}
		if row["scopes"] == nil {
			row["scopes"] = []any{}
		}
		fake.nextID++
		identifier := fmt.Sprintf("0e0e0e0e-0000-0000-0000-%012d", fake.nextID)
		row["id"] = identifier
		row["service_account_id"] = fmt.Sprintf("5a5a5a5a-0000-0000-0000-%012d", fake.nextID)
		row["created_at"] = "2026-11-08T10:00:00Z"
		fake.policies[identifier] = row
		fake.order = append(fake.order, identifier)
		writeJSON(t, writer, http.StatusCreated, row)
	})
	fake.handle("DELETE /api/v1/org/organisation/oidc-trust-policies/{policy_id}", func(writer http.ResponseWriter, request *http.Request) {
		identifier := request.PathValue("policy_id")
		if _, live := fake.policies[identifier]; !live {
			writeJSON(t, writer, http.StatusNotFound, map[string]any{"detail": "Trust policy not found"})
			return
		}
		delete(fake.policies, identifier)
		fake.deletes = append(fake.deletes, identifier)
		writeJSON(t, writer, http.StatusOK, map[string]any{"deleted": true, "revoked_tokens": 0})
	})
	return fake
}

// TestAccOIDCTrustPolicyLifecycle creates a policy pinned to a ref and an
// environment, reads it back unchanged, replaces it on any change (the
// platform has no update), imports it, lists it, and deletes it on destroy.
func TestAccOIDCTrustPolicyLifecycle(t *testing.T) {
	fake := newOIDCTrustFake(t)
	config := func(ref string) string {
		return fake.providerBlock() + fmt.Sprintf(`
resource "ankra_oidc_trust_policy" "deploy" {
  name                = "infra-deploy"
  repository_owner    = "acme"
  repository_name     = "infra"
  repository_owner_id = 1001
  repository_id       = 2002
  ref                 = %q
  environment         = "production"
  role_slug           = "operator"
}
`, ref)
	}
	var firstID string

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy: func(_ *terraform.State) error {
			if len(fake.policies) != 0 {
				return fmt.Errorf("policies left after destroy: %v", fake.policies)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: config("refs/heads/main"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ankra_oidc_trust_policy.deploy", "ttl_seconds", "900"),
					resource.TestCheckResourceAttr("ankra_oidc_trust_policy.deploy", "allow_pull_request", "false"),
					resource.TestCheckNoResourceAttr("ankra_oidc_trust_policy.deploy", "scopes"),
					resource.TestCheckNoResourceAttr("ankra_oidc_trust_policy.deploy", "job_workflow_ref"),
					resource.TestCheckResourceAttr("ankra_oidc_trust_policy.deploy", "service_account_id", "5a5a5a5a-0000-0000-0000-000000000001"),
					func(state *terraform.State) error {
						firstID = state.RootModule().Resources["ankra_oidc_trust_policy.deploy"].Primary.ID
						body := fake.createBodies[0]
						if string(body["job_workflow_ref"]) != "null" || string(body["scopes"]) != "null" || string(body["ttl_seconds"]) != "900" ||
							string(body["allow_pull_request"]) != "false" || string(body["ref"]) != `"refs/heads/main"` {
							return fmt.Errorf("unexpected create body: %v", body)
						}
						return nil
					},
				),
			},
			{
				Config:   config("refs/heads/main"),
				PlanOnly: true,
			},
			{
				Config: config("refs/tags/v1"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("ankra_oidc_trust_policy.deploy", plancheck.ResourceActionDestroyBeforeCreate)},
				},
				Check: func(_ *terraform.State) error {
					if len(fake.deletes) != 1 || fake.deletes[0] != firstID || len(fake.policies) != 1 {
						return fmt.Errorf("a ref change must delete the old policy and create another: deletes %v, policies %v", fake.deletes, fake.policies)
					}
					return nil
				},
			},
			{
				ResourceName:      "ankra_oidc_trust_policy.deploy",
				ImportState:       true,
				ImportStateVerify: true,
			},
			{
				Config: config("refs/tags/v1") + `
data "ankra_oidc_trust_policies" "all" {}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.ankra_oidc_trust_policies.all", "policies.#", "1"),
					resource.TestCheckResourceAttr("data.ankra_oidc_trust_policies.all", "policies.0.ref", "refs/tags/v1"),
					resource.TestCheckResourceAttr("data.ankra_oidc_trust_policies.all", "policies.0.scopes.#", "0"),
					resource.TestCheckNoResourceAttr("data.ankra_oidc_trust_policies.all", "policies.0.job_workflow_ref"),
				),
			},
		},
	})
}

// TestAccOIDCTrustPolicyScopesAndRemoval narrows the minted tokens to MCP
// scopes, and plans the policy's recreation when it was deleted outside
// Terraform.
func TestAccOIDCTrustPolicyScopesAndRemoval(t *testing.T) {
	fake := newOIDCTrustFake(t)
	config := fake.providerBlock() + `
resource "ankra_oidc_trust_policy" "mcp" {
  name                = "agents"
  repository_owner    = "acme"
  repository_name     = "agents"
  repository_owner_id = 1001
  repository_id       = 3003
  job_workflow_ref    = "acme/agents/.github/workflows/run.yml@refs/heads/main"
  allow_pull_request  = true
  role_slug           = "viewer"
  scopes              = ["mcp:read"]
  ttl_seconds         = 300
}
`
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: config,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ankra_oidc_trust_policy.mcp", "scopes.#", "1"),
					resource.TestCheckResourceAttr("ankra_oidc_trust_policy.mcp", "ttl_seconds", "300"),
					resource.TestCheckResourceAttr("ankra_oidc_trust_policy.mcp", "allow_pull_request", "true"),
					resource.TestCheckNoResourceAttr("ankra_oidc_trust_policy.mcp", "ref"),
				),
			},
			{
				PreConfig: func() {
					for identifier := range fake.policies {
						delete(fake.policies, identifier)
					}
				},
				Config: config,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("ankra_oidc_trust_policy.mcp", plancheck.ResourceActionCreate)},
				},
			},
		},
	})
}

func TestAccOIDCTrustPolicyValidation(t *testing.T) {
	fake := newRouteFake(t)
	base := `
  name                = "ci"
  repository_owner    = "acme"
  repository_name     = "infra"
  repository_owner_id = 1001
  repository_id       = 2002
`
	for _, testCase := range []struct {
		label    string
		extra    string
		expected string
	}{
		{label: "wildcard ref", extra: `ref = "refs/heads/*"` + "\n" + `role_slug = "viewer"`, expected: "Invalid ref"},
		{label: "short ref", extra: `ref = "main"` + "\n" + `role_slug = "viewer"`, expected: "Invalid ref"},
		{label: "custom role", extra: `role_slug = "deployer"`, expected: "Invalid value"},
		{label: "unknown scope", extra: `role_slug = "viewer"` + "\n" + `scopes = ["clusters.write"]`, expected: "Unknown scope"},
		{label: "empty scopes", extra: `role_slug = "viewer"` + "\n" + `scopes = []`, expected: "Empty scopes"},
		{label: "long ttl", extra: `role_slug = "viewer"` + "\n" + `ttl_seconds = 7200`, expected: "Value out of range"},
	} {
		t.Run(testCase.label, func(t *testing.T) {
			resource.Test(t, resource.TestCase{
				ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
				Steps: []resource.TestStep{
					{
						Config:      fake.providerBlock() + "resource \"ankra_oidc_trust_policy\" \"bad\" {" + base + testCase.extra + "\n}\n",
						PlanOnly:    true,
						ExpectError: regexp.MustCompile(testCase.expected),
					},
				},
			})
		})
	}
}

func TestAccOIDCTrustPolicyRefused(t *testing.T) {
	fake := newRouteFake(t)
	fake.handle("POST /api/v1/org/organisation/oidc-trust-policies", func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(t, writer, http.StatusForbidden, map[string]any{"detail": "A service account cannot manage OIDC trust policies; an organisation administrator must"})
	})
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: fake.providerBlock() + `
resource "ankra_oidc_trust_policy" "ci" {
  name                = "ci"
  repository_owner    = "acme"
  repository_name     = "infra"
  repository_owner_id = 1001
  repository_id       = 2002
  role_slug           = "viewer"
}
`,
				ExpectError: regexp.MustCompile("Unable to create OIDC trust policy"),
			},
		},
	})
}
