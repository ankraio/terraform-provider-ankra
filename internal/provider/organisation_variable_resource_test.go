// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

const (
	variableTestProject = "11111111-2222-3333-4444-555555555555"
	organisationWide    = ""
)

// variableFake holds organisation variables the way the platform does
// (go/internal/varsapi): create answers 200 and never echoes a secret, the
// listing hides secret values, an update's explicit null description clears
// the stored one, and delete answers 200 or 404. Variables live in layers keyed
// by the X-Ankra-Project-Id the request carried; a project sees its own rows
// plus the organisation-wide rows it does not shadow, and writes by name
// resolve the project row first, then the organisation-wide one.
type variableFake struct {
	*routeFake
	layers  map[string]map[string]map[string]any
	created int
}

func newVariableFake(t *testing.T) *variableFake {
	fake := &variableFake{routeFake: newRouteFake(t), layers: map[string]map[string]map[string]any{}}

	fake.handle("GET /api/v1/org/variables", func(writer http.ResponseWriter, request *http.Request) {
		visible := map[string]map[string]any{}
		for name, row := range fake.layer(organisationWide) {
			visible[name] = row
		}
		for name, row := range fake.layer(request.Header.Get("X-Ankra-Project-Id")) {
			visible[name] = row
		}
		names := make([]string, 0, len(visible))
		for name := range visible {
			names = append(names, name)
		}
		sort.Strings(names)
		listing := make([]map[string]any, 0, len(names))
		for _, name := range names {
			listing = append(listing, publicVariableRow(visible[name]))
		}
		writeJSON(t, writer, http.StatusOK, map[string]any{"variables": listing})
	})
	fake.handle("POST /api/v1/org/variables", func(writer http.ResponseWriter, request *http.Request) {
		var body map[string]any
		decodeJSON(t, request, &body)
		name, _ := body["name"].(string)
		layer := fake.layer(request.Header.Get("X-Ankra-Project-Id"))
		if _, exists := layer[name]; exists {
			writeJSON(t, writer, http.StatusConflict, map[string]any{"detail": "exists"})
			return
		}
		fake.created++
		body["id"] = fmt.Sprintf("var-%d", fake.created)
		layer[name] = body
		writeJSON(t, writer, http.StatusOK, map[string]any{"variable": publicVariableRow(body)})
	})
	fake.handle("PUT /api/v1/org/variables/{name}", func(writer http.ResponseWriter, request *http.Request) {
		stored := fake.resolve(request)
		if stored == nil {
			writeJSON(t, writer, http.StatusNotFound, map[string]any{"detail": "not found"})
			return
		}
		var body map[string]json.RawMessage
		decodeJSON(t, request, &body)
		if _, present := body["description"]; !present {
			t.Errorf("update omitted the description key, so a removed description would never clear")
		}
		for key, raw := range body {
			var value any
			_ = json.Unmarshal(raw, &value)
			stored[key] = value
		}
		writeJSON(t, writer, http.StatusOK, map[string]any{"variable": publicVariableRow(stored)})
	})
	fake.handle("DELETE /api/v1/org/variables/{name}", func(writer http.ResponseWriter, request *http.Request) {
		name := request.PathValue("name")
		for _, scope := range []string{request.Header.Get("X-Ankra-Project-Id"), organisationWide} {
			if _, exists := fake.layer(scope)[name]; exists {
				delete(fake.layer(scope), name)
				writeJSON(t, writer, http.StatusOK, map[string]any{"deleted": true})
				return
			}
		}
		writeJSON(t, writer, http.StatusNotFound, map[string]any{"detail": "not found"})
	})
	return fake
}

func (fake *variableFake) layer(scope string) map[string]map[string]any {
	if fake.layers[scope] == nil {
		fake.layers[scope] = map[string]map[string]any{}
	}
	return fake.layers[scope]
}

func (fake *variableFake) resolve(request *http.Request) map[string]any {
	name := request.PathValue("name")
	if row, exists := fake.layer(request.Header.Get("X-Ankra-Project-Id"))[name]; exists {
		return row
	}
	return fake.layer(organisationWide)[name]
}

func publicVariableRow(stored map[string]any) map[string]any {
	row := map[string]any{}
	for key, value := range stored {
		row[key] = value
	}
	if row["is_secret"] == true {
		row["value"] = nil
	}
	return row
}

