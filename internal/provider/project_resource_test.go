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
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

const projectTestDefaultID = "0d0d0d0d-0000-0000-0000-000000000001"

// projectFake holds the organisation's projects the way the bearer project
// routes do (go/internal/projectsapi, usecase/projects): the listing and the
// detail carry the default project, create answers 201 and refuses a taken
// slug with 409, a rename trims nothing the provider sends, delete refuses
// the default project and a project that still holds resources (409), and an
// unknown id is a 404 "Project not found". With isDisabled set every route
// answers the projects-off 404.
type projectFake struct {
	*routeFake
	projects   map[string]map[string]any
	holding    map[string]bool
	patches    []map[string]any
	isDisabled bool
	nextID     int
}

func newProjectFake(t *testing.T) *projectFake {
	fake := &projectFake{routeFake: newRouteFake(t), projects: map[string]map[string]any{}, holding: map[string]bool{}}
	fake.projects[projectTestDefaultID] = map[string]any{
		"id": projectTestDefaultID, "name": "Default", "slug": "default", "description": "",
		"is_default": true, "is_current": true, "created_at": "2026-10-28T15:39:00Z",
	}
	disabled := func(writer http.ResponseWriter) bool {
		if fake.isDisabled {
			writeJSON(t, writer, http.StatusNotFound, map[string]any{"detail": "Projects are not enabled for this organisation"})
		}
		return fake.isDisabled
	}
	notFound := func(writer http.ResponseWriter) {
		writeJSON(t, writer, http.StatusNotFound, map[string]any{"detail": "Project not found"})
	}

	fake.handle("GET /api/v1/org/projects", func(writer http.ResponseWriter, _ *http.Request) {
		if disabled(writer) {
			return
		}
		identifiers := make([]string, 0, len(fake.projects))
		for identifier := range fake.projects {
			identifiers = append(identifiers, identifier)
		}
		sort.Strings(identifiers)
		listing := []map[string]any{}
		for _, identifier := range identifiers {
			listing = append(listing, fake.projects[identifier])
		}
		writeJSON(t, writer, http.StatusOK, map[string]any{"projects": listing})
	})
	fake.handle("POST /api/v1/org/projects", func(writer http.ResponseWriter, request *http.Request) {
		if disabled(writer) {
			return
		}
		var body map[string]any
		decodeJSON(t, request, &body)
		for _, row := range fake.projects {
			if row["slug"] == body["slug"] {
				writeJSON(t, writer, http.StatusConflict, map[string]any{"detail": fmt.Sprintf("A project with slug '%v' already exists", body["slug"])})
				return
			}
		}
		fake.nextID++
		identifier := fmt.Sprintf("0d0d0d0d-0000-0000-0000-%012d", fake.nextID+1)
		row := map[string]any{
			"id": identifier, "name": strings.TrimSpace(fmt.Sprint(body["name"])), "slug": body["slug"], "description": body["description"],
			"is_default": false, "is_current": false, "created_at": "2026-11-08T10:00:00Z",
		}
		fake.projects[identifier] = row
		writeJSON(t, writer, http.StatusCreated, row)
	})
	fake.handle("GET /api/v1/org/projects/{project_id}", func(writer http.ResponseWriter, request *http.Request) {
		if disabled(writer) {
			return
		}
		row, exists := fake.projects[request.PathValue("project_id")]
		if !exists {
			notFound(writer)
			return
		}
		writeJSON(t, writer, http.StatusOK, row)
	})
	fake.handle("PATCH /api/v1/org/projects/{project_id}", func(writer http.ResponseWriter, request *http.Request) {
		if disabled(writer) {
			return
		}
		row, exists := fake.projects[request.PathValue("project_id")]
		if !exists {
			notFound(writer)
			return
		}
		var body map[string]any
		decodeJSON(t, request, &body)
		fake.patches = append(fake.patches, body)
		for _, key := range []string{"name", "description"} {
			if value, present := body[key]; present {
				row[key] = value
			}
		}
		writeJSON(t, writer, http.StatusOK, row)
	})
	fake.handle("DELETE /api/v1/org/projects/{project_id}", func(writer http.ResponseWriter, request *http.Request) {
		if disabled(writer) {
			return
		}
		identifier := request.PathValue("project_id")
		row, exists := fake.projects[identifier]
		switch {
		case !exists:
			notFound(writer)
		case row["is_default"] == true:
			writeJSON(t, writer, http.StatusConflict, map[string]any{"detail": "The default project cannot be deleted"})
		case fake.holding[identifier]:
			writeJSON(t, writer, http.StatusConflict, map[string]any{"detail": "Project still holds resources: 1 cluster"})
		default:
			delete(fake.projects, identifier)
			writeJSON(t, writer, http.StatusOK, map[string]any{"success": true, "retired_assignments": 0, "repointed_members": 0, "pinned_tokens": 0})
		}
	})
	return fake
}

func (fake *projectFake) idForSlug(slug string) string {
	for identifier, row := range fake.projects {
		if row["slug"] == slug {
			return identifier
		}
	}
	return ""
}

