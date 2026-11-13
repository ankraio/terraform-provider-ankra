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

// addonDefinitionFake mirrors the add-on definition routes
// (go/internal/helmapi): definitions are addressed by name, chart links are
// added one at a time and removed by query, and global definitions refuse
// writes with 403.
type addonDefinitionFake struct {
	*routeFake
	definition map[string]any
	links      []map[string]any
	unlinks    int
}

func newAddonDefinitionFake(t *testing.T) *addonDefinitionFake {
	fake := &addonDefinitionFake{routeFake: newRouteFake(t)}
	fake.handle("POST /api/v1/org/helm/addon-definitions", func(writer http.ResponseWriter, request *http.Request) {
		var body map[string]any
		decodeJSON(t, request, &body)
		if body["name"] == "cert-manager" {
			writeJSON(t, writer, http.StatusConflict, map[string]any{"detail": "An add-on definition named cert-manager already exists"})
			return
		}
		body["id"] = "8b2c3d4e-5f6a-4b7c-8d9e-0f1a2b3c4d5e"
		body["source"] = "organisation"
		fake.definition = body
		writeJSON(t, writer, http.StatusOK, body)
	})
	fake.handle("GET /api/v1/org/helm/addon-definitions/{name}", func(writer http.ResponseWriter, request *http.Request) {
		if fake.definition == nil || request.PathValue("name") != fake.definition["name"] {
			writeJSON(t, writer, http.StatusNotFound, map[string]any{"detail": "Add-on definition not found"})
			return
		}
		detail := map[string]any{"chart_links": append([]map[string]any{
			{"id": "0", "chart_name": "ingress", "registry_url": "https://charts.example", "link_type": "include", "source": "global"},
		}, fake.links...)}
		for key, value := range fake.definition {
			detail[key] = value
		}
		writeJSON(t, writer, http.StatusOK, detail)
	})
	fake.handle("PUT /api/v1/org/helm/addon-definitions/{name}", func(writer http.ResponseWriter, request *http.Request) {
		var body map[string]any
		decodeJSON(t, request, &body)
		for key, value := range body {
			fake.definition[key] = value
		}
		writeJSON(t, writer, http.StatusOK, fake.definition)
	})
	fake.handle("DELETE /api/v1/org/helm/addon-definitions/{name}", func(writer http.ResponseWriter, request *http.Request) {
		fake.definition = nil
		fake.links = nil
		writeJSON(t, writer, http.StatusOK, map[string]any{"name": request.PathValue("name"), "deleted": true})
	})
	fake.handle("POST /api/v1/org/helm/addon-definitions/{name}/chart-links", func(writer http.ResponseWriter, request *http.Request) {
		var body map[string]any
		decodeJSON(t, request, &body)
		body["source"] = "organisation"
		fake.links = append(fake.links, body)
		writeJSON(t, writer, http.StatusOK, body)
	})
	fake.handle("DELETE /api/v1/org/helm/addon-definitions/{name}/chart-links", func(writer http.ResponseWriter, request *http.Request) {
		query := request.URL.Query()
		kept := []map[string]any{}
		for _, link := range fake.links {
			if link["chart_name"] != query.Get("chart_name") || link["registry_url"] != query.Get("registry_url") || link["link_type"] != query.Get("link_type") {
				kept = append(kept, link)
			}
		}
		fake.links = kept
		fake.unlinks++
		writeJSON(t, writer, http.StatusOK, map[string]any{"deleted": true})
	})
	return fake
}

func addonDefinitionConfig(fake *addonDefinitionFake, name, displayName, links string) string {
	return fake.providerBlock() + fmt.Sprintf(`
resource "ankra_addon_definition" "observability" {
  name              = %q
  display_name      = %q
  category          = "monitoring"
  match_chart_names = ["kube-prometheus-stack"]
  chart_links       = %s
}
`, name, displayName, links)
}

func TestAccAddonDefinitionLifecycle(t *testing.T) {
	fake := newAddonDefinitionFake(t)
	include := `{ chart_name = "grafana", registry_url = "https://grafana.github.io/helm-charts", link_type = "include" }`
	exclude := `{ chart_name = "loki", registry_url = "https://grafana.github.io/helm-charts", link_type = "exclude" }`
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy: func(_ *terraform.State) error {
			if fake.definition != nil {
				return fmt.Errorf("destroy must delete the definition")
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: addonDefinitionConfig(fake, "observability", "Observability", "["+include+"]"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ankra_addon_definition.observability", "chart_links.#", "1"),
					resource.TestCheckNoResourceAttr("ankra_addon_definition.observability", "description"),
				),
			},
			{
				Config: addonDefinitionConfig(fake, "observability", "Observability stack", "["+exclude+"]"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ankra_addon_definition.observability", "display_name", "Observability stack"),
					resource.TestCheckResourceAttr("ankra_addon_definition.observability", "chart_links.0.chart_name", "loki"),
					func(_ *terraform.State) error {
						if fake.unlinks != 1 || len(fake.links) != 1 {
							return fmt.Errorf("a changed link set must unlink the dropped link and link the new one: %d unlinks, links %v", fake.unlinks, fake.links)
						}
						return nil
					},
				),
			},
			{
				ResourceName:      "ankra_addon_definition.observability",
				ImportState:       true,
				ImportStateId:     "observability",
				ImportStateVerify: true,
			},
			{
				Config:      addonDefinitionConfig(fake, "cert-manager", "cert-manager", "[]"),
				ExpectError: regexp.MustCompile(`(?s)Unable to create the add-on definition.*already exists`),
			},
		},
	})
}
