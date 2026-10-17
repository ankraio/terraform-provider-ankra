// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

// alertFake holds alert destinations and routes the way the platform does
// (go/internal/alertsapi, go/internal/notifyroutesapi): the destination
// create answers 200 with the URL masked and does not store a description,
// get and list return the stored URL, the listing pages, an update keeps any
// member that is absent or null and treats an empty channel_id as "back to
// URL delivery", and the routes PATCH clears a nullable filter sent as null.
type alertFake struct {
	*routeFake
	destinations map[string]map[string]any
	order        []string
	routes       map[string]map[string]any
	created      int
	// destinationUpdates records every destination update body, in order.
	destinationUpdates []map[string]json.RawMessage
	// routeWrites records every route create or update body, in order.
	routeWrites []map[string]json.RawMessage
}

func newAlertFake(t *testing.T) *alertFake {
	fake := &alertFake{
		routeFake:    newRouteFake(t),
		destinations: map[string]map[string]any{},
		routes:       map[string]map[string]any{},
	}
	masked := func(row map[string]any) map[string]any {
		copied := map[string]any{}
		for key, value := range row {
			copied[key] = value
		}
		copied["url"] = "https://***"
		return copied
	}

	fake.handle("GET /api/v1/org/alerts/integrations", func(writer http.ResponseWriter, request *http.Request) {
		page, _ := strconv.Atoi(request.URL.Query().Get("page"))
		pageSize, _ := strconv.Atoi(request.URL.Query().Get("page_size"))
		if page < 1 {
			page = 1
		}
		if pageSize < 1 || pageSize > 100 {
			t.Errorf("destinations listing asked for page_size %d", pageSize)
		}
		// The fake pages two at a time whatever was asked, so the walk is tested.
		pageSize = 2
		items := []map[string]any{}
		for _, destinationID := range fake.order {
			if row, exists := fake.destinations[destinationID]; exists {
				items = append(items, row)
			}
		}
		totalPages := (len(items) + pageSize - 1) / pageSize
		start := (page - 1) * pageSize
		end := start + pageSize
		if start > len(items) {
			start = len(items)
		}
		if end > len(items) {
			end = len(items)
		}
		writeJSON(t, writer, http.StatusOK, map[string]any{
			"items":      items[start:end],
			"pagination": map[string]any{"page": page, "page_size": pageSize, "total_count": len(items), "total_pages": totalPages},
		})
	})
	fake.handle("POST /api/v1/org/alerts/integrations", func(writer http.ResponseWriter, request *http.Request) {
		var body map[string]any
		decodeJSON(t, request, &body)
		if _, hasDescription := body["description"]; hasDescription {
			t.Errorf("create sent a description, which the platform drops")
		}
		fake.created++
		destinationID := fmt.Sprintf("00000000-0000-0000-0000-%012d", fake.created)
		row := map[string]any{
			"id": destinationID, "name": body["name"], "url": body["url"], "channel_id": body["channel_id"],
			"channel_name": body["channel_name"], "teams_tenant_id": body["teams_tenant_id"],
			"integration_type": "slack", "description": nil, "template": body["template"], "enabled": true,
		}
		if integrationType, set := body["integration_type"]; set {
			row["integration_type"] = integrationType
		}
		if enabled, set := body["enabled"]; set {
			row["enabled"] = enabled
		}
		if row["url"] == nil {
			row["url"] = ""
		}
		fake.destinations[destinationID] = row
		fake.order = append(fake.order, destinationID)
		writeJSON(t, writer, http.StatusOK, map[string]any{"item": masked(row)})
	})
	fake.handle("GET /api/v1/org/alerts/integrations/{id}", func(writer http.ResponseWriter, request *http.Request) {
		row, exists := fake.destinations[request.PathValue("id")]
		if !exists {
			writeJSON(t, writer, http.StatusNotFound, map[string]any{"detail": "Integration not found"})
			return
		}
		writeJSON(t, writer, http.StatusOK, map[string]any{"item": row})
	})
	fake.handle("PUT /api/v1/org/alerts/integrations/{id}", func(writer http.ResponseWriter, request *http.Request) {
		row, exists := fake.destinations[request.PathValue("id")]
		if !exists {
			writeJSON(t, writer, http.StatusNotFound, map[string]any{"detail": "Integration not found"})
			return
		}
		var body map[string]json.RawMessage
		decodeJSON(t, request, &body)
		fake.destinationUpdates = append(fake.destinationUpdates, body)
		for _, key := range []string{"name", "url", "description", "template", "enabled"} {
			var value any
			if raw, present := body[key]; present {
				_ = json.Unmarshal(raw, &value)
			}
			if value != nil {
				row[key] = value
			}
		}
		var tenant, channel any
		if raw, present := body["teams_tenant_id"]; present {
			_ = json.Unmarshal(raw, &tenant)
			if tenant == "" {
				row["teams_tenant_id"] = nil
			} else if tenant != nil {
				row["teams_tenant_id"] = tenant
			}
		}
		if raw, present := body["channel_id"]; present {
			_ = json.Unmarshal(raw, &channel)
		}
		if channel == "" {
			row["channel_id"], row["channel_name"], row["teams_tenant_id"] = nil, nil, nil
		} else if channel != nil {
			var channelName any
			_ = json.Unmarshal(body["channel_name"], &channelName)
			row["channel_id"], row["channel_name"] = channel, channelName
		}
		if row["channel_id"] != nil {
			row["url"] = nil
		}
		writeJSON(t, writer, http.StatusOK, map[string]any{"item": masked(row)})
	})
	fake.handle("DELETE /api/v1/org/alerts/integrations/{id}", func(writer http.ResponseWriter, request *http.Request) {
		if _, exists := fake.destinations[request.PathValue("id")]; !exists {
			writeJSON(t, writer, http.StatusNotFound, map[string]any{"detail": "Integration not found"})
			return
		}
		delete(fake.destinations, request.PathValue("id"))
		writeJSON(t, writer, http.StatusOK, map[string]any{"success": true, "message": "Integration deleted"})
	})

	fake.handle("GET /api/v1/org/notifications/routes", func(writer http.ResponseWriter, _ *http.Request) {
		items := []map[string]any{}
		for _, row := range fake.routes {
			items = append(items, row)
		}
		sort.Slice(items, func(left, right int) bool {
			return alertFakeNumber(items[left]["priority"]) < alertFakeNumber(items[right]["priority"])
		})
		writeJSON(t, writer, http.StatusOK, map[string]any{"items": items})
	})
	fake.handle("POST /api/v1/org/notifications/routes", func(writer http.ResponseWriter, request *http.Request) {
		var body map[string]json.RawMessage
		decodeJSON(t, request, &body)
		fake.routeWrites = append(fake.routeWrites, body)
		if _, exists := fake.destinations[alertFakeString(body["destination_id"])]; !exists {
			writeJSON(t, writer, http.StatusNotFound, map[string]any{"detail": "Destination not found in this organisation."})
			return
		}
		fake.created++
		row := map[string]any{
			"id": fmt.Sprintf("10000000-0000-0000-0000-%012d", fake.created), "destination_type": "webhook",
			"kind": nil, "kinds": nil, "kinds_negated": false, "severity": nil, "cluster_id": nil, "source_id": nil,
			"application_id": nil, "reference_pattern": nil, "priority": float64(100), "stop_on_match": false,
			"mode": "include", "enabled": true,
		}
		alertFakeApplyRoute(row, body)
		fake.routes[dnsFakeText(row["id"])] = row
		writeJSON(t, writer, http.StatusOK, row)
	})
	fake.handle("PATCH /api/v1/org/notifications/routes/{id}", func(writer http.ResponseWriter, request *http.Request) {
		row, exists := fake.routes[request.PathValue("id")]
		if !exists {
			writeJSON(t, writer, http.StatusNotFound, map[string]any{"detail": "Route not found"})
			return
		}
		var body map[string]json.RawMessage
		decodeJSON(t, request, &body)
		fake.routeWrites = append(fake.routeWrites, body)
		alertFakeApplyRoute(row, body)
		writeJSON(t, writer, http.StatusOK, row)
	})
	fake.handle("DELETE /api/v1/org/notifications/routes/{id}", func(writer http.ResponseWriter, request *http.Request) {
		if _, exists := fake.routes[request.PathValue("id")]; !exists {
			writeJSON(t, writer, http.StatusNotFound, map[string]any{"detail": "Route not found"})
			return
		}
		delete(fake.routes, request.PathValue("id"))
		writer.WriteHeader(http.StatusNoContent)
	})
	return fake
}

