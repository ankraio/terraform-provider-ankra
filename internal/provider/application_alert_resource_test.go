// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
)

const (
	applicationAlertFakeApplicationID = "5b0c4a9e-2f43-4b8e-9a51-1f0d8e7c2a10"
	applicationAlertFakeProduction    = "e1000000-0000-4000-8000-000000000001"
	applicationAlertFakeStaging       = "e1000000-0000-4000-8000-000000000002"
	applicationAlertFakeOps           = "d1000000-0000-4000-8000-000000000001"
	applicationAlertFakePager         = "d1000000-0000-4000-8000-000000000002"
	applicationAlertFakeFirstID       = "a1000000-0000-4000-8000-000000000001"
	applicationAlertFakeOrganisation  = "a2000000-0000-4000-8000-000000000001"
)

// applicationAlertFakeAlert is one stored application alert.
type applicationAlertFakeAlert struct {
	name          string
	environmentID string
	condition     map[string]any
	sendTo        []string
	severity      string
}

// applicationAlertFake mirrors the application alert routes of the
// published API contract: the list carries the application's own alerts
// first and an organisation rule after them, a create stores warning when
// the body has no severity and answers 201, an update keeps the stored
// severity when the body has none, and an unknown application or alert is a
// 404.
type applicationAlertFake struct {
	*routeFake
	alerts     map[string]*applicationAlertFakeAlert
	order      []string
	created    int
	lastCreate map[string]any
	lastUpdate map[string]any
	// listStatus, when set, is the status every list answers with.
	listStatus int
	// truncated lists only the organisation rule, marked truncated.
	truncated bool
}

func newApplicationAlertFake(t *testing.T) *applicationAlertFake {
	fake := &applicationAlertFake{routeFake: newRouteFake(t), alerts: map[string]*applicationAlertFakeAlert{}}
	destinations := map[string]string{applicationAlertFakeOps: "ops", applicationAlertFakePager: "pager"}
	render := func(alertID string) map[string]any {
		alert := fake.alerts[alertID]
		sendTo := []map[string]any{}
		for _, destinationID := range alert.sendTo {
			sendTo = append(sendTo, map[string]any{"integration_id": destinationID, "type": "slack", "label": destinations[destinationID]})
		}
		condition := map[string]any{"route": nil}
		for key, value := range alert.condition {
			condition[key] = value
		}
		if _, hasOperator := condition["operator"]; !hasOperator {
			condition["operator"] = "gt"
		}
		return map[string]any{
			"alert_id": alertID, "rule_id": strings.Replace(alertID, "a1", "b1", 1), "name": alert.name, "own": true,
			"environment_id": alert.environmentID, "says": "a sentence", "condition": condition, "severity": alert.severity,
			"send_to": sendTo, "enabled": true, "last_firing": nil, "evaluated_at": nil, "evaluation": "not_evaluated", "unknown_reason": nil,
		}
	}
	organisationRule := map[string]any{
		"alert_id": applicationAlertFakeOrganisation, "rule_id": applicationAlertFakeOrganisation, "name": "Cluster down", "own": false,
		"environment_id": nil, "says": "Cluster down", "condition": nil, "severity": "critical", "send_to": []any{}, "enabled": true,
		"last_firing": nil, "evaluated_at": nil, "evaluation": "ok", "unknown_reason": nil,
	}
	// admit refuses what the platform refuses after parsing the body, in its
	// words, and stores the fields of an admitted body into alert.
	admit := func(writer http.ResponseWriter, body map[string]any, alert *applicationAlertFakeAlert) bool {
		environmentID, _ := body["environment_id"].(string)
		if environmentID != applicationAlertFakeProduction && environmentID != applicationAlertFakeStaging {
			writeJSON(t, writer, http.StatusUnprocessableEntity, map[string]any{"detail": "environment_id is not an environment of this application."})
			return false
		}
		listed, _ := body["send_to"].([]any)
		if len(listed) == 0 {
			writeJSON(t, writer, http.StatusUnprocessableEntity, map[string]any{"detail": "send_to must name 1 to 5 places to send alerts to."})
			return false
		}
		sendTo := []string{}
		for _, raw := range listed {
			destinationID, _ := raw.(string)
			if _, known := destinations[destinationID]; !known {
				writeJSON(t, writer, http.StatusUnprocessableEntity, map[string]any{
					"detail": "send_to names " + destinationID + ", which is not a place to send alerts to."})
				return false
			}
			sendTo = append(sendTo, destinationID)
		}
		alert.name, _ = body["name"].(string)
		alert.environmentID = environmentID
		alert.condition, _ = body["condition"].(map[string]any)
		alert.sendTo = sendTo
		if severity, set := body["severity"].(string); set {
			alert.severity = severity
		}
		return true
	}
	application := func(writer http.ResponseWriter, request *http.Request) bool {
		if request.PathValue("application_id") != applicationAlertFakeApplicationID {
			writeJSON(t, writer, http.StatusNotFound, map[string]any{"detail": "Application not found"})
			return false
		}
		return true
	}
	lookup := func(writer http.ResponseWriter, request *http.Request) (string, bool) {
		if !application(writer, request) {
			return "", false
		}
		alertID := request.PathValue("alert_id")
		if _, exists := fake.alerts[alertID]; !exists {
			writeJSON(t, writer, http.StatusNotFound, map[string]any{"detail": "Alert not found"})
			return "", false
		}
		return alertID, true
	}
	fake.handle("GET /api/v1/org/applications/{application_id}/alerts", func(writer http.ResponseWriter, request *http.Request) {
		if fake.listStatus != 0 {
			writeJSON(t, writer, fake.listStatus, map[string]any{"detail": "permission_denied: alerts.read"})
			return
		}
		if !application(writer, request) {
			return
		}
		listed := []map[string]any{}
		if !fake.truncated {
			for _, alertID := range fake.order {
				if _, exists := fake.alerts[alertID]; exists {
					listed = append(listed, render(alertID))
				}
			}
		}
		listed = append(listed, organisationRule)
		writeJSON(t, writer, http.StatusOK, map[string]any{"alerts": listed, "truncated": fake.truncated, "limit": 100, "covered_by_organisation_rules": nil})
	})
	fake.handle("POST /api/v1/org/applications/{application_id}/alerts", func(writer http.ResponseWriter, request *http.Request) {
		if !application(writer, request) {
			return
		}
		var body map[string]any
		decodeJSON(t, request, &body)
		fake.lastCreate = body
		alert := &applicationAlertFakeAlert{severity: "warning"}
		if !admit(writer, body, alert) {
			return
		}
		fake.created++
		alertID := fmt.Sprintf("a1000000-0000-4000-8000-%012d", fake.created)
		fake.alerts[alertID] = alert
		fake.order = append(fake.order, alertID)
		writeJSON(t, writer, http.StatusCreated, render(alertID))
	})
	fake.handle("PUT /api/v1/org/applications/{application_id}/alerts/{alert_id}", func(writer http.ResponseWriter, request *http.Request) {
		alertID, found := lookup(writer, request)
		if !found {
			return
		}
		var body map[string]any
		decodeJSON(t, request, &body)
		fake.lastUpdate = body
		if !admit(writer, body, fake.alerts[alertID]) {
			return
		}
		writeJSON(t, writer, http.StatusOK, render(alertID))
	})
	fake.handle("DELETE /api/v1/org/applications/{application_id}/alerts/{alert_id}", func(writer http.ResponseWriter, request *http.Request) {
		if alertID, found := lookup(writer, request); found {
			delete(fake.alerts, alertID)
			writeJSON(t, writer, http.StatusOK, map[string]any{"success": true, "message": "Alert deleted"})
		}
	})
	return fake
}

