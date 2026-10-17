// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

func TestAccOrganisationAccessDataSources(t *testing.T) {
	fake := newTokenFake(t)
	fake.handle("GET /api/v1/org/organisation/roles", func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(t, writer, http.StatusOK, map[string]any{"roles": []map[string]any{
			{"slug": "owner", "name": "Owner", "description": "", "builtin": true, "permissions": []string{"members.manage", "clusters.write"}},
			{"id": "c0ffee00-0000-0000-0000-000000000001", "slug": "deployer", "name": "Deployer", "description": "Ships apps", "builtin": false, "permissions": []string{}},
		}})
	})
	fake.service["ffffffff-0000-0000-0000-000000000001"] = map[string]any{
		"id": "ffffffff-0000-0000-0000-000000000001", "account_id": "acct-ci", "account_name": "ci", "label": "main", "role_slug": "",
		"expires_at": "2027-01-15T10:00:00", "created_at": "2026-10-17T09:00:00", "revoked_at": nil, "last_used_at": "2026-10-17T09:30:00",
		"revoked": false, "scopes": []string{}, "project_id": nil, "project_name": nil,
	}

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: fake.providerBlock() + `
data "ankra_organisation_roles" "all" {}
data "ankra_api_tokens" "mine" {}
data "ankra_service_tokens" "all" {}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.ankra_organisation_roles.all", "roles.#", "2"),
					resource.TestCheckNoResourceAttr("data.ankra_organisation_roles.all", "roles.0.id"),
					resource.TestCheckResourceAttr("data.ankra_organisation_roles.all", "roles.0.permissions.#", "2"),
					resource.TestCheckResourceAttr("data.ankra_organisation_roles.all", "roles.1.id", "c0ffee00-0000-0000-0000-000000000001"),
					resource.TestCheckResourceAttr("data.ankra_organisation_roles.all", "roles.1.builtin", "false"),
					resource.TestCheckResourceAttr("data.ankra_api_tokens.mine", "tokens.#", "0"),
					resource.TestCheckResourceAttr("data.ankra_service_tokens.all", "tokens.#", "1"),
					resource.TestCheckResourceAttr("data.ankra_service_tokens.all", "tokens.0.role_slug", ""),
					resource.TestCheckResourceAttr("data.ankra_service_tokens.all", "tokens.0.last_used_at", "2026-10-17T09:30:00"),
					resource.TestCheckNoResourceAttr("data.ankra_service_tokens.all", "tokens.0.revoked_at"),
				),
			},
		},
	})
}

func TestAccServiceTokensDataSourceForbidden(t *testing.T) {
	fake := newRouteFake(t)
	fake.handle("GET /api/v1/org/organisation/service-tokens", func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(t, writer, http.StatusForbidden, map[string]any{"detail": "You do not have permission to view service tokens"})
	})
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:      fake.providerBlock() + `data "ankra_service_tokens" "all" {}`,
				ExpectError: regexp.MustCompile("Unable to list service tokens"),
			},
		},
	})
}

// TestAccServiceTokenAccountWithoutRole pins the stand-down decision: an
// account whose role assignments were removed reads back with an empty role,
// the plan shows it, and the apply re-mints the token on the same account,
// which grants the configured role again.
func TestAccServiceTokenAccountWithoutRole(t *testing.T) {
	fake := newTokenFake(t)
	config := fake.providerBlock() + `
