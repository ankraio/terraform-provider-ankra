// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"

	"ankra.io/terraform-provider-ankra/internal/client"
)

// registerProjects answers the project listing with the given slug -> ID
// pairs and counts how often it was asked.
func registerProjects(fake *routeFake, projects map[string]string, listings *int) {
	fake.handle("GET /api/v1/org/projects", func(writer http.ResponseWriter, request *http.Request) {
		if header := request.Header.Get("X-Ankra-Project-Id"); header != "" && !uuidPattern.MatchString(header) {
			fake.t.Errorf("the project listing carried a non-ID project header %q", header)
		}
		*listings++
		rows := []map[string]any{}
		for slug, identifier := range projects {
			rows = append(rows, map[string]any{"id": identifier, "slug": slug, "name": strings.ToUpper(slug), "is_default": false})
		}
		writeJSON(fake.t, writer, http.StatusOK, map[string]any{"projects": rows})
	})
}

func slugProviderBlock(fake *routeFake, project string) string {
	return fmt.Sprintf(`
provider "ankra" {
  token      = "test-token"
  base_url   = %q
  project_id = %q
}
`, fake.server.URL, project)
}

// TestAccProviderResolvesProjectSlug pins that a slug in project_id is
// resolved once, from the project listing, and that every other request
// carries the project's ID, never the slug.
func TestAccProviderResolvesProjectSlug(t *testing.T) {
	lanesFastPolling(t)
	fake := newLanesFake(t)
	listings := 0
	registerProjects(fake.routeFake, map[string]string{"platform": lanesTestProject, "other": lanesOtherTestProject}, &listings)

	configuration := slugProviderBlock(fake.routeFake, "platform") + `
resource "ankra_proxmox_cluster" "lab" {
  name                  = "tf-proxmox"
  credential_id         = "cred-1"
  ssh_key_credential_id = "ssh-1"
  node                  = "pve1"
  bridge                = "vmbr0"
}

data "ankra_clusters" "here" {
  depends_on = [ankra_proxmox_cluster.lab]
}
`
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: configuration,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ankra_proxmox_cluster.lab", "project_id", lanesTestProject),
					resource.TestCheckResourceAttr("data.ankra_clusters.here", "clusters.#", "1"),
					resource.TestCheckResourceAttr("data.ankra_clusters.here", "clusters.0.project_id", lanesTestProject),
					func(_ *terraform.State) error {
						if outside := fake.requestsOutsideProject(lanesTestProject); len(outside) != 0 {
							return fmt.Errorf("requests not scoped to the resolved project: %v", outside)
						}
						for _, cluster := range fake.clusters {
							if cluster.project != lanesTestProject {
								return fmt.Errorf("cluster created in project %q", cluster.project)
							}
						}
						return nil
					},
				),
			},
		},
	})
	if listings == 0 {
		t.Error("the slug was never resolved against the project listing")
	}
}

// TestAccProviderProjectSlugFailures pins that a slug that resolves to no
// project fails before any other request, so nothing is sent unscoped or
// with the slug as the project header.
func TestAccProviderProjectSlugFailures(t *testing.T) {
	cases := map[string]struct {
		projects map[string]string
		expected *regexp.Regexp
	}{
		"unknown slug":      {projects: map[string]string{"other": lanesOtherTestProject}, expected: regexp.MustCompile(`no\s+project\s+with\s+slug\s+"platform"`)},
		"projects disabled": {projects: nil, expected: regexp.MustCompile(`projects\s+are\s+not\s+enabled\s+for\s+this\s+organisation`)},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			fake := newRouteFake(t)
			listings := 0
			if testCase.projects != nil {
				registerProjects(fake, testCase.projects, &listings)
			}
			fake.handle("GET /api/v1/clusters", func(writer http.ResponseWriter, request *http.Request) {
				_, _ = writer.Write([]byte(writeClusterListing(map[string]string{"cl-1": "alpha"}, request.URL.Query())))
			})
			resource.Test(t, resource.TestCase{
				ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
				Steps: []resource.TestStep{
					{
						Config: slugProviderBlock(fake, "platform") + `
resource "ankra_organisation_variable" "region" {
  name  = "region"
  value = "fsn1"
}

data "ankra_clusters" "all" {}
`,
						ExpectError: testCase.expected,
					},
				},
			})
			for _, served := range fake.served() {
				if served != "GET /api/v1/org/projects" {
					t.Errorf("%s reached the platform although the project slug did not resolve", served)
				}
			}
		})
	}
}