// TestAccProjectLifecycle creates a project, renames it and changes its
// description in place, clears a description set outside Terraform, replaces
// it on a slug change and imports it by slug. The organisation holds two
// projects from the first apply on and the provider sets no project_id: the
// project resource and data sources must not demand one.
func TestAccProjectLifecycle(t *testing.T) {
	fake := newProjectFake(t)
	config := func(name, slug, description string) string {
		descriptionLine := ""
		if description != "" {
			descriptionLine = fmt.Sprintf("  description = %q\n", description)
		}
		return fake.providerBlock() + fmt.Sprintf(`
resource "ankra_project" "platform" {
  name = %q
  slug = %q
%s}
`, name, slug, descriptionLine)
	}
	var firstID string

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy: func(_ *terraform.State) error {
			if len(fake.projects) != 1 || fake.projects[projectTestDefaultID] == nil {
				return fmt.Errorf("destroy must delete the managed project and keep the default one: %v", fake.projects)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: config("Platform team", "platform", "Shared clusters"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ankra_project.platform", "slug", "platform"),
					resource.TestCheckResourceAttr("ankra_project.platform", "description", "Shared clusters"),
					resource.TestCheckResourceAttr("ankra_project.platform", "is_default", "false"),
					resource.TestCheckResourceAttr("ankra_project.platform", "created_at", "2026-11-08T10:00:00Z"),
					func(state *terraform.State) error {
						firstID = state.RootModule().Resources["ankra_project.platform"].Primary.ID
						if firstID != fake.idForSlug("platform") {
							return fmt.Errorf("state id %q is not the created project", firstID)
						}
						return nil
					},
				),
			},
			{
				Config: config("Platform engineering", "platform", ""),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("ankra_project.platform", plancheck.ResourceActionUpdate)},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ankra_project.platform", "name", "Platform engineering"),
					resource.TestCheckNoResourceAttr("ankra_project.platform", "description"),
					func(_ *terraform.State) error {
						row := fake.projects[firstID]
						if row["name"] != "Platform engineering" || row["description"] != "" {
							return fmt.Errorf("the rename must clear the description it no longer configures: %v", row)
						}
						return nil
					},
				),
			},
			{
				// A description set in the portal is drift, cleared in place.
				PreConfig: func() { fake.projects[firstID]["description"] = "set in the portal" },
				Config:    config("Platform engineering", "platform", ""),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("ankra_project.platform", plancheck.ResourceActionUpdate)},
				},
				Check: func(_ *terraform.State) error {
					if fake.projects[firstID]["description"] != "" {
						return fmt.Errorf("drift was not cleared: %v", fake.projects[firstID])
					}
					return nil
				},
			},
			{
				Config: config("Platform engineering", "platform-eng", ""),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("ankra_project.platform", plancheck.ResourceActionDestroyBeforeCreate)},
				},
				Check: func(_ *terraform.State) error {
					if _, stillThere := fake.projects[firstID]; stillThere || fake.idForSlug("platform-eng") == "" {
						return fmt.Errorf("a slug change must replace the project: %v", fake.projects)
					}
					return nil
				},
			},
			{
				ResourceName:      "ankra_project.platform",
				ImportState:       true,
				ImportStateId:     "platform-eng",
				ImportStateVerify: true,
			},
			{
				Config: config("Platform engineering", "platform-eng", "") + `
data "ankra_projects" "all" {}

data "ankra_project" "default" {
  project = "default"
}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.ankra_projects.all", "projects.#", "2"),
					resource.TestCheckResourceAttr("data.ankra_project.default", "id", projectTestDefaultID),
					resource.TestCheckResourceAttr("data.ankra_project.default", "is_default", "true"),
					resource.TestCheckResourceAttr("data.ankra_project.default", "name", "Default"),
				),
			},
		},
	})
}

// TestAccProjectDeletedOutsideTerraform plans the project's recreation when
// it is gone, and a refused delete (the project still holds resources) fails
// the destroy and keeps the project in state.
func TestAccProjectDeletedOutsideTerraform(t *testing.T) {
	fake := newProjectFake(t)
	config := fake.providerBlock() + `
resource "ankra_project" "team" {
  name = "Team"
  slug = "team"
}
`
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{Config: config},
			{
				PreConfig: func() { delete(fake.projects, fake.idForSlug("team")) },
				Config:    config,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("ankra_project.team", plancheck.ResourceActionCreate)},
				},
			},
			{
				PreConfig:   func() { fake.holding[fake.idForSlug("team")] = true },
				Config:      config,
				Destroy:     true,
				ExpectError: regexp.MustCompile("Unable to delete project"),
			},
			{
				PreConfig: func() { fake.holding = map[string]bool{} },
				Config:    config,
			},
		},
	})
}

// TestAccProjectsDisabled reports projects being off instead of planning the
// project's recreation.
func TestAccProjectsDisabled(t *testing.T) {
	fake := newProjectFake(t)
	config := fake.providerBlock() + `
resource "ankra_project" "team" {
  name = "Team"
  slug = "team"
}
`
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{Config: config},
			{
				PreConfig:   func() { fake.isDisabled = true },
				Config:      config,
				ExpectError: regexp.MustCompile("Projects are not enabled"),
			},
			{
				PreConfig: func() { fake.isDisabled = false },
				Config:    config,
			},
		},
	})
}

func TestAccProjectValidation(t *testing.T) {
	fake := newRouteFake(t)
	for _, testCase := range []struct {
		name     string
		slug     string
		expected string
	}{
		{name: "Default", slug: "default", expected: "Reserved project slug"},
		{name: "Upper", slug: "Platform", expected: "Invalid project slug"},
		{name: "Uuid", slug: "0d0d0d0d-0000-0000-0000-000000000009", expected: "Invalid project slug"},
		{name: " Padded ", slug: "padded", expected: "Invalid project name"},
	} {
		t.Run(testCase.slug, func(t *testing.T) {
			resource.Test(t, resource.TestCase{
				ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
				Steps: []resource.TestStep{
					{
						Config: fake.providerBlock() + fmt.Sprintf(`
resource "ankra_project" "bad" {
  name = %q
  slug = %q
}
`, testCase.name, testCase.slug),
						PlanOnly:    true,
						ExpectError: regexp.MustCompile(testCase.expected),
					},
				},
			})
		})
	}
}