resource "ankra_service_token" "ci" {
  name      = "ci-deployer"
  role_slug = "member"
}
`
	var firstID string
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: config,
				Check: func(state *terraform.State) error {
					firstID = state.RootModule().Resources["ankra_service_token.ci"].Primary.ID
					return nil
				},
			},
			{
				PreConfig: func() {
					for _, row := range fake.service {
						row["role_slug"] = ""
					}
				},
				Config: config,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("ankra_service_token.ci", plancheck.ResourceActionUpdate)},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ankra_service_token.ci", "role_slug", "member"),
					func(state *terraform.State) error {
						if state.RootModule().Resources["ankra_service_token.ci"].Primary.ID == firstID {
							return fmt.Errorf("the token was not re-minted")
						}
						if len(fake.service) != 1 || len(fake.standDowns) != 0 {
							return fmt.Errorf("re-mint left %v tokens or stood the account down (%v)", fake.service, fake.standDowns)
						}
						for _, row := range fake.service {
							if row["role_slug"] != "member" {
								return fmt.Errorf("the account did not get its role back: %v", row["role_slug"])
							}
						}
						return nil
					},
				),
			},
			{
				Config:             config,
				PlanOnly:           true,
				ExpectNonEmptyPlan: false,
			},
		},
	})
}

// secretSlotFake holds slots the way the platform does
// (go/internal/cliapi/mcpservers.go): the value is never answered, a label is
// unique per organisation, PUT replaces the value in place, and delete
// answers 409 while something references the slot.
type secretSlotFake struct {
	*routeFake
	slots      map[string]map[string]any
	values     map[string]string
	referenced map[string]bool
	created    int
	puts       int
}

func newSecretSlotFake(t *testing.T) *secretSlotFake {
	fake := &secretSlotFake{routeFake: newRouteFake(t), slots: map[string]map[string]any{}, values: map[string]string{}, referenced: map[string]bool{}}
	fake.handle("GET /api/v1/org/secret-slots", func(writer http.ResponseWriter, _ *http.Request) {
		identifiers := make([]string, 0, len(fake.slots))
		for identifier := range fake.slots {
			identifiers = append(identifiers, identifier)
		}
		sort.Strings(identifiers)
		listing := []map[string]any{}
		for _, identifier := range identifiers {
			listing = append(listing, fake.slots[identifier])
		}
		writeJSON(t, writer, http.StatusOK, map[string]any{"slots": listing})
	})
	fake.handle("POST /api/v1/org/secret-slots", func(writer http.ResponseWriter, request *http.Request) {
		var body map[string]json.RawMessage
		decodeJSON(t, request, &body)
		if _, present := body["ttl_seconds"]; present {
			t.Errorf("a ttl must not be sent: the slot would expire")
		}
		var label, value string
		var hint any
		_ = json.Unmarshal(body["label"], &label)
		_ = json.Unmarshal(body["value"], &value)
		_ = json.Unmarshal(body["hint"], &hint)
		for _, slot := range fake.slots {
			if slot["label"] == label {
				writeJSON(t, writer, http.StatusConflict, map[string]any{"detail": fmt.Sprintf("A slot with label '%s' already exists in this organisation", label)})
				return
			}
		}
		fake.created++
		identifier := fmt.Sprintf("5107a000-0000-0000-0000-%012d", fake.created)
		fake.slots[identifier] = map[string]any{
			"slot_id": identifier, "label": label, "hint": hint, "sentinel": "${SECRET_SLOT:" + identifier + "}", "created_via": "user",
			"created_at": "2026-10-17T09:00:00Z", "expires_at": nil, "pinned_at": nil, "last_accessed_at": nil, "access_count": 0,
			"created_by_user_id": nil, "references": []any{},
		}
		fake.values[identifier] = value
		writeJSON(t, writer, http.StatusOK, fake.slots[identifier])
	})
	fake.handle("PUT /api/v1/org/secret-slots/{slot_id}", func(writer http.ResponseWriter, request *http.Request) {
		slot, exists := fake.slots[request.PathValue("slot_id")]
		if !exists {
			writeJSON(t, writer, http.StatusNotFound, map[string]any{"detail": "Secret slot not found"})
			return
		}
		var body map[string]json.RawMessage
		decodeJSON(t, request, &body)
		if len(body) != 1 {
			writeJSON(t, writer, http.StatusBadRequest, map[string]any{"detail": "exactly one of value or generate"})
			return
		}
		var value string
		_ = json.Unmarshal(body["value"], &value)
		fake.values[request.PathValue("slot_id")] = value
		fake.puts++
		writeJSON(t, writer, http.StatusOK, slot)
	})
	fake.handle("DELETE /api/v1/org/secret-slots/{slot_id}", func(writer http.ResponseWriter, request *http.Request) {
		identifier := request.PathValue("slot_id")
		switch {
		case fake.slots[identifier] == nil:
			writeJSON(t, writer, http.StatusNotFound, map[string]any{"detail": "Secret slot not found"})
		case fake.referenced[identifier]:
			writeJSON(t, writer, http.StatusConflict, map[string]any{"detail": "Secret slot is still referenced"})
		default:
			delete(fake.slots, identifier)
			delete(fake.values, identifier)
			writeJSON(t, writer, http.StatusOK, map[string]any{"deleted": true})
		}
	})
	return fake
}

func TestAccSecretSlotLifecycle(t *testing.T) {
	fake := newSecretSlotFake(t)
	config := func(label, value string) string {
		return fake.providerBlock() + fmt.Sprintf(`