func alertFakeString(raw json.RawMessage) string {
	var value string
	_ = json.Unmarshal(raw, &value)
	return value
}

// alertFakeApplyRoute applies a route write the way decodeRouteUpdate and
// NormaliseKindFilter do: a nullable filter present as null is cleared, a
// non-nullable member present as null is ignored, and the legacy kind is
// derived from a single positive kind.
func alertFakeApplyRoute(row map[string]any, body map[string]json.RawMessage) {
	for _, key := range []string{"severity", "cluster_id", "source_id", "application_id", "reference_pattern"} {
		if raw, present := body[key]; present {
			var value any
			_ = json.Unmarshal(raw, &value)
			row[key] = value
		}
	}
	for _, key := range []string{"destination_id", "priority", "stop_on_match", "mode", "enabled", "kinds_negated"} {
		if raw, present := body[key]; present {
			var value any
			_ = json.Unmarshal(raw, &value)
			if value != nil {
				row[key] = value
			}
		}
	}
	if raw, present := body["kinds"]; present {
		var kinds []any
		_ = json.Unmarshal(raw, &kinds)
		if len(kinds) == 0 {
			row["kinds"], row["kinds_negated"] = nil, false
		} else {
			row["kinds"] = kinds
		}
	}
	row["kind"] = nil
	if kinds, isList := row["kinds"].([]any); isList && len(kinds) == 1 && row["kinds_negated"] == false {
		row["kind"] = kinds[0]
	}
}

