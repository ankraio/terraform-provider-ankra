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

// helmFake holds Helm registries, registry credentials and the chart
// catalogue the way the platform does (go/internal/helmapi and
// usecase/helmcatalog): a refused registry or credential create still answers
// 200 with an errors list, the stored URL loses its trailing slash, a new
// registry syncs every 120 seconds, the registry detail carries no
// identifier, the credential detail needs the reveal permission and never
// returns the password, and deletes answer 200 with a body or 404.
type helmFake struct {
	*routeFake
	registries         map[string]map[string]any
	credentials        map[string]map[string]any
	charts             map[string]map[string]any
	credentialsCreated int
	registriesCreated  int
	isRevealDenied     bool
	// Failure injection for the create paths: a 500 on the interval update,
	// and on the next registry detail reads or credential listings.
	isIntervalUpdateFailing bool
	failingRegistryReads    int
	failingCredentialLists  int
}

func newHelmFake(t *testing.T) *helmFake {
	fake := &helmFake{
		routeFake:   newRouteFake(t),
		registries:  map[string]map[string]any{},
		credentials: map[string]map[string]any{},
		charts:      map[string]map[string]any{},
	}

	fake.handle("POST /api/v1/org/helm/registries", func(writer http.ResponseWriter, request *http.Request) {
		var body struct {
			Spec map[string]map[string]any `json:"spec"`
		}
		decodeJSON(t, request, &body)
		if len(body.Spec) != 1 {
			writeJSON(t, writer, http.StatusUnprocessableEntity, map[string]any{"detail": "spec must contain exactly one registry kind"})
			return
		}
		for kindKey, specification := range body.Spec {
			name, _ := specification["name"].(string)
			registryURL := strings.TrimSuffix(fmt.Sprint(specification["url"]), "/")
			kind := "HelmHTTPRegistry"
			if kindKey == "helm_oci_registry" {
				kind = "HelmOCIRegistry"
				if !strings.HasPrefix(registryURL, "oci://") {
					t.Errorf("an %s URL was sent as an OCI registry", registryURL)
				}
				if _, present := specification["exclude_charts"]; present {
					t.Errorf("exclude_charts was sent for an OCI registry")
				}
			} else if strings.HasPrefix(registryURL, "oci://") {
				t.Errorf("an oci:// URL was sent as an HTTP registry")
			}
			if refusal := fake.registryRefusal(name, registryURL, specification); refusal != nil {
				writeJSON(t, writer, http.StatusOK, map[string]any{"errors": []any{map[string]any{
					"name": name, "kind": kind, "errors": []any{refusal},
				}}})
				return
			}
			stored := map[string]any{
				"name":              name,
				"url":               registryURL,
				"credential_name":   specification["credential_name"],
				"kind":              kindKey,
				"read_job_interval": float64(120),
			}
			if kindKey == "helm_http_registry" {
				excludeCharts := specification["exclude_charts"]
				if excludeCharts == nil {
					excludeCharts = []any{}
				}
				stored["exclude_charts"] = excludeCharts
			}
			fake.registries[name] = stored
			fake.registriesCreated++
		}
		writeJSON(t, writer, http.StatusOK, map[string]any{"errors": []any{}})
	})
	fake.handle("GET /api/v1/org/helm/registries", func(writer http.ResponseWriter, _ *http.Request) {
		names := make([]string, 0, len(fake.registries))
		for name := range fake.registries {
			names = append(names, name)
		}
		sort.Strings(names)
		listing := []any{}
		for _, name := range names {
			row := helmRegistryRow(fake.registries[name])
			row["is_global"] = false
			row["chart_count"] = 3
			row["last_sync_error"] = nil
			listing = append(listing, row)
		}
		writeJSON(t, writer, http.StatusOK, map[string]any{
			"result":     listing,
			"pagination": map[string]any{"page": 1, "page_size": 100, "total_pages": 1, "total_count": len(listing)},
		})
	})
	fake.handle("GET /api/v1/org/helm/registries/{name}", func(writer http.ResponseWriter, request *http.Request) {
		if fake.failingRegistryReads > 0 {
			fake.failingRegistryReads--
			writeJSON(t, writer, http.StatusInternalServerError, map[string]any{"detail": "Internal server error"})
			return
		}
		stored, exists := fake.registries[request.PathValue("name")]
		if !exists {
			writeJSON(t, writer, http.StatusNotFound, map[string]any{"detail": "Unknown registry"})
			return
		}
		writeJSON(t, writer, http.StatusOK, map[string]any{
			"registry":          helmRegistryRow(stored),
			"charts":            []any{},
			"indexing":          false,
			"read_job_interval": stored["read_job_interval"],
			"recent_sync_jobs":  []any{},
			"resource_state":    "running",
			"last_sync_error":   nil,
		})
	})
	fake.handle("PUT /api/v1/org/helm/registries/{name}", func(writer http.ResponseWriter, request *http.Request) {
		stored, exists := fake.registries[request.PathValue("name")]
		if !exists {
			writeJSON(t, writer, http.StatusNotFound, map[string]any{"detail": "Registry not found"})
			return
		}
		var body map[string]any
		decodeJSON(t, request, &body)
		if fake.isIntervalUpdateFailing {
			writeJSON(t, writer, http.StatusInternalServerError, map[string]any{"detail": "Internal server error"})
			return
		}
		if len(body) != 1 || body["read_job_interval"] == nil {
			t.Errorf("registry update sent %v; only read_job_interval is updatable", body)
		}
		stored["read_job_interval"] = body["read_job_interval"]
		writeJSON(t, writer, http.StatusOK, map[string]any{})
	})
	fake.handle("DELETE /api/v1/org/helm/registries/{name}", func(writer http.ResponseWriter, request *http.Request) {
		name := request.PathValue("name")
		if _, exists := fake.registries[name]; !exists {
			writeJSON(t, writer, http.StatusNotFound, map[string]any{"detail": "Registry not found"})
			return
		}
		delete(fake.registries, name)
		writeJSON(t, writer, http.StatusOK, map[string]any{"marked_for_deletion_at": "2026-10-17T10:00:00Z"})
	})

	fake.handle("POST /api/v1/org/helm/credentials", func(writer http.ResponseWriter, request *http.Request) {
		var body map[string]any
		decodeJSON(t, request, &body)
		name, _ := body["name"].(string)
		if _, exists := fake.credentials[name]; exists {
			writeJSON(t, writer, http.StatusOK, map[string]any{"success": false, "errors": []any{map[string]any{
				"name": name, "kind": "registry_credential",
				"errors": []any{map[string]any{"key": "name", "message": "A credential with this name already exists."}},
			}}})
			return
		}
		fake.credentialsCreated++
		body["id"] = fmt.Sprintf("cred-%d", fake.credentialsCreated)
		fake.credentials[name] = body
		writeJSON(t, writer, http.StatusOK, map[string]any{"success": true, "errors": []any{}})
	})
	fake.handle("GET /api/v1/org/helm/credentials", func(writer http.ResponseWriter, _ *http.Request) {
		if fake.failingCredentialLists > 0 {
			fake.failingCredentialLists--
			writeJSON(t, writer, http.StatusInternalServerError, map[string]any{"detail": "Internal server error"})
			return
		}
		names := make([]string, 0, len(fake.credentials))
		for name := range fake.credentials {
			names = append(names, name)
		}
		sort.Strings(names)
		listing := []any{}
		for _, name := range names {
			listing = append(listing, map[string]any{
				"id": fake.credentials[name]["id"], "name": name, "created_at": "2026-10-17T10:00:00Z",
			})
		}
		writeJSON(t, writer, http.StatusOK, map[string]any{"credentials": listing, "total_count": len(listing)})
	})
	fake.handle("GET /api/v1/org/helm/credentials/{name}", func(writer http.ResponseWriter, request *http.Request) {
		if fake.isRevealDenied {
			writeJSON(t, writer, http.StatusForbidden, map[string]any{"detail": "Missing permission credentials.reveal"})
			return
		}
		stored, exists := fake.credentials[request.PathValue("name")]
		if !exists {
			writeJSON(t, writer, http.StatusNotFound, map[string]any{"detail": "Registry Credential not found"})
			return
		}
		writeJSON(t, writer, http.StatusOK, map[string]any{
			"id": stored["id"], "name": stored["name"], "username": stored["username"],
			"provider": "helm", "system": false, "created_at": "2026-10-17T10:00:00Z", "updated_at": "2026-10-17T10:00:00Z",
		})
	})
	fake.handle("PUT /api/v1/org/helm/credentials/{name}", func(writer http.ResponseWriter, request *http.Request) {
		stored, exists := fake.credentials[request.PathValue("name")]
		if !exists {
			writeJSON(t, writer, http.StatusNotFound, map[string]any{"detail": "registry_credential not found"})
			return
		}
		var body map[string]any
		decodeJSON(t, request, &body)
		if body["username"] == nil || body["password"] == nil {
			writeJSON(t, writer, http.StatusUnprocessableEntity, map[string]any{"detail": "username and password are required"})
			return
		}
		stored["username"] = body["username"]
		stored["password"] = body["password"]
		writeJSON(t, writer, http.StatusOK, map[string]any{"errors": []any{}})
	})
	fake.handle("DELETE /api/v1/org/helm/credentials/{name}", func(writer http.ResponseWriter, request *http.Request) {
		name := request.PathValue("name")
		if _, exists := fake.credentials[name]; !exists {
			writeJSON(t, writer, http.StatusNotFound, map[string]any{"detail": "registry_credential not found"})
			return
		}
		delete(fake.credentials, name)
		writeJSON(t, writer, http.StatusOK, map[string]any{"success": true})
	})

	fake.handle("POST /api/v1/org/stacks/charts/details", func(writer http.ResponseWriter, request *http.Request) {
		var body map[string]string
		decodeJSON(t, request, &body)
		chart, exists := fake.charts[strings.TrimSuffix(body["repository_url"], "/")+"/"+body["chart_name"]]
		if !exists {
			writeJSON(t, writer, http.StatusNotFound, map[string]any{"detail": "Chart not found or does not belong to the organisation."})
			return
		}
		writeJSON(t, writer, http.StatusOK, chart)
	})
	return fake
}

