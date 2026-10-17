// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"testing"
	"time"

	"ankra.io/terraform-provider-ankra/internal/client"
)

// costFake holds cost budgets, the autopilot policy and the pricing settings
// the way the platform's costapi routes do: field-by-field bodies that refuse
// unknown fields, absent-keeps and null-clears, a budget scope that cannot
// change, partial autopilot defaults merged over hands-off, and a settings
// read that answers the default analysed-cluster limit as 8.
type costFake struct {
	*routeFake
	budgets map[string]map[string]any
	// isTokenPinned makes the fake answer like a token pinned to one
	// project: project_id=all is a 403 and the plain listing holds only that
	// project's budgets.
	isTokenPinned bool
	// budgetOrganisations is the organisation each budget was created in; a
	// listing or delete under another organisation does not see it.
	budgetOrganisations map[string]string
	// budgetUpdates records every budget update body as sent.
	budgetUpdates []map[string]json.RawMessage

	autopilotKinds      []string
	autopilotDefaults   map[string]string
	quietHours          map[string]any
	notificationRouteID any
	// autopilotWrites records every policy write body as sent.
	autopilotWrites []map[string]json.RawMessage
	clusterNames    map[string]string
	overrides       map[string]map[string]any
	// overrideWrites records every override body as sent.
	overrideWrites []map[string]json.RawMessage

	discount             float64
	currency             string
	includeEgress        bool
	analysedClusterLimit any
	// settingsWrites records every settings body as sent.
	settingsWrites []map[string]json.RawMessage
}

func costIsNull(raw json.RawMessage) bool {
	return bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}

