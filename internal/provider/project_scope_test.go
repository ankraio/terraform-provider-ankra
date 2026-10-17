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
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

const otherVariableTestProject = "99999999-8888-7777-6666-555555555555"

// TestAccFloatingProjectScopeIsRefused pins the guard: an organisation with
// several projects and no provider project_id would have every request follow
// the user's portal selection, so the new resources refuse before sending any
// write.
func TestAccFloatingProjectScopeIsRefused(t *testing.T) {
	fake := newVariableFake(t)
	fake.handle("GET /api/v1/org/projects", func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(t, writer, http.StatusOK, map[string]any{"projects": []map[string]any{{"id": variableTestProject}, {"id": otherVariableTestProject}}})
	})

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: fake.providerBlock() + `
resource "ankra_organisation_variable" "region" {
  name  = "region"
  value = "fsn1"
}
`,
				ExpectError: regexp.MustCompile(`Provider project_id is required`),
			},
		},
	})
	for _, served := range fake.served() {
		if !strings.HasPrefix(served, "GET ") {
			t.Errorf("a write reached the platform under a floating scope: %s", served)
		}
	}
}

// TestAccScopeMoveKeepsTheResource pins that a 404 caused by moving the
// provider to another project neither plans a recreation nor lets destroy
// report success: the variable lives on in the old project.
func TestAccScopeMoveKeepsTheResource(t *testing.T) {
	fake := newVariableFake(t)
	inProject := func(project string) string {
		return fmt.Sprintf(`
provider "ankra" {
  token      = "test-token"
  base_url   = %q
  project_id = %q
}

resource "ankra_organisation_variable" "region" {
  name  = "region"
  value = "fsn1"
}
`, fake.server.URL, project)
	}

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{Config: inProject(variableTestProject)},
			{
				Config:      inProject(otherVariableTestProject),
				ExpectError: regexp.MustCompile(`Object not visible in the provider's current scope`),
			},
			{
				Config: inProject(variableTestProject),
				Check: func(_ *terraform.State) error {
					if len(fake.layer(otherVariableTestProject)) != 0 {
						return fmt.Errorf("a second variable was created in the new project")
					}
					if _, exists := fake.layer(variableTestProject)["region"]; !exists {
						return fmt.Errorf("the variable in the original project is gone")
					}
					return nil
				},
			},
		},
	})
}