// registryRefusal mirrors CreateHelmChartRegistry's validation: a duplicate
// name or URL, or a credential that does not exist.
func (fake *helmFake) registryRefusal(name, registryURL string, specification map[string]any) map[string]any {
	for _, stored := range fake.registries {
		if stored["name"] == name {
			return map[string]any{"key": "name", "message": "A helm repository with this name already exists."}
		}
		if stored["url"] == registryURL {
			return map[string]any{"key": "url", "message": "A helm repository with this url already exists."}
		}
	}
	if credentialName, _ := specification["credential_name"].(string); credentialName != "" {
		if _, exists := fake.credentials[credentialName]; !exists {
			return map[string]any{"key": "credential_name", "message": fmt.Sprintf("Registry credential '%s' not found.", credentialName)}
		}
	}
	return nil
}

func helmRegistryRow(stored map[string]any) map[string]any {
	row := map[string]any{
		"name":               stored["name"],
		"url":                stored["url"],
		"credential_name":    stored["credential_name"],
		"delete_permanently": false,
		"created_at":         "2026-10-17T10:00:00Z",
		"updated_at":         "2026-10-17T10:00:00Z",
	}
	if stored["kind"] == "helm_http_registry" {
		row["exclude_charts"] = stored["exclude_charts"]
	}
	return row
}

