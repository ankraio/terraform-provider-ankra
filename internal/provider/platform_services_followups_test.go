// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

const (
	followupApplicationID = "12121212-1212-4212-8212-121212121212"
	followupCredentialID  = "34343434-3434-4434-8434-343434343434"
	followupProjectID     = "56565656-5656-4656-8656-565656565656"
)

func TestAccServiceDataSources(t *testing.T) {
	fake := newRouteFake(t)
	var instanceQueries []string
	fake.handle("GET /api/v1/org/service-packages", func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Query().Get("limit") != "50" {
			t.Errorf("packages listed without the largest page: %s", request.URL.RawQuery)
		}
		if request.URL.Query().Get("after") == "" {
			writeJSON(t, writer, http.StatusOK, map[string]any{"next_cursor": "page-2", "items": []map[string]any{
				{"id": "p-1", "publisher_id": "org", "name": "postgres", "version": "1.0.0", "capability": "postgresql",
					"digest": "sha256:1", "first_party": true, "created_at": "2026-10-01T00:00:00Z"},
			}})
			return
		}
		writeJSON(t, writer, http.StatusOK, map[string]any{"next_cursor": nil, "items": []map[string]any{
			{"id": "p-2", "publisher_id": "org", "name": "redis", "version": "2.0.0", "capability": "redis",
				"digest": "sha256:2", "first_party": false, "created_at": "2026-10-02T00:00:00Z"},
		}})
	})
	fake.handle("GET /api/v1/org/service-admission/instances", func(writer http.ResponseWriter, request *http.Request) {
		instanceQueries = append(instanceQueries, request.URL.RawQuery)
		writeJSON(t, writer, http.StatusOK, map[string]any{"next_cursor": nil, "scan_capped": false, "items": []map[string]any{
			{"id": "s-1", "name": "orders-db", "package_version_id": "p-1", "generation": 3, "mode": "customer",
				"cluster_id": "c-1", "cluster_name": "prod", "region": "eu-north-1", "data_boundary": "eu",
				"consumers": []map[string]any{{"id": "b-2"}, {"id": "b-1"}}, "state": "released", "readiness": "ready",
				"created_at": "2026-10-01T00:00:00Z", "released_at": "2026-10-02T00:00:00Z"},
		}})
	})
	fake.handle("GET /api/v1/org/service-admission/consumers", func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Query().Get("application_id") != followupApplicationID {
			writeJSON(t, writer, http.StatusUnprocessableEntity, map[string]any{"detail": "application_id is required"})
			return
		}
		writeJSON(t, writer, http.StatusOK, map[string]any{"next_cursor": nil, "items": []map[string]any{
			{"id": "b-1", "application_id": followupApplicationID, "cluster_id": "c-1", "namespace": "orders",
				"allow_planned": true, "local_only": false, "revision": 2, "updated_at": "2026-10-01T00:00:00Z"},
		}})
	})

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: fake.providerBlock() + fmt.Sprintf(`
data "ankra_service_packages" "all" {}

data "ankra_services" "released" {
  cluster_id = %q
  state      = "released"
}

data "ankra_service_consumers" "orders" {
  application_id = %q
}
`, followupCredentialID, followupApplicationID),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.ankra_service_packages.all", "packages.#", "2"),
					resource.TestCheckResourceAttr("data.ankra_service_packages.all", "packages.1.name", "redis"),
					resource.TestCheckResourceAttr("data.ankra_services.released", "services.0.state", "released"),
					resource.TestCheckResourceAttr("data.ankra_services.released", "services.0.released_at", "2026-10-02T00:00:00Z"),
					resource.TestCheckResourceAttr("data.ankra_services.released", "services.0.connected_consumer_ids.0", "b-1"),
					resource.TestCheckResourceAttr("data.ankra_service_consumers.orders", "consumers.0.namespace", "orders"),
					resource.TestCheckResourceAttr("data.ankra_service_consumers.orders", "consumers.0.allow_planned", "true"),
					func(_ *terraform.State) error {
						for _, query := range instanceQueries {
							if !strings.Contains(query, "state=released") || !strings.Contains(query, "cluster_id="+followupCredentialID) {
								return fmt.Errorf("services listed without the filters: %s", query)
							}
						}
						return nil
					},
				),
			},
		},
	})
}

