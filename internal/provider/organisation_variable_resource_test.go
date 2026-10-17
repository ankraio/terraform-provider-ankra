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

// variableFake holds organisation variables the way the platform does: the
// listing hides secret values, and an update's explicit null description
// clears the stored one.
type variableFake struct {
	*routeFake
	variables map[string]map[string]any
}

func newVariableFake(t *testing.T) *variableFake {
	fake := &variableFake{routeFake: newRouteFake(t), variables: map[string]map[string]any{}}
	created := 0

	fake.handle("GET /api/v1/org/variables", func(writer http.ResponseWriter, _ *http.Request) {
		names := make([]string, 0, len(fake.variables))
		for name := range fake.variables {
			names = append(names, name)
		}
		sort.Strings(names)
		listing := make([]map[string]any, 0, len(names))
		for _, name := range names {
			row := map[string]any{}
			for key, value := range fake.variables[name] {
				row[key] = value
			}
			if row["is_secret"] == true {
				row["value"] = nil
			}
			listing = append(listing, row)
		}
		writeJSON(t, writer, http.StatusOK, map[string]any{"variables": listing})
	})
	fake.handle("POST /api/v1/org/variables", func(writer http.ResponseWriter, request *http.Request) {
		var body map[string]any
		decodeJSON(t, request, &body)
		name, _ := body["name"].(string)
		if _, exists := fake.variables[name]; exists {
			writeJSON(t, writer, http.StatusConflict, map[string]any{"detail": "exists"})
			return
		}
		created++
		body["id"] = fmt.Sprintf("var-%d", created)
		fake.variables[name] = body
		writeJSON(t, writer, http.StatusCreated, map[string]any{"variable": body})
	})
	fake.handle("PUT /api/v1/org/variables/{name}", func(writer http.ResponseWriter, request *http.Request) {
		stored, exists := fake.variables[request.PathValue("name")]
		if !exists {
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
		writeJSON(t, writer, http.StatusOK, map[string]any{"variable": stored})
	})
	fake.handle("DELETE /api/v1/org/variables/{name}", func(writer http.ResponseWriter, request *http.Request) {
		delete(fake.variables, request.PathValue("name"))
		writer.WriteHeader(http.StatusNoContent)
	})
	return fake
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
					// The listing hides the secret, so state keeps the configured value.
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
						if description := fake.variables["region"]["description"]; description != nil {
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
				PreConfig: func() { delete(fake.variables, "region") },
				Config:    withoutDescription,
				Check: func(_ *terraform.State) error {
					if _, exists := fake.variables["region"]; !exists {
						return fmt.Errorf("variable deleted outside Terraform was not recreated")
					}
					return nil
				},
			},
		},
	})
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
