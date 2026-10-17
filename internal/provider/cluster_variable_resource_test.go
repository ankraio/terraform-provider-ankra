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

const clusterConfigurationTestCluster = "6f1c2a40-0000-4000-8000-000000000001"

// clusterConfigurationFake holds cluster variables the way the platform does
// (go/internal/varsapi): create answers 200 with the variable, the update
// writes the description it is given so a null clears it, delete answers 200
// {"deleted":true} or 404 "Variable not found", and a cluster outside the
// request's scope answers 404 "Cluster not found" on every route.
type clusterConfigurationFake struct {
	*routeFake
	variables        map[string]map[string]any
	variablesCreated int
	isClusterHidden  bool
}

func newClusterConfigurationFake(t *testing.T) *clusterConfigurationFake {
	fake := &clusterConfigurationFake{
		routeFake: newRouteFake(t),
		variables: map[string]map[string]any{},
	}
	clusterMissing := func(writer http.ResponseWriter, request *http.Request) bool {
		if request.PathValue("cluster_id") == clusterConfigurationTestCluster && !fake.isClusterHidden {
			return false
		}
		writeJSON(t, writer, http.StatusNotFound, map[string]any{"detail": "Cluster not found"})
		return true
	}

	variablesPath := "/api/v1/org/clusters/imported/{cluster_id}/variables"
	fake.handle("GET "+variablesPath, func(writer http.ResponseWriter, request *http.Request) {
		if clusterMissing(writer, request) {
			return
		}
		names := make([]string, 0, len(fake.variables))
		for name := range fake.variables {
			names = append(names, name)
		}
		sort.Strings(names)
		listing := []any{}
		for _, name := range names {
			listing = append(listing, fake.variables[name])
		}
		writeJSON(t, writer, http.StatusOK, map[string]any{"variables": listing})
	})
	fake.handle("POST "+variablesPath, func(writer http.ResponseWriter, request *http.Request) {
		if clusterMissing(writer, request) {
			return
		}
		var body map[string]any
		decodeJSON(t, request, &body)
		name, _ := body["name"].(string)
		if _, exists := fake.variables[name]; exists {
			writeJSON(t, writer, http.StatusConflict, map[string]any{"detail": "A variable with this name already exists"})
			return
		}
		fake.variablesCreated++
		stored := map[string]any{
			"id":              fmt.Sprintf("cvar-%d", fake.variablesCreated),
			"cluster_id":      clusterConfigurationTestCluster,
			"organisation_id": "org-1",
			"name":            name,
			"value":           body["value"],
			"description":     body["description"],
			"created_at":      "2026-10-17T10:00:00Z",
			"updated_at":      "2026-10-17T10:00:00Z",
		}
		fake.variables[name] = stored
		writeJSON(t, writer, http.StatusOK, map[string]any{"variable": stored})
	})
	fake.handle("PUT "+variablesPath+"/{name}", func(writer http.ResponseWriter, request *http.Request) {
		if clusterMissing(writer, request) {
			return
		}
		stored, exists := fake.variables[request.PathValue("name")]
		if !exists {
			writeJSON(t, writer, http.StatusNotFound, map[string]any{"detail": "Variable not found"})
			return
		}
		var body map[string]json.RawMessage
		decodeJSON(t, request, &body)
		if _, present := body["value"]; !present {
			writeJSON(t, writer, http.StatusUnprocessableEntity, map[string]any{"detail": "value is required"})
			return
		}
		var value, description any
		_ = json.Unmarshal(body["value"], &value)
		_ = json.Unmarshal(body["description"], &description)
		stored["value"] = value
		stored["description"] = description
		writeJSON(t, writer, http.StatusOK, map[string]any{"variable": stored})
	})
	fake.handle("DELETE "+variablesPath+"/{name}", func(writer http.ResponseWriter, request *http.Request) {
		if clusterMissing(writer, request) {
			return
		}
		name := request.PathValue("name")
		if _, exists := fake.variables[name]; !exists {
			writeJSON(t, writer, http.StatusNotFound, map[string]any{"detail": "Variable not found"})
			return
		}
		delete(fake.variables, name)
		writeJSON(t, writer, http.StatusOK, map[string]any{"deleted": true})
	})

	return fake
}

