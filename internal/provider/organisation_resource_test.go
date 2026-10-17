// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

const (
	organisationTestCurrent = "aaaaaaaa-0000-0000-0000-000000000001"
	organisationTestCaller  = "cccccccc-0000-0000-0000-000000000001"
)

// organisationFake holds organisations the way the token surface does
// (go/internal/cliapi/organisation.go): create answers 201 and makes the
// caller an admin, a detail read of an organisation the caller is not in
// answers 404, an invite answers 200 with the pending roster row (400 when
// the invitee is already active, 422 for a role outside the literal), and
// DELETE /user takes a JSON body and answers 404 when the membership is gone.
type organisationFake struct {
	*routeFake
	organisations map[string]*organisationFakeRow
	invites       []map[string]any
	removals      []map[string]any
	nextID        int
}

type organisationFakeRow struct {
	name    string
	slug    string
	members []map[string]any
}

func newOrganisationFake(t *testing.T) *organisationFake {
	fake := &organisationFake{routeFake: newRouteFake(t), organisations: map[string]*organisationFakeRow{}}
	fake.organisations[organisationTestCurrent] = &organisationFakeRow{
		name: "Current", slug: "current",
		members: []map[string]any{{"id": organisationTestCaller, "invite_id": "row-0", "email": "owner@example.com", "status": "active", "role": "admin"}},
	}

	fake.handle("GET /api/v1/org/organisation", func(writer http.ResponseWriter, _ *http.Request) {
		identifiers := make([]string, 0, len(fake.organisations))
		for identifier := range fake.organisations {
			identifiers = append(identifiers, identifier)
		}
		sort.Strings(identifiers)
		listing := []map[string]any{}
		for _, identifier := range identifiers {
			row := fake.organisations[identifier]
			listing = append(listing, map[string]any{
				"organisation_id": identifier, "name": row.name, "slug": row.slug,
				"user_current": identifier == organisationTestCurrent, "status": "active", "role": "admin",
			})
		}
		writeJSON(t, writer, http.StatusOK, listing)
	})
	fake.handle("POST /api/v1/org/organisation", func(writer http.ResponseWriter, request *http.Request) {
		var body map[string]any
		decodeJSON(t, request, &body)
		name, _ := body["name"].(string)
		fake.nextID++
		identifier := fmt.Sprintf("bbbbbbbb-0000-0000-0000-%012d", fake.nextID)
		slug := strings.ToLower(strings.ReplaceAll(name, " ", "-"))
		fake.organisations[identifier] = &organisationFakeRow{
			name: name, slug: slug,
			members: []map[string]any{{"id": organisationTestCaller, "invite_id": "row-x", "email": "owner@example.com", "status": "active", "role": "admin"}},
		}
		writeJSON(t, writer, http.StatusCreated, map[string]any{"organisation_id": identifier, "slug": slug, "message": "created"})
	})
	fake.handle("GET /api/v1/org/organisation/{organisation_id}", func(writer http.ResponseWriter, request *http.Request) {
		row, exists := fake.organisations[request.PathValue("organisation_id")]
		if !exists {
			writeJSON(t, writer, http.StatusNotFound, map[string]any{"detail": "Organisation not found"})
			return
		}
		writeJSON(t, writer, http.StatusOK, map[string]any{
			"organisation_id": request.PathValue("organisation_id"), "name": row.name, "slug": row.slug,
			"created_at": "2026-10-01T10:00:00.000000+00:00", "users": row.members, "mfa_required": false,
		})
	})
	fake.handle("POST /api/v1/org/organisation/invite", func(writer http.ResponseWriter, request *http.Request) {
		var body map[string]any
		decodeJSON(t, request, &body)
		fake.invites = append(fake.invites, body)
		role, _ := body["role"].(string)
		if role != "member" && role != "admin" && role != "read-only" {
			writeJSON(t, writer, http.StatusUnprocessableEntity, map[string]any{"detail": []map[string]any{{"loc": []string{"body", "role"}, "msg": "bad role"}}})
			return
		}
		row, exists := fake.organisations[fmt.Sprint(body["organisation_id"])]
		if !exists {
			writeJSON(t, writer, http.StatusForbidden, map[string]any{"detail": "Only organisation admins may invite new users"})
			return
		}
		email := fmt.Sprint(body["invitee_email"])
		for _, member := range row.members {
			if member["email"] == email && member["status"] == "active" {
				writeJSON(t, writer, http.StatusBadRequest, map[string]any{"detail": email + " is already a member of this organisation."})
				return
			}
		}
		fake.nextID++
		pending := map[string]any{
			"id": fmt.Sprintf("dddddddd-0000-0000-0000-%012d", fake.nextID), "invite_id": fmt.Sprintf("row-%d", fake.nextID),
			"email": email, "status": "pending", "role": role, "user_current": false,
		}
		row.members = append(row.members, pending)
		writeJSON(t, writer, http.StatusOK, map[string]any{"success": true, "message": "Invite sent", "pending_user": pending})
	})
	fake.handle("DELETE /api/v1/org/organisation/user", func(writer http.ResponseWriter, request *http.Request) {
		var body map[string]any
		decodeJSON(t, request, &body)
		fake.removals = append(fake.removals, body)
		row, exists := fake.organisations[fmt.Sprint(body["organisation_id"])]
		if exists {
			for index, member := range row.members {
				if member["id"] == body["user_id"] {
					row.members = append(row.members[:index], row.members[index+1:]...)
					writeJSON(t, writer, http.StatusOK, map[string]any{"success": true, "message": "removed"})
					return
				}
			}
		}
		writeJSON(t, writer, http.StatusNotFound, map[string]any{"detail": "Membership not found."})
	})
	return fake
}

