// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

// stackVariablesFake holds stack variables the way the platform does
// (go/internal/cliapi/imported_stack_variables.go): GET answers the deployed
// variables of a stack, PUT sets one key and DELETE removes one, each leaving
// the other keys alone; a key the stack does not hold deletes with
// changed=false, a missing stack answers 404 "Stack not found in this
// cluster", a stack being deleted refuses writes with 409, and a cluster
// outside the request's scope answers 404 "Cluster not found".
type stackVariablesFake struct {
	*routeFake
	stacks          map[string]map[string]string
	deletePending   map[string]bool
	isClusterHidden bool
	writes          []string
}

func newStackVariablesFake(t *testing.T) *stackVariablesFake {
	fake := &stackVariablesFake{
		routeFake:     newRouteFake(t),
		stacks:        map[string]map[string]string{"platform": {"region": "eu-north"}},
		deletePending: map[string]bool{},
	}
	variablesPath := "/api/v1/org/clusters/imported/{cluster_id}/stacks/{stack_name}/variables"
	stackFor := func(writer http.ResponseWriter, request *http.Request) (map[string]string, bool) {
		if request.PathValue("cluster_id") != clusterConfigurationTestCluster || fake.isClusterHidden {
			writeJSON(t, writer, http.StatusNotFound, map[string]any{"detail": "Cluster not found"})
			return nil, false
		}
		variables, exists := fake.stacks[request.PathValue("stack_name")]
		if !exists {
			writeJSON(t, writer, http.StatusNotFound, map[string]any{"detail": "Stack not found in this cluster"})
			return nil, false
		}
		if request.Method != http.MethodGet && fake.deletePending[request.PathValue("stack_name")] {
			writeJSON(t, writer, http.StatusConflict, map[string]any{"detail": "Stack is pending deletion. Orphaned resources have been marked for cleanup."})
			return nil, false
		}
		return variables, true
	}
	writeOutcome := func(writer http.ResponseWriter, stackName string, set map[string]string, unset []string, isChanged bool) {
		var commit any
		if isChanged {
			commit = "0123abcd"
		}
		writeJSON(t, writer, http.StatusOK, map[string]any{
			"stack_name": stackName, "changed": isChanged, "set": set, "unset": unset,
			"commit_sha": commit, "commit_url": nil, "operation_id": nil, "job_count": 0,
		})
	}

	fake.handle("GET "+variablesPath, func(writer http.ResponseWriter, request *http.Request) {
		variables, found := stackFor(writer, request)
		if !found {
			return
		}
		writeJSON(t, writer, http.StatusOK, map[string]any{
			"stack_name": request.PathValue("stack_name"), "variables": variables, "deploy_wave": 2,
			"description": nil, "has_pending_draft": false, "delete_pending": fake.deletePending[request.PathValue("stack_name")],
		})
	})
	fake.handle("PUT "+variablesPath+"/{variable_name}", func(writer http.ResponseWriter, request *http.Request) {
		variables, found := stackFor(writer, request)
		if !found {
			return
		}
		var body map[string]json.RawMessage
		decodeJSON(t, request, &body)
		var value *string
		if raw, present := body["value"]; present {
			_ = json.Unmarshal(raw, &value)
		}
		if value == nil {
			writeJSON(t, writer, http.StatusUnprocessableEntity, map[string]any{"detail": "value is required"})
			return
		}
		name := request.PathValue("variable_name")
		fake.writes = append(fake.writes, "PUT "+name)
		isChanged := variables[name] != *value
		variables[name] = *value
		writeOutcome(writer, request.PathValue("stack_name"), map[string]string{name: *value}, []string{}, isChanged)
	})
	fake.handle("DELETE "+variablesPath+"/{variable_name}", func(writer http.ResponseWriter, request *http.Request) {
		variables, found := stackFor(writer, request)
		if !found {
			return
		}
		name := request.PathValue("variable_name")
		fake.writes = append(fake.writes, "DELETE "+name)
		_, isChanged := variables[name]
		delete(variables, name)
		writeOutcome(writer, request.PathValue("stack_name"), map[string]string{}, []string{name}, isChanged)
	})
	return fake
}