func newCostFake(t *testing.T) *costFake {
	fake := &costFake{
		routeFake:           newRouteFake(t),
		budgets:             map[string]map[string]any{},
		budgetOrganisations: map[string]string{},
		autopilotKinds:      []string{"production", "staging", "development", "preview", "unknown"},
		autopilotDefaults: map[string]string{
			"production": "hands-off", "staging": "hands-off", "development": "hands-off", "preview": "hands-off", "unknown": "hands-off",
		},
		clusterNames: map[string]string{
			"11111111-1111-1111-1111-111111111111": "prod-eu",
			"22222222-2222-2222-2222-222222222222": "dev-eu",
		},
		overrides:            map[string]map[string]any{},
		currency:             "eur",
		analysedClusterLimit: nil,
	}
	created := 0

	fake.handle("GET /api/v1/org/cloud-cost/budgets", func(writer http.ResponseWriter, request *http.Request) {
		isAll := request.URL.Query().Get("project_id") == "all"
		if fake.isTokenPinned && isAll {
			writeJSON(t, writer, http.StatusForbidden, map[string]any{"detail": "This token is limited to one project"})
			return
		}
		if !fake.isTokenPinned && !isAll {
			t.Errorf("budget listing without project_id=all would hide other projects' budgets: %s", request.URL.RawQuery)
		}
		identifiers := make([]string, 0, len(fake.budgets))
		for identifier, budget := range fake.budgets {
			if !isAll && budget["scope_kind"] != "project" {
				continue
			}
			if organisation, isTracked := fake.budgetOrganisations[identifier]; isTracked && organisation != request.Header.Get(client.OrganisationHeader) {
				continue
			}
			identifiers = append(identifiers, identifier)
		}
		sort.Strings(identifiers)
		listing := make([]map[string]any, 0, len(identifiers))
		for _, identifier := range identifiers {
			listing = append(listing, fake.budgets[identifier])
		}
		writeJSON(t, writer, http.StatusOK, map[string]any{"budgets": listing})
	})
	fake.handle("POST /api/v1/org/cloud-cost/budgets", func(writer http.ResponseWriter, request *http.Request) {
		var body map[string]json.RawMessage
		decodeJSON(t, request, &body)
		budget := map[string]any{"notify_at_pct": 80, "owner_user_id": nil, "scope_id": nil,
			"projection": map[string]any{"status": "unknown", "month_to_date_cents": nil, "projected_month_end_cents": nil}}
		for field, raw := range body {
			var value any
			_ = json.Unmarshal(raw, &value)
			switch field {
			case "name", "scope_kind", "monthly_cents", "notify_at_pct", "owner_user_id":
				budget[field] = value
			case "currency":
				budget[field] = strings.ToLower(platformServicesText(value))
			case "scope_id":
				budget[field] = strings.ToLower(platformServicesText(value))
			default:
				writeJSON(t, writer, http.StatusBadRequest, map[string]any{"detail": field + " is not a field of a budget"})
				return
			}
		}
		for _, existing := range fake.budgets {
			if existing["scope_kind"] == budget["scope_kind"] && existing["scope_id"] == budget["scope_id"] {
				writeJSON(t, writer, http.StatusConflict, map[string]any{"detail": "This scope already has a budget"})
				return
			}
		}
		created++
		budget["id"] = fmt.Sprintf("00000000-0000-0000-0000-%012d", created)
		budget["scope_name"] = "scope " + fmt.Sprint(budget["scope_id"])
		fake.budgets[platformServicesText(budget["id"])] = budget
		fake.budgetOrganisations[platformServicesText(budget["id"])] = request.Header.Get(client.OrganisationHeader)
		writeJSON(t, writer, http.StatusCreated, budget)
	})
	fake.handle("PUT /api/v1/org/cloud-cost/budgets/{budget_id}", func(writer http.ResponseWriter, request *http.Request) {
		budget, exists := fake.budgets[request.PathValue("budget_id")]
		if !exists {
			writeJSON(t, writer, http.StatusNotFound, map[string]any{"detail": "Budget not found"})
			return
		}
		var body map[string]json.RawMessage
		decodeJSON(t, request, &body)
		fake.budgetUpdates = append(fake.budgetUpdates, body)
		for field, raw := range body {
			var value any
			_ = json.Unmarshal(raw, &value)
			switch field {
			case "scope_kind", "scope_id":
				writeJSON(t, writer, http.StatusBadRequest, map[string]any{"detail": "a budget's scope cannot change"})
				return
			case "currency":
				budget[field] = strings.ToLower(platformServicesText(value))
			case "name", "monthly_cents", "notify_at_pct", "owner_user_id":
				budget[field] = value
			default:
				writeJSON(t, writer, http.StatusBadRequest, map[string]any{"detail": field + " is not a field of a budget"})
				return
			}
		}
		writeJSON(t, writer, http.StatusOK, budget)
	})
	fake.handle("DELETE /api/v1/org/cloud-cost/budgets/{budget_id}", func(writer http.ResponseWriter, request *http.Request) {
		organisation, isTracked := fake.budgetOrganisations[request.PathValue("budget_id")]
		if isTracked && organisation != request.Header.Get(client.OrganisationHeader) {
			writeJSON(t, writer, http.StatusNotFound, map[string]any{"detail": "Budget not found"})
			return
		}
		if _, exists := fake.budgets[request.PathValue("budget_id")]; !exists {
			writeJSON(t, writer, http.StatusNotFound, map[string]any{"detail": "Budget not found"})
			return
		}
		delete(fake.budgets, request.PathValue("budget_id"))
		writer.WriteHeader(http.StatusNoContent)
	})

	fake.handle("GET /api/v1/org/cloud-cost/autopilot", func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(t, writer, http.StatusOK, fake.autopilotPolicy())
	})
	fake.handle("PUT /api/v1/org/cloud-cost/autopilot", func(writer http.ResponseWriter, request *http.Request) {
		var body map[string]json.RawMessage
		decodeJSON(t, request, &body)
		fake.autopilotWrites = append(fake.autopilotWrites, body)
		for field, raw := range body {
			switch field {
			case "defaults":
				if costIsNull(raw) {
					writeJSON(t, writer, http.StatusBadRequest, map[string]any{"detail": "defaults cannot be null"})
					return
				}
				var defaults map[string]string
				_ = json.Unmarshal(raw, &defaults)
				for kind, tier := range defaults {
					fake.autopilotDefaults[kind] = tier
				}
			case "quiet_hours":
				fake.quietHours = nil
				if !costIsNull(raw) {
					_ = json.Unmarshal(raw, &fake.quietHours)
				}
			case "notification_route_id":
				fake.notificationRouteID = nil
				if !costIsNull(raw) {
					_ = json.Unmarshal(raw, &fake.notificationRouteID)
				}
			default:
				writeJSON(t, writer, http.StatusBadRequest, map[string]any{"detail": field + " is not a field of the autopilot policy"})
				return
			}
		}
		writeJSON(t, writer, http.StatusOK, fake.autopilotPolicy())
	})
	fake.handle("PUT /api/v1/org/cloud-cost/autopilot/clusters/{cluster_id}", func(writer http.ResponseWriter, request *http.Request) {
		clusterID := request.PathValue("cluster_id")
		if _, exists := fake.clusterNames[clusterID]; !exists {
			writeJSON(t, writer, http.StatusNotFound, map[string]any{"detail": "Cluster not found"})
			return
		}
		var body map[string]json.RawMessage
		decodeJSON(t, request, &body)
		fake.overrideWrites = append(fake.overrideWrites, body)
		override := map[string]any{"set_by": "user-1", "set_at": "2026-10-17T00:00:00Z", "expires_at": nil}
		for field, raw := range body {
			var value any
			_ = json.Unmarshal(raw, &value)
			switch field {
			case "tier", "reason":
				override[field] = value
			case "expires_at":
				if value != nil {
					expiresAt, parseError := time.Parse(time.RFC3339Nano, platformServicesText(value))
					if parseError != nil {
						writeJSON(t, writer, http.StatusUnprocessableEntity, map[string]any{"detail": "expires_at is not a datetime"})
						return
					}
					override[field] = expiresAt.UTC().Format(time.RFC3339Nano)
				}
			}
		}
		if override["tier"] == nil || override["reason"] == nil {
			writeJSON(t, writer, http.StatusUnprocessableEntity, map[string]any{"detail": "tier and reason are required"})
			return
		}
		fake.overrides[clusterID] = override
		writeJSON(t, writer, http.StatusOK, fake.autopilotCluster(clusterID))
	})
	fake.handle("DELETE /api/v1/org/cloud-cost/autopilot/clusters/{cluster_id}", func(writer http.ResponseWriter, request *http.Request) {
		clusterID := request.PathValue("cluster_id")
		if _, exists := fake.clusterNames[clusterID]; !exists {
			writeJSON(t, writer, http.StatusNotFound, map[string]any{"detail": "Cluster not found"})
			return
		}
		delete(fake.overrides, clusterID)
		writeJSON(t, writer, http.StatusOK, fake.autopilotCluster(clusterID))
	})

	fake.handle("GET /api/v1/org/cloud-cost/settings", func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(t, writer, http.StatusOK, fake.settings())
	})
	fake.handle("PUT /api/v1/org/cloud-cost/settings", func(writer http.ResponseWriter, request *http.Request) {
		var body map[string]json.RawMessage
		decodeJSON(t, request, &body)
		fake.settingsWrites = append(fake.settingsWrites, body)
		if body["effective_discount_pct"] == nil || body["currency"] == nil {
			writeJSON(t, writer, http.StatusUnprocessableEntity, map[string]any{"detail": "missing"})
			return
		}
		_ = json.Unmarshal(body["effective_discount_pct"], &fake.discount)
		var currency string
		_ = json.Unmarshal(body["currency"], &currency)
		fake.currency = strings.ToLower(currency)
		fake.includeEgress = false
		if raw, present := body["include_network_egress_estimate"]; present {
			_ = json.Unmarshal(raw, &fake.includeEgress)
		}
		if raw, present := body["analysed_cluster_limit"]; present {
			fake.analysedClusterLimit = nil
			if !costIsNull(raw) {
				_ = json.Unmarshal(raw, &fake.analysedClusterLimit)
			}
		}
		writeJSON(t, writer, http.StatusOK, fake.settings())
	})
	return fake
}