// TestAccProviderRejectsInvalidProjectReference pins the configure-time
// check: a value that is neither an ID nor a slug sends nothing.
func TestAccProviderRejectsInvalidProjectReference(t *testing.T) {
	fake := newRouteFake(t)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:      slugProviderBlock(fake, "Platform Team") + `data "ankra_clusters" "all" {}`,
				ExpectError: regexp.MustCompile("Invalid project_id"),
			},
		},
	})
	if served := fake.served(); len(served) != 0 {
		t.Errorf("an invalid project reached the platform: %v", served)
	}
}

// TestAccClustersDataSourceProjectFilter pins that the data source's
// project_id lists another project's clusters, by ID or slug, while the
// provider stays scoped to its own.
func TestAccClustersDataSourceProjectFilter(t *testing.T) {
	fake := newRouteFake(t)
	listings := 0
	registerProjects(fake, map[string]string{"platform": lanesTestProject, "other": lanesOtherTestProject}, &listings)
	clustersByProject := map[string]map[string]string{
		lanesTestProject:      {"cl-1": "alpha"},
		lanesOtherTestProject: {"cl-2": "beta"},
	}
	fake.handle("GET /api/v1/clusters", func(writer http.ResponseWriter, request *http.Request) {
		_, _ = writer.Write([]byte(writeClusterListing(clustersByProject[request.Header.Get("X-Ankra-Project-Id")], request.URL.Query())))
	})

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: slugProviderBlock(fake, lanesTestProject) + fmt.Sprintf(`
data "ankra_clusters" "own" {}

data "ankra_clusters" "by_slug" {
  project_id = "other"
}

data "ankra_clusters" "by_id" {
  project_id = %q
}
`, lanesOtherTestProject),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.ankra_clusters.own", "clusters.0.name", "alpha"),
					resource.TestCheckResourceAttr("data.ankra_clusters.own", "clusters.0.project_id", lanesTestProject),
					resource.TestCheckResourceAttr("data.ankra_clusters.by_slug", "clusters.#", "1"),
					resource.TestCheckResourceAttr("data.ankra_clusters.by_slug", "clusters.0.name", "beta"),
					resource.TestCheckResourceAttr("data.ankra_clusters.by_slug", "clusters.0.project_id", lanesOtherTestProject),
					resource.TestCheckResourceAttr("data.ankra_clusters.by_id", "clusters.0.name", "beta"),
				),
			},
			{
				Config: slugProviderBlock(fake, lanesTestProject) + `
data "ankra_clusters" "missing" {
  project_id = "nowhere"
}
`,
				ExpectError: regexp.MustCompile(`no\s+project\s+with\s+slug\s+"nowhere"`),
			},
		},
	})
}