func TestAccOrganisationVariableLifecycle(t *testing.T) {
	fake := newVariableFake(t)

	withDescription := fake.providerBlock() + `
resource "ankra_organisation_variable" "region" {
  name        = "region"
  value       = "fsn1"
  description = "default region"
}

resource "ankra_organisation_variable" "key" {
  name      = "api_key"
  value     = "s3cret"
  is_secret = true
}

data "ankra_organisation_variables" "all" {
  depends_on = [ankra_organisation_variable.region, ankra_organisation_variable.key]
}
`
	withoutDescription := fake.providerBlock() + `
resource "ankra_organisation_variable" "region" {
  name  = "region"
  value = "nbg1"
}

resource "ankra_organisation_variable" "key" {
  name      = "api_key"
  value     = "s3cret"
  is_secret = true
}
`

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: withDescription,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("ankra_organisation_variable.region", "id"),
					resource.TestCheckResourceAttr("ankra_organisation_variable.region", "description", "default region"),
					resource.TestCheckResourceAttr("ankra_organisation_variable.region", "is_secret", "false"),
					// Neither create nor the listing returns the secret, so state keeps the configured value.
					resource.TestCheckResourceAttr("ankra_organisation_variable.key", "value", "s3cret"),
					resource.TestCheckResourceAttr("data.ankra_organisation_variables.all", "variables.#", "2"),
					resource.TestCheckNoResourceAttr("data.ankra_organisation_variables.all", "variables.0.value"),
					resource.TestCheckResourceAttr("data.ankra_organisation_variables.all", "variables.1.value", "fsn1"),
				),
			},
			{
				Config: withoutDescription,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ankra_organisation_variable.region", "value", "nbg1"),
					resource.TestCheckNoResourceAttr("ankra_organisation_variable.region", "description"),
					func(_ *terraform.State) error {
						if description := fake.layer(organisationWide)["region"]["description"]; description != nil {
							return fmt.Errorf("platform still holds description %v", description)
						}
						return nil
					},
				),
			},
			{
				ResourceName:      "ankra_organisation_variable.region",
				ImportState:       true,
				ImportStateId:     "region",
				ImportStateVerify: true,
			},
			{
				// Deleted outside Terraform: the next plan recreates it.
				PreConfig: func() { delete(fake.layer(organisationWide), "region") },
				Config:    withoutDescription,
				Check: func(_ *terraform.State) error {
					if _, exists := fake.layer(organisationWide)["region"]; !exists {
						return fmt.Errorf("variable deleted outside Terraform was not recreated")
					}
					return nil
				},
			},
		},
	})
}

// TestAccOrganisationVariableDoesNotAdoptAnotherRow pins the read guard: when
// the name Terraform manages now resolves to a different variable (here the
// project row was deleted and an organisation-wide row of the same name shows
// through), Read drops it from state and recreates the project row instead of
// adopting, overwriting and later deleting the organisation-wide one.
func TestAccOrganisationVariableDoesNotAdoptAnotherRow(t *testing.T) {
	fake := newVariableFake(t)
	config := fmt.Sprintf(`
provider "ankra" {
  token      = "test-token"
  base_url   = %q
  project_id = %q
}

resource "ankra_organisation_variable" "region" {
  name  = "region"
  value = "fsn1"
}
`, fake.server.URL, variableTestProject)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: config,
				Check: func(_ *terraform.State) error {
					if _, exists := fake.layer(variableTestProject)["region"]; !exists {
						return fmt.Errorf("variable was not created in the configured project")
					}
					return nil
				},
			},
			{
				PreConfig: func() {
					delete(fake.layer(variableTestProject), "region")
					fake.layer(organisationWide)["region"] = map[string]any{"id": "var-org-wide", "name": "region", "value": "portal-managed", "is_secret": false}
				},
				Config: config,
				Check: func(_ *terraform.State) error {
					if value := fake.layer(organisationWide)["region"]["value"]; value != "portal-managed" {
						return fmt.Errorf("organisation-wide variable was overwritten with %v", value)
					}
					if _, exists := fake.layer(variableTestProject)["region"]; !exists {
						return fmt.Errorf("project variable was not recreated")
					}
					return nil
				},
			},
		},
		CheckDestroy: func(_ *terraform.State) error {
			if _, exists := fake.layer(organisationWide)["region"]; !exists {
				return fmt.Errorf("destroy deleted the organisation-wide variable Terraform never managed")
			}
			return nil
		},
	})
}

func TestAccOrganisationVariableRejectsNamesAtPlan(t *testing.T) {
	fake := newRouteFake(t)

	for _, name := range []string{"llm_provider", "ai_default_agents_review", "team/region"} {
		resource.Test(t, resource.TestCase{
			ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config: fake.providerBlock() + fmt.Sprintf(`
resource "ankra_organisation_variable" "bad" {
  name  = %q
  value = "x"
}
`, name),
					PlanOnly:    true,
					ExpectError: regexp.MustCompile(`Reserved variable name|Invalid variable name`),
				},
			},
		})
	}
	if served := fake.served(); len(served) != 0 {
		t.Errorf("a refused name still reached the platform: %v", served)
	}
}

func TestAccOrganisationVariableMissingToken(t *testing.T) {
	t.Setenv("ANKRA_TOKEN", "")
	fake := newRouteFake(t)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: fmt.Sprintf(`
provider "ankra" {
  base_url = %q
}

resource "ankra_organisation_variable" "region" {
  name  = "region"
  value = "fsn1"
}
`, fake.server.URL),
				ExpectError: regexp.MustCompile("Missing API token"),
			},
		},
	})
	if served := fake.served(); len(served) != 0 {
		t.Errorf("sent %v without a token", served)
	}
}
