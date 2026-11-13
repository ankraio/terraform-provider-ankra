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

// organisationPolicyFake mirrors the pipeline policy route
// (go/internal/pipelinesapi) and the organisation provenance policy route:
// the pipeline policy PUT changes only the settings it names and refuses an
// empty body, and the provenance PUT stores a mode or clears it with null.
type organisationPolicyFake struct {
	*routeFake
	pipelinePolicy map[string]any
	pipelinePuts   []map[string]any
	provenanceMode any
	denied         bool
}

func newOrganisationPolicyFake(t *testing.T) *organisationPolicyFake {
	fake := &organisationPolicyFake{routeFake: newRouteFake(t), pipelinePolicy: map[string]any{
		"allowed_image_prefixes": []string{"ghcr.io/ankraio/"}, "allowed_network_tiers": []string{"egress-https"},
		"allowed_credentials": []string{}, "allowed_node_selectors": map[string]any{}, "allowed_tolerations": []any{},
		"allowed_runtime_classes": []string{}, "max_cpu": "4", "max_memory": "8Gi", "max_cache_size": "20Gi", "max_gpu": 0,
		"max_agent_tool_calls_per_run": 200, "webhook_allowed_hosts": []string{}, "external_providers": []string{},
		"updated_by": "", "updated_at": nil,
	}}
	fake.handle("GET /api/v1/org/pipeline-policy", func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(t, writer, http.StatusOK, fake.pipelinePolicy)
	})
	fake.handle("PUT /api/v1/org/pipeline-policy", func(writer http.ResponseWriter, request *http.Request) {
		if fake.denied {
			writeJSON(t, writer, http.StatusForbidden, map[string]any{"detail": "pipelines.manage is required"})
			return
		}
		var body map[string]any
		decodeJSON(t, request, &body)
		if len(body) == 0 {
			writeJSON(t, writer, http.StatusUnprocessableEntity, map[string]any{"detail": "name at least one setting"})
			return
		}
		fake.pipelinePuts = append(fake.pipelinePuts, body)
		for key, value := range body {
			fake.pipelinePolicy[key] = value
		}
		fake.pipelinePolicy["updated_by"] = "terraform"
		writeJSON(t, writer, http.StatusOK, fake.pipelinePolicy)
	})
	fake.handle("GET /api/v1/org/provenance/policy", func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(t, writer, http.StatusOK, map[string]any{"mode": fake.provenanceMode})
	})
	fake.handle("PUT /api/v1/org/provenance/policy", func(writer http.ResponseWriter, request *http.Request) {
		if fake.denied {
			writeJSON(t, writer, http.StatusForbidden, map[string]any{"detail": "security.manage is required"})
			return
		}
		var body map[string]any
		decodeJSON(t, request, &body)
		fake.provenanceMode = body["mode"]
		writeJSON(t, writer, http.StatusOK, map[string]any{"mode": fake.provenanceMode})
	})
	return fake
}

func TestAccPipelinePolicyLifecycle(t *testing.T) {
	fake := newOrganisationPolicyFake(t)
	config := func(maxCPU string) string {
		return fake.providerBlock() + fmt.Sprintf(`
resource "ankra_pipeline_policy" "ceiling" {
  allowed_network_tiers  = ["egress-https", "egress-all"]
  allowed_node_selectors = { pool = ["ci", "gpu"] }
  allowed_tolerations    = [{ key = "gpu", operator = "Exists", value = "", effect = "NoSchedule" }]
  max_cpu                = %q
}
`, maxCPU)
	}
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy: func(_ *terraform.State) error {
			if fake.pipelinePolicy["max_cpu"] != "8" {
				return fmt.Errorf("destroy must leave the policy as it is")
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: config("6"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ankra_pipeline_policy.ceiling", "max_memory", "8Gi"),
					resource.TestCheckResourceAttr("ankra_pipeline_policy.ceiling", "allowed_image_prefixes.#", "1"),
					resource.TestCheckResourceAttr("ankra_pipeline_policy.ceiling", "allowed_node_selectors.pool.#", "2"),
					resource.TestCheckResourceAttr("ankra_pipeline_policy.ceiling", "updated_by", "terraform"),
					func(_ *terraform.State) error {
						if _, sent := fake.pipelinePuts[0]["max_memory"]; sent {
							return fmt.Errorf("the create must write only the configured settings, wrote %v", fake.pipelinePuts[0])
						}
						if _, sent := fake.pipelinePuts[0]["allowed_image_prefixes"]; sent {
							return fmt.Errorf("allowed_image_prefixes belongs to the CI settings and must never be written")
						}
						return nil
					},
				),
			},
			{
				Config: config("8"),
				Check:  resource.TestCheckResourceAttr("ankra_pipeline_policy.ceiling", "max_cpu", "8"),
			},
			{
				ResourceName:      "ankra_pipeline_policy.ceiling",
				ImportState:       true,
				ImportStateId:     "pipeline_policy",
				ImportStateVerify: true,
			},
		},
	})
}

func TestAccPipelinePolicyRefused(t *testing.T) {
	fake := newOrganisationPolicyFake(t)
	fake.denied = true
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: fake.providerBlock() + `
resource "ankra_pipeline_policy" "ceiling" {
  max_gpu = 1
}
`,
				ExpectError: regexp.MustCompile(`(?s)Unable to set the pipeline policy.*pipelines.manage is required`),
			},
		},
	})
}

func TestAccProvenancePolicyLifecycle(t *testing.T) {
	fake := newOrganisationPolicyFake(t)
	config := func(mode string) string {
		return fake.providerBlock() + fmt.Sprintf(`
resource "ankra_provenance_policy" "default" {
  mode = %q
}
`, mode)
	}
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy: func(_ *terraform.State) error {
			if fake.provenanceMode != "enforce" {
				return fmt.Errorf("destroy must leave the mode as it is, found %v", fake.provenanceMode)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{Config: config("warn"), Check: resource.TestCheckResourceAttr("ankra_provenance_policy.default", "mode", "warn")},
			{Config: config("enforce"), Check: resource.TestCheckResourceAttr("ankra_provenance_policy.default", "id", "provenance_policy")},
			{ResourceName: "ankra_provenance_policy.default", ImportState: true, ImportStateId: "provenance_policy", ImportStateVerify: true},
			{
				PreConfig: func() { fake.provenanceMode = nil },
				Config:    config("enforce"),
				Check: func(_ *terraform.State) error {
					if fake.provenanceMode != "enforce" {
						return fmt.Errorf("a mode cleared outside Terraform must be set again")
					}
					return nil
				},
			},
			{Config: config("strict"), ExpectError: regexp.MustCompile("Invalid value")},
		},
	})
}