func TestAccHelmRegistryLifecycle(t *testing.T) {
	fake := newHelmFake(t)
	fake.charts["https://charts.example.com/podinfo"] = map[string]any{
		"name": "podinfo", "icon": "", "repository_name": "example", "repository_url": "https://charts.example.com",
		"versions": []string{"6.7.1", "6.7.0", "7.0.0-rc.1"}, "profiles": []any{},
	}

	configuration := func(username, password string, interval int) string {
		return fake.providerBlock() + fmt.Sprintf(`
resource "ankra_helm_registry_credential" "example" {
  name     = "example-pull"
  username = %q
  password = %q
}

resource "ankra_helm_registry" "example" {
  name              = "example"
  url               = "https://charts.example.com"
  credential_name   = ankra_helm_registry_credential.example.name
  exclude_charts    = ["legacy"]
  read_job_interval = %d
}

resource "ankra_helm_registry" "ghcr" {
  name = "ghcr"
  url  = "oci://ghcr.io/acme/charts"
}

data "ankra_helm_registries" "all" {
  depends_on = [ankra_helm_registry.example, ankra_helm_registry.ghcr]
}

data "ankra_helm_registry_credentials" "all" {
  depends_on = [ankra_helm_registry_credential.example]
}

data "ankra_helm_chart" "podinfo" {
  chart_name   = "podinfo"
  registry_url = ankra_helm_registry.example.url
}
`, username, password, interval)
	}

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy: func(_ *terraform.State) error {
			if len(fake.registries) != 0 || len(fake.credentials) != 0 {
				return fmt.Errorf("left behind registries %v and credentials %v", fake.registries, fake.credentials)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: configuration("robot", "first-secret", 300),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ankra_helm_registry_credential.example", "id", "cred-1"),
					resource.TestCheckResourceAttr("ankra_helm_registry.example", "kind", "http"),
					resource.TestCheckResourceAttr("ankra_helm_registry.example", "url", "https://charts.example.com"),
					resource.TestCheckResourceAttr("ankra_helm_registry.example", "read_job_interval", "300"),
					resource.TestCheckResourceAttr("ankra_helm_registry.ghcr", "kind", "oci"),
					resource.TestCheckResourceAttr("ankra_helm_registry.ghcr", "read_job_interval", "120"),
					resource.TestCheckNoResourceAttr("ankra_helm_registry.ghcr", "exclude_charts"),
					resource.TestCheckResourceAttr("data.ankra_helm_registries.all", "registries.#", "2"),
					resource.TestCheckResourceAttr("data.ankra_helm_registries.all", "registries.0.name", "example"),
					resource.TestCheckResourceAttr("data.ankra_helm_registries.all", "registries.0.credential_name", "example-pull"),
					resource.TestCheckResourceAttr("data.ankra_helm_registries.all", "registries.1.kind", "oci"),
					resource.TestCheckResourceAttr("data.ankra_helm_registry_credentials.all", "credentials.0.name", "example-pull"),
					resource.TestCheckResourceAttr("data.ankra_helm_chart.podinfo", "registry_name", "example"),
					resource.TestCheckResourceAttr("data.ankra_helm_chart.podinfo", "latest_version", "6.7.1"),
					resource.TestCheckResourceAttr("data.ankra_helm_chart.podinfo", "versions.#", "3"),
					func(_ *terraform.State) error {
						stored := fake.registries["example"]
						if stored["url"] != "https://charts.example.com" || stored["credential_name"] != "example-pull" {
							return fmt.Errorf("platform holds %v", stored)
						}
						if stored["read_job_interval"] != float64(300) {
							return fmt.Errorf("read_job_interval was not set after create: %v", stored["read_job_interval"])
						}
						return nil
					},
				),
			},
			{
				Config: configuration("robot2", "second-secret", 600),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ankra_helm_registry.example", "read_job_interval", "600"),
					resource.TestCheckResourceAttr("ankra_helm_registry_credential.example", "username", "robot2"),
					func(_ *terraform.State) error {
						if fake.credentials["example-pull"]["password"] != "second-secret" {
							return fmt.Errorf("password was not rotated in place")
						}
						if fake.credentialsCreated != 1 {
							return fmt.Errorf("credential was replaced instead of updated")
						}
						return nil
					},
				),
			},
			{
				ResourceName:      "ankra_helm_registry.example",
				ImportState:       true,
				ImportStateId:     "example",
				ImportStateVerify: true,
			},
			{
				ResourceName:            "ankra_helm_registry_credential.example",
				ImportState:             true,
				ImportStateId:           "example-pull",
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"password"},
			},
			{
				// Username changed outside Terraform: the next plan restores it.
				PreConfig: func() { fake.credentials["example-pull"]["username"] = "someone-else" },
				Config:    configuration("robot2", "second-secret", 600),
				Check: func(_ *terraform.State) error {
					if fake.credentials["example-pull"]["username"] != "robot2" {
						return fmt.Errorf("username drift was not corrected")
					}
					return nil
				},
			},
			{
				// Deleted outside Terraform: the next plan recreates it.
				PreConfig: func() { delete(fake.registries, "ghcr") },
				Config:    configuration("robot2", "second-secret", 600),
				Check: func(_ *terraform.State) error {
					if _, exists := fake.registries["ghcr"]; !exists {
						return fmt.Errorf("registry deleted outside Terraform was not recreated")
					}
					return nil
				},
			},
		},
	})
}

