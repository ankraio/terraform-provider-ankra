// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"ankra.io/terraform-provider-ankra/internal/client"
)

const aiTestInstallation = "4242"

// aiFake holds the AI review bindings, related repositories, spend cap and
// AI environment the way the platform does
// (go/internal/aigatewayapi, go/internal/usecase/gatewayprofiles,
// go/internal/billingapi, go/internal/usecase/aienvironment): rules are
// stored lowercased and an absent review_drafts or max_reviews_per_pr keeps
// the stored value (or the binding's for a new rule); related pairs are
// stored lowercased and ordered, relating an existing pair answers the
// existing row, delete answers 204 or 404; the spend cap and the
// environment keep absent keys and clear on null, and the environment
// lowercases its preview names.
type aiFake struct {
	*routeFake
	rules             map[string]map[string]any
	related           map[string]map[string]any
	spendCap          map[string]any
	environment       map[string]any
	ruleWrites        []map[string]json.RawMessage
	spendCapWrites    []map[string]json.RawMessage
	environmentWrites []map[string]json.RawMessage
	created           int
}

func aiFakeEnvironmentDefaults() map[string]any {
	return map[string]any{
		"staging_cluster_id": nil, "workspace_ttl_hours": float64(8), "demo_ttl_hours": float64(24), "default_gateway_mode": "ask",
		"demo_base_domain": nil, "demo_ingress_class_name": nil, "demo_tls_secret_name": nil, "demo_cert_issuer_name": nil,
	}
}

