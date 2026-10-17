// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

const (
	deployFakeClusterID     = "22222222-2222-4222-8222-222222222222"
	deployFakeApplicationID = "33333333-3333-4333-8333-333333333333"
)

// deployServiceFake stands in for the deploy and service admission routes:
// a join token is shown once (201) and never read back, the placement
// policy GET answers 409 for an undeclared cluster and every PUT is a
// compare-and-swap on the revision, and consumer bindings are created or
// updated by POST (200) at an expected revision and removed by DELETE (204)
// at the revision last read.
type deployServiceFake struct {
	*routeFake
	joinTokenBodies []map[string]json.RawMessage
	minted          int
	policy          map[string]any
	consumers       map[string]map[string]any
	consumerBodies  []map[string]any
	// isApplicationsOff makes every service admission route answer the
	// platform's bare 404 for an organisation without the feature.
	isApplicationsOff bool
}

// handleAdmission registers a service admission route, which answers a bare
// 404 "Not Found" while the organisation's applications feature is off.
func (fake *deployServiceFake) handleAdmission(pattern string, handler http.HandlerFunc) {
	fake.handle(pattern, func(writer http.ResponseWriter, request *http.Request) {
		if fake.isApplicationsOff {
			writeJSON(fake.t, writer, http.StatusNotFound, map[string]any{"detail": "Not Found"})
			return
		}
		handler(writer, request)
	})
}

// platformServicesText reads a decoded JSON value as a string; anything else
// reads as empty, which the fakes then treat as an unknown key or id.
func platformServicesText(value any) string {
	text, _ := value.(string)
	return text
}

func newDeployServiceFake(t *testing.T) *deployServiceFake {
	fake := &deployServiceFake{routeFake: newRouteFake(t), consumers: map[string]map[string]any{}}

	fake.handle("POST /api/v1/org/environments/{environment}/host-join-tokens", func(writer http.ResponseWriter, request *http.Request) {
		var body map[string]json.RawMessage
		decodeJSON(t, request, &body)
		fake.joinTokenBodies = append(fake.joinTokenBodies, body)
		fake.minted++
		writeJSON(t, writer, http.StatusCreated, map[string]any{
			"join_token":  fmt.Sprintf("ahjt_secret_%d", fake.minted),
			"expires_at":  fmt.Sprintf("2030-01-01T1%d:00:00Z", fake.minted),
			"environment": request.PathValue("environment"),
		})
	})
	fake.handle("GET /api/v1/org/environments", func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(t, writer, http.StatusOK, map[string]any{"truncated": false, "environments": []map[string]any{
			{"id": "44444444-4444-4444-8444-444444444444", "name": "edge", "created_by": nil,
				"created_at": "2026-10-17T00:00:00Z", "updated_at": "2026-10-17T00:00:00Z"},
		}})
	})
	fake.handle("GET /api/v1/org/host-targets", func(writer http.ResponseWriter, request *http.Request) {
		if environment := request.URL.Query().Get("environment"); environment != "" && environment != "edge" {
			writeJSON(t, writer, http.StatusNotFound, map[string]any{"detail": "Environment not found"})
			return
		}
		writeJSON(t, writer, http.StatusOK, map[string]any{"truncated": false, "host_targets": []map[string]any{
			{"id": "55555555-5555-4555-8555-555555555555", "name": "kiosk-1", "environment": "edge",
				"environment_id": "44444444-4444-4444-8444-444444444444", "labels": map[string]string{"site": "oslo"},
				"status": "unknown", "agent_version": nil, "hostname": nil, "os": nil, "arch": nil,
				"supported_job_types": nil, "policy": nil, "running": nil, "last_heartbeat_at": nil,
				"registered_at": "2026-10-17T00:00:00Z", "revoked_at": nil, "revoked_by": nil, "created_by": nil,
				"created_at": "2026-10-17T00:00:00Z", "updated_at": "2026-10-17T00:00:00Z"},
		}})
	})

	policyPath := "/api/v1/org/service-admission/cluster-policies/{cluster}"
	fake.handleAdmission("GET "+policyPath, func(writer http.ResponseWriter, _ *http.Request) {
		if fake.policy == nil {
			writeJSON(t, writer, http.StatusConflict, map[string]any{"detail": "Configure the cluster data policy before connecting services"})
			return
		}
		writeJSON(t, writer, http.StatusOK, fake.policy)
	})
	fake.handleAdmission("PUT "+policyPath, func(writer http.ResponseWriter, request *http.Request) {
		var body map[string]any
		decodeJSON(t, request, &body)
		revision := float64(0)
		if fake.policy != nil {
			revision, _ = fake.policy["revision"].(float64)
		}
		if body["expected_revision"] != revision {
			writeJSON(t, writer, http.StatusConflict, map[string]any{"detail": "conflict"})
			return
		}
		fake.policy = map[string]any{"cluster_id": request.PathValue("cluster"), "region": body["region"],
			"data_boundary": body["data_boundary"], "local_only": body["local_only"], "revision": revision + 1,
			"source": "declared", "updated_at": "2026-10-17T00:00:00Z"}
		writeJSON(t, writer, http.StatusOK, fake.policy)
	})

	fake.handleAdmission("POST /api/v1/org/service-admission/consumers", func(writer http.ResponseWriter, request *http.Request) {
		var body map[string]any
		decodeJSON(t, request, &body)
		fake.consumerBodies = append(fake.consumerBodies, body)
		var existing map[string]any
		for _, consumer := range fake.consumers {
			if consumer["application_id"] == body["application_id"] && consumer["cluster_id"] == body["cluster_id"] && consumer["namespace"] == body["namespace"] {
				existing = consumer
			}
		}
		revision := float64(0)
		if existing != nil {
			revision, _ = existing["binding_revision"].(float64)
		}
		if body["expected_revision"] != revision {
			writeJSON(t, writer, http.StatusConflict, map[string]any{"detail": "conflict"})
			return
		}
		if existing == nil {
			existing = map[string]any{"id": fmt.Sprintf("66666666-6666-4666-8666-%012d", len(fake.consumers)+1),
				"application_id": body["application_id"], "organisation_id": "77777777-7777-4777-8777-777777777777",
				"cluster_id": body["cluster_id"], "namespace": body["namespace"], "region": "eu-north-1",
				"data_boundary": "eu", "policy_revision": float64(1), "installation_id": nil}
			fake.consumers[platformServicesText(existing["id"])] = existing
		}
		existing["local_only"] = body["local_only"]
		existing["binding_kind"] = "planned"
		existing["binding_revision"] = revision + 1
		writeJSON(t, writer, http.StatusOK, existing)
	})
	fake.handleAdmission("GET /api/v1/org/service-admission/consumers/{id}", func(writer http.ResponseWriter, request *http.Request) {
		consumer, exists := fake.consumers[request.PathValue("id")]
		if !exists {
			writeJSON(t, writer, http.StatusNotFound, map[string]any{"detail": "Service admission resource not found"})
			return
		}
		writeJSON(t, writer, http.StatusOK, consumer)
	})
	fake.handleAdmission("DELETE /api/v1/org/service-admission/consumers/{id}", func(writer http.ResponseWriter, request *http.Request) {
		consumer, exists := fake.consumers[request.PathValue("id")]
		if !exists {
			writeJSON(t, writer, http.StatusNotFound, map[string]any{"detail": "Service admission resource not found"})
			return
		}
		expected, err := strconv.ParseFloat(request.URL.Query().Get("expected_revision"), 64)
		if err != nil || expected != consumer["binding_revision"] {
			writeJSON(t, writer, http.StatusConflict, map[string]any{"detail": "conflict"})
			return
		}
		delete(fake.consumers, request.PathValue("id"))
		writer.WriteHeader(http.StatusNoContent)
	})
	return fake
}