func TestAccHelmRegistryCredentialWithoutRevealPermission(t *testing.T) {
	fake := newHelmFake(t)
	fake.isRevealDenied = true

	configuration := fake.providerBlock() + `
resource "ankra_helm_registry_credential" "example" {
  name     = "example-pull"
  username = "robot"
  password = "secret"
}
`
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: configuration,
				Check:  resource.TestCheckResourceAttr("ankra_helm_registry_credential.example", "id", "cred-1"),
			},
			{
				// Without the reveal permission the listing still finds it, so a
				// deletion outside Terraform is detected.
				PreConfig: func() { delete(fake.credentials, "example-pull") },
				Config:    configuration,
				Check: func(_ *terraform.State) error {
					if _, exists := fake.credentials["example-pull"]; !exists {
						return fmt.Errorf("credential deleted outside Terraform was not recreated")
					}
					return nil
				},
			},
		},
	})
}

func TestAccHelmRegistryDoesNotAdoptAnotherRow(t *testing.T) {
	fake := newHelmFake(t)
	configuration := fake.providerBlock() + `
resource "ankra_helm_registry" "example" {
  name = "example"
  url  = "https://charts.example.com"
}
`
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{Config: configuration},
			{
				// The name now resolves to a different registry (another project's,
				// or one that took the name): Read must drop ours, not adopt it.
				PreConfig: func() {
					fake.registries["example"]["url"] = "https://other.example.com"
				},
				Config:             configuration,
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
			{
				RefreshState:       true,
				ExpectNonEmptyPlan: true,
				Check: func(state *terraform.State) error {
					if _, exists := state.RootModule().Resources["ankra_helm_registry.example"]; exists {
						return fmt.Errorf("refresh adopted the other registry")
					}
					return nil
				},
			},
		},
		CheckDestroy: func(_ *terraform.State) error {
			for _, served := range fake.served() {
				if strings.HasPrefix(served, "DELETE ") {
					return fmt.Errorf("destroyed a registry it no longer owned: %s", served)
				}
			}
			delete(fake.registries, "example")
			return nil
		},
	})
}