func newAIFake(t *testing.T) *aiFake {
	fake := &aiFake{
		routeFake: newRouteFake(t), rules: map[string]map[string]any{}, related: map[string]map[string]any{},
		spendCap:    map[string]any{"daily_chat_usd_cap": nil, "daily_token_soft_cap": nil, "daily_token_hard_cap": nil},
		environment: aiFakeEnvironmentDefaults(),
	}

	fake.handle("GET /api/v1/org/ai-gateway/scm-bindings", func(writer http.ResponseWriter, _ *http.Request) {
		names := make([]string, 0, len(fake.rules))
		for name := range fake.rules {
			names = append(names, name)
		}
		sort.Strings(names)
		overrides := []map[string]any{}
		for _, name := range names {
			overrides = append(overrides, fake.rules[name])
		}
		writeJSON(t, writer, http.StatusOK, map[string]any{"bindings": []map[string]any{{
			"provider": "github", "binding_external_id": aiTestInstallation, "display_name": "acme", "detail": "",
			"ai_review": true, "mention_replies": true, "pr_previews": false, "merge_when_green": false, "review_model": "",
			"review_drafts": false, "max_reviews_per_pr": 0, "repo_overrides": overrides,
		}}})
	})
	fake.handle("PUT /api/v1/org/ai-gateway/scm-bindings/{provider}/{binding_external_id}/repo-overrides", func(writer http.ResponseWriter, request *http.Request) {
		if request.PathValue("provider") != "github" || request.PathValue("binding_external_id") != aiTestInstallation {
			writeJSON(t, writer, http.StatusNotFound, map[string]any{"detail": "Unknown source-control binding."})
			return
		}
		var body map[string]json.RawMessage
		decodeJSON(t, request, &body)
		fake.ruleWrites = append(fake.ruleWrites, body)
		var name string
		_ = json.Unmarshal(body["repo_full_name"], &name)
		name = strings.ToLower(name)
		rule, exists := fake.rules[name]
		if !exists {
			rule = map[string]any{"repo_full_name": name, "review_model": "", "review_drafts": false, "max_reviews_per_pr": float64(0)}
			fake.rules[name] = rule
		}
		for key, raw := range body {
			if key == "repo_full_name" {
				continue
			}
			var value any
			_ = json.Unmarshal(raw, &value)
			rule[key] = value
		}
		writeJSON(t, writer, http.StatusOK, map[string]any{"success": true, "repo_full_name": name})
	})
	fake.handle("DELETE /api/v1/org/ai-gateway/scm-bindings/{provider}/{binding_external_id}/repo-overrides", func(writer http.ResponseWriter, request *http.Request) {
		name := strings.ToLower(request.URL.Query().Get("repo_full_name"))
		_, removed := fake.rules[name]
		delete(fake.rules, name)
		writeJSON(t, writer, http.StatusOK, map[string]any{"success": true, "repo_full_name": name, "removed": removed})
	})

	fake.handle("GET /api/v1/org/ai-gateway/related-repos", func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Query().Get("binding_external_id") != aiTestInstallation {
			writeJSON(t, writer, http.StatusNotFound, map[string]any{"detail": "GitHub App installation not found."})
			return
		}
		listing := []map[string]any{}
		for _, row := range fake.related {
			listing = append(listing, row)
		}
		writeJSON(t, writer, http.StatusOK, map[string]any{"related_repos": listing, "total": len(listing)})
	})
	fake.handle("POST /api/v1/org/ai-gateway/related-repos", func(writer http.ResponseWriter, request *http.Request) {
		var body map[string]any
		decodeJSON(t, request, &body)
		first := strings.ToLower(fmt.Sprint(body["repo_full_name"]))
		second := strings.ToLower(fmt.Sprint(body["related_repo_full_name"]))
		if first == second {
			writeJSON(t, writer, http.StatusUnprocessableEntity, map[string]any{"detail": "A repository cannot be related to itself."})
			return
		}
		if first > second {
			first, second = second, first
		}
		for _, row := range fake.related {
			if row["repo_full_name"] == first && row["related_repo_full_name"] == second {
				writeJSON(t, writer, http.StatusOK, row)
				return
			}
		}
		fake.created++
		identifier := fmt.Sprintf("77777777-0000-0000-0000-%012d", fake.created)
		row := map[string]any{"id": identifier, "provider": body["provider"], "repo_full_name": first, "related_repo_full_name": second}
		fake.related[identifier] = row
		writeJSON(t, writer, http.StatusOK, row)
	})
	fake.handle("DELETE /api/v1/org/ai-gateway/related-repos/{related_repo_id}", func(writer http.ResponseWriter, request *http.Request) {
		if _, exists := fake.related[request.PathValue("related_repo_id")]; !exists {
			writeJSON(t, writer, http.StatusNotFound, map[string]any{"detail": "Related repository not found."})
			return
		}
		delete(fake.related, request.PathValue("related_repo_id"))
		writer.WriteHeader(http.StatusNoContent)
	})

	renderSpendCap := func() map[string]any {
		row := map[string]any{"plan_default_usd": 5.0, "spent_today_usd": 0.0, "token_budget_state": "unlimited"}
		for key, value := range fake.spendCap {
			row[key] = value
		}
		row["effective_usd"] = 5.0
		if value, isSet := fake.spendCap["daily_chat_usd_cap"].(float64); isSet {
			row["effective_usd"] = value
		}
		return row
	}
	fake.handle("GET /api/v1/org/billing/ai-spend-cap", func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(t, writer, http.StatusOK, renderSpendCap())
	})
	fake.handle("PUT /api/v1/org/billing/ai-spend-cap", func(writer http.ResponseWriter, request *http.Request) {
		var body map[string]json.RawMessage
		decodeJSON(t, request, &body)
		fake.spendCapWrites = append(fake.spendCapWrites, body)
		next := map[string]any{}
		for key, value := range fake.spendCap {
			next[key] = value
		}
		for key, raw := range body {
			var value any
			_ = json.Unmarshal(raw, &value)
			next[key] = value
		}
		soft, hasSoft := next["daily_token_soft_cap"].(float64)
		hard, hasHard := next["daily_token_hard_cap"].(float64)
		if hasSoft && hasHard && hard < soft {
			writeJSON(t, writer, http.StatusBadRequest, map[string]any{"detail": "the hard token cap cannot be below the soft cap"})
			return
		}
		fake.spendCap = next
		writeJSON(t, writer, http.StatusOK, renderSpendCap())
	})

	renderEnvironment := func() map[string]any {
		row := map[string]any{"staging_cluster_name": nil, "staging_cluster_online": false, "demo_ttl_role_caps": map[string]any{"admin": 720}, "dns_root_domain": nil}
		for key, value := range fake.environment {
			row[key] = value
		}
		if fake.environment["staging_cluster_id"] != nil {
			row["staging_cluster_name"] = "staging"
		}
		return row
	}
	fake.handle("GET /api/v1/org/ai-environment", func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(t, writer, http.StatusOK, renderEnvironment())
	})
	fake.handle("PUT /api/v1/org/ai-environment", func(writer http.ResponseWriter, request *http.Request) {
		var body map[string]json.RawMessage
		decodeJSON(t, request, &body)
		fake.environmentWrites = append(fake.environmentWrites, body)
		if _, present := body["dns_root_domain"]; present {
			t.Errorf("the AI environment write must not touch dns_root_domain")
		}
		for key, raw := range body {
			var value any
			_ = json.Unmarshal(raw, &value)
			if text, isText := value.(string); isText && strings.HasPrefix(key, "demo_") {
				text = strings.ToLower(strings.TrimSpace(text))
				if text == "" {
					value = nil
				} else {
					value = text
				}
			}
			if value == nil && (key == "workspace_ttl_hours" || key == "demo_ttl_hours" || key == "default_gateway_mode") {
				continue
			}
			fake.environment[key] = value
		}
		writeJSON(t, writer, http.StatusOK, renderEnvironment())
	})
	return fake
}