func TestAccDeployHostJoinToken(t *testing.T) {
	fake := newDeployServiceFake(t)

	configuration := func(ttlLine string) string {
		return fake.providerBlock() + fmt.Sprintf(`
resource "ankra_host_join_token" "edge" {
  environment = "edge"
  %s
}

data "ankra_host_targets" "edge" {
  environment = "edge"
}

data "ankra_deploy_environments" "all" {}
`, ttlLine)
	}

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: configuration(""),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ankra_host_join_token.edge", "join_token", "ahjt_secret_1"),
					resource.TestCheckResourceAttr("ankra_host_join_token.edge", "expires_at", "2030-01-01T11:00:00Z"),
					resource.TestCheckResourceAttr("data.ankra_host_targets.edge", "host_targets.#", "1"),
					resource.TestCheckResourceAttr("data.ankra_host_targets.edge", "host_targets.0.labels.site", "oslo"),
					resource.TestCheckNoResourceAttr("data.ankra_host_targets.edge", "host_targets.0.hostname"),
					resource.TestCheckResourceAttr("data.ankra_deploy_environments.all", "environments.0.name", "edge"),
					func(_ *terraform.State) error {
						if _, present := fake.joinTokenBodies[0]["ttl_seconds"]; present {
							return fmt.Errorf("an unset ttl was sent: %s", fake.joinTokenBodies[0]["ttl_seconds"])
						}
						return nil
					},
				),
			},
			{
				// The token is never read back, so a refresh keeps it.
				Config:   configuration(""),
				PlanOnly: true,
			},
			{
				Config: configuration("ttl_seconds = 600"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ankra_host_join_token.edge", "join_token", "ahjt_secret_2"),
					func(_ *terraform.State) error {
						if string(fake.joinTokenBodies[1]["ttl_seconds"]) != "600" {
							return fmt.Errorf("ttl sent as %s", fake.joinTokenBodies[1]["ttl_seconds"])
						}
						return nil
					},
				),
			},
			{
				ResourceName:  "ankra_host_join_token.edge",
				ImportState:   true,
				ImportStateId: "edge",
				ExpectError:   regexp.MustCompile("Import not supported"),
			},
		},
	})
}

