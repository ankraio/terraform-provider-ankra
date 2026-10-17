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
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

const (
	routeTestProject      = "11111111-2222-3333-4444-555555555555"
	otherRouteTestProject = "66666666-7777-8888-9999-000000000000"
	routeTestDestination  = "20000000-0000-0000-0000-000000000001"
)

// newRouteProjectFake is the alert fake with one destination already in
// place, so route tests can name it without creating it.
func newRouteProjectFake(t *testing.T) *alertFake {
	fake := newAlertFake(t)
	fake.destinations[routeTestDestination] = map[string]any{
		"id": routeTestDestination, "name": "pager", "url": "https://hooks.example.com/1", "channel_id": nil, "channel_name": nil,
		"integration_type": "webhook", "teams_tenant_id": nil, "description": nil, "template": nil, "enabled": true,
	}
	fake.order = append(fake.order, routeTestDestination)
	return fake
}

// lastRouteProject returns the project_id member of the last route write
// as raw JSON, or "absent".
func lastRouteProject(fake *alertFake) string {
	if len(fake.routeWrites) == 0 {
		return "no write"
	}
	raw, present := fake.routeWrites[len(fake.routeWrites)-1]["project_id"]
	if !present {
		return "absent"
	}
	return string(raw)
}

func TestAccAlertRouteProject(t *testing.T) {
	fake := newRouteProjectFake(t)
	configuration := func(projectArguments string) string {
		return fake.providerBlock() + fmt.Sprintf(`
resource "ankra_alert_route" "scoped" {
  destination_id = %q
  severity       = "critical"
  %s
}
`, routeTestDestination, projectArguments)
	}
	routeIDs := []string{}
	recordRouteID := func(state *terraform.State) error {
		routeIDs = append(routeIDs, state.RootModule().Resources["ankra_alert_route.scoped"].Primary.ID)
		return nil
	}
	expectProject := func(want string) resource.TestCheckFunc {
		return func(_ *terraform.State) error {
			if got := lastRouteProject(fake); got != want {
				return fmt.Errorf("route create sent project_id %s, want %s", got, want)
			}
			return nil
		}
	}

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				// Unset: the create leaves project_id out, so the platform
				// stamps the request's project as before.
				Config: configuration(""),
				Check: resource.ComposeAggregateTestCheckFunc(
					expectProject("absent"),
					resource.TestCheckNoResourceAttr("ankra_alert_route.scoped", "project_id"),
					resource.TestCheckResourceAttr("ankra_alert_route.scoped", "organisation_wide", "false"),
					recordRouteID,
				),
			},
			{
				Config: configuration(fmt.Sprintf("project_id = %q", routeTestProject)),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("ankra_alert_route.scoped", plancheck.ResourceActionDestroyBeforeCreate),
					},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					expectProject(`"`+routeTestProject+`"`),
					resource.TestCheckResourceAttr("ankra_alert_route.scoped", "project_id", routeTestProject),
					recordRouteID,
				),
			},
			{
				Config:   configuration(fmt.Sprintf("project_id = %q", routeTestProject)),
				PlanOnly: true,
			},
			{
				Config: configuration("organisation_wide = true"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("ankra_alert_route.scoped", plancheck.ResourceActionDestroyBeforeCreate),
					},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					expectProject("null"),
					resource.TestCheckResourceAttr("ankra_alert_route.scoped", "organisation_wide", "true"),
					recordRouteID,
					func(_ *terraform.State) error {
						if len(fake.routes) != 1 {
							return fmt.Errorf("replacing the route left %d routes, want 1", len(fake.routes))
						}
						if routeIDs[0] == routeIDs[1] || routeIDs[1] == routeIDs[2] {
							return fmt.Errorf("a project change did not replace the route: ids %v", routeIDs)
						}
						return nil
					},
				),
			},
		},
	})
}

func TestAccAlertRouteProjectImportAdopts(t *testing.T) {
	fake := newRouteProjectFake(t)
	const routeID = "10000000-0000-0000-0000-000000000099"
	fake.routes[routeID] = map[string]any{
		"id": routeID, "destination_id": routeTestDestination, "destination_type": "webhook",
		"kind": nil, "kinds": nil, "kinds_negated": false, "severity": nil, "cluster_id": nil, "source_id": nil,
		"application_id": nil, "reference_pattern": nil, "priority": float64(100), "stop_on_match": false,
		"mode": "include", "enabled": true,
	}
	configuration := func(projectArguments string) string {
		return fake.providerBlock() + fmt.Sprintf(`
resource "ankra_alert_route" "existing" {
  destination_id = %q
  %s
}
`, routeTestDestination, projectArguments)
	}

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:             configuration("organisation_wide = true"),
				ResourceName:       "ankra_alert_route.existing",
				ImportState:        true,
				ImportStateId:      routeID,
				ImportStatePersist: true,
			},
			{
				// The platform does not return the project, so the first
				// apply adopts the configured one in place.
				Config: configuration("organisation_wide = true"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("ankra_alert_route.existing", plancheck.ResourceActionUpdate),
					},
				},
				Check: func(_ *terraform.State) error {
					if _, exists := fake.routes[routeID]; !exists || len(fake.routes) != 1 {
						return fmt.Errorf("adopting an imported route replaced it: %v", fake.routes)
					}
					return nil
				},
			},
			{
				// Adopted: a later change replaces the route.
				Config: configuration(fmt.Sprintf("project_id = %q", routeTestProject)),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("ankra_alert_route.existing", plancheck.ResourceActionDestroyBeforeCreate),
					},
				},
			},
		},
	})
}