resource "ankra_secret_slot" "github" {
  label = %q
  value = %q
  hint  = "GitHub MCP token"
}
`, label, value)
	}
	var firstID string

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy: func(_ *terraform.State) error {
			if len(fake.slots) != 0 {
				return fmt.Errorf("slots left after destroy: %v", fake.slots)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: config("github-token", "ghp_one"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ankra_secret_slot.github", "created_via", "user"),
					resource.TestCheckResourceAttr("ankra_secret_slot.github", "hint", "GitHub MCP token"),
					resource.TestCheckNoResourceAttr("ankra_secret_slot.github", "pinned_at"),
					func(state *terraform.State) error {
						attributes := state.RootModule().Resources["ankra_secret_slot.github"].Primary.Attributes
						firstID = attributes["id"]
						if attributes["sentinel"] != "${SECRET_SLOT:"+firstID+"}" {
							return fmt.Errorf("sentinel %q does not name the slot", attributes["sentinel"])
						}
						if fake.values[firstID] != "ghp_one" {
							return fmt.Errorf("the platform holds %q", fake.values[firstID])
						}
						return nil
					},
				),
			},
			{
				Config: config("github-token", "ghp_two"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("ankra_secret_slot.github", plancheck.ResourceActionUpdate)},
				},
				Check: func(state *terraform.State) error {
					if state.RootModule().Resources["ankra_secret_slot.github"].Primary.ID != firstID || fake.values[firstID] != "ghp_two" || fake.puts != 1 {
						return fmt.Errorf("a value change must replace the value in place: puts %d, values %v", fake.puts, fake.values)
					}
					return nil
				},
			},
			{
				Config: config("github-pat", "ghp_two"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("ankra_secret_slot.github", plancheck.ResourceActionDestroyBeforeCreate)},
				},
			},
			{
				ResourceName:            "ankra_secret_slot.github",
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"value"},
			},
			{
				PreConfig: func() {
					for identifier := range fake.slots {
						fake.referenced[identifier] = true
					}
				},
				Config:      config("github-pat", "ghp_two"),
				Destroy:     true,
				ExpectError: regexp.MustCompile(`(?s)Unable to delete secret slot.*references the slot`),
			},
			{
				PreConfig: func() { fake.referenced = map[string]bool{} },
				Config:    config("github-pat", "ghp_two"),
			},
			{
				PreConfig: func() {
					for identifier := range fake.slots {
						delete(fake.slots, identifier)
					}
				},
				Config: config("github-pat", "ghp_two"),
				Check: func(_ *terraform.State) error {
					if len(fake.slots) != 1 {
						return fmt.Errorf("a slot deleted outside Terraform was not recreated")
					}
					return nil
				},
			},
		},
	})
}

func TestAccSecretSlotLabelTaken(t *testing.T) {
	fake := newSecretSlotFake(t)
	fake.slots["5107a000-0000-0000-0000-000000000099"] = map[string]any{"slot_id": "5107a000-0000-0000-0000-000000000099", "label": "taken", "sentinel": "x"}
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: fake.providerBlock() + `
resource "ankra_secret_slot" "dup" {
  label = "taken"
  value = "secret"
}
`,
				ExpectError: regexp.MustCompile(`(?s)Unable to create secret slot.*import it by`),
			},
			{
				Config: fake.providerBlock() + `