func TestAccClusterVariableLifecycle(t *testing.T) {
	fake := newClusterConfigurationFake(t)
	configuration := func(value, description string) string {
		descriptionLine := ""
		if description != "" {
			descriptionLine = fmt.Sprintf("description = %q", description)
		}
		return fake.providerBlock() + fmt.Sprintf(`
resource "ankra_cluster_variable" "domain" {
  cluster_id = %q
  name       = "domain"
  value      = %q
  %s
}

data "ankra_cluster_variables" "all" {
  cluster_id = %q
  depends_on = [ankra_cluster_variable.domain]
}
`, clusterConfigurationTestCluster, value, descriptionLine, clusterConfigurationTestCluster)
	}

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy: func(_ *terraform.State) error {
			if len(fake.variables) != 0 {
				return fmt.Errorf("left behind %v", fake.variables)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: configuration("apps.example.com", "ingress domain"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ankra_cluster_variable.domain", "id", "cvar-1"),
					resource.TestCheckResourceAttr("ankra_cluster_variable.domain", "description", "ingress domain"),
					resource.TestCheckResourceAttr("data.ankra_cluster_variables.all", "variables.#", "1"),
					resource.TestCheckResourceAttr("data.ankra_cluster_variables.all", "variables.0.value", "apps.example.com"),
				),
			},
			{
				Config: configuration("apps.example.org", ""),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ankra_cluster_variable.domain", "value", "apps.example.org"),
					resource.TestCheckNoResourceAttr("ankra_cluster_variable.domain", "description"),
					func(_ *terraform.State) error {
						if description := fake.variables["domain"]["description"]; description != nil {
							return fmt.Errorf("platform still holds description %v", description)
						}
						if fake.variablesCreated != 1 {
							return fmt.Errorf("variable was replaced instead of updated")
						}
						return nil
					},
				),
			},
			{
				ResourceName:      "ankra_cluster_variable.domain",
				ImportState:       true,
				ImportStateId:     clusterConfigurationTestCluster + "/domain",
				ImportStateVerify: true,
			},
			{
				// Value changed outside Terraform: the next plan restores it.
				PreConfig: func() { fake.variables["domain"]["value"] = "drifted" },
				Config:    configuration("apps.example.org", ""),
				Check: func(_ *terraform.State) error {
					if fake.variables["domain"]["value"] != "apps.example.org" {
						return fmt.Errorf("value drift was not corrected")
					}
					return nil
				},
			},
			{
				// Deleted and recreated outside Terraform under the same name:
				// Read drops ours instead of adopting the new row.
				PreConfig: func() {
					fake.variables["domain"]["id"] = "cvar-other"
				},
				Config:             configuration("apps.example.org", ""),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
			{
				PreConfig: func() { delete(fake.variables, "domain") },
				Config:    configuration("apps.example.org", ""),
				Check: func(_ *terraform.State) error {
					if _, exists := fake.variables["domain"]; !exists {
						return fmt.Errorf("variable deleted outside Terraform was not recreated")
					}
					return nil
				},
			},
		},
	})
}

func TestAccClusterVariableOutsideProjectScopeIsNotDeleted(t *testing.T) {
	fake := newClusterConfigurationFake(t)
	configuration := fake.providerBlock() + fmt.Sprintf(`
resource "ankra_cluster_variable" "domain" {
  cluster_id = %q
  name       = "domain"
  value      = "apps.example.com"
}
`, clusterConfigurationTestCluster)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{Config: configuration},
			{
				// The provider's project no longer includes the cluster: the
				// platform answers 404 "Cluster not found". Read must fail with
				// guidance, not drop the variable from state and plan a create.
				PreConfig:   func() { fake.isClusterHidden = true },
				Config:      configuration,
				ExpectError: regexp.MustCompile(`(?s)not visible.*set project_id.*terraform state rm`),
			},
			{
				PreConfig: func() { fake.isClusterHidden = false },
				Config:    configuration,
				Check: func(_ *terraform.State) error {
					if fake.variablesCreated != 1 {
						return fmt.Errorf("the variable was recreated %d times while its cluster was out of scope", fake.variablesCreated-1)
					}
					return nil
				},
			},
		},
	})
}

func TestAccClusterVariableImportIDIsStrict(t *testing.T) {
	fake := newClusterConfigurationFake(t)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: fake.providerBlock() + fmt.Sprintf(`
resource "ankra_cluster_variable" "domain" {
  cluster_id = %q
  name       = "domain"
  value      = "x"
}
`, clusterConfigurationTestCluster),
				ResourceName:  "ankra_cluster_variable.domain",
				ImportState:   true,
				ImportStateId: "domain",
				ExpectError:   regexp.MustCompile("expected an import ID of the form cluster_id/name"),
			},
		},
	})
}