func TestAccAlertRouteProjectOutsideProviderProject(t *testing.T) {
	fake := newRouteProjectFake(t)
	provider := fmt.Sprintf(`
provider "ankra" {
  token      = "test-token"
  base_url   = %q
  project_id = %q
}
`, fake.server.URL, routeTestProject)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: provider + fmt.Sprintf(`
resource "ankra_alert_route" "elsewhere" {
  destination_id = %q
  project_id     = %q
}
`, routeTestDestination, otherRouteTestProject),
				ExpectError: regexp.MustCompile(`Alert route project outside the provider's project`),
			},
			{
				Config: provider + fmt.Sprintf(`
resource "ankra_alert_route" "both" {
  destination_id    = %q
  project_id        = %q
  organisation_wide = true
}
`, routeTestDestination, routeTestProject),
				ExpectError: regexp.MustCompile(`organisation_wide and project_id conflict`),
			},
		},
	})
	for _, served := range fake.served() {
		if strings.HasPrefix(served, "POST ") {
			t.Errorf("a refused route reached the platform: %s", served)
		}
	}
}

// newCatalogueFake serves the alert and DNS catalogues the way
// go/internal/alertsapi and go/internal/dnsrecordsapi do: the channel
// listings, the ingest credential listing (no tokens), the cluster DNS
// zones, and a Cloudflare record listing that reports truncation.
func newCatalogueFake(t *testing.T) (*routeFake, *[]string) {
	fake := newRouteFake(t)
	cloudflareQueries := []string{}
	fake.handle("GET /api/v1/org/alerts/integrations/slack/channels", func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(t, writer, http.StatusOK, map[string]any{
			"team_id": "T1", "team_name": "Acme",
			"channels": []map[string]any{{"id": "C1", "name": "alerts", "is_private": false}, {"id": "C2", "name": "oncall", "is_private": true}},
		})
	})
	fake.handle("GET /api/v1/org/alerts/integrations/teams/channels", func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(t, writer, http.StatusOK, map[string]any{"channels": []map[string]any{
			{"id": "19:abc@thread.tacv2", "name": "General", "team_id": "team-1", "team_name": "Platform", "tenant_id": "tenant-1"},
		}})
	})
	fake.handle("GET /api/v1/org/alerts/ingest-credentials", func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(t, writer, http.StatusOK, map[string]any{"items": []map[string]any{{
			"id": "30000000-0000-0000-0000-000000000001", "name": "alertmanager", "cluster_id": nil, "scope": "platform", "enabled": true,
			"created_at": "2026-10-17T00:00:00Z", "updated_at": "2026-10-17T00:00:00Z", "last_used_at": nil,
		}}})
	})
	fake.handle("GET /api/v1/org/dns/cluster-zones", func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(t, writer, http.StatusOK, map[string]any{"items": []map[string]any{
			{"cluster_id": "c-1", "cluster_name": "prod", "fqdn": "prod.acme.ankra.cc", "state": "active", "last_error": nil},
			{"cluster_id": "c-2", "cluster_name": "dev", "fqdn": "dev.acme.ankra.cc", "state": "failed", "last_error": "delegation refused"},
		}})
	})
	fake.handle("GET /api/v1/org/cloudflare/domains/{zone}/records", func(writer http.ResponseWriter, request *http.Request) {
		cloudflareQueries = append(cloudflareQueries, request.URL.RawQuery)
		if request.PathValue("zone") != "zone-1" {
			writeJSON(t, writer, http.StatusNotFound, map[string]any{"detail": "Cloudflare domain not found"})
			return
		}
		writeJSON(t, writer, http.StatusOK, map[string]any{
			"domain": map[string]any{"id": "zone-1", "name": "example.com"},
			"items": []map[string]any{
				{"id": "r1", "zone_id": "zone-1", "zone_name": "example.com", "name": "www.example.com", "record_type": "A", "content": "192.0.2.1",
					"ttl": 1, "is_ttl_automatic": true, "proxied": true, "proxiable": true, "priority": nil, "comment": "", "tags": []string{}, "managed_by": "external"},
				{"id": "r2", "zone_id": "zone-1", "zone_name": "example.com", "name": "example.com", "record_type": "MX", "content": "mail.example.com",
					"ttl": 300, "is_ttl_automatic": false, "proxied": false, "proxiable": false, "priority": 10, "comment": "Managed by Ankra", "tags": []string{}, "managed_by": "ankra"},
			},
			"is_truncated": true,
		})
	})
	return fake, &cloudflareQueries
}