resource "ankra_secret_slot" "bad" {
  label = "two\nlines"
  value = ""
}
`,
				ExpectError: regexp.MustCompile(`Invalid label|Invalid secret value`),
			},
		},
	})
}

func TestAccAIReviewBindingSettings(t *testing.T) {
	fake := newOrganisationAIFake(t)
	config := func(attributes string) string {
		return fake.providerBlock() + fmt.Sprintf(`
resource "ankra_ai_review_binding" "github" {
  scm_provider        = "github"
  binding_external_id = %q
%s}
`, aiTestInstallation, attributes)
	}
	first := config(`  ai_review        = true
  mention_replies  = false
  merge_when_green = true
  review_model     = "claude-sonnet"
`)
	second := config(`  ai_review       = false
  mention_replies = false
  custom_instructions = "Prefer small diffs."
`)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy: func(_ *terraform.State) error {
			if fake.binding["custom_instructions"] != "Prefer small diffs." {
				return fmt.Errorf("destroy must leave the binding's settings as they are, got %v", fake.binding)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: first,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ankra_ai_review_binding.github", "id", "github/"+aiTestInstallation),
					resource.TestCheckResourceAttr("ankra_ai_review_binding.github", "display_name", "acme"),
					resource.TestCheckResourceAttr("ankra_ai_review_binding.github", "merge_when_green", "true"),
					resource.TestCheckResourceAttr("ankra_ai_review_binding.github", "pr_previews", "false"),
					resource.TestCheckResourceAttr("ankra_ai_review_binding.github", "max_reviews_per_pr", "0"),
					func(_ *terraform.State) error {
						write := fake.bindingWrites[0]
						for _, absent := range []string{"pr_previews", "review_drafts", "max_reviews_per_pr", "custom_instructions"} {
							if _, present := write[absent]; present {
								return fmt.Errorf("an unset %s must be left out so the platform keeps it, got %v", absent, write)
							}
						}
						if string(write["merge_when_green"]) != "true" {
							return fmt.Errorf("merge_when_green was not sent: %v", write)
						}
						return nil
					},
				),
			},
			{
				Config: second,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ankra_ai_review_binding.github", "ai_review", "false"),
					resource.TestCheckResourceAttr("ankra_ai_review_binding.github", "merge_when_green", "true"),
					resource.TestCheckResourceAttr("ankra_ai_review_binding.github", "review_model", "claude-sonnet"),
					resource.TestCheckResourceAttr("ankra_ai_review_binding.github", "custom_instructions", "Prefer small diffs."),
				),
			},
			{
				ResourceName:      "ankra_ai_review_binding.github",
				ImportState:       true,
				ImportStateId:     "github/" + aiTestInstallation,
				ImportStateVerify: true,
			},
		},
	})
}

func TestAccAIReviewBindingUnknown(t *testing.T) {
	fake := newOrganisationAIFake(t)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: fake.providerBlock() + `
resource "ankra_ai_review_binding" "gitlab" {
  scm_provider        = "gitlab"
  binding_external_id = "0b0b0b0b-0000-0000-0000-000000000000"
  ai_review           = true
  mention_replies     = true
}
`,
				ExpectError: regexp.MustCompile(`(?s)Unable to write AI review binding settings.*connect the source-control provider`),
			},
			{
				Config: fake.providerBlock() + `
