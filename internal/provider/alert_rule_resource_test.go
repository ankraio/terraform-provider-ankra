// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"fmt"
	"net/http"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

const alertRuleFakeDestinationID = "d1000000-0000-0000-0000-000000000001"

// alertRuleFakeAlert is one stored alert definition.
type alertRuleFakeAlert struct {
	name           string
	description    string
	enabled        bool
	destinationIDs []string
	rules          []map[string]any
}

// alertRuleFake mirrors the bearer alert definition routes
// (go/internal/alertsapi/handlers_alerts.go): the get route reports the
// alert with its rule count and destination ids but not its rules, and an
// update replaces the rule set only when it names rules.
type alertRuleFake struct {
	*routeFake
	alerts      map[string]*alertRuleFakeAlert
	created     int
	ruleWrites  int
	lastCreate  map[string]any
	lastUpdate  map[string]any
	destination string
}

func newAlertRuleFake(t *testing.T) *alertRuleFake {
	fake := &alertRuleFake{routeFake: newRouteFake(t), alerts: map[string]*alertRuleFakeAlert{}, destination: "ops-slack"}
	resolve := func(writer http.ResponseWriter, body map[string]any) ([]string, bool) {
		identifiers := []string{}
		names, _ := body["integration_names"].([]any)
		for _, name := range names {
			if name != fake.destination {
				writeJSON(t, writer, http.StatusBadRequest, map[string]any{"detail": fmt.Sprintf("Webhook '%s' not found", name)})
				return nil, false
			}
			identifiers = append(identifiers, alertRuleFakeDestinationID)
		}
		return identifiers, true
	}
	rulesOf := func(body map[string]any) []map[string]any {
		rules := []map[string]any{}
		listed, _ := body["rules"].([]any)
		for _, rule := range listed {
			if fields, isObject := rule.(map[string]any); isObject {
				rules = append(rules, fields)
			}
		}
		return rules
	}
	answer := func(writer http.ResponseWriter, alertID string) {
		alert := fake.alerts[alertID]
		var description any
		if alert.description != "" {
			description = alert.description
		}
		severity := "warning"
		for _, rule := range alert.rules {
			if rule["severity"] == "critical" {
				severity = "critical"
			}
		}
		writeJSON(t, writer, http.StatusOK, map[string]any{"item": map[string]any{
			"id": alertID, "name": alert.name, "description": description, "enabled": alert.enabled, "state": "active",
			"status": "ok", "severity": severity, "webhook_ids": alert.destinationIDs, "rule_count": len(alert.rules),
		}, "tickets": []any{}})
	}
	lookup := func(writer http.ResponseWriter, request *http.Request) (string, bool) {
		alertID := request.PathValue("alert_id")
		if _, exists := fake.alerts[alertID]; !exists {
			writeJSON(t, writer, http.StatusNotFound, map[string]any{"detail": "Alert not found"})
			return "", false
		}
		return alertID, true
	}
	fake.handle("POST /api/v1/org/alerts/alerts", func(writer http.ResponseWriter, request *http.Request) {
		var body map[string]any
		decodeJSON(t, request, &body)
		fake.lastCreate = body
		destinationIDs, resolved := resolve(writer, body)
		if !resolved {
			return
		}
		fake.created++
		alertID := fmt.Sprintf("a1000000-0000-0000-0000-%012d", fake.created)
		name, _ := body["name"].(string)
		alert := &alertRuleFakeAlert{name: name, enabled: true, destinationIDs: destinationIDs, rules: rulesOf(body)}
		if description, set := body["description"].(string); set {
			alert.description = description
		}
		if enabled, set := body["enabled"].(bool); set {
			alert.enabled = enabled
		}
		fake.alerts[alertID] = alert
		fake.ruleWrites++
		answer(writer, alertID)
	})
	fake.handle("GET /api/v1/org/alerts/alerts/{alert_id}", func(writer http.ResponseWriter, request *http.Request) {
		if alertID, found := lookup(writer, request); found {
			answer(writer, alertID)
		}
	})
	fake.handle("PUT /api/v1/org/alerts/alerts/{alert_id}", func(writer http.ResponseWriter, request *http.Request) {
		alertID, found := lookup(writer, request)
		if !found {
			return
		}
		var body map[string]any
		decodeJSON(t, request, &body)
		fake.lastUpdate = body
		alert := fake.alerts[alertID]
		if _, sent := body["integration_names"]; sent {
			destinationIDs, resolved := resolve(writer, body)
			if !resolved {
				return
			}
			alert.destinationIDs = destinationIDs
		}
		if name, set := body["name"].(string); set {
			alert.name = name
		}
		if description, set := body["description"].(string); set {
			alert.description = description
		}
		if enabled, set := body["enabled"].(bool); set {
			alert.enabled = enabled
		}
		if rules := rulesOf(body); len(rules) > 0 {
			alert.rules = rules
			fake.ruleWrites++
		}
		answer(writer, alertID)
	})
	fake.handle("DELETE /api/v1/org/alerts/alerts/{alert_id}", func(writer http.ResponseWriter, request *http.Request) {
		if alertID, found := lookup(writer, request); found {
			delete(fake.alerts, alertID)
			writeJSON(t, writer, http.StatusOK, map[string]any{"success": true, "message": "Alert deleted"})
		}
	})
	fake.handle("GET /api/v1/org/alerts/integrations", func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(t, writer, http.StatusOK, map[string]any{
			"items":      []map[string]any{{"id": alertRuleFakeDestinationID, "name": fake.destination, "integration_type": "slack", "enabled": true}},
			"pagination": map[string]any{"page": 1, "total_pages": 1},
		})
	})
	return fake
}