func stackVariableConfiguration(fake *stackVariablesFake, value string) string {
	return fake.providerBlock() + fmt.Sprintf(`
resource "ankra_cluster_stack_variable" "domain" {
  cluster_id = %q
  stack_name = "platform"
  name       = "ingress_domain"
  value      = %q
}

data "ankra_cluster_stack_variables" "platform" {
  cluster_id = %q
  stack_name = "platform"
  depends_on = [ankra_cluster_stack_variable.domain]
}
`, clusterConfigurationTestCluster, value, clusterConfigurationTestCluster)
}

func TestAccClusterStackVariableLifecycle(t *testing.T) {
	fake := newStackVariablesFake(t)
	resourceID := clusterConfigurationTestCluster + "/platform/ingress_domain"

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy: func(_ *terraform.State) error {
			if _, present := fake.stacks["platform"]["ingress_domain"]; present {
				return fmt.Errorf("left behind ingress_domain: %v", fake.stacks["platform"])
			}
			if fake.stacks["platform"]["region"] != "eu-north" {
				return fmt.Errorf("destroy touched a key it does not manage: %v", fake.stacks["platform"])
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: stackVariableConfiguration(fake, "apps.example.com"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ankra_cluster_stack_variable.domain", "id", resourceID),
					resource.TestCheckResourceAttr("ankra_cluster_stack_variable.domain", "value", "apps.example.com"),
					resource.TestCheckResourceAttr("data.ankra_cluster_stack_variables.platform", "variables.%", "2"),
					resource.TestCheckResourceAttr("data.ankra_cluster_stack_variables.platform", "variables.region", "eu-north"),
					resource.TestCheckResourceAttr("data.ankra_cluster_stack_variables.platform", "variables.ingress_domain", "apps.example.com"),
					resource.TestCheckResourceAttr("data.ankra_cluster_stack_variables.platform", "deploy_wave", "2"),
					resource.TestCheckResourceAttr("data.ankra_cluster_stack_variables.platform", "has_pending_draft", "false"),
					resource.TestCheckNoResourceAttr("data.ankra_cluster_stack_variables.platform", "description"),
				),
			},
			{
				Config: stackVariableConfiguration(fake, "apps.example.org"),
				Check: func(_ *terraform.State) error {
					if fake.stacks["platform"]["ingress_domain"] != "apps.example.org" {
						return fmt.Errorf("value was not updated: %v", fake.stacks["platform"])
					}
					if last := fake.writes[len(fake.writes)-1]; last != "PUT ingress_domain" {
						return fmt.Errorf("the change was not written in place: %v", fake.writes)
					}
					for _, write := range fake.writes {
						if write == "DELETE ingress_domain" {
							return fmt.Errorf("a value change replaced the key: %v", fake.writes)
						}
					}
					return nil
				},
			},
			{
				ResourceName:      "ankra_cluster_stack_variable.domain",
				ImportState:       true,
				ImportStateId:     resourceID,
				ImportStateVerify: true,
			},
			{
				// Changed outside Terraform: the next apply sets it back.
				PreConfig: func() { fake.stacks["platform"]["ingress_domain"] = "drifted" },
				Config:    stackVariableConfiguration(fake, "apps.example.org"),
				Check: func(_ *terraform.State) error {
					if fake.stacks["platform"]["ingress_domain"] != "apps.example.org" {
						return fmt.Errorf("value drift was not corrected: %v", fake.stacks["platform"])
					}
					return nil
				},
			},
			{
				// Removed outside Terraform (a whole-map write, say): the
				// next apply sets it again.
				PreConfig: func() { delete(fake.stacks["platform"], "ingress_domain") },
				Config:    stackVariableConfiguration(fake, "apps.example.org"),
				Check: func(_ *terraform.State) error {
					if fake.stacks["platform"]["ingress_domain"] != "apps.example.org" {
						return fmt.Errorf("the removed key was not set again: %v", fake.stacks["platform"])
					}
					return nil
				},
			},
		},
	})
}