func TestAccHelmRejectsInvalidConfigurationAtPlan(t *testing.T) {
	fake := newHelmFake(t)
	cases := map[string]string{
		`resource "ankra_helm_registry" "bad" {
  name = "ankra-harbor"
  url  = "oci://registry.example.com/org"
}`: "Reserved registry name",
		`resource "ankra_helm_registry" "bad" {
  name = "team/charts"
  url  = "https://charts.example.com"
}`: "Invalid name",
		`resource "ankra_helm_registry" "bad" {
  name           = "ghcr"
  url            = "oci://ghcr.io/acme"
  exclude_charts = ["x"]
}`: "not supported for OCI registries",
		`resource "ankra_helm_registry" "bad" {
  name              = "fast"
  url               = "https://charts.example.com"
  read_job_interval = 30
}`: "Invalid read_job_interval",
		`resource "ankra_helm_registry" "bad" {
  name = "slash"
  url  = "https://charts.example.com/"
}`: "Trailing slash in url",
		`resource "ankra_helm_registry_credential" "bad" {
  name     = "ankra-harbor-pull"
  username = "u"
  password = "p"
}`: "Reserved credential name",
	}
	for configuration, expected := range cases {
		resource.Test(t, resource.TestCase{
			ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
			Steps: []resource.TestStep{{
				Config:      fake.providerBlock() + configuration,
				ExpectError: regexp.MustCompile(expected),
			}},
		})
	}
	if served := fake.served(); len(served) != 0 {
		t.Errorf("invalid configuration reached the API: %v", served)
	}
}