func TestAccDeployHostJoinTokenRefusesLongLifetime(t *testing.T) {
	fake := newDeployServiceFake(t)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config: fake.providerBlock() + `
resource "ankra_host_join_token" "edge" {
  environment = "Edge_Hosts"
  ttl_seconds = 90000
}
`,
			PlanOnly:    true,
			ExpectError: regexp.MustCompile("(Value out of range|Invalid environment name)"),
		}},
	})
	if served := fake.served(); len(served) != 0 {
		t.Errorf("a refused plan sent %v", served)
	}
}

func TestAccServicePlacementPolicyAndConsumer(t *testing.T) {
	fake := newDeployServiceFake(t)

	configuration := func(region string, localOnly bool) string {
		return fake.providerBlock() + fmt.Sprintf(`
resource "ankra_service_placement_policy" "prod" {
  cluster_id    = %q
  region        = %q
  data_boundary = "eu"
}

resource "ankra_service_consumer" "orders" {
  application_id = %q
  cluster_id     = ankra_service_placement_policy.prod.cluster_id
  namespace      = "orders"
  allow_planned  = true
  local_only     = %t
}
`, deployFakeClusterID, region, deployFakeApplicationID, localOnly)
	}

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: configuration("eu-north-1", false),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ankra_service_placement_policy.prod", "revision", "1"),
					resource.TestCheckResourceAttr("ankra_service_consumer.orders", "binding_revision", "1"),
					resource.TestCheckResourceAttr("ankra_service_consumer.orders", "binding_kind", "planned"),
					resource.TestCheckNoResourceAttr("ankra_service_consumer.orders", "installation_id"),
					func(_ *terraform.State) error {
						body := fake.consumerBodies[0]
						if body["expected_revision"] != float64(0) || body["allow_planned"] != true {
							return fmt.Errorf("bind sent %v", body)
						}
						return nil
					},
				),
			},
			{
				// Updates carry the revision last read.
				Config: configuration("eu-west-1", true),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ankra_service_placement_policy.prod", "region", "eu-west-1"),
					resource.TestCheckResourceAttr("ankra_service_placement_policy.prod", "revision", "2"),
					resource.TestCheckResourceAttr("ankra_service_consumer.orders", "local_only", "true"),
					resource.TestCheckResourceAttr("ankra_service_consumer.orders", "binding_revision", "2"),
					func(_ *terraform.State) error {
						body := fake.consumerBodies[len(fake.consumerBodies)-1]
						if body["expected_revision"] != float64(1) {
							return fmt.Errorf("update sent expected revision %v", body["expected_revision"])
						}
						return nil
					},
				),
			},
			{
				ResourceName:      "ankra_service_placement_policy.prod",
				ImportState:       true,
				ImportStateId:     deployFakeClusterID,
				ImportStateVerify: true,
			},
			{
				ResourceName:            "ankra_service_consumer.orders",
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"allow_planned"},
			},
			{
				// Changed outside Terraform: the next plan restores it at the
				// revision the refresh read.
				PreConfig: func() {
					fake.policy["region"] = "us-east-1"
					fake.policy["revision"] = float64(3)
				},
				Config: configuration("eu-west-1", true),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ankra_service_placement_policy.prod", "revision", "4"),
					func(_ *terraform.State) error {
						if fake.policy["region"] != "eu-west-1" {
							return fmt.Errorf("region not restored: %v", fake.policy["region"])
						}
						return nil
					},
				),
			},
			{
				// The binding removed outside Terraform is bound again.
				PreConfig: func() { fake.consumers = map[string]map[string]any{} },
				Config:    configuration("eu-west-1", true),
				Check: func(_ *terraform.State) error {
					if len(fake.consumers) != 1 {
						return fmt.Errorf("binding not recreated: %v", fake.consumers)
					}
					return nil
				},
			},
		},
		CheckDestroy: func(_ *terraform.State) error {
			if len(fake.consumers) != 0 {
				return fmt.Errorf("destroy left bindings %v", fake.consumers)
			}
			// The platform cannot withdraw a declaration: destroy leaves it.
			if fake.policy == nil {
				return fmt.Errorf("the policy should survive destroy")
			}
			return nil
		},
	})
}

func TestAccServicePlacementPolicyRefusesUnspecified(t *testing.T) {
	fake := newDeployServiceFake(t)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config: fake.providerBlock() + fmt.Sprintf(`
resource "ankra_service_placement_policy" "prod" {
  cluster_id    = %q
  region        = "unspecified"
  data_boundary = "EU"
}
`, deployFakeClusterID),
			PlanOnly:    true,
			ExpectError: regexp.MustCompile("Invalid location"),
		}},
	})
	if served := fake.served(); len(served) != 0 {
		t.Errorf("a refused plan sent %v", served)
	}
}
