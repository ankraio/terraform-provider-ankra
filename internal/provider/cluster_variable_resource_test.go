// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

const clusterConfigurationTestCluster = "6f1c2a40-0000-4000-8000-000000000001"

// clusterConfigurationFake holds cluster and stack variables the way the
// platform does. Cluster variables (go/internal/varsapi): create answers 200
// with the variable, the update writes the description it is given so a null
// clears it, delete answers 200 {"deleted":true} or 404, and an unknown
// cluster is a 404. Stacks (go/internal/cliapi imported routes): the listing
// filters by a search substring and carries each stack's description,
// deploy_wave and variables; the partial PATCH replaces the variables map,
// keeps the members it is not sent, and may refuse the Git write-back with a
// 422 GIT_PUSH_DEFERRED after applying the change.
type clusterConfigurationFake struct {
	*routeFake
	variables        map[string]map[string]any
	stacks           map[string]map[string]any
	variablesCreated int
	stackPatches     int
	deferNextGitPush bool
}

func newClusterConfigurationFake(t *testing.T) *clusterConfigurationFake {
	fake := &clusterConfigurationFake{
		routeFake: newRouteFake(t),
		variables: map[string]map[string]any{},
		stacks:    map[string]map[string]any{},
	}
	clusterMissing := func(writer http.ResponseWriter, request *http.Request) bool {
		if request.PathValue("cluster_id") == clusterConfigurationTestCluster {
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

	stacksPath := "/api/v1/org/clusters/imported/{cluster_id}/stacks"
	fake.handle("GET "+stacksPath, func(writer http.ResponseWriter, request *http.Request) {
		if clusterMissing(writer, request) {
			return
		}
		search := request.URL.Query().Get("search")
		names := []string{}
		for name := range fake.stacks {
			if strings.Contains(strings.ToLower(name), strings.ToLower(search)) {
				names = append(names, name)
			}
		}
		sort.Strings(names)
		listing := []any{}
		for _, name := range names {
			stack := fake.stacks[name]
			listing = append(listing, map[string]any{
				"name": name, "description": stack["description"], "description_from_file": nil,
				"variables": stack["variables"], "manifests": stack["manifests"], "addons": []any{},
				"applications": []any{}, "deploy_wave": stack["deploy_wave"], "state": "running",
				"delete_permanently": false, "draft_id": nil, "jobs": []any{}, "is_draft_only": false,
				"lifecycle": "deployed_clean", "has_encrypted_values": false,
			})
		}
		writeJSON(t, writer, http.StatusOK, map[string]any{
			"stacks": listing, "drafts": []any{},
			"pagination": map[string]any{"page": 1, "page_size": 100, "total_pages": 1, "total_count": len(listing)},
		})
	})
	fake.handle("PATCH "+stacksPath+"/{stack_name}", func(writer http.ResponseWriter, request *http.Request) {
		if clusterMissing(writer, request) {
			return
		}
		stackName := request.PathValue("stack_name")
		stack, exists := fake.stacks[stackName]
		if !exists {
			writeJSON(t, writer, http.StatusNotFound, map[string]any{"detail": "Stack not found"})
			return
		}
		var body struct {
			PartialStack bool `json:"partial_stack"`
			Spec         struct {
				Stacks []map[string]json.RawMessage `json:"stacks"`
			} `json:"spec"`
		}
		decodeJSON(t, request, &body)
		if !body.PartialStack || len(body.Spec.Stacks) != 1 {
			t.Errorf("stack variables must be a partial update of exactly one stack, got %+v", body)
			writeJSON(t, writer, http.StatusBadRequest, map[string]any{"detail": "Exactly one stack must be provided"})
			return
		}
		sent := body.Spec.Stacks[0]
		var name, description string
		var deployWave float64
		var variables map[string]string
		var manifests, addons []any
		_ = json.Unmarshal(sent["name"], &name)
		_ = json.Unmarshal(sent["description"], &description)
		_ = json.Unmarshal(sent["deploy_wave"], &deployWave)
		_ = json.Unmarshal(sent["manifests"], &manifests)
		_ = json.Unmarshal(sent["addons"], &addons)
		if err := json.Unmarshal(sent["variables"], &variables); err != nil || variables == nil {
			t.Errorf("the variables map must be sent, an absent one would keep the stored map: %s", sent["variables"])
		}
		if name != stackName || len(manifests) != 0 || len(addons) != 0 {
			t.Errorf("partial stack update sent name %q, manifests %v, addons %v", name, manifests, addons)
		}
		if description != stack["description"] || deployWave != stack["deploy_wave"] {
			t.Errorf("stack metadata was not carried back: description %q, deploy_wave %v", description, deployWave)
		}
		stack["variables"] = variables
		fake.stackPatches++
		if fake.deferNextGitPush {
			fake.deferNextGitPush = false
			writeJSON(t, writer, http.StatusUnprocessableEntity, map[string]any{
				"detail": "The change is live; the commit to Git is pending.", "error_code": "GIT_PUSH_DEFERRED",
			})
			return
		}
		writeJSON(t, writer, http.StatusOK, map[string]any{"stack_name": stackName, "errors": []any{}, "job_count": 1})
	})
	return fake
}

func clusterConfigurationStackVariables(fake *clusterConfigurationFake, stackName string) map[string]string {
	variables, ok := fake.stacks[stackName]["variables"].(map[string]string)
	if !ok {
		fake.t.Fatalf("stack %q holds variables of type %T", stackName, fake.stacks[stackName]["variables"])
	}
	return variables
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

func TestAccClusterStackVariableLifecycle(t *testing.T) {
	fake := newClusterConfigurationFake(t)
	fake.stacks["platform"] = map[string]any{
		"description": "ingress and certificates",
		"deploy_wave": float64(1),
		"variables":   map[string]string{"kept": "untouched"},
		"manifests":   []any{map[string]any{"name": "namespace"}},
	}
	fake.stacks["platform-extras"] = map[string]any{
		"description": "", "deploy_wave": float64(0), "variables": map[string]string{}, "manifests": []any{},
	}
	configuration := func(replicas string) string {
		return fake.providerBlock() + fmt.Sprintf(`
resource "ankra_cluster_stack_variable" "replicas" {
  cluster_id = %[1]q
  stack_name = "platform"
  name       = "replicas"
  value      = %[2]q
}

resource "ankra_cluster_stack_variable" "domain" {
  cluster_id = %[1]q
  stack_name = "platform"
  name       = "domain"
  value      = "apps.example.com"
}

data "ankra_cluster_stack_variables" "platform" {
  cluster_id = %[1]q
  stack_name = "platform"
  depends_on = [ankra_cluster_stack_variable.replicas, ankra_cluster_stack_variable.domain]
}
`, clusterConfigurationTestCluster, replicas)
	}
	stackVariables := func() map[string]string {
		return clusterConfigurationStackVariables(fake, "platform")
	}

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy: func(_ *terraform.State) error {
			variables := stackVariables()
			if len(variables) != 1 || variables["kept"] != "untouched" {
				return fmt.Errorf("destroy left the stack with %v; only the variable it did not manage should remain", variables)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				// Both variables are written in parallel; the per-stack lock keeps
				// either from dropping the other's change.
				Config: configuration("2"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ankra_cluster_stack_variable.replicas", "id", clusterConfigurationTestCluster+"/platform/replicas"),
					resource.TestCheckResourceAttr("data.ankra_cluster_stack_variables.platform", "variables.%", "3"),
					resource.TestCheckResourceAttr("data.ankra_cluster_stack_variables.platform", "variables.kept", "untouched"),
					func(_ *terraform.State) error {
						variables := stackVariables()
						if variables["replicas"] != "2" || variables["domain"] != "apps.example.com" || variables["kept"] != "untouched" {
							return fmt.Errorf("stack holds %v", variables)
						}
						if len(clusterConfigurationStackVariables(fake, "platform-extras")) != 0 {
							return fmt.Errorf("a stack whose name only contains the target was written")
						}
						return nil
					},
				),
			},
			{
				PreConfig: func() { fake.deferNextGitPush = true },
				Config:    configuration("3"),
				Check: func(_ *terraform.State) error {
					if stackVariables()["replicas"] != "3" {
						return fmt.Errorf("update was not applied: %v", stackVariables())
					}
					return nil
				},
			},
			{
				ResourceName:      "ankra_cluster_stack_variable.replicas",
				ImportState:       true,
				ImportStateId:     clusterConfigurationTestCluster + "/platform/replicas",
				ImportStateVerify: true,
			},
			{
				PreConfig: func() { delete(stackVariables(), "domain") },
				Config:    configuration("3"),
				Check: func(_ *terraform.State) error {
					if stackVariables()["domain"] != "apps.example.com" {
						return fmt.Errorf("variable deleted outside Terraform was not recreated")
					}
					return nil
				},
			},
		},
	})
}

func TestAccClusterStackVariableRefusesToTakeOverAnExistingVariable(t *testing.T) {
	fake := newClusterConfigurationFake(t)
	fake.stacks["platform"] = map[string]any{
		"description": "", "deploy_wave": float64(0), "variables": map[string]string{"replicas": "5"}, "manifests": []any{},
	}
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: fake.providerBlock() + fmt.Sprintf(`
resource "ankra_cluster_stack_variable" "replicas" {
  cluster_id = %q
  stack_name = "platform"
  name       = "replicas"
  value      = "2"
}
`, clusterConfigurationTestCluster),
				ExpectError: regexp.MustCompile("Stack variable already exists"),
			},
			{
				Config: fake.providerBlock() + fmt.Sprintf(`
resource "ankra_cluster_stack_variable" "missing" {
  cluster_id = %q
  stack_name = "absent"
  name       = "replicas"
  value      = "2"
}
`, clusterConfigurationTestCluster),
				ExpectError: regexp.MustCompile("Stack not found"),
			},
		},
	})
	if fake.stackPatches != 0 {
		t.Errorf("sent %d stack updates for refused variables", fake.stackPatches)
	}
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