func TestAccAlertAndDNSCatalogueDataSources(t *testing.T) {
	fake, cloudflareQueries := newCatalogueFake(t)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: fake.providerBlock() + `
data "ankra_slack_channels" "all" {}
data "ankra_teams_channels" "all" {}
data "ankra_alert_ingest_credentials" "all" {}
data "ankra_cluster_dns_zones" "all" {}
data "ankra_cloudflare_dns_records" "mx" {
  zone_id         = "zone-1"
  credential_name = "main"
  record_type     = "MX"
  search          = "mail"
}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.ankra_slack_channels.all", "team_name", "Acme"),
					resource.TestCheckResourceAttr("data.ankra_slack_channels.all", "channels.#", "2"),
					resource.TestCheckResourceAttr("data.ankra_slack_channels.all", "channels.1.is_private", "true"),
					resource.TestCheckResourceAttr("data.ankra_teams_channels.all", "channels.0.tenant_id", "tenant-1"),
					resource.TestCheckResourceAttr("data.ankra_alert_ingest_credentials.all", "credentials.0.scope", "platform"),
					resource.TestCheckResourceAttr("data.ankra_alert_ingest_credentials.all", "credentials.0.webhook_path",
						"/webhooks/alerts/30000000-0000-0000-0000-000000000001"),
					resource.TestCheckNoResourceAttr("data.ankra_alert_ingest_credentials.all", "credentials.0.cluster_id"),
					resource.TestCheckNoResourceAttr("data.ankra_alert_ingest_credentials.all", "credentials.0.last_used_at"),
					resource.TestCheckResourceAttr("data.ankra_cluster_dns_zones.all", "zones.#", "2"),
					resource.TestCheckResourceAttr("data.ankra_cluster_dns_zones.all", "zones.1.last_error", "delegation refused"),
					resource.TestCheckNoResourceAttr("data.ankra_cluster_dns_zones.all", "zones.0.last_error"),
					resource.TestCheckResourceAttr("data.ankra_cloudflare_dns_records.mx", "is_truncated", "true"),
					resource.TestCheckResourceAttr("data.ankra_cloudflare_dns_records.mx", "records.#", "2"),
					resource.TestCheckResourceAttr("data.ankra_cloudflare_dns_records.mx", "records.1.priority", "10"),
					resource.TestCheckNoResourceAttr("data.ankra_cloudflare_dns_records.mx", "records.0.priority"),
					resource.TestCheckNoResourceAttr("data.ankra_cloudflare_dns_records.mx", "records.0.comment"),
					func(_ *terraform.State) error {
						for _, query := range *cloudflareQueries {
							if query != "credential_name=main&record_type=MX&search=mail" {
								return fmt.Errorf("Cloudflare listing sent query %q, want the filters and credential", query)
							}
						}
						return nil
					},
				),
			},
		},
	})
}

func TestAccAlertAndDNSCatalogueFailures(t *testing.T) {
	fake := newRouteFake(t)
	fake.handle("GET /api/v1/org/alerts/integrations/slack/channels", func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(t, writer, http.StatusNotFound, map[string]any{"detail": "No Slack workspace connected"})
	})
	fake.handle("GET /api/v1/org/alerts/integrations/teams/channels", func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(t, writer, http.StatusOK, map[string]any{"channels": []map[string]any{}})
	})
	fake.handle("GET /api/v1/org/alerts/ingest-credentials", func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(t, writer, http.StatusOK, map[string]any{"items": []map[string]any{}})
	})
	fake.handle("GET /api/v1/org/cloudflare/domains/{zone}/records", func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(t, writer, http.StatusNotFound, map[string]any{"detail": "Cloudflare domain not found"})
	})

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: fake.providerBlock() + `
data "ankra_teams_channels" "none" {}
data "ankra_alert_ingest_credentials" "none" {}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.ankra_teams_channels.none", "channels.#", "0"),
					resource.TestCheckResourceAttr("data.ankra_alert_ingest_credentials.none", "credentials.#", "0"),
				),
			},
			{
				Config:      fake.providerBlock() + `data "ankra_slack_channels" "none" {}`,
				ExpectError: regexp.MustCompile(`no connected Slack workspace`),
			},
			{
				Config:      fake.providerBlock() + `data "ankra_cloudflare_dns_records" "missing" { zone_id = "zone-404" }`,
				ExpectError: regexp.MustCompile(`Unable to list Cloudflare DNS records`),
			},
		},
	})
}