func TestAccServiceDataSourcesFeatureOff(t *testing.T) {
	fake := newRouteFake(t)
	fake.handle("GET /api/v1/org/service-packages", func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(t, writer, http.StatusNotFound, map[string]any{"detail": "Not Found"})
	})
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:      fake.providerBlock() + `data "ankra_service_packages" "all" {}`,
				ExpectError: regexp.MustCompile("Managed services not available"),
			},
		},
	})
}

func TestAccRemediationAndDispositionDataSources(t *testing.T) {
	fake := newRouteFake(t)
	var dispositionQueries []string
	fake.handle("GET /api/v1/org/ai-remediation/policy", func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(t, writer, http.StatusOK, map[string]any{
			"enabled": false, "autonomy_level": "propose", "tier_overrides": map[string]any{}, "slack_webhook_id": nil,
			"approver_user_ids": []string{}, "max_actions_per_incident": 5, "cooldown_minutes": 30,
			"cluster_allow_list": nil, "updated_at": nil,
		})
	})
	fake.handle("GET /api/v1/org/security/vulnerability-dispositions", func(writer http.ResponseWriter, request *http.Request) {
		dispositionQueries = append(dispositionQueries, request.URL.RawQuery)
		page := request.URL.Query().Get("page")
		pageNumber := 1
		_, _ = fmt.Sscanf(page, "%d", &pageNumber)
		writeJSON(t, writer, http.StatusOK, map[string]any{
			"pagination": map[string]any{"page": pageNumber, "page_size": 100, "total_pages": 2, "total_count": 2},
			"result": []map[string]any{{
				"id": "d-" + page, "selector": map[string]any{"organisation_id": "o", "cve_id": "CVE-2026-0001"},
				"disposition": "accepted_risk", "reason": "vendor patch pending", "expires_at": nil,
				"expire_when_fix_available": true, "created_by": "u-1", "status": "active", "match_health": "healthy",
				"matched_occurrence_count": 4, "revoked_at": nil, "created_at": "2026-10-01T00:00:00Z",
			}},
		})
	})

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: fake.providerBlock() + `
data "ankra_security_dispositions" "bad" {
  statuses = ["open"]
}
`,
				ExpectError: regexp.MustCompile("Invalid status"),
			},
			{
				Config: fake.providerBlock() + `
data "ankra_ai_remediation_policy" "this" {}

data "ankra_security_dispositions" "live" {
  statuses    = ["active", "expiring"]
  disposition = "accepted_risk"
}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.ankra_ai_remediation_policy.this", "autonomy_level", "propose"),
					resource.TestCheckResourceAttr("data.ankra_ai_remediation_policy.this", "max_actions_per_incident", "5"),
					resource.TestCheckNoResourceAttr("data.ankra_ai_remediation_policy.this", "cluster_allow_list"),
					resource.TestCheckNoResourceAttr("data.ankra_ai_remediation_policy.this", "updated_at"),
					resource.TestCheckResourceAttr("data.ankra_security_dispositions.live", "dispositions.#", "2"),
					resource.TestCheckResourceAttr("data.ankra_security_dispositions.live", "dispositions.1.id", "d-2"),
					resource.TestCheckResourceAttr("data.ankra_security_dispositions.live", "dispositions.0.cve_id", "CVE-2026-0001"),
					resource.TestCheckNoResourceAttr("data.ankra_security_dispositions.live", "dispositions.0.package_name"),
					func(_ *terraform.State) error {
						for _, query := range dispositionQueries {
							if !strings.Contains(query, "status=active&status=expiring") || !strings.Contains(query, "disposition=accepted_risk") {
								return fmt.Errorf("dispositions listed without the filters: %s", query)
							}
						}
						return nil
					},
				),
			},
		},
	})
}

// costRateCardFake stores cards the way the platform does: the write echoes
// amounts as sent, the read pads them to six decimals, and deleting a card
// not set succeeds.
type costRateCardFake struct {
	*routeFake
	organisationCard map[string]any
	credentialCards  map[string]map[string]any
	puts             []map[string]any
}

func newCostRateCardFake(t *testing.T) *costRateCardFake {
	fake := &costRateCardFake{routeFake: newRouteFake(t), credentialCards: map[string]map[string]any{}}
	padded := func(card map[string]any) map[string]any {
		if card == nil {
			return nil
		}
		answer := map[string]any{"currency": card["currency"]}
		for _, key := range []string{"vcpu_monthly_amount", "memory_gb_monthly_amount", "storage_gb_monthly_amount"} {
			var value float64
			_, _ = fmt.Sscanf(fmt.Sprint(card[key]), "%g", &value)
			answer[key] = fmt.Sprintf("%.6f", value)
		}
		return answer
	}
	fake.handle("GET /api/v1/org/cloud-cost/rate-cards", func(writer http.ResponseWriter, _ *http.Request) {
		credentials := []map[string]any{}
		row := map[string]any{"credential_id": followupCredentialID, "credential_name": "proxmox-lab", "provider": "proxmox",
			"source": "none", "currency": "usd", "vcpu_monthly_amount": "0", "memory_gb_monthly_amount": "0",
			"storage_gb_monthly_amount": "0", "cluster_count": 1}
		if card, exists := fake.credentialCards[followupCredentialID]; exists {
			for key, value := range padded(card) {
				row[key] = value
			}
			row["source"] = "credential"
		} else if fake.organisationCard != nil {
			row["source"] = "organisation"
		}
		credentials = append(credentials, row)
		writeJSON(t, writer, http.StatusOK, map[string]any{"currency": "usd", "organisation_card": padded(fake.organisationCard),
			"provider_defaults": map[string]any{}, "credentials": credentials, "unpriced_cluster_count": 0})
	})
	fake.handle("PUT /api/v1/org/cloud-cost/rate-cards", func(writer http.ResponseWriter, request *http.Request) {
		var body map[string]any
		decodeJSON(t, request, &body)
		fake.puts = append(fake.puts, body)
		card := map[string]any{"currency": strings.ToLower(fmt.Sprint(body["currency"]))}
		for _, key := range []string{"vcpu_monthly_amount", "memory_gb_monthly_amount", "storage_gb_monthly_amount"} {
			card[key] = body[key]
		}
		credentialID, _ := body["credential_id"].(string)
		switch credentialID {
		case "", "organisation":
			fake.organisationCard = card
		case followupCredentialID:
			fake.credentialCards[credentialID] = card
		default:
			writeJSON(t, writer, http.StatusBadRequest, map[string]any{"detail": "invalid rate card: unknown credential"})
			return
		}
		writeJSON(t, writer, http.StatusOK, card)
	})
	fake.handle("DELETE /api/v1/org/cloud-cost/rate-cards/{credential_id}", func(writer http.ResponseWriter, request *http.Request) {
		if request.PathValue("credential_id") == "organisation" {
			fake.organisationCard = nil
		} else {
			delete(fake.credentialCards, request.PathValue("credential_id"))
		}
		writer.WriteHeader(http.StatusNoContent)
	})
	return fake
}

func TestAccCostRateCards(t *testing.T) {
	fake := newCostRateCardFake(t)
	config := func(vcpu string) string {
		return fake.providerBlock() + fmt.Sprintf(`
resource "ankra_cost_rate_card" "fallback" {
  currency            = "EUR"
  vcpu_monthly_amount = %q
}

resource "ankra_cost_rate_card" "lab" {
  credential_id            = %q
  currency                 = "usd"
  vcpu_monthly_amount      = "12.50"
  memory_gb_monthly_amount = "3"
}
`, vcpu, followupCredentialID)
	}

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy: func(_ *terraform.State) error {
			if fake.organisationCard != nil || len(fake.credentialCards) != 0 {
				return fmt.Errorf("cards left after destroy: %v %v", fake.organisationCard, fake.credentialCards)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: config("3"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ankra_cost_rate_card.fallback", "id", "organisation"),
					resource.TestCheckResourceAttr("ankra_cost_rate_card.fallback", "currency", "EUR"),
					resource.TestCheckResourceAttr("ankra_cost_rate_card.fallback", "vcpu_monthly_amount", "3"),
					resource.TestCheckResourceAttr("ankra_cost_rate_card.fallback", "storage_gb_monthly_amount", "0"),
					resource.TestCheckResourceAttr("ankra_cost_rate_card.lab", "id", followupCredentialID),
					resource.TestCheckResourceAttr("ankra_cost_rate_card.lab", "vcpu_monthly_amount", "12.50"),
					func(_ *terraform.State) error {
						for _, body := range fake.puts {
							if _, present := body["credential_id"]; !present {
								return fmt.Errorf("a card write left out credential_id: %v", body)
							}
						}
						return nil
					},
				),
			},
			{
				// The read pads amounts ("3.000000"): the same values plan nothing.
				Config:   config("3"),
				PlanOnly: true,
			},
			{
				Config: config("4.25"),
				Check:  resource.TestCheckResourceAttr("ankra_cost_rate_card.fallback", "vcpu_monthly_amount", "4.25"),
			},
			{
				// A card removed outside Terraform is set again.
				PreConfig: func() { delete(fake.credentialCards, followupCredentialID) },
				Config:    config("4.25"),
				Check: func(_ *terraform.State) error {
					if _, exists := fake.credentialCards[followupCredentialID]; !exists {
						return fmt.Errorf("the removed credential card was not set again")
					}
					return nil
				},
			},
			{
				ResourceName:      "ankra_cost_rate_card.fallback",
				ImportState:       true,
				ImportStateId:     "organisation",
				ImportStateVerify: true,
				// The import reads the platform's spelling.
				ImportStateVerifyIgnore: []string{"currency"},
			},
		},
	})
}

func TestAccCostRateCardValidation(t *testing.T) {
	fake := newCostRateCardFake(t)
	for name, attributes := range map[string]string{
		"Rate card prices nothing": `currency = "usd"`,
		"Invalid amount":           "currency = \"usd\"\n  vcpu_monthly_amount = \"-1\"",
		"Invalid value":            "currency = \"btc\"\n  vcpu_monthly_amount = \"1\"",
	} {
		resource.Test(t, resource.TestCase{
			ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
			Steps: []resource.TestStep{{
				Config:      fake.providerBlock() + "resource \"ankra_cost_rate_card\" \"bad\" {\n  " + attributes + "\n}\n",
				ExpectError: regexp.MustCompile(name),
			}},
		})
	}
	if len(fake.puts) != 0 {
		t.Errorf("an invalid card reached the platform: %v", fake.puts)
	}
}

func TestAccCostAutopilotProjectDefault(t *testing.T) {
	fake := newRouteFake(t)
	projects := map[string]map[string]any{}
	var putBodies []map[string]json.RawMessage
	var projectListings, deletes int
	registerProjects(fake, map[string]string{"payments": followupProjectID}, &projectListings)
	fake.handle("GET /api/v1/org/cloud-cost/autopilot", func(writer http.ResponseWriter, _ *http.Request) {
		layers := []map[string]any{}
		for _, project := range projects {
			layers = append(layers, project)
		}
		writeJSON(t, writer, http.StatusOK, map[string]any{"is_set": false, "defaults": map[string]any{}, "clusters": []any{},
			"environment_kinds": []string{"production"}, "project_defaults": layers})
	})
	fake.handle("PUT /api/v1/org/cloud-cost/autopilot/projects/{project_id}", func(writer http.ResponseWriter, request *http.Request) {
		if request.PathValue("project_id") != followupProjectID {
			writeJSON(t, writer, http.StatusNotFound, map[string]any{"detail": "Project not found"})
			return
		}
		var body map[string]json.RawMessage
		decodeJSON(t, request, &body)
		putBodies = append(putBodies, body)
		var defaults map[string]string
		_ = json.Unmarshal(body["defaults"], &defaults)
		project := map[string]any{"project_id": followupProjectID, "project_name": "payments", "defaults": defaults,
			"set_by": "u-1", "set_at": "2026-10-18T00:00:00Z"}
		projects[followupProjectID] = project
		writeJSON(t, writer, http.StatusOK, project)
	})
	fake.handle("DELETE /api/v1/org/cloud-cost/autopilot/projects/{project_id}", func(writer http.ResponseWriter, request *http.Request) {
		deletes++
		delete(projects, request.PathValue("project_id"))
		writeJSON(t, writer, http.StatusOK, map[string]any{"project_id": request.PathValue("project_id"), "project_name": "payments",
			"defaults": map[string]any{}, "set_by": "", "set_at": "0001-01-01T00:00:00Z"})
	})
	config := func(defaults string) string {
		return fake.providerBlock() + fmt.Sprintf(`
resource "ankra_cost_autopilot_project_default" "payments" {
  project_id = %q
  defaults   = %s
}
`, followupProjectID, defaults)
	}

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy: func(_ *terraform.State) error {
			if len(projects) != 0 {
				return fmt.Errorf("project defaults left after destroy: %v", projects)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config:      config(`{ production = "turbo" }`),
				ExpectError: regexp.MustCompile("Unknown tier"),
			},
			{
				Config:      config(`{}`),
				ExpectError: regexp.MustCompile("Empty defaults"),
			},
			{
				Config: config(`{ production = "managed", preview = "ephemeral" }`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ankra_cost_autopilot_project_default.payments", "project_name", "payments"),
					resource.TestCheckResourceAttr("ankra_cost_autopilot_project_default.payments", "defaults.preview", "ephemeral"),
				),
			},
			{
				// The map replaces the layer as a whole.
				Config: config(`{ production = "scheduled" }`),
				Check: func(_ *terraform.State) error {
					defaults, _ := projects[followupProjectID]["defaults"].(map[string]string)
					if len(defaults) != 1 || defaults["production"] != "scheduled" {
						return fmt.Errorf("the project layer was not replaced: %v", defaults)
					}
					return nil
				},
			},
			{
				PreConfig:          func() { delete(projects, followupProjectID) },
				Config:             config(`{ production = "scheduled" }`),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
			{
				Config: config(`{ production = "scheduled" }`),
			},
			{
				ResourceName:      "ankra_cost_autopilot_project_default.payments",
				ImportState:       true,
				ImportStateId:     followupProjectID,
				ImportStateVerify: true,
			},
			{
				// The same project by slug is an in-place change, not a move.
				Config: strings.Replace(config(`{ production = "scheduled" }`), fmt.Sprintf("%q", followupProjectID), `"payments"`, 1),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ankra_cost_autopilot_project_default.payments", "project_id", "payments"),
					resource.TestCheckResourceAttr("ankra_cost_autopilot_project_default.payments", "id", followupProjectID),
					func(_ *terraform.State) error {
						if deletes != 0 {
							return fmt.Errorf("respelling the project as its slug replaced the resource")
						}
						return nil
					},
				),
			},
		},
	})
}