func TestAccClusterStackVariableRefusesKeyAlreadySet(t *testing.T) {
	fake := newStackVariablesFake(t)
	fake.stacks["platform"]["ingress_domain"] = "set.elsewhere.example.com"
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config:      stackVariableConfiguration(fake, "apps.example.com"),
			ExpectError: regexp.MustCompile(`(?s)already\s+holds\s+the\s+variable.*terraform\s+import\s+<resource\s+address>\s+` + clusterConfigurationTestCluster + `/platform/ingress_domain`),
		}},
	})
	if fake.stacks["platform"]["ingress_domain"] != "set.elsewhere.example.com" || len(fake.writes) != 0 {
		t.Errorf("the existing key was overwritten: %v %v", fake.stacks["platform"], fake.writes)
	}
}

func TestAccClusterStackVariableStackGone(t *testing.T) {
	fake := newStackVariablesFake(t)
	configuration := fake.providerBlock() + fmt.Sprintf(`
resource "ankra_cluster_stack_variable" "domain" {
  cluster_id = %q
  stack_name = "platform"
  name       = "ingress_domain"
  value      = "apps.example.com"
}
`, clusterConfigurationTestCluster)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{Config: configuration},
			{
				// The stack was deleted: its variables went with it, so the
				// key reads as gone and the plan recreates it.
				PreConfig:          func() { delete(fake.stacks, "platform") },
				Config:             configuration,
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
			{
				// Creating the key on a stack the cluster does not have fails.
				Config:      configuration,
				ExpectError: regexp.MustCompile(`Stack not found in this cluster`),
			},
			{
				// A stack being deleted refuses writes; destroy treats the key
				// as gone with it.
				PreConfig: func() {
					fake.stacks["platform"] = map[string]string{}
					fake.deletePending["platform"] = false
				},
				Config: configuration,
			},
			{
				PreConfig: func() { fake.deletePending["platform"] = true },
				Config:    configuration,
				Destroy:   true,
			},
		},
	})
}

func TestAccClusterStackVariableClusterOutOfScope(t *testing.T) {
	fake := newStackVariablesFake(t)
	configuration := fake.providerBlock() + fmt.Sprintf(`
resource "ankra_cluster_stack_variable" "domain" {
  cluster_id = %q
  stack_name = "platform"
  name       = "ingress_domain"
  value      = "apps.example.com"
}
`, clusterConfigurationTestCluster)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{Config: configuration},
			{
				// Out of the provider's scope: an error, never a silent drop.
				PreConfig:   func() { fake.isClusterHidden = true },
				Config:      configuration,
				ExpectError: regexp.MustCompile(`(?s)not visible to this token.*set project_id`),
			},
			{
				PreConfig: func() { fake.isClusterHidden = false },
				Config:    configuration,
				Check: func(_ *terraform.State) error {
					if fake.stacks["platform"]["ingress_domain"] != "apps.example.com" {
						return fmt.Errorf("the key was lost while out of scope: %v", fake.stacks["platform"])
					}
					return nil
				},
			},
		},
	})
}

func TestAccClusterStackVariableNameValidation(t *testing.T) {
	fake := newStackVariablesFake(t)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config: fake.providerBlock() + fmt.Sprintf(`
resource "ankra_cluster_stack_variable" "domain" {
  cluster_id = %q
  stack_name = "platform"
  name       = "ingress-domain"
  value      = "apps.example.com"
}
`, clusterConfigurationTestCluster),
			ExpectError: regexp.MustCompile(`letters, digits and underscores`),
		}},
	})
	if len(fake.served()) != 0 {
		t.Errorf("an invalid name reached the platform: %v", fake.served())
	}
}
