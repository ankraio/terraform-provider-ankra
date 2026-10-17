// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

const securityTestOrganisationID = "0b7d3c1e-0000-4000-8000-000000000001"

// securityFake holds disposition policies, the compliance catalogue and
// report schedules the way the platform does: policies are only listed
// (paged, revoked ones included) and revoked softly, an update answers the
// bare policy while a create answers a policy and preview envelope, the
// framework catalogue is a fixed list with a per-organisation toggle, and a
// report schedule can be replaced but never removed.
type securityFake struct {
	*routeFake
	policies      map[string]map[string]any
	policyOrder   []string
	revokeReasons map[string]string
	frameworks    map[string]map[string]any
	schedules     map[string]map[string]any
	clusters      map[string]bool
	scheduleWrite []map[string]any
	previews      []map[string]any
}

// securityFakeSelectorHash derives a selector hash the way the platform
// does in effect: from the anchoring occurrence and the scope, so a preview
// and a create of the same pair agree and any other pair differs.
func securityFakeSelectorHash(occurrenceID any, scope string) string {
	if scope == "" {
		scope = "organisation_addon"
	}
	return fmt.Sprintf("hash-%v-%s", occurrenceID, scope)
}

func newSecurityFake(t *testing.T) *securityFake {
	fake := &securityFake{
		routeFake:     newRouteFake(t),
		policies:      map[string]map[string]any{},
		revokeReasons: map[string]string{},
		frameworks: map[string]map[string]any{
			"cis-kubernetes": {"key": "cis-kubernetes", "title": "CIS Kubernetes Benchmark", "short_title": "CIS", "version": "1.9", "description": "CIS", "control_count": 120, "automated_control_count": 90, "enabled": false, "enabled_at": nil},
			"soc2":           {"key": "soc2", "title": "SOC 2", "short_title": "SOC 2", "version": "2017", "description": "SOC 2", "control_count": 60, "automated_control_count": 20, "enabled": false, "enabled_at": nil},
		},
		schedules: map[string]map[string]any{},
		clusters:  map[string]bool{"5e3c2a10-7d4b-4f6e-9a21-3b4c5d6e7f80": true},
	}
	created := 0

	fake.handle("GET /api/v1/org/security/vulnerability-dispositions", func(writer http.ResponseWriter, request *http.Request) {
		page, _ := strconv.Atoi(request.URL.Query().Get("page"))
		pageSize, _ := strconv.Atoi(request.URL.Query().Get("page_size"))
		if page < 1 || pageSize < 1 || pageSize > 100 {
			t.Errorf("listing sent page %d page_size %d", page, pageSize)
		}
		totalPages := (len(fake.policyOrder) + pageSize - 1) / pageSize
		start := (page - 1) * pageSize
		result := []map[string]any{}
		for index := start; index < len(fake.policyOrder) && index < start+pageSize; index++ {
			result = append(result, fake.policies[fake.policyOrder[index]])
		}
		writeJSON(t, writer, http.StatusOK, map[string]any{
			"result":     result,
			"pagination": map[string]any{"page": page, "page_size": pageSize, "total_pages": totalPages, "total_count": len(fake.policyOrder)},
		})
	})
	fake.handle("POST /api/v1/org/security/vulnerability-dispositions/preview", func(writer http.ResponseWriter, request *http.Request) {
		var body map[string]any
		decodeJSON(t, request, &body)
		fake.previews = append(fake.previews, body)
		scope, _ := body["scope"].(string)
		writeJSON(t, writer, http.StatusOK, map[string]any{
			"policy_id": nil, "selector": map[string]any{"cve_id": "CVE-2026-0001"},
			"selector_hash": securityFakeSelectorHash(body["occurrence_id"], scope), "affected_occurrences": 3,
		})
	})
	fake.handle("POST /api/v1/org/security/vulnerability-dispositions", func(writer http.ResponseWriter, request *http.Request) {
		var body map[string]any
		decodeJSON(t, request, &body)
		for _, key := range []string{"occurrence_id", "disposition", "reason", "expire_when_fix_available", "allow_unmatched"} {
			if _, present := body[key]; !present {
				t.Errorf("create omitted %q: %v", key, body)
			}
		}
		if body["disposition"] == "accepted_risk" && body["expires_at"] == nil {
			writeJSON(t, writer, http.StatusBadRequest, map[string]any{"detail": "Accepted risk requires a review deadline"})
			return
		}
		scope, _ := body["scope"].(string)
		selector := map[string]any{"organisation_id": securityTestOrganisationID, "cve_id": "CVE-2026-0001", "package_type": "golang", "package_name": "golang.org/x/net"}
		switch scope {
		case "", "organisation_addon":
			selector["addon_slug"] = "ingress-nginx"
		case "workload":
			selector["cluster_id"] = "5e3c2a10-7d4b-4f6e-9a21-3b4c5d6e7f80"
			selector["workload_kind"] = "Deployment"
			selector["workload_namespace"] = "web"
			selector["workload_name"] = "frontend"
			selector["explicit_broad"] = true
		case "image":
			selector["image_digest"] = "sha256:abc"
			selector["explicit_broad"] = true
		default:
			writeJSON(t, writer, http.StatusBadRequest, map[string]any{"detail": "unsupported scope"})
			return
		}
		created++
		id := fmt.Sprintf("00000000-0000-4000-8000-%012d", created)
		policy := map[string]any{
			"id":                        id,
			"selector":                  selector,
			"selector_hash":             securityFakeSelectorHash(body["occurrence_id"], scope),
			"disposition":               body["disposition"],
			"reason":                    body["reason"],
			"expires_at":                securityFakeTimestamp(t, body["expires_at"]),
			"expire_when_fix_available": body["expire_when_fix_available"],
			"created_by":                "user-1",
			"updated_by":                "user-1",
			"status":                    "active",
			"match_health":              "matched",
			"matched_occurrence_count":  3,
			"active_match_count":        3,
			"fix_available_match_count": 0,
			"revoked_at":                nil,
			"revoked_by":                nil,
			"revoked_reason":            nil,
			"created_at":                "2026-10-17T10:00:00.123456Z",
			"updated_at":                "2026-10-17T10:00:00.123456Z",
		}
		fake.policies[id] = policy
		fake.policyOrder = append(fake.policyOrder, id)
		writeJSON(t, writer, http.StatusCreated, map[string]any{"policy": policy, "preview": map[string]any{"affected_occurrences": 3}})
	})
	fake.handle("PATCH /api/v1/org/security/vulnerability-dispositions/{id}", func(writer http.ResponseWriter, request *http.Request) {
		policy, exists := fake.policies[request.PathValue("id")]
		if !exists {
			writeJSON(t, writer, http.StatusNotFound, map[string]any{"detail": "policy not found"})
			return
		}
		if policy["revoked_at"] != nil {
			writeJSON(t, writer, http.StatusConflict, map[string]any{"detail": "policy is revoked"})
			return
		}
		var body map[string]json.RawMessage
		decodeJSON(t, request, &body)
		if len(body) == 0 {
			writeJSON(t, writer, http.StatusBadRequest, map[string]any{"detail": "At least one policy field is required"})
			return
		}
		for key, raw := range body {
			var value any
			_ = json.Unmarshal(raw, &value)
			if value == nil {
				t.Errorf("update sent null %q, which the platform reads as unchanged", key)
				continue
			}
			if key == "expires_at" {
				value = securityFakeTimestamp(t, value)
			}
			policy[key] = value
		}
		writeJSON(t, writer, http.StatusOK, policy)
	})
	fake.handle("POST /api/v1/org/security/vulnerability-dispositions/{id}/revoke", func(writer http.ResponseWriter, request *http.Request) {
		policy, exists := fake.policies[request.PathValue("id")]
		if !exists {
			writeJSON(t, writer, http.StatusNotFound, map[string]any{"detail": "policy not found"})
			return
		}
		var body map[string]string
		decodeJSON(t, request, &body)
		if body["reason"] == "" {
			writeJSON(t, writer, http.StatusBadRequest, map[string]any{"detail": "reason is required"})
			return
		}
		if policy["revoked_at"] != nil {
			writeJSON(t, writer, http.StatusConflict, map[string]any{"detail": "policy is revoked"})
			return
		}
		fake.revokeReasons[request.PathValue("id")] = body["reason"]
		policy["revoked_at"] = "2026-10-17T11:00:00Z"
		policy["status"] = "revoked"
		policy["match_health"] = "revoked"
		writeJSON(t, writer, http.StatusOK, policy)
	})

	fake.handle("GET /api/v1/org/security/compliance/frameworks", func(writer http.ResponseWriter, _ *http.Request) {
		keys := make([]string, 0, len(fake.frameworks))
		for key := range fake.frameworks {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		frameworks := make([]map[string]any, 0, len(keys))
		for _, key := range keys {
			frameworks = append(frameworks, fake.frameworks[key])
		}
		writeJSON(t, writer, http.StatusOK, map[string]any{"frameworks": frameworks, "generated_at": "2026-10-17T10:00:00Z"})
	})
	fake.handle("PATCH /api/v1/org/security/compliance/frameworks/{key}", func(writer http.ResponseWriter, request *http.Request) {
		framework, exists := fake.frameworks[request.PathValue("key")]
		if !exists {
			writeJSON(t, writer, http.StatusNotFound, map[string]any{"detail": "compliance framework not found"})
			return
		}
		var body map[string]any
		decodeJSON(t, request, &body)
		enabled, isBool := body["enabled"].(bool)
		if !isBool {
			writeJSON(t, writer, http.StatusBadRequest, map[string]any{"detail": "enabled is required"})
			return
		}
		framework["enabled"] = enabled
		framework["enabled_at"] = nil
		if enabled {
			framework["enabled_at"] = "2026-10-17T10:00:00Z"
		}
		writeJSON(t, writer, http.StatusOK, framework)
	})

	fake.handle("GET /api/v1/org/clusters/imported/{cluster}/security/report-schedule", func(writer http.ResponseWriter, request *http.Request) {
		if !fake.clusters[request.PathValue("cluster")] {
			writeJSON(t, writer, http.StatusNotFound, map[string]any{"detail": "Cluster not found"})
			return
		}
		schedule, exists := fake.schedules[request.PathValue("cluster")]
		if !exists {
			writeJSON(t, writer, http.StatusOK, map[string]any{"configured": false, "schedule": nil})
			return
		}
		writeJSON(t, writer, http.StatusOK, map[string]any{"configured": true, "schedule": schedule})
	})
	fake.handle("PUT /api/v1/org/clusters/imported/{cluster}/security/report-schedule", func(writer http.ResponseWriter, request *http.Request) {
		if !fake.clusters[request.PathValue("cluster")] {
			writeJSON(t, writer, http.StatusNotFound, map[string]any{"detail": "Cluster not found"})
			return
		}
		var body map[string]any
		decodeJSON(t, request, &body)
		fake.scheduleWrite = append(fake.scheduleWrite, body)
		recipients, isList := body["recipients"].([]any)
		if !isList {
			t.Errorf("schedule write sent recipients %v, not a list", body["recipients"])
		}
		if body["enabled"] == true && len(recipients) == 0 {
			writeJSON(t, writer, http.StatusUnprocessableEntity, map[string]any{"detail": "At least one recipient is required when reports are enabled"})
			return
		}
		schedule := map[string]any{
			"frequency":    body["frequency"],
			"recipients":   recipients,
			"enabled":      body["enabled"],
			"last_sent_at": nil,
			"next_due_at":  "2026-10-24T09:00:00Z",
			"updated_at":   "2026-10-17T10:00:00Z",
		}
		fake.schedules[request.PathValue("cluster")] = schedule
		writeJSON(t, writer, http.StatusOK, map[string]any{"configured": true, "schedule": schedule})
	})
	return fake
}

// securityFakeTimestamp stores a deadline as the platform does: parsed as a
// JSON time and answered in UTC with fractional seconds.
func securityFakeTimestamp(t *testing.T, value any) any {
	text, isString := value.(string)
	if !isString {
		return nil
	}
	parsed, err := time.Parse(time.RFC3339, text)
	if err != nil {
		t.Errorf("deadline %q is not RFC 3339: %v", text, err)
		return nil
	}
	return parsed.UTC().Format(time.RFC3339Nano)
}

func (fake *securityFake) liveDispositionIDs() []string {
	live := []string{}
	for _, id := range fake.policyOrder {
		if fake.policies[id]["revoked_at"] == nil {
			live = append(live, id)
		}
	}
	return live
}

func TestAccSecurityDispositionLifecycle(t *testing.T) {
	fake := newSecurityFake(t)

	acknowledged := fake.providerBlock() + `
resource "ankra_security_disposition" "frontend" {
  occurrence_id = "11111111-1111-4111-8111-111111111111"
  scope         = "workload"
  disposition   = "acknowledged"
  reason        = "Not reachable from outside the cluster"
}
`
	withDeadline := fake.providerBlock() + `
resource "ankra_security_disposition" "frontend" {
  occurrence_id             = "11111111-1111-4111-8111-111111111111"
  scope                     = "workload"
  disposition               = "acknowledged"
  reason                    = "Patched in the next release"
  expires_at                = "2027-01-15"
  expire_when_fix_available = true
}
`

	var firstID string
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: acknowledged,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ankra_security_disposition.frontend", "scope", "workload"),
					resource.TestCheckResourceAttr("ankra_security_disposition.frontend", "selector.workload_name", "frontend"),
					resource.TestCheckNoResourceAttr("ankra_security_disposition.frontend", "selector.addon_slug"),
					resource.TestCheckResourceAttr("ankra_security_disposition.frontend", "status", "active"),
					resource.TestCheckResourceAttr("ankra_security_disposition.frontend", "matched_occurrence_count", "3"),
					resource.TestCheckResourceAttr("ankra_security_disposition.frontend", "expire_when_fix_available", "false"),
					resource.TestCheckNoResourceAttr("ankra_security_disposition.frontend", "expires_at"),
					func(state *terraform.State) error {
						firstID = state.RootModule().Resources["ankra_security_disposition.frontend"].Primary.ID
						return nil
					},
				),
			},
			{
				// Reason and lifecycle change in place; the date form is sent
				// as an RFC 3339 instant and kept as written.
				Config: withDeadline,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ankra_security_disposition.frontend", "expires_at", "2027-01-15"),
					resource.TestCheckResourceAttr("ankra_security_disposition.frontend", "reason", "Patched in the next release"),
					func(state *terraform.State) error {
						id := state.RootModule().Resources["ankra_security_disposition.frontend"].Primary.ID
						if id != firstID {
							return fmt.Errorf("an in-place change replaced the policy: %s -> %s", firstID, id)
						}
						policy := fake.policies[id]
						if policy["expires_at"] != "2027-01-15T00:00:00Z" || policy["expire_when_fix_available"] != true {
							return fmt.Errorf("platform holds %v / %v", policy["expires_at"], policy["expire_when_fix_available"])
						}
						return nil
					},
				),
			},
			{
				ResourceName:            "ankra_security_disposition.frontend",
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"occurrence_id", "expires_at"},
			},
			{
				// A deadline cannot be cleared, so removing it replaces the
				// policy: the old one is revoked and a new one created.
				Config: acknowledged,
				Check: func(state *terraform.State) error {
					id := state.RootModule().Resources["ankra_security_disposition.frontend"].Primary.ID
					if id == firstID {
						return fmt.Errorf("removing the deadline did not replace the policy")
					}
					if fake.revokeReasons[firstID] != "Revoked by Terraform" {
						return fmt.Errorf("replaced policy was revoked with %q", fake.revokeReasons[firstID])
					}
					return nil
				},
			},
			{
				// Revoked outside Terraform: the next plan recreates it.
				PreConfig: func() {
					for _, id := range fake.liveDispositionIDs() {
						fake.policies[id]["revoked_at"] = "2026-10-17T12:00:00Z"
						fake.policies[id]["status"] = "revoked"
					}
				},
				Config: acknowledged,
				Check: func(_ *terraform.State) error {
					if live := fake.liveDispositionIDs(); len(live) != 1 {
						return fmt.Errorf("expected one live policy after recreation, got %v", live)
					}
					return nil
				},
			},
		},
		CheckDestroy: func(_ *terraform.State) error {
			if live := fake.liveDispositionIDs(); len(live) != 0 {
				return fmt.Errorf("policies still live after destroy: %v", live)
			}
			return nil
		},
	})
}