func TestAccAIReviewLifecycle(t *testing.T) {
	fake := newAIFake(t)
	first := fake.providerBlock() + fmt.Sprintf(`
resource "ankra_ai_review_repository" "portal" {
  scm_provider        = "github"
  binding_external_id = %[1]q
  repo_full_name      = "Acme/Portal"
  ai_review           = true
  mention_replies     = false
  pr_previews         = true
  review_model        = "claude-sonnet"
  max_reviews_per_pr  = 3
}

resource "ankra_ai_review_related_repository" "api" {
  binding_external_id    = %[1]q
  repo_full_name         = "Acme/Portal"
  related_repo_full_name = "acme/api"
}
`, aiTestInstallation)
	second := fake.providerBlock() + fmt.Sprintf(`
resource "ankra_ai_review_repository" "portal" {
  scm_provider        = "github"
  binding_external_id = %[1]q
  repo_full_name      = "Acme/Portal"
  ai_review           = false
  mention_replies     = false
  pr_previews         = true
}

resource "ankra_ai_review_related_repository" "api" {
  binding_external_id    = %[1]q
  repo_full_name         = "Acme/Portal"
  related_repo_full_name = "acme/api"
}
`, aiTestInstallation)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy: func(_ *terraform.State) error {
			if len(fake.rules) != 0 || len(fake.related) != 0 {
				return fmt.Errorf("left after destroy: rules %v, related %v", fake.rules, fake.related)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: first,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ankra_ai_review_repository.portal", "repo_full_name", "Acme/Portal"),
					resource.TestCheckResourceAttr("ankra_ai_review_repository.portal", "review_drafts", "false"),
					resource.TestCheckResourceAttr("ankra_ai_review_repository.portal", "max_reviews_per_pr", "3"),
					resource.TestCheckResourceAttr("ankra_ai_review_related_repository.api", "scm_provider", "github"),
					func(_ *terraform.State) error {
						if _, present := fake.ruleWrites[0]["review_drafts"]; present {
							return fmt.Errorf("an unset review_drafts must be left out so the platform keeps its value")
						}
						return nil
					},
				),
			},
			{
				Config: second,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ankra_ai_review_repository.portal", "ai_review", "false"),
					resource.TestCheckNoResourceAttr("ankra_ai_review_repository.portal", "review_model"),
					resource.TestCheckResourceAttr("ankra_ai_review_repository.portal", "max_reviews_per_pr", "3"),
					func(_ *terraform.State) error {
						if fake.rules["acme/portal"]["review_model"] != "" {
							return fmt.Errorf("removing review_model must send an empty model to inherit the binding's, platform holds %v", fake.rules["acme/portal"]["review_model"])
						}
						return nil
					},
				),
			},
			{
				ResourceName:            "ankra_ai_review_repository.portal",
				ImportState:             true,
				ImportStateId:           "github/" + aiTestInstallation + "/acme/portal",
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"repo_full_name"},
			},
			{
				ResourceName:            "ankra_ai_review_related_repository.api",
				ImportState:             true,
				ImportStateIdFunc:       aiRelatedImportID,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"repo_full_name", "related_repo_full_name"},
			},
			{
				PreConfig: func() {
					fake.rules = map[string]map[string]any{}
					fake.related = map[string]map[string]any{}
				},
				Config: second,
				Check: func(_ *terraform.State) error {
					if len(fake.rules) != 1 || len(fake.related) != 1 {
						return fmt.Errorf("rule and pair removed outside Terraform were not recreated")
					}
					return nil
				},
			},
		},
	})
}

func aiRelatedImportID(state *terraform.State) (string, error) {
	attributes := state.RootModule().Resources["ankra_ai_review_related_repository.api"].Primary.Attributes
	return "github/" + aiTestInstallation + "/" + attributes["id"], nil
}

func TestAIReviewPairMatchesEitherOrder(t *testing.T) {
	model := aiReviewRelatedRepositoryModel{}
	stored := client.AIReviewRelatedRepository{RepoFullName: "acme/api", RelatedRepoFullName: "acme/portal"}
	for _, pair := range [][2]string{{"Acme/Portal", "acme/api"}, {"acme/api", "ACME/portal"}} {
		model.RepoFullName = types.StringValue(pair[0])
		model.RelatedRepoFullName = types.StringValue(pair[1])
		if !aiReviewPairMatches(model, stored) {
			t.Errorf("%v should match the stored pair %v", pair, stored)
		}
	}
	model.RepoFullName = types.StringValue("acme/web")
	if aiReviewPairMatches(model, stored) {
		t.Errorf("a different repository matched the stored pair")
	}
}