func alertRuleConfig(fake *alertRuleFake, enabled bool, description, severity string) string {
	descriptionLine := ""
	if description != "" {
		descriptionLine = fmt.Sprintf("description = %q", description)
	}
	return fake.providerBlock() + fmt.Sprintf(`
resource "ankra_alert_rule" "disk" {
  name              = "node-disk-pressure"
  %s
  enabled           = %t
  destination_names = ["ops-slack"]
  rules = [{
    name          = "disk pressure"
    resource_type = "node"
    all_clusters  = true
    severity      = %q
    conditions = [{
      condition_type     = "status"
      condition_operator = "equals"
      condition_value    = "DiskPressure"
    }]
  }]
}
`, descriptionLine, enabled, severity)
}

func TestAccAlertRuleLifecycle(t *testing.T) {
	fake := newAlertRuleFake(t)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy: func(_ *terraform.State) error {
			if len(fake.alerts) != 0 {
				return fmt.Errorf("destroy must delete the alert, %d left", len(fake.alerts))
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: alertRuleConfig(fake, true, "Nodes running out of disk", "critical"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ankra_alert_rule.disk", "id", "a1000000-0000-0000-0000-000000000001"),
					resource.TestCheckResourceAttr("ankra_alert_rule.disk", "enabled", "true"),
					resource.TestCheckResourceAttr("ankra_alert_rule.disk", "rule_count", "1"),
					resource.TestCheckResourceAttr("ankra_alert_rule.disk", "severity", "critical"),
					resource.TestCheckResourceAttr("ankra_alert_rule.disk", "destination_names.#", "1"),
					resource.TestCheckResourceAttr("ankra_alert_rule.disk", "rules.0.conditions.0.condition_value", "DiskPressure"),
					func(_ *terraform.State) error {
						rules, _ := fake.lastCreate["rules"].([]any)
						if len(rules) != 1 {
							return fmt.Errorf("the create must send one rule: %v", fake.lastCreate)
						}
						rule, _ := rules[0].(map[string]any)
						if _, sent := rule["cooldown_minutes"]; sent {
							return fmt.Errorf("an unset rule member must be left to the platform default: %v", rule)
						}
						return nil
					},
				),
			},
			{
				Config: alertRuleConfig(fake, false, "", "critical"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ankra_alert_rule.disk", "enabled", "false"),
					resource.TestCheckNoResourceAttr("ankra_alert_rule.disk", "description"),
					func(_ *terraform.State) error {
						if _, sent := fake.lastUpdate["rules"]; sent || fake.ruleWrites != 1 {
							return fmt.Errorf("muting the alert must keep its rules: %d rule writes, update %v", fake.ruleWrites, fake.lastUpdate)
						}
						return nil
					},
				),
			},
			{
				Config: alertRuleConfig(fake, false, "", "warning"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ankra_alert_rule.disk", "severity", "warning"),
					func(_ *terraform.State) error {
						if fake.ruleWrites != 2 {
							return fmt.Errorf("a rule change must replace the rule set, %d rule writes", fake.ruleWrites)
						}
						return nil
					},
				),
			},
			{
				ResourceName:            "ankra_alert_rule.disk",
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"rules"},
			},
			{
				PreConfig: func() {
					for _, alert := range fake.alerts {
						alert.rules = append(alert.rules, map[string]any{"name": "added elsewhere"})
						alert.enabled = true
					}
				},
				Config: alertRuleConfig(fake, false, "", "warning"),
				Check: func(_ *terraform.State) error {
					for _, alert := range fake.alerts {
						if len(alert.rules) != 1 || alert.enabled {
							return fmt.Errorf("rules and enabled changed outside Terraform must be set back: %d rules, enabled %t", len(alert.rules), alert.enabled)
						}
					}
					return nil
				},
			},
		},
	})
}

func TestAccAlertRuleRefusals(t *testing.T) {
	fake := newAlertRuleFake(t)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: fake.providerBlock() + `
resource "ankra_alert_rule" "empty" {
  name  = "empty"
  rules = []
}
`,
				ExpectError: regexp.MustCompile(`No rules`),
			},
			{
				Config: fake.providerBlock() + `
resource "ankra_alert_rule" "loud" {
  name = "loud"
  rules = [{
    name          = "loud"
    resource_type = "node"
    severity      = "page"
    conditions    = []
  }]
}
`,
				ExpectError: regexp.MustCompile(`Invalid value`),
			},
			{
				Config: fake.providerBlock() + `
resource "ankra_alert_rule" "unrouted" {
  name              = "unrouted"
  destination_names = ["nowhere"]
  rules = [{
    name          = "unrouted"
    resource_type = "node"
    conditions    = []
  }]
}
`,
				ExpectError: regexp.MustCompile(`(?s)Unable to create alert rule.*nowhere`),
			},
		},
	})
}