func TestAccHelmRegistryIntervalFailureDoesNotTaint(t *testing.T) {
	fake := newHelmFake(t)
	fake.isIntervalUpdateFailing = true
	configuration := fake.providerBlock() + `
resource "ankra_helm_registry" "example" {
  name              = "example"
  url               = "https://charts.example.com"
  read_job_interval = 300
}
`
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				// The registry is created; the failed interval update is only a
				// warning, so the registry is not tainted. The refresh after
				// apply reads the platform's 120, so the plan is not empty.
				Config:             configuration,
				ExpectNonEmptyPlan: true,
				Check: func(state *terraform.State) error {
					if state.RootModule().Resources["ankra_helm_registry.example"].Primary.Tainted {
						return fmt.Errorf("the registry was tainted, so the next apply would replace it")
					}
					if fake.registries["example"]["read_job_interval"] != float64(120) {
						return fmt.Errorf("platform holds interval %v", fake.registries["example"]["read_job_interval"])
					}
					return nil
				},
			},
			{
				// The pending difference is applied as an in-place update.
				PreConfig: func() { fake.isIntervalUpdateFailing = false },
				Config:    configuration,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("ankra_helm_registry.example", plancheck.ResourceActionUpdate),
					},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ankra_helm_registry.example", "read_job_interval", "300"),
					func(_ *terraform.State) error {
						if fake.registriesCreated != 1 {
							return fmt.Errorf("the registry was recreated instead of updated in place")
						}
						if fake.registries["example"]["read_job_interval"] != float64(300) {
							return fmt.Errorf("platform holds interval %v", fake.registries["example"]["read_job_interval"])
						}
						return nil
					},
				),
			},
		},
	})
}

func TestAccHelmCreateReadFailureTaintsInsteadOfOrphaning(t *testing.T) {
	fake := newHelmFake(t)
	configuration := fake.providerBlock() + `
resource "ankra_helm_registry_credential" "example" {
  name     = "example-pull"
  username = "robot"
  password = "secret"
}

resource "ankra_helm_registry" "example" {
  name = "example"
  url  = "https://charts.example.com"
}
`
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				// Both POSTs succeed and the read-back that follows fails.
				PreConfig: func() {
					fake.failingRegistryReads = 1
					fake.failingCredentialLists = 1
				},
				Config:      configuration,
				ExpectError: regexp.MustCompile("Unable to read Helm registry"),
			},
			{
				// Without state the next apply would POST again and hit the
				// duplicate-name refusal. With tainted state it replaces both.
				Config: configuration,
				Check: func(_ *terraform.State) error {
					if fake.registriesCreated != 2 || fake.credentialsCreated != 2 {
						return fmt.Errorf("expected each tainted object to be replaced once, got %d registry and %d credential creates",
							fake.registriesCreated, fake.credentialsCreated)
					}
					if len(fake.registries) != 1 || len(fake.credentials) != 1 {
						return fmt.Errorf("left %d registries and %d credentials", len(fake.registries), len(fake.credentials))
					}
					return nil
				},
			},
		},
	})
}