// TestAccHelmProjectID pins that a registry and a credential with their own
// project_id are created in that project (body and header) and read and
// deleted there, while ones without it keep the existing request, which
// carries no project_id key.
func TestAccHelmProjectID(t *testing.T) {
	fake := newHelmFake(t)
	listings := 0
	registerProjects(fake.routeFake, map[string]string{"platform": lanesTestProject, "other": lanesOtherTestProject}, &listings)
	configuration := slugProviderBlock(fake.routeFake, lanesTestProject) + `
resource "ankra_helm_registry_credential" "scoped" {
  name       = "scoped-pull"
  username   = "robot"
  password   = "secret"
  project_id = "other"
}

resource "ankra_helm_registry" "scoped" {
  name            = "scoped"
  url             = "https://charts.example.com"
  credential_name = ankra_helm_registry_credential.scoped.name
  project_id      = "other"
}

resource "ankra_helm_registry" "default" {
  name = "default"
  url  = "https://charts.example.org"
}
`
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy: func(_ *terraform.State) error {
			if len(fake.registries) != 0 || len(fake.credentials) != 0 {
				return fmt.Errorf("left after destroy: %v %v", fake.registries, fake.credentials)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: configuration,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ankra_helm_registry.scoped", "project_id", "other"),
					resource.TestCheckNoResourceAttr("ankra_helm_registry.default", "project_id"),
					func(_ *terraform.State) error {
						if got := fake.registryProjects["scoped"]; got != fmt.Sprintf("%q", lanesOtherTestProject) {
							return fmt.Errorf("scoped registry create sent project_id %s", got)
						}
						if got := fake.registryProjects["default"]; got != "absent" {
							return fmt.Errorf("an unscoped registry create sent project_id %s", got)
						}
						if got := fake.credentials["scoped-pull"]["project_id"]; got != lanesOtherTestProject {
							return fmt.Errorf("scoped credential create sent project_id %v", got)
						}
						fake.mutex.Lock()
						defer fake.mutex.Unlock()
						for index, served := range fake.requests {
							isScoped := strings.Contains(served, "/registries/scoped") || strings.Contains(served, "/credentials/scoped-pull")
							if isScoped && fake.projectHeaders[index] != lanesOtherTestProject {
								return fmt.Errorf("%s carried project %q", served, fake.projectHeaders[index])
							}
							if strings.Contains(served, "/registries/default") && fake.projectHeaders[index] != lanesTestProject {
								return fmt.Errorf("%s carried project %q", served, fake.projectHeaders[index])
							}
						}
						return nil
					},
				),
			},
			{
				Config:   configuration,
				PlanOnly: true,
			},
			{
				ResourceName:      "ankra_helm_registry.scoped",
				ImportState:       true,
				ImportStateId:     "other/scoped",
				ImportStateVerify: true,
			},
			{
				ResourceName:            "ankra_helm_registry_credential.scoped",
				ImportState:             true,
				ImportStateId:           "other/scoped-pull",
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"password"},
			},
			{
				// The same project written as its ID instead of its slug is an
				// in-place change, not a move.
				Config: helmProjectPattern.ReplaceAllString(configuration, fmt.Sprintf("project_id = %q", lanesOtherTestProject)),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("ankra_helm_registry.scoped", plancheck.ResourceActionUpdate),
						plancheck.ExpectResourceAction("ankra_helm_registry_credential.scoped", plancheck.ResourceActionUpdate),
					},
				},
				Check: resource.TestCheckResourceAttr("ankra_helm_registry.scoped", "project_id", lanesOtherTestProject),
			},
			{
				// Another project is a move, which replaces.
				Config: helmProjectPattern.ReplaceAllString(configuration, `project_id = "platform"`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("ankra_helm_registry.scoped", plancheck.ResourceActionDestroyBeforeCreate),
						plancheck.ExpectResourceAction("ankra_helm_registry_credential.scoped", plancheck.ResourceActionDestroyBeforeCreate),
					},
				},
			},
		},
	})
}

// helmProjectPattern matches the `project_id = "other"` lines of a Helm
// configuration, whatever their alignment.
var helmProjectPattern = regexp.MustCompile(`project_id\s+= "other"`)