// applicationAlertConfig is one application alert; an empty severity leaves
// the attribute out of the configuration.
func applicationAlertConfig(fake *applicationAlertFake, name, environmentID, severity string, threshold float64, holdMinutes int, sendTo ...string) string {
	severityLine := ""
	if severity != "" {
		severityLine = fmt.Sprintf("severity = %q", severity)
	}
	quoted := make([]string, 0, len(sendTo))
	for _, destinationID := range sendTo {
		quoted = append(quoted, fmt.Sprintf("%q", destinationID))
	}
	return fake.providerBlock() + fmt.Sprintf(`
resource "ankra_application_alert" "slow" {
  application_id = %q
  environment_id = %q
  name           = %q
  %s
  condition = {
    metric       = "p95_ms"
    threshold    = %g
    hold_minutes = %d
  }
  send_to = [%s]
}
`, applicationAlertFakeApplicationID, environmentID, name, severityLine, threshold, holdMinutes, strings.Join(quoted, ", "))
}

func TestAccApplicationAlertLifecycle(t *testing.T) {
	fake := newApplicationAlertFake(t)
	const address = "ankra_application_alert.slow"
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
				Config: applicationAlertConfig(fake, "Slow responses", applicationAlertFakeProduction, "", 800, 5, applicationAlertFakeOps),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(address, "id", applicationAlertFakeFirstID),
					resource.TestCheckResourceAttr(address, "rule_id", "b1000000-0000-4000-8000-000000000001"),
					resource.TestCheckResourceAttr(address, "severity", "warning"),
					resource.TestCheckResourceAttr(address, "enabled", "true"),
					resource.TestCheckResourceAttr(address, "condition.condition_type", "app_metric"),
					resource.TestCheckResourceAttr(address, "condition.operator", "gt"),
					resource.TestCheckResourceAttr(address, "condition.threshold", "800"),
					resource.TestCheckResourceAttr(address, "send_to.#", "1"),
					resource.TestCheckTypeSetElemAttr(address, "send_to.*", applicationAlertFakeOps),
					func(_ *terraform.State) error {
						if _, sent := fake.lastCreate["severity"]; sent {
							return fmt.Errorf("a create without severity must leave it to the platform: %v", fake.lastCreate)
						}
						condition, _ := fake.lastCreate["condition"].(map[string]any)
						if condition["condition_type"] != "app_metric" || condition["operator"] != "gt" || condition["hold_minutes"] != float64(5) {
							return fmt.Errorf("the create must send the whole condition: %v", condition)
						}
						return nil
					},
				),
			},
			{
				Config: applicationAlertConfig(fake, "Slow responses", applicationAlertFakeProduction, "critical", 1000, 10,
					applicationAlertFakeOps, applicationAlertFakePager),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction(address, plancheck.ResourceActionUpdate),
				}},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(address, "id", applicationAlertFakeFirstID),
					resource.TestCheckResourceAttr(address, "severity", "critical"),
					resource.TestCheckResourceAttr(address, "condition.hold_minutes", "10"),
					resource.TestCheckResourceAttr(address, "send_to.#", "2"),
					func(_ *terraform.State) error {
						if fake.lastUpdate["severity"] != "critical" {
							return fmt.Errorf("a configured severity must be sent: %v", fake.lastUpdate)
						}
						return nil
					},
				),
			},
			{
				// Leaving severity out keeps the stored one: the plan shows the
				// stored severity, and the update does not send one.
				Config: applicationAlertConfig(fake, "Slow p95", applicationAlertFakeProduction, "", 1000, 10,
					applicationAlertFakeOps, applicationAlertFakePager),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction(address, plancheck.ResourceActionUpdate),
					plancheck.ExpectKnownValue(address, tfjsonpath.New("severity"), knownvalue.StringExact("critical")),
				}},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(address, "name", "Slow p95"),
					resource.TestCheckResourceAttr(address, "severity", "critical"),
					func(_ *terraform.State) error {
						if _, sent := fake.lastUpdate["severity"]; sent {
							return fmt.Errorf("an update without a configured severity must not send one: %v", fake.lastUpdate)
						}
						if fake.alerts[applicationAlertFakeFirstID].severity != "critical" {
							return fmt.Errorf("the stored severity must be kept, got %q", fake.alerts[applicationAlertFakeFirstID].severity)
						}
						return nil
					},
				),
			},
			{
				// Another environment is an in-place change, not a new alert.
				Config: applicationAlertConfig(fake, "Slow p95", applicationAlertFakeStaging, "", 1000, 10,
					applicationAlertFakeOps, applicationAlertFakePager),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction(address, plancheck.ResourceActionUpdate),
				}},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(address, "id", applicationAlertFakeFirstID),
					resource.TestCheckResourceAttr(address, "environment_id", applicationAlertFakeStaging),
				),
			},
			{
				ResourceName:      address,
				ImportState:       true,
				ImportStateVerify: true,
				ImportStateIdFunc: func(*terraform.State) (string, error) {
					return applicationAlertFakeApplicationID + "/" + applicationAlertFakeFirstID, nil
				},
			},
			{
				// A severity changed outside Terraform, with none configured,
				// is the platform's to keep: no plan to change it back.
				PreConfig: func() { fake.alerts[applicationAlertFakeFirstID].severity = "info" },
				Config: applicationAlertConfig(fake, "Slow p95", applicationAlertFakeStaging, "", 1000, 10,
					applicationAlertFakeOps, applicationAlertFakePager),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}},
				Check:            resource.TestCheckResourceAttr(address, "severity", "info"),
			},
		},
	})
}

