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

const organisationRoleFakeID = "7a1e0c2b-3d4f-4a5b-8c6d-9e0f1a2b3c4d"

// organisationRoleFake mirrors the role routes (go/internal/iamapi): the
// listing carries the built-in roles beside the custom ones, PATCH replaces
// a custom role's fields, and DELETE refuses a role someone still holds.
type organisationRoleFake struct {
	*routeFake
	role    map[string]any
	holders int
	denied  bool
}

func newOrganisationRoleFake(t *testing.T) *organisationRoleFake {
	fake := &organisationRoleFake{routeFake: newRouteFake(t)}
	fake.handle("GET /api/v1/org/organisation/roles", func(writer http.ResponseWriter, _ *http.Request) {
		roles := []any{map[string]any{"slug": "member", "name": "Member", "description": "", "permissions": []string{"clusters.read"}, "builtin": true}}
		if fake.role != nil {
			roles = append(roles, fake.role)
		}
		writeJSON(t, writer, http.StatusOK, map[string]any{"roles": roles})
	})
	fake.handle("POST /api/v1/org/organisation/roles", func(writer http.ResponseWriter, request *http.Request) {
		if fake.denied {
			writeJSON(t, writer, http.StatusForbidden, map[string]any{"detail": "members.manage is required"})
			return
		}
		var body map[string]any
		decodeJSON(t, request, &body)
		fake.role = map[string]any{
			"id": organisationRoleFakeID, "slug": body["slug"], "name": body["name"], "description": body["description"],
			"permissions": body["permissions"], "kube_grants": body["kube_grants"], "builtin": false,
		}
		writeJSON(t, writer, http.StatusOK, fake.role)
	})
	fake.handle("PATCH /api/v1/org/organisation/roles/{id}", func(writer http.ResponseWriter, request *http.Request) {
		var body map[string]any
		decodeJSON(t, request, &body)
		if _, hasSlug := body["slug"]; hasSlug {
			t.Errorf("the update must not send the slug")
		}
		for _, key := range []string{"name", "description", "permissions", "kube_grants"} {
			fake.role[key] = body[key]
		}
		writeJSON(t, writer, http.StatusOK, fake.role)
	})
	fake.handle("DELETE /api/v1/org/organisation/roles/{id}", func(writer http.ResponseWriter, _ *http.Request) {
		if fake.holders > 0 {
			writeJSON(t, writer, http.StatusConflict, map[string]any{"detail": "1 person still holds Deployer. Give them another role first.", "reason": "role_in_use"})
			return
		}
		fake.role = nil
		writeJSON(t, writer, http.StatusOK, map[string]any{"success": true})
	})
	return fake
}

func organisationRoleConfig(fake *organisationRoleFake, name, permissions, grants string) string {
	return fake.providerBlock() + fmt.Sprintf(`
resource "ankra_organisation_role" "deployer" {
  slug        = "deployer"
  name        = %q
  permissions = %s
%s
}
`, name, permissions, grants)
}

func TestAccOrganisationRoleLifecycle(t *testing.T) {
	fake := newOrganisationRoleFake(t)
	grants := `  kube_grants = [{ scope = "namespace", namespace = "apps", k8s_role = "edit" }]`
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy: func(_ *terraform.State) error {
			if fake.role != nil {
				return fmt.Errorf("destroy must delete the role")
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: organisationRoleConfig(fake, "Deployer", `["clusters.read"]`, ""),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ankra_organisation_role.deployer", "id", organisationRoleFakeID),
					resource.TestCheckResourceAttr("ankra_organisation_role.deployer", "description", ""),
					resource.TestCheckNoResourceAttr("ankra_organisation_role.deployer", "kube_grants"),
				),
			},
			{
				Config: organisationRoleConfig(fake, "Deployers", `["clusters.read", "applications.write"]`, grants),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ankra_organisation_role.deployer", "name", "Deployers"),
					resource.TestCheckResourceAttr("ankra_organisation_role.deployer", "permissions.#", "2"),
					resource.TestCheckResourceAttr("ankra_organisation_role.deployer", "kube_grants.0.namespace", "apps"),
				),
			},
			{
				ResourceName:      "ankra_organisation_role.deployer",
				ImportState:       true,
				ImportStateId:     "deployer",
				ImportStateVerify: true,
			},
		},
	})
}

func TestAccOrganisationRoleRefusals(t *testing.T) {
	fake := newOrganisationRoleFake(t)
	fake.denied = true
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:      organisationRoleConfig(fake, "Deployer", `["clusters.read"]`, ""),
				ExpectError: regexp.MustCompile(`(?s)Unable to create the organisation role.*members.manage is required`),
			},
			{
				Config:      organisationRoleConfig(fake, "Deployer", `["clusters.read"]`, `  kube_grants = [{ scope = "everything", k8s_role = "edit" }]`),
				ExpectError: regexp.MustCompile(`Invalid value`),
			},
			{
				PreConfig: func() { fake.denied = false },
				Config:    organisationRoleConfig(fake, "Deployer", `["clusters.read"]`, ""),
			},
			{
				PreConfig:   func() { fake.holders = 1 },
				Config:      fake.providerBlock(),
				ExpectError: regexp.MustCompile(`(?s)Unable to delete the organisation role.*role_in_use`),
			},
			{
				PreConfig: func() { fake.holders = 0 },
				Config:    fake.providerBlock(),
			},
		},
	})
}