// TestAccAISpendCapAndEnvironmentLifecycle pins that the singletons manage
// only what configuration sets: values set in the portal survive create,
// update and destroy, and destroy resets only the managed fields.
func TestAccAISpendCapAndEnvironmentLifecycle(t *testing.T) {
	fake := newAIFake(t)
	fake.spendCap["daily_token_soft_cap"] = float64(1000000)
	fake.environment["staging_cluster_id"] = "12121212-0000-0000-0000-000000000001"
	fake.environment["demo_ttl_hours"] = float64(36)
	first := fake.providerBlock() + `
resource "ankra_ai_spend_cap" "this" {
  daily_chat_usd_cap = 25
}

resource "ankra_ai_environment" "this" {
  default_gateway_mode = "agent"
  demo_base_domain     = "Previews.Example.com"
}
`
	second := fake.providerBlock() + `
resource "ankra_ai_spend_cap" "this" {
  daily_token_hard_cap = 5000000
}

resource "ankra_ai_environment" "this" {
  demo_ttl_hours = 48
}
`

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy: func(_ *terraform.State) error {
			if fake.spendCap["daily_token_hard_cap"] != nil {
				return fmt.Errorf("destroy did not clear the managed hard cap: %v", fake.spendCap)
			}
			if fake.spendCap["daily_token_soft_cap"] != float64(1000000) || fake.spendCap["daily_chat_usd_cap"] != float64(25) {
				return fmt.Errorf("destroy touched caps Terraform no longer manages: %v", fake.spendCap)
			}
			if fake.environment["demo_ttl_hours"] != float64(24) {
				return fmt.Errorf("destroy did not reset the managed demo_ttl_hours: %v", fake.environment)
			}
			if fake.environment["staging_cluster_id"] == nil || fake.environment["default_gateway_mode"] != "agent" || fake.environment["demo_base_domain"] != "previews.example.com" {
				return fmt.Errorf("destroy touched settings Terraform no longer manages: %v", fake.environment)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: first,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ankra_ai_spend_cap.this", "effective_usd", "25"),
					resource.TestCheckResourceAttr("ankra_ai_spend_cap.this", "daily_token_soft_cap", "1000000"),
					resource.TestCheckResourceAttr("ankra_ai_environment.this", "demo_base_domain", "Previews.Example.com"),
					resource.TestCheckResourceAttr("ankra_ai_environment.this", "staging_cluster_name", "staging"),
					resource.TestCheckResourceAttr("ankra_ai_environment.this", "demo_ttl_hours", "36"),
					func(_ *terraform.State) error {
						if len(fake.spendCapWrites) != 1 || len(fake.spendCapWrites[0]) != 1 {
							return fmt.Errorf("the cap write must name only the configured cap, got %v", fake.spendCapWrites)
						}
						if len(fake.environmentWrites) != 1 || len(fake.environmentWrites[0]) != 2 {
							return fmt.Errorf("the environment write must name only the configured settings, got %v", fake.environmentWrites)
						}
						return nil
					},
				),
			},
			{
				Config: second,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ankra_ai_spend_cap.this", "daily_chat_usd_cap", "25"),
					resource.TestCheckResourceAttr("ankra_ai_environment.this", "default_gateway_mode", "agent"),
					func(_ *terraform.State) error {
						write := fake.spendCapWrites[len(fake.spendCapWrites)-1]
						if len(write) != 1 || string(write["daily_token_hard_cap"]) != "5000000" {
							return fmt.Errorf("a dropped cap must stop being written, got %v", write)
						}
						environmentWrite := fake.environmentWrites[len(fake.environmentWrites)-1]
						if _, present := environmentWrite["staging_cluster_id"]; present {
							return fmt.Errorf("the provider must never send staging_cluster_id it was not given, got %v", environmentWrite)
						}
						return nil
					},
				),
			},
			{
				ResourceName:      "ankra_ai_spend_cap.this",
				ImportState:       true,
				ImportStateId:     "ai_spend_cap",
				ImportStateVerify: true,
			},
			{
				ResourceName:            "ankra_ai_environment.this",
				ImportState:             true,
				ImportStateId:           "ai_environment",
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"demo_base_domain"},
			},
			{
				PreConfig: func() {
					fake.spendCap["daily_token_hard_cap"] = float64(9000000)
					fake.environment["demo_ttl_hours"] = float64(12)
				},
				Config: second,
				Check: func(_ *terraform.State) error {
					if fake.spendCap["daily_token_hard_cap"] != float64(5000000) || fake.environment["demo_ttl_hours"] != float64(48) {
						return fmt.Errorf("drift was not corrected: cap %v, environment %v", fake.spendCap, fake.environment)
					}
					return nil
				},
			},
		},
	})
}