func TestAccApplicationAlertReadTellsGoneFromUnreadable(t *testing.T) {
	fake := newApplicationAlertFake(t)
	config := applicationAlertConfig(fake, "Restarts", applicationAlertFakeProduction, "warning", 3, 1, applicationAlertFakeOps)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy: func(_ *terraform.State) error {
			if len(fake.alerts) != 0 {
				return fmt.Errorf("destroy must delete the alert, %d left", len(fake.alerts))
			}
			return nil
		},
		Steps: []resource.TestStep{
			{Config: config},
			{
				// A refused read is an error; the alert stays in state.
				PreConfig:   func() { fake.listStatus = http.StatusForbidden },
				Config:      config,
				ExpectError: regexp.MustCompile(`Unable to read application alert`),
			},
			{
				// A list cut short without the alert does not say it is gone.
				PreConfig: func() {
					fake.listStatus = 0
					fake.truncated = true
				},
				Config:      config,
				ExpectError: regexp.MustCompile(`(?s)Unable to read application alert.*cannot be read`),
			},
			{
				// The platform's own answer that the alert is gone plans it again.
				PreConfig: func() {
					fake.truncated = false
					delete(fake.alerts, applicationAlertFakeFirstID)
				},
				Config:             config,
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
			{
				Config: config,
				Check: func(_ *terraform.State) error {
					if fake.created != 2 || len(fake.alerts) != 1 {
						return fmt.Errorf("an alert deleted elsewhere must be created again: %d creates, %d alerts", fake.created, len(fake.alerts))
					}
					return nil
				},
			},
		},
	})
}