func TestAccAlertDestinationAndRouteLifecycle(t *testing.T) {
	fake := newAlertFake(t)

	initial := fake.providerBlock() + `
resource "ankra_alert_destination" "pager" {
  name             = "pager"
  integration_type = "pagerduty"
  url              = "https://events.example.com/hook?token=one"
  description      = "on-call"
}

resource "ankra_alert_destination" "first" {
  name = "first"
  url  = "https://hooks.example.com/1"
}

resource "ankra_alert_destination" "second" {
  name = "second"
  url  = "https://hooks.example.com/2"
}

resource "ankra_alert_route" "critical" {
  destination_id = ankra_alert_destination.pager.id
  kinds          = ["alert_trigger_fired"]
  severity       = "critical"
  priority       = 10
  stop_on_match  = true
}

data "ankra_alert_destinations" "all" {
  depends_on = [ankra_alert_destination.pager, ankra_alert_destination.first, ankra_alert_destination.second]
}

data "ankra_alert_routes" "all" {
  depends_on = [ankra_alert_route.critical]
}
`
	changed := fake.providerBlock() + `
resource "ankra_alert_destination" "pager" {
  name             = "pager"
  integration_type = "pagerduty"
  url              = "https://events.example.com/hook?token=two"
  enabled          = false
}

resource "ankra_alert_destination" "first" {
  name         = "first"
  channel_id   = "C123"
  channel_name = "alerts"
}

resource "ankra_alert_destination" "second" {
  name    = "second"
  url     = "https://hooks.example.com/2"
  enabled = false
}

resource "ankra_alert_route" "critical" {
  destination_id = ankra_alert_destination.first.id
  kinds          = ["alert_trigger_fired", "pipeline_run_failed"]
  kinds_negated  = true
  mode           = "exclude"
}
`

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: initial,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ankra_alert_destination.pager", "description", "on-call"),
					resource.TestCheckResourceAttr("ankra_alert_destination.pager", "enabled", "true"),
					resource.TestCheckResourceAttr("ankra_alert_destination.first", "integration_type", "slack"),
					resource.TestCheckResourceAttr("ankra_alert_route.critical", "kind", "alert_trigger_fired"),
					resource.TestCheckResourceAttr("ankra_alert_route.critical", "destination_type", "webhook"),
					resource.TestCheckResourceAttr("ankra_alert_route.critical", "mode", "include"),
					resource.TestCheckResourceAttr("data.ankra_alert_destinations.all", "destinations.#", "3"),
					resource.TestCheckResourceAttr("data.ankra_alert_routes.all", "routes.#", "1"),
					resource.TestCheckResourceAttr("data.ankra_alert_routes.all", "routes.0.priority", "10"),
					func(_ *terraform.State) error {
						for _, row := range fake.destinations {
							if row["name"] == "pager" && row["description"] != "on-call" {
								return fmt.Errorf("description was not stored by the follow-up update: %v", row["description"])
							}
						}
						return nil
					},
				),
			},
			{
				Config: changed,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckNoResourceAttr("ankra_alert_destination.pager", "description"),
					resource.TestCheckResourceAttr("ankra_alert_destination.pager", "enabled", "false"),
					resource.TestCheckResourceAttr("ankra_alert_destination.first", "channel_id", "C123"),
					resource.TestCheckNoResourceAttr("ankra_alert_destination.first", "url"),
					resource.TestCheckNoResourceAttr("ankra_alert_route.critical", "kind"),
					resource.TestCheckNoResourceAttr("ankra_alert_route.critical", "severity"),
					resource.TestCheckResourceAttr("ankra_alert_route.critical", "kinds_negated", "true"),
					resource.TestCheckResourceAttr("ankra_alert_route.critical", "priority", "100"),
					func(_ *terraform.State) error {
						for _, row := range fake.destinations {
							if row["name"] == "pager" {
								if row["description"] != "" || row["url"] != "https://events.example.com/hook?token=two" {
									return fmt.Errorf("pager not updated as configured: %v", row)
								}
							}
						}
						for _, update := range fake.destinationUpdates {
							if alertFakeString(update["name"]) == "second" {
								if _, sentURL := update["url"]; sentURL {
									return fmt.Errorf("an update that did not change the URL re-sent it, which drops a migrated destination's Vault path")
								}
							}
						}
						last := fake.routeWrites[len(fake.routeWrites)-1]
						if string(last["severity"]) != "null" {
							return fmt.Errorf("route update sent severity %s, want an explicit null to clear it", last["severity"])
						}
						if _, sentLegacyKind := last["kind"]; sentLegacyKind {
							return fmt.Errorf("route update sent the legacy kind member")
						}
						return nil
					},
				),
			},
			{
				ResourceName:      "ankra_alert_destination.pager",
				ImportState:       true,
				ImportStateVerify: true,
			},
			{
				ResourceName:      "ankra_alert_route.critical",
				ImportState:       true,
				ImportStateVerify: true,
			},
			{
				// Changed outside Terraform: the URL drift is detected and put back.
				PreConfig: func() {
					for _, row := range fake.destinations {
						if row["name"] == "pager" {
							row["url"] = "https://attacker.example.com"
						}
					}
				},
				Config: changed,
				Check: func(_ *terraform.State) error {
					for _, row := range fake.destinations {
						if row["name"] == "pager" && row["url"] != "https://events.example.com/hook?token=two" {
							return fmt.Errorf("URL drift was not corrected: %v", row["url"])
						}
					}
					return nil
				},
			},
			{
				// Deleted outside Terraform: the next plan recreates them.
				PreConfig: func() {
					for destinationID, row := range fake.destinations {
						if row["name"] == "second" {
							delete(fake.destinations, destinationID)
						}
					}
					for routeID := range fake.routes {
						delete(fake.routes, routeID)
					}
				},
				Config: changed,
				Check: func(_ *terraform.State) error {
					if len(fake.routes) != 1 {
						return fmt.Errorf("route deleted outside Terraform was not recreated")
					}
					for _, row := range fake.destinations {
						if row["name"] == "second" {
							return nil
						}
					}
					return fmt.Errorf("destination deleted outside Terraform was not recreated")
				},
			},
		},
	})
}

// alertFakeNumber reads a fake row member as a JSON number.
func alertFakeNumber(value any) float64 {
	number, _ := value.(float64)
	return number
}