func TestAccSecurityDispositionListingPages(t *testing.T) {
	fake := newSecurityFake(t)
	for index := 0; index < 150; index++ {
		id := fmt.Sprintf("filler-%03d", index)
		fake.policies[id] = map[string]any{"id": id, "status": "revoked", "revoked_at": "2026-01-01T00:00:00Z", "selector": map[string]any{}, "created_at": "2026-01-01T00:00:00Z"}
		fake.policyOrder = append(fake.policyOrder, id)
	}

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: fake.providerBlock() + `
resource "ankra_security_disposition" "addon" {
  occurrence_id = "22222222-2222-4222-8222-222222222222"
  disposition   = "accepted_risk"
  reason        = "Compensating network policy"
  expires_at    = "2027-03-01T12:00:00+02:00"
  revoke_reason = "Network policy removed"
}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ankra_security_disposition.addon", "scope", "organisation_addon"),
					resource.TestCheckResourceAttr("ankra_security_disposition.addon", "selector.addon_slug", "ingress-nginx"),
					resource.TestCheckResourceAttr("ankra_security_disposition.addon", "expires_at", "2027-03-01T12:00:00+02:00"),
				),
			},
		},
		CheckDestroy: func(_ *terraform.State) error {
			for id, reason := range fake.revokeReasons {
				if reason != "Network policy removed" {
					return fmt.Errorf("policy %s revoked with %q", id, reason)
				}
			}
			if len(fake.revokeReasons) != 1 {
				return fmt.Errorf("expected one revocation, got %v", fake.revokeReasons)
			}
			return nil
		},
	})
}

func TestAccSecurityComplianceFrameworkLifecycle(t *testing.T) {
	fake := newSecurityFake(t)

	config := fake.providerBlock() + `
resource "ankra_security_compliance_framework" "cis" {
  key = "cis-kubernetes"
}

data "ankra_security_compliance_frameworks" "all" {
  depends_on = [ankra_security_compliance_framework.cis]
}
`

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: config,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ankra_security_compliance_framework.cis", "id", "cis-kubernetes"),
					resource.TestCheckResourceAttr("ankra_security_compliance_framework.cis", "title", "CIS Kubernetes Benchmark"),
					resource.TestCheckResourceAttr("ankra_security_compliance_framework.cis", "enabled_at", "2026-10-17T10:00:00Z"),
					resource.TestCheckResourceAttr("data.ankra_security_compliance_frameworks.all", "frameworks.#", "2"),
					resource.TestCheckResourceAttr("data.ankra_security_compliance_frameworks.all", "frameworks.0.key", "cis-kubernetes"),
					resource.TestCheckResourceAttr("data.ankra_security_compliance_frameworks.all", "frameworks.0.enabled", "true"),
					resource.TestCheckResourceAttr("data.ankra_security_compliance_frameworks.all", "frameworks.1.enabled", "false"),
				),
			},
			{
				ResourceName:      "ankra_security_compliance_framework.cis",
				ImportState:       true,
				ImportStateId:     "cis-kubernetes",
				ImportStateVerify: true,
			},
			{
				// Disabled outside Terraform: the next plan re-enables it.
				PreConfig: func() { fake.frameworks["cis-kubernetes"]["enabled"] = false },
				Config:    config,
				Check: func(_ *terraform.State) error {
					if fake.frameworks["cis-kubernetes"]["enabled"] != true {
						return fmt.Errorf("framework disabled outside Terraform was not re-enabled")
					}
					return nil
				},
			},
		},
		CheckDestroy: func(_ *terraform.State) error {
			if fake.frameworks["cis-kubernetes"]["enabled"] != false {
				return fmt.Errorf("destroy left the framework enabled")
			}
			return nil
		},
	})
}

func TestAccSecurityReportScheduleLifecycle(t *testing.T) {
	fake := newSecurityFake(t)

	weekly := fake.providerBlock() + `
resource "ankra_security_report_schedule" "production" {
  cluster_id = "5e3c2a10-7d4b-4f6e-9a21-3b4c5d6e7f80"
  frequency  = "weekly"
  recipients = ["security@example.com", "platform@example.com"]
}
`
	pausedMonthly := fake.providerBlock() + `
resource "ankra_security_report_schedule" "production" {
  cluster_id = "5e3c2a10-7d4b-4f6e-9a21-3b4c5d6e7f80"
  frequency  = "monthly"
  enabled    = false
}
`

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: weekly,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ankra_security_report_schedule.production", "id", "5e3c2a10-7d4b-4f6e-9a21-3b4c5d6e7f80"),
					resource.TestCheckResourceAttr("ankra_security_report_schedule.production", "enabled", "true"),
					resource.TestCheckResourceAttr("ankra_security_report_schedule.production", "recipients.#", "2"),
					resource.TestCheckResourceAttr("ankra_security_report_schedule.production", "next_due_at", "2026-10-24T09:00:00Z"),
				),
			},
			{
				// Recipients removed: the write sends an empty list, not null.
				Config: pausedMonthly,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ankra_security_report_schedule.production", "frequency", "monthly"),
					resource.TestCheckNoResourceAttr("ankra_security_report_schedule.production", "recipients"),
					func(_ *terraform.State) error {
						last := fake.scheduleWrite[len(fake.scheduleWrite)-1]
						if recipients, _ := last["recipients"].([]any); len(recipients) != 0 || last["enabled"] != false {
							return fmt.Errorf("unexpected write %v", last)
						}
						return nil
					},
				),
			},
			{
				ResourceName:      "ankra_security_report_schedule.production",
				ImportState:       true,
				ImportStateId:     "5e3c2a10-7d4b-4f6e-9a21-3b4c5d6e7f80",
				ImportStateVerify: true,
			},
			{
				// Gone from the platform (no schedule stored): recreated.
				PreConfig: func() { delete(fake.schedules, "5e3c2a10-7d4b-4f6e-9a21-3b4c5d6e7f80") },
				Config:    weekly,
				Check: func(_ *terraform.State) error {
					if fake.schedules["5e3c2a10-7d4b-4f6e-9a21-3b4c5d6e7f80"]["frequency"] != "weekly" {
						return fmt.Errorf("schedule was not recreated: %v", fake.schedules["5e3c2a10-7d4b-4f6e-9a21-3b4c5d6e7f80"])
					}
					return nil
				},
			},
		},
		CheckDestroy: func(_ *terraform.State) error {
			schedule := fake.schedules["5e3c2a10-7d4b-4f6e-9a21-3b4c5d6e7f80"]
			if recipients, _ := schedule["recipients"].([]any); schedule["enabled"] != false || len(recipients) != 0 {
				return fmt.Errorf("destroy left the schedule sending: %v", schedule)
			}
			return nil
		},
	})
}

// A cluster that leaves the provider's scope answers 404 "Cluster not
// found". That must neither drop the schedule from state nor let destroy
// report success while the platform keeps emailing reports.
func TestAccSecurityReportScheduleClusterOutOfScope(t *testing.T) {
	fake := newSecurityFake(t)
	const clusterID = "5e3c2a10-7d4b-4f6e-9a21-3b4c5d6e7f80"
	configuration := fake.providerBlock() + `
resource "ankra_security_report_schedule" "production" {
  cluster_id = "` + clusterID + `"
  frequency  = "weekly"
  recipients = ["security@example.com"]
}
`
	stillSending := func(_ *terraform.State) error {
		if fake.schedules[clusterID]["enabled"] != true {
			return fmt.Errorf("schedule changed: %v", fake.schedules[clusterID])
		}
		return nil
	}

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{Config: configuration},
			{
				PreConfig:   func() { fake.clusters[clusterID] = false },
				Config:      configuration,
				ExpectError: regexp.MustCompile("Cluster not in scope"),
			},
			{
				Config:      configuration,
				Destroy:     true,
				ExpectError: regexp.MustCompile("Cluster not in scope"),
				Check:       stillSending,
			},
			{
				PreConfig: func() { fake.clusters[clusterID] = true },
				Config:    configuration,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ankra_security_report_schedule.production", "recipients.#", "1"),
					stillSending,
				),
			},
		},
	})
}
