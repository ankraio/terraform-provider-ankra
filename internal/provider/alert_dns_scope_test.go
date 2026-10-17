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

const (
	alertDNSScopeProject      = "11111111-2222-3333-4444-555555555555"
	alertDNSScopeOtherProject = "99999999-8888-7777-6666-555555555555"
)

// alertDNSScopeFake serves the project-scoped objects of this domain the way
// the platform does: an alert route and a DNS credential belong to the
// project the creating request carried in X-Ankra-Project-Id, and a request
// from another project neither lists nor deletes them (404). Alert
// destinations are organisation-wide.
type alertDNSScopeFake struct {
	*routeFake
	routes      map[string]map[string]any
	credentials map[string]map[string]any
	created     int
}

func newAlertDNSScopeFake(t *testing.T) *alertDNSScopeFake {
	fake := &alertDNSScopeFake{routeFake: newRouteFake(t), routes: map[string]map[string]any{}, credentials: map[string]map[string]any{}}
	visible := func(row map[string]any, request *http.Request) bool {
		return row["project"] == request.Header.Get("X-Ankra-Project-Id")
	}
	destination := map[string]any{"id": "00000000-0000-0000-0000-000000000001", "name": "pager", "url": "https://hooks.example.com/1",
		"integration_type": "slack", "enabled": true}

	fake.handle("POST /api/v1/org/alerts/integrations", func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(t, writer, http.StatusOK, map[string]any{"item": map[string]any{"id": destination["id"], "url": "https://***"}})
	})
	fake.handle("GET /api/v1/org/alerts/integrations/{id}", func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(t, writer, http.StatusOK, map[string]any{"item": destination})
	})
	fake.handle("DELETE /api/v1/org/alerts/integrations/{id}", func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(t, writer, http.StatusOK, map[string]any{"success": true, "message": "Integration deleted"})
	})

	fake.handle("GET /api/v1/org/notifications/routes", func(writer http.ResponseWriter, request *http.Request) {
		items := []map[string]any{}
		for _, row := range fake.routes {
			if visible(row, request) {
				items = append(items, row)
			}
		}
		writeJSON(t, writer, http.StatusOK, map[string]any{"items": items})
	})
	fake.handle("POST /api/v1/org/notifications/routes", func(writer http.ResponseWriter, request *http.Request) {
		fake.created++
		row := map[string]any{
			"id": fmt.Sprintf("10000000-0000-0000-0000-%012d", fake.created), "destination_id": destination["id"], "destination_type": "webhook",
			"kinds": nil, "kind": nil, "kinds_negated": false, "priority": 100, "stop_on_match": false, "mode": "include", "enabled": true,
			"project": request.Header.Get("X-Ankra-Project-Id"),
		}
		fake.routes[dnsFakeText(row["id"])] = row
		writeJSON(t, writer, http.StatusOK, row)
	})
	fake.handle("DELETE /api/v1/org/notifications/routes/{id}", func(writer http.ResponseWriter, request *http.Request) {
		row, exists := fake.routes[request.PathValue("id")]
		if !exists || !visible(row, request) {
			writeJSON(t, writer, http.StatusNotFound, map[string]any{"detail": "Route not found"})
			return
		}
		delete(fake.routes, request.PathValue("id"))
		writer.WriteHeader(http.StatusNoContent)
	})

	fake.handle("GET /api/v1/credentials/dns", func(writer http.ResponseWriter, request *http.Request) {
		listing := []map[string]any{}
		for _, row := range fake.credentials {
			if visible(row, request) {
				listing = append(listing, map[string]any{"id": row["id"], "name": row["name"], "provider": "dns"})
			}
		}
		writeJSON(t, writer, http.StatusOK, listing)
	})
	fake.handle("POST /api/v1/credentials/dns", func(writer http.ResponseWriter, request *http.Request) {
		var body map[string]any
		decodeJSON(t, request, &body)
		fake.created++
		row := map[string]any{"id": fmt.Sprintf("20000000-0000-0000-0000-%012d", fake.created), "name": body["name"],
			"project": request.Header.Get("X-Ankra-Project-Id")}
		fake.credentials[dnsFakeText(row["id"])] = row
		writeJSON(t, writer, http.StatusCreated, map[string]any{"success": true, "id": row["id"], "name": row["name"], "errors": []any{}})
	})
	fake.handle("DELETE /api/v1/org/credentials/{id}", func(writer http.ResponseWriter, request *http.Request) {
		row, exists := fake.credentials[request.PathValue("id")]
		if !exists || !visible(row, request) {
			writeJSON(t, writer, http.StatusNotFound, map[string]any{"detail": "Credential not found"})
			return
		}
		delete(fake.credentials, request.PathValue("id"))
		writeJSON(t, writer, http.StatusOK, map[string]any{"id": request.PathValue("id")})
	})
	return fake
}

// TestAccAlertDNSScopeMoveKeepsTheResources pins that moving the provider to
// another project neither drops the project-scoped route and credential from
// state (planning duplicates) nor lets destroy report them deleted.
func TestAccAlertDNSScopeMoveKeepsTheResources(t *testing.T) {
	fake := newAlertDNSScopeFake(t)
	inProject := func(project string) string {
		return fmt.Sprintf(`
provider "ankra" {
  token      = "test-token"
  base_url   = %q
  project_id = %q
}

resource "ankra_alert_destination" "pager" {
  name = "pager"
  url  = "https://hooks.example.com/1"
}

resource "ankra_alert_route" "all" {
  destination_id = ankra_alert_destination.pager.id
}

resource "ankra_dns_credential" "webhook" {
  name                 = "webhook"
  webhook_provider_url = "https://dns.example.com/hook"
}
`, fake.server.URL, project)
	}
	original := func(_ *terraform.State) error {
		if len(fake.routes) != 1 || len(fake.credentials) != 1 {
			return fmt.Errorf("want one route and one credential, have %d and %d", len(fake.routes), len(fake.credentials))
		}
		return nil
	}

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{Config: inProject(alertDNSScopeProject), Check: original},
			{
				Config:      inProject(alertDNSScopeOtherProject),
				ExpectError: regexp.MustCompile(`Object not visible in the provider's current scope`),
			},
			{
				Config:      inProject(alertDNSScopeOtherProject),
				Destroy:     true,
				ExpectError: regexp.MustCompile(`Object not visible in the provider's current scope`),
			},
			{Config: inProject(alertDNSScopeProject), Check: original},
		},
	})
	if len(fake.routes) != 0 || len(fake.credentials) != 0 {
		t.Errorf("destroy in the original project left %d routes and %d credentials", len(fake.routes), len(fake.credentials))
	}
}