resource "ankra_ai_review_binding" "svn" {
  scm_provider        = "svn"
  binding_external_id = "1"
  ai_review           = true
  mention_replies     = true
}
`,
				ExpectError: regexp.MustCompile(`value must be one of`),
			},
		},
	})
}

// TestAccAIEnvironmentRoleCapsAndHomeChannel pins the merge-on-write design
// of demo_ttl_role_caps: only the configured tiers are sent, the others keep
// the organisation's values, and destroy resets only the tiers the
// configuration set (and clears a home channel it set).
func TestAccAIEnvironmentRoleCapsAndHomeChannel(t *testing.T) {
	fake := newOrganisationAIFake(t)
	fake.environment["demo_ttl_role_caps"] = map[string]any{"admin": float64(720), "operator": float64(100), "member": float64(72)}
	first := fake.providerBlock() + `
resource "ankra_ai_environment" "this" {
  demo_ttl_role_caps = {
    member = 24
  }
  home_slack_channel_id   = "C0123"
  home_slack_channel_name = "ankra-alerts"
}
`
	second := fake.providerBlock() + `
resource "ankra_ai_environment" "this" {
  demo_ttl_role_caps = {
    admin  = 300
    member = 24
  }
}
`

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy: func(_ *terraform.State) error {
			caps, _ := fake.environment["demo_ttl_role_caps"].(map[string]any)
			if caps["admin"] != float64(720) || caps["member"] != float64(72) || caps["operator"] != float64(100) {
				return fmt.Errorf("destroy must reset the configured tiers only, got %v", caps)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: first,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ankra_ai_environment.this", "demo_ttl_role_caps.member", "24"),
					resource.TestCheckResourceAttr("ankra_ai_environment.this", "demo_ttl_role_caps.operator", "100"),
					resource.TestCheckResourceAttr("ankra_ai_environment.this", "demo_ttl_role_caps.admin", "720"),
					resource.TestCheckResourceAttr("ankra_ai_environment.this", "home_slack_channel_id", "C0123"),
					resource.TestCheckResourceAttr("ankra_ai_environment.this", "home_slack_channel_name", "ankra-alerts"),
					resource.TestCheckResourceAttr("ankra_ai_environment.this", "home_slack_team_id", "T0001"),
					func(_ *terraform.State) error {
						if string(fake.environmentWrites[0]["demo_ttl_role_caps"]) != `{"member":24}` {
							return fmt.Errorf("only the configured tier may be sent, got %s", fake.environmentWrites[0]["demo_ttl_role_caps"])
						}
						return nil
					},
				),
			},
			{
				Config: second,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ankra_ai_environment.this", "demo_ttl_role_caps.admin", "300"),
					resource.TestCheckResourceAttr("ankra_ai_environment.this", "demo_ttl_role_caps.operator", "100"),
					// Dropping the home channel from configuration stops
					// managing it without clearing it.
					resource.TestCheckResourceAttr("ankra_ai_environment.this", "home_slack_channel_id", "C0123"),
				),
			},
			{
				Config:   second,
				PlanOnly: true,
			},
		},
	})
}

func TestAccAIEnvironmentPlanTimeValidation(t *testing.T) {
	fake := newOrganisationAIFake(t)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: fake.providerBlock() + `
resource "ankra_ai_environment" "this" {
  home_slack_channel_name = "alerts"
}
`,
				ExpectError: regexp.MustCompile("Home Slack channel name without its id"),
			},
			{
				Config: fake.providerBlock() + `
resource "ankra_ai_environment" "this" {
  demo_ttl_role_caps = {
    operator = 0
  }
}
`,
				ExpectError: regexp.MustCompile("Value out of range"),
			},
		},
	})
	if served := fake.served(); len(served) != 0 {
		t.Errorf("an invalid configuration reached the platform: %v", served)
	}
}