// TestAccHelmImportBareNameKeepsWorking pins that a bare name still imports
// from the provider's project, with project_id null.
func TestAccHelmImportBareNameKeepsWorking(t *testing.T) {
	fake := newHelmFake(t)
	configuration := fake.providerBlock() + `
resource "ankra_helm_registry" "default" {
  name = "default"
  url  = "https://charts.example.org"
}
`
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{Config: configuration},
			{
				ResourceName:      "ankra_helm_registry.default",
				ImportState:       true,
				ImportStateId:     "default",
				ImportStateVerify: true,
			},
			{
				ResourceName:  "ankra_helm_registry.default",
				ImportState:   true,
				ImportStateId: "/default",
				ExpectError:   regexp.MustCompile("Invalid import ID"),
			},
		},
	})
}

// TestAccLaneClusterUpdateWithNullProjectID pins that an in-place change to
// a cluster whose project_id is null (no provider project_id, or a cluster
// created before 0.4.0) plans the null from state rather than an unknown,
// which the Update methods, never reading the project back, would write to
// state and fail the apply with. UseStateForUnknown carries a null state
// value over; this pins it.
func TestAccLaneClusterUpdateWithNullProjectID(t *testing.T) {
	lanesFastPolling(t)
	fake := newLanesFake(t)
	configuration := func(forceDestroy bool) string {
		return fake.providerBlock() + fmt.Sprintf(`
resource "ankra_proxmox_cluster" "lab" {
  name                  = "tf-proxmox"
  credential_id         = "cred-1"
  ssh_key_credential_id = "ssh-1"
  node                  = "pve1"
  bridge                = "vmbr0"
  force_destroy         = %t
}
`, forceDestroy)
	}
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: configuration(true),
				Check:  resource.TestCheckNoResourceAttr("ankra_proxmox_cluster.lab", "project_id"),
			},
			{
				Config: configuration(false),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("ankra_proxmox_cluster.lab", plancheck.ResourceActionUpdate),
						plancheck.ExpectKnownValue("ankra_proxmox_cluster.lab", tfjsonpath.New("project_id"), knownvalue.Null()),
					},
				},
				Check: resource.TestCheckResourceAttr("ankra_proxmox_cluster.lab", "force_destroy", "false"),
			},
		},
	})
}

// TestProjectSlugLookupRetriesAfterFailure pins that a failed project
// listing is not cached: the next request resolves the slug, and later
// lookups, including InProject with surrounding whitespace, reuse the cache.
func TestProjectSlugLookupRetriesAfterFailure(t *testing.T) {
	fake := newRouteFake(t)
	listings := 0
	isFailing := true
	fake.handle("GET /api/v1/org/projects", func(writer http.ResponseWriter, _ *http.Request) {
		listings++
		if isFailing {
			writeJSON(t, writer, http.StatusInternalServerError, map[string]any{"detail": "Internal server error"})
			return
		}
		writeJSON(t, writer, http.StatusOK, map[string]any{"projects": []map[string]any{
			{"id": lanesTestProject, "slug": "platform"},
			{"id": lanesOtherTestProject, "slug": "other"},
		}})
	})
	apiClient := client.NewClient(fake.server.URL, "test-token", "test")
	apiClient.ProjectSlug = "platform"

	if err := apiClient.ResolveProject(context.Background()); err == nil {
		t.Fatal("ResolveProject succeeded while the listing failed")
	}
	isFailing = false
	if err := apiClient.ResolveProject(context.Background()); err != nil {
		t.Fatalf("ResolveProject did not retry after a failed listing: %v", err)
	}
	if got := apiClient.ScopeProjectID(); got != lanesTestProject {
		t.Errorf("ScopeProjectID = %q, want %q", got, lanesTestProject)
	}
	scoped, err := apiClient.InProject(context.Background(), "  other ")
	if err != nil {
		t.Fatalf("InProject: %v", err)
	}
	if scoped.ProjectID != lanesOtherTestProject {
		t.Errorf("InProject resolved %q, want %q", scoped.ProjectID, lanesOtherTestProject)
	}
	if listings != 2 {
		t.Errorf("listed projects %d times, want 2 (one failure, then one cached listing)", listings)
	}
}