func TestAccApplicationAlertRefusals(t *testing.T) {
	fake := newApplicationAlertFake(t)
	resourceWith := func(body string) string {
		return fake.providerBlock() + `
resource "ankra_application_alert" "refused" {
  application_id = "` + applicationAlertFakeApplicationID + `"
` + body + `
}
`
	}
	valid := map[string]string{
		"environment_id": `"` + applicationAlertFakeProduction + `"`,
		"name":           `"Failures"`,
		"condition":      `{ metric = "failed_share_pct", threshold = 5, hold_minutes = 5 }`,
		"send_to":        `["` + applicationAlertFakeOps + `"]`,
	}
	with := func(overrides map[string]string) string {
		lines := []string{}
		for _, key := range []string{"environment_id", "name", "condition", "send_to", "severity"} {
			value, overridden := overrides[key]
			if !overridden {
				value = valid[key]
			}
			if value != "" {
				lines = append(lines, "  "+key+" = "+value)
			}
		}
		return resourceWith(strings.Join(lines, "\n"))
	}
	sixDestinations := []string{}
	for index := 1; index <= 6; index++ {
		sixDestinations = append(sixDestinations, fmt.Sprintf(`"d1000000-0000-4000-8000-%012d"`, index))
	}
	planRefusals := []struct {
		overrides map[string]string
		message   string
	}{
		{map[string]string{"condition": `{ metric = "latency", threshold = 5, hold_minutes = 5 }`}, `Invalid value`},
		{map[string]string{"condition": `{ metric = "p95_ms", operator = "lt", threshold = 5, hold_minutes = 5 }`}, `Invalid value`},
		{map[string]string{"condition": `{ metric = "p95_ms", condition_type = "status", threshold = 5, hold_minutes = 5 }`}, `Invalid value`},
		{map[string]string{"condition": `{ metric = "p95_ms", threshold = 5, hold_minutes = 0 }`}, `Value out of range`},
		{map[string]string{"condition": `{ metric = "p95_ms", threshold = 5, hold_minutes = 61 }`}, `Value out of range`},
		{map[string]string{"condition": `{ metric = "p95_ms", threshold = -1, hold_minutes = 5 }`}, `threshold must be a number of zero or more`},
		{map[string]string{"condition": `{ metric = "failed_share_pct", threshold = 101, hold_minutes = 5 }`}, `threshold is a percentage for failed_share_pct`},
		{map[string]string{"send_to": `[]`}, `send_to must name 1 to 5 places`},
		{map[string]string{"send_to": "[" + strings.Join(sixDestinations, ", ") + "]"}, `send_to must name 1 to 5 places`},
		{map[string]string{"send_to": `["ops-slack"]`}, `send_to must list the ids of places`},
		{map[string]string{"severity": `"page"`}, `Invalid value`},
		{map[string]string{"name": `""`}, `name must be 1 to 100 characters`},
		{map[string]string{"name": `"` + strings.Repeat("x", 101) + `"`}, `name must be 1 to 100 characters`},
		{map[string]string{"name": `" Failures"`}, `must not start or end with whitespace`},
		{map[string]string{"environment_id": `"production"`}, `environment_id must be the id of one of this application's environments`},
	}
	steps := make([]resource.TestStep, 0, len(planRefusals)+1)
	for _, refusal := range planRefusals {
		// Terraform wraps a long diagnostic, so any run of whitespace
		// matches a space of the message.
		steps = append(steps, resource.TestStep{
			Config:      with(refusal.overrides),
			PlanOnly:    true,
			ExpectError: regexp.MustCompile(strings.ReplaceAll(regexp.QuoteMeta(refusal.message), " ", `\s+`)),
		})
	}
	// A destination the platform does not know is refused at apply, in the
	// platform's words.
	steps = append(steps, resource.TestStep{
		Config:      with(map[string]string{"send_to": `["d1000000-0000-4000-8000-000000000009"]`}),
		ExpectError: regexp.MustCompile(`(?s)Unable to create application alert.*not a place to send alerts to`),
	})
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps:                    steps,
	})
}