func (fake *organisationFake) memberByEmail(organisationID, email string) map[string]any {
	row, exists := fake.organisations[organisationID]
	if !exists {
		return nil
	}
	for _, member := range row.members {
		if member["email"] == email {
			return member
		}
	}
	return nil
}

func TestAccOrganisationLifecycle(t *testing.T) {
	fake := newOrganisationFake(t)
	config := func(name string) string {
		return fake.providerBlock() + fmt.Sprintf(`
resource "ankra_organisation" "team" {
  name    = %q
  country = "SE"
}
`, name)
	}
	var createdID string

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy: func(_ *terraform.State) error {
			if _, exists := fake.organisations[createdID]; !exists {
				return fmt.Errorf("destroy deleted organisation %s; the API cannot, so destroy must only forget it", createdID)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: config("Platform Team"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ankra_organisation.team", "slug", "platform-team"),
					func(state *terraform.State) error {
						createdID = state.RootModule().Resources["ankra_organisation.team"].Primary.ID
						return nil
					},
				),
			},
			{
				Config:      config("Renamed Team"),
				ExpectError: regexp.MustCompile("Organisation cannot be renamed"),
			},
			{
				ResourceName:            "ankra_organisation.team",
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"country"},
			},
			{
				PreConfig: func() { delete(fake.organisations, createdID) },
				Config:    config("Platform Team"),
				Check: func(state *terraform.State) error {
					recreated := state.RootModule().Resources["ankra_organisation.team"].Primary.ID
					if recreated == createdID {
						return fmt.Errorf("organisation gone outside Terraform was not recreated")
					}
					createdID = recreated
					return nil
				},
			},
		},
	})
}

func TestAccOrganisationMemberLifecycle(t *testing.T) {
	fake := newOrganisationFake(t)
	config := func(role string) string {
		return fake.providerBlock() + fmt.Sprintf(`
resource "ankra_organisation_member" "alice" {
  email = "alice@example.com"
  role  = %q
}

data "ankra_organisation" "current" {
  depends_on = [ankra_organisation_member.alice]
}

data "ankra_organisations" "all" {}
`, role)
	}

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy: func(_ *terraform.State) error {
			if member := fake.memberByEmail(organisationTestCurrent, "alice@example.com"); member != nil {
				return fmt.Errorf("member still in the organisation after destroy: %v", member)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: config("member"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ankra_organisation_member.alice", "organisation_id", organisationTestCurrent),
					resource.TestCheckResourceAttr("ankra_organisation_member.alice", "status", "pending"),
					resource.TestCheckResourceAttr("data.ankra_organisation.current", "id", organisationTestCurrent),
					resource.TestCheckResourceAttr("data.ankra_organisation.current", "members.#", "2"),
					resource.TestCheckResourceAttr("data.ankra_organisation.current", "members.1.email", "alice@example.com"),
					resource.TestCheckResourceAttr("data.ankra_organisations.all", "organisations.0.user_current", "true"),
					func(_ *terraform.State) error {
						invite := fake.invites[0]
						if invite["organisation_id"] != organisationTestCurrent || invite["invitee_email"] != "alice@example.com" || invite["role"] != "member" {
							return fmt.Errorf("invite body %v does not match the backend contract", invite)
						}
						return nil
					},
				),
			},
			{
				Config: config("admin"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ankra_organisation_member.alice", "role", "admin"),
					func(_ *terraform.State) error {
						if len(fake.removals) != 1 || fake.removals[0]["organisation_id"] != organisationTestCurrent {
							return fmt.Errorf("a role change must remove and re-invite; removals %v", fake.removals)
						}
						return nil
					},
				),
			},
			{
				Config:      config("owner"),
				ExpectError: regexp.MustCompile(`"owner" is not accepted`),
			},
			{
				ResourceName:      "ankra_organisation_member.alice",
				ImportState:       true,
				ImportStateIdFunc: organisationMemberImportID,
				ImportStateVerify: true,
			},
			{
				ResourceName:  "ankra_organisation_member.alice",
				ImportState:   true,
				ImportStateId: "missing-slash",
				ExpectError:   regexp.MustCompile("expected an import id of the form"),
			},
			{
				PreConfig: func() {
					row := fake.organisations[organisationTestCurrent]
					row.members = row.members[:1]
				},
				Config: config("admin"),
				Check: func(_ *terraform.State) error {
					if fake.memberByEmail(organisationTestCurrent, "alice@example.com") == nil {
						return fmt.Errorf("member removed outside Terraform was not invited again")
					}
					return nil
				},
			},
		},
	})
}

func organisationMemberImportID(state *terraform.State) (string, error) {
	attributes := state.RootModule().Resources["ankra_organisation_member.alice"].Primary.Attributes
	return attributes["organisation_id"] + "/" + attributes["id"], nil
}