func (fake *costFake) autopilotCluster(clusterID string) map[string]any {
	tier, source := fake.autopilotDefaults["production"], "environment"
	var override any
	if stored, exists := fake.overrides[clusterID]; exists {
		tier, source, override = platformServicesText(stored["tier"]), "override", stored
	}
	return map[string]any{"cluster_id": clusterID, "cluster_name": fake.clusterNames[clusterID], "environment": "",
		"environment_kind": "production", "override": override, "reason": "", "source": source, "tier": tier}
}

func (fake *costFake) autopilotPolicy() map[string]any {
	identifiers := make([]string, 0, len(fake.clusterNames))
	for identifier := range fake.clusterNames {
		identifiers = append(identifiers, identifier)
	}
	sort.Strings(identifiers)
	clusters := make([]map[string]any, 0, len(identifiers))
	for _, identifier := range identifiers {
		clusters = append(clusters, fake.autopilotCluster(identifier))
	}
	defaults := map[string]string{}
	for kind, tier := range fake.autopilotDefaults {
		defaults[kind] = tier
	}
	return map[string]any{
		"is_set": len(fake.autopilotWrites) > 0, "defaults": defaults, "recommended_defaults": map[string]string{},
		"quiet_hours": fake.quietHours, "notification_route_id": fake.notificationRouteID,
		"environment_kinds": fake.autopilotKinds, "tiers": []any{}, "clusters": clusters,
	}
}

func (fake *costFake) settings() map[string]any {
	limit := fake.analysedClusterLimit
	if limit == nil {
		limit = 8
	}
	return map[string]any{"effective_discount_pct": fake.discount, "currency": fake.currency,
		"include_network_egress_estimate": fake.includeEgress, "analysed_cluster_limit": limit,
		"display_currency": fake.currency, "fx_rates": map[string]float64{}}
}
