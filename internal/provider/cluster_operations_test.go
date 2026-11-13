// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

const (
	operationsFakeHetznerID  = "c0000000-0000-4000-8000-000000000001"
	operationsFakeImportedID = "c0000000-0000-4000-8000-000000000002"
	operationsFakeUpcloudID  = "c0000000-0000-4000-8000-000000000003"
	operationsFakeOVHID      = "c0000000-0000-4000-8000-000000000004"
)

// clusterOperationsFake mirrors the cluster operation routes: the cluster
// listing names each cluster's kind and environment, the lane routes answer
// "Cluster not found" for a cluster of another lane, and the Gateway API can
// only be turned on.
type clusterOperationsFake struct {
	*routeFake
	environments   map[string]string
	criticality    string
	retentionDays  int
	zoneState      string
	gatewayEnabled bool
	k3sComponents  []string
	k3sWrites      int
	loadBalancer   any
	gatewayPlans   []string
	zones          []string
	ovhModels      []string
	hasMetrics     bool
}

func newClusterOperationsFake(t *testing.T) *clusterOperationsFake {
	fake := &clusterOperationsFake{routeFake: newRouteFake(t), environments: map[string]string{}, criticality: "production", retentionDays: 7,
		zoneState: "none", k3sComponents: []string{}, zones: []string{"fi-hel1", "fi-hel2", "de-fra1"}, hasMetrics: true}
	kinds := map[string]string{operationsFakeHetznerID: "hetzner", operationsFakeImportedID: "imported", operationsFakeUpcloudID: "upcloud", operationsFakeOVHID: "ovh"}
	fake.handle("GET /api/v1/clusters", func(writer http.ResponseWriter, request *http.Request) {
		wanted := request.URL.Query().Get("cluster_id")
		rows := []map[string]any{}
		for id, kind := range kinds {
			if wanted == "" || wanted == id {
				row := map[string]any{"id": id, "name": "cluster-" + kind, "kind": kind, "state": "running"}
				if environment, set := fake.environments[id]; set {
					row["environment"] = environment
				}
				rows = append(rows, row)
			}
		}
		writeJSON(t, writer, http.StatusOK, map[string]any{"result": rows, "pagination": map[string]any{"total_count": len(rows), "total_pages": 1, "page": 1, "page_size": 100}})
	})
	inLane := func(writer http.ResponseWriter, request *http.Request) bool {
		if kinds[request.PathValue("cluster_id")] != request.PathValue("lane") {
			writeJSON(t, writer, http.StatusNotFound, map[string]any{"detail": "Cluster not found"})
			return false
		}
		return true
	}
	fake.handle("PATCH /api/v1/org/clusters/imported/{cluster_id}/environment", func(writer http.ResponseWriter, request *http.Request) {
		var body map[string]any
		decodeJSON(t, request, &body)
		fake.environments[request.PathValue("cluster_id")], _ = body["environment"].(string)
		answer := map[string]any{"environment": body["environment"]}
		if criticality, set := body["criticality"].(string); set {
			fake.criticality = criticality
			answer["criticality"] = criticality
		}
		writeJSON(t, writer, http.StatusOK, answer)
	})
	fake.handle("GET /api/v1/org/clusters/{cluster_id}/criticality", func(writer http.ResponseWriter, request *http.Request) {
		writeJSON(t, writer, http.StatusOK, map[string]any{"cluster_id": request.PathValue("cluster_id"), "criticality": fake.criticality})
	})
	retentionView := func(clusterID string) map[string]any {
		return map[string]any{"cluster_id": clusterID, "retention_days": fake.retentionDays, "is_default": fake.retentionDays == 7,
			"default_retention_days": 7, "minimum_retention_days": 1, "maximum_retention_days": 90, "keep_latest": 4}
	}
	fake.handle("GET /api/v1/org/clusters/{cluster_id}/state-snapshot-retention", func(writer http.ResponseWriter, request *http.Request) {
		writeJSON(t, writer, http.StatusOK, retentionView(request.PathValue("cluster_id")))
	})
	fake.handle("PUT /api/v1/org/clusters/{cluster_id}/state-snapshot-retention", func(writer http.ResponseWriter, request *http.Request) {
		var body map[string]any
		decodeJSON(t, request, &body)
		days, _ := body["retention_days"].(float64)
		fake.retentionDays = int(days)
		writeJSON(t, writer, http.StatusOK, retentionView(request.PathValue("cluster_id")))
	})
	zoneView := func() map[string]any {
		fqdn := ""
		if fake.zoneState != "none" {
			fqdn = "prod.acme.ankra.cc"
		}
		return map[string]any{"success": true, "fqdn": fqdn, "state": fake.zoneState}
	}
	fake.handle("GET /api/v1/clusters/{cluster_id}/dns-zone", func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(t, writer, http.StatusOK, zoneView())
	})
	fake.handle("POST /api/v1/clusters/{cluster_id}/dns-zone", func(writer http.ResponseWriter, _ *http.Request) {
		fake.zoneState = "pending"
		writeJSON(t, writer, http.StatusOK, zoneView())
	})
	fake.handle("DELETE /api/v1/clusters/{cluster_id}/dns-zone", func(writer http.ResponseWriter, _ *http.Request) {
		fake.zoneState = "none"
		writeJSON(t, writer, http.StatusOK, zoneView())
	})
	gatewayView := func() map[string]any {
		phase := "off"
		if fake.gatewayEnabled {
			phase = "crds_pending"
		}
		return map[string]any{"available": true, "unavailable_reason": "", "enabled": fake.gatewayEnabled, "phase": phase}
	}
	fake.handle("GET /api/v1/clusters/{lane}/{cluster_id}/gateway-api", func(writer http.ResponseWriter, request *http.Request) {
		if inLane(writer, request) {
			writeJSON(t, writer, http.StatusOK, gatewayView())
		}
	})
	fake.handle("PUT /api/v1/clusters/{lane}/{cluster_id}/gateway-api", func(writer http.ResponseWriter, request *http.Request) {
		if inLane(writer, request) {
			fake.gatewayEnabled = true
			writeJSON(t, writer, http.StatusOK, gatewayView())
		}
	})
	fake.handle("GET /api/v1/clusters/{lane}/{cluster_id}/k3s-disabled-components", func(writer http.ResponseWriter, request *http.Request) {
		if inLane(writer, request) {
			writeJSON(t, writer, http.StatusOK, map[string]any{"k3s_disabled_components": fake.k3sComponents, "resource_ids": []string{}, "control_plane_count": 3, "applies": "rolling"})
		}
	})
	fake.handle("PUT /api/v1/clusters/{lane}/{cluster_id}/k3s-disabled-components", func(writer http.ResponseWriter, request *http.Request) {
		if !inLane(writer, request) {
			return
		}
		var body struct {
			Components []string `json:"k3s_disabled_components"`
		}
		decodeJSON(t, request, &body)
		fake.k3sComponents = body.Components
		fake.k3sWrites++
		writeJSON(t, writer, http.StatusOK, map[string]any{"k3s_disabled_components": fake.k3sComponents, "resource_ids": []string{}, "control_plane_count": 3, "applies": "rolling"})
	})
	fake.handle("GET /api/v1/clusters/upcloud/{cluster_id}/load-balancer-plan", func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(t, writer, http.StatusOK, map[string]any{"load_balancer_plan": fake.loadBalancer, "managed": true, "load_balancers_observed": false,
			"load_balancers": nil, "observation_error": nil, "available_plans": nil})
	})
	fake.handle("PUT /api/v1/clusters/upcloud/{cluster_id}/load-balancer-plan", func(writer http.ResponseWriter, request *http.Request) {
		var body map[string]any
		decodeJSON(t, request, &body)
		previous := fake.loadBalancer
		fake.loadBalancer = body["load_balancer_plan"]
		writeJSON(t, writer, http.StatusOK, map[string]any{"load_balancer_plan": fake.loadBalancer, "previous_load_balancer_plan": previous, "changed": true, "resource_ids": []string{}})
	})
	fake.handle("PUT /api/v1/clusters/upcloud/{cluster_id}/gateway-plan", func(writer http.ResponseWriter, request *http.Request) {
		var body map[string]any
		decodeJSON(t, request, &body)
		plan, _ := body["gateway_plan"].(string)
		fake.gatewayPlans = append(fake.gatewayPlans, plan)
		writeJSON(t, writer, http.StatusAccepted, map[string]any{"gateway_plan": plan, "plan_change_request_id": "r", "resource_ids": []string{},
			"deferred_resource_ids": []string{}, "notice": "outbound access is interrupted"})
	})
	fake.handle("PUT /api/v1/clusters/upcloud/{cluster_id}/zones", func(writer http.ResponseWriter, request *http.Request) {
		var body struct {
			Zones []string `json:"zones"`
		}
		decodeJSON(t, request, &body)
		var added []string
		for _, zone := range body.Zones {
			if !slices.Contains(fake.zones, zone) {
				fake.zones = append(fake.zones, zone)
				added = append(added, zone)
			}
		}
		writeJSON(t, writer, http.StatusOK, map[string]any{"zones": fake.zones, "added_zones": added})
	})
	fake.handle("PUT /api/v1/clusters/ovh/{cluster_id}/gateway-model", func(writer http.ResponseWriter, request *http.Request) {
		var body map[string]any
		decodeJSON(t, request, &body)
		model, _ := body["gateway_model"].(string)
		fake.ovhModels = append(fake.ovhModels, model)
		writeJSON(t, writer, http.StatusOK, map[string]any{"gateway_model": model, "resource_ids": []string{}})
	})
	fake.handle("GET /api/v1/org/clusters/imported/{cluster_id}/metrics-source", func(writer http.ResponseWriter, _ *http.Request) {
		if !fake.hasMetrics {
			writeJSON(t, writer, http.StatusNotFound, map[string]any{"detail": "This cluster has no metrics source."})
			return
		}
		writeJSON(t, writer, http.StatusOK, map[string]any{"provider": "prometheus", "endpoint": "https://mimir.example/prometheus", "state": "connected",
			"flavor": "mimir", "credential_name": "mimir-reader", "credential": map[string]any{"name": "mimir-reader", "state": "ok", "username": "reader"}})
	})
	fake.handle("GET /api/v1/org/clusters/imported/{cluster_id}/log-source", func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(t, writer, http.StatusNotFound, map[string]any{"detail": "This cluster has no log source."})
	})
	return fake
}

func TestAccClusterEnvironmentAndRetention(t *testing.T) {
	fake := newClusterOperationsFake(t)
	config := func(environment string, days int) string {
		return fake.providerBlock() + fmt.Sprintf(`
resource "ankra_cluster_environment" "prod" {
  cluster_id  = %[1]q
  environment = %[2]q
}

resource "ankra_cluster_state_snapshot_retention" "prod" {
  cluster_id     = %[1]q
  retention_days = %[3]d
}
`, operationsFakeImportedID, environment, days)
	}
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy: func(_ *terraform.State) error {
			if fake.environments[operationsFakeImportedID] != "production" || fake.retentionDays != 30 {
				return fmt.Errorf("destroy must leave the environment and the retention window as they are")
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: config("staging", 14),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ankra_cluster_environment.prod", "criticality", "production"),
					resource.TestCheckResourceAttr("ankra_cluster_state_snapshot_retention.prod", "is_default", "false"),
					resource.TestCheckResourceAttr("ankra_cluster_state_snapshot_retention.prod", "keep_latest", "4"),
				),
			},
			{
				Config: config("production", 30),
				Check:  resource.TestCheckResourceAttr("ankra_cluster_environment.prod", "environment", "production"),
			},
			{
				ResourceName:      "ankra_cluster_environment.prod",
				ImportState:       true,
				ImportStateId:     operationsFakeImportedID,
				ImportStateVerify: true,
			},
			{
				PreConfig: func() { fake.environments[operationsFakeImportedID] = "sandbox" },
				Config:    config("production", 30),
				Check: func(_ *terraform.State) error {
					if fake.environments[operationsFakeImportedID] != "production" {
						return fmt.Errorf("an environment changed outside Terraform must be set back")
					}
					return nil
				},
			},
			{Config: config("production", 120), ExpectError: regexp.MustCompile("Value out of range")},
			{Config: config("production", 30)},
		},
	})
}

func TestAccClusterDNSZoneGatewayAPIAndK3s(t *testing.T) {
	fake := newClusterOperationsFake(t)
	config := func(components string) string {
		return fake.providerBlock() + fmt.Sprintf(`
resource "ankra_cluster_dns_zone" "prod" {
  cluster_id = %[1]q
}

resource "ankra_cluster_gateway_api" "prod" {
  cluster_id = %[1]q
}

resource "ankra_cluster_k3s_disabled_components" "prod" {
  cluster_id = %[1]q
  components = %[2]s
}
`, operationsFakeHetznerID, components)
	}
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy: func(_ *terraform.State) error {
			if fake.zoneState != "none" {
				return fmt.Errorf("destroy must hand the DNS zone back")
			}
			if !fake.gatewayEnabled || !slices.Contains(fake.k3sComponents, "servicelb") {
				return fmt.Errorf("destroy must leave the Gateway API and the k3s components as they are")
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: config(`["traefik"]`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ankra_cluster_dns_zone.prod", "fqdn", "prod.acme.ankra.cc"),
					resource.TestCheckResourceAttr("ankra_cluster_gateway_api.prod", "cluster_kind", "hetzner"),
					resource.TestCheckResourceAttr("ankra_cluster_gateway_api.prod", "phase", "crds_pending"),
					resource.TestCheckResourceAttr("ankra_cluster_k3s_disabled_components.prod", "components.#", "1"),
				),
			},
			{
				Config: config(`["traefik", "servicelb"]`),
				Check: func(_ *terraform.State) error {
					if fake.k3sWrites != 2 {
						return fmt.Errorf("a changed list must be written, written %d times", fake.k3sWrites)
					}
					return nil
				},
			},
			{
				ResourceName:      "ankra_cluster_k3s_disabled_components.prod",
				ImportState:       true,
				ImportStateId:     operationsFakeHetznerID,
				ImportStateVerify: true,
			},
			{
				Config:      config(`["traefik", "nginx"]`),
				ExpectError: regexp.MustCompile("Invalid value"),
			},
			{
				Config: fake.providerBlock() + fmt.Sprintf(`
resource "ankra_cluster_gateway_api" "imported" {
  cluster_id = %q
}
`, operationsFakeImportedID),
				ExpectError: regexp.MustCompile(`(?s)Cluster kind not served.*"imported"`),
			},
		},
	})
}

func TestAccProviderClusterSettings(t *testing.T) {
	fake := newClusterOperationsFake(t)
	config := func(gatewayPlan, zones, model string) string {
		return fake.providerBlock() + fmt.Sprintf(`
resource "ankra_upcloud_cluster_settings" "prod" {
  cluster_id         = %[1]q
  load_balancer_plan = "production-small"
  gateway_plan       = %[2]q
  zones              = %[3]s
}

resource "ankra_ovh_cluster_settings" "prod" {
  cluster_id    = %[4]q
  gateway_model = %[5]q
}
`, operationsFakeUpcloudID, gatewayPlan, zones, operationsFakeOVHID, model)
	}
	pool := `["fi-hel1", "fi-hel2", "de-fra1"]`
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: config("standard", pool, "s"),
				Check:  resource.TestCheckResourceAttr("ankra_upcloud_cluster_settings.prod", "load_balancer_plan", "production-small"),
			},
			{
				Config: config("standard", `["fi-hel1", "fi-hel2", "de-fra1", "nl-ams1"]`, "m"),
				Check: func(_ *terraform.State) error {
					if len(fake.gatewayPlans) != 1 || len(fake.zones) != 4 || len(fake.ovhModels) != 2 {
						return fmt.Errorf("only changed settings may be written: gateway plans %v, zones %v, models %v", fake.gatewayPlans, fake.zones, fake.ovhModels)
					}
					return nil
				},
			},
			{
				Config:      config("standard", pool, "m"),
				ExpectError: regexp.MustCompile(`(?s)Zones cannot be removed.*nl-ams1`),
			},
			{
				Config: fake.providerBlock() + fmt.Sprintf(`
resource "ankra_ovh_cluster_settings" "wrong" {
  cluster_id    = %q
  gateway_model = "m"
}
`, operationsFakeUpcloudID),
				ExpectError: regexp.MustCompile("Cluster kind not served"),
			},
		},
	})
}

func TestAccClusterMetricsSourceDataSource(t *testing.T) {
	fake := newClusterOperationsFake(t)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: fake.providerBlock() + fmt.Sprintf(`
data "ankra_cluster_metrics_source" "prod" {
  cluster_id = %q
}
`, operationsFakeImportedID),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.ankra_cluster_metrics_source.prod", "metrics.flavor", "mimir"),
					resource.TestCheckResourceAttr("data.ankra_cluster_metrics_source.prod", "metrics.credential_name", "mimir-reader"),
					resource.TestCheckNoResourceAttr("data.ankra_cluster_metrics_source.prod", "logs.provider_name"),
				),
			},
		},
	})
}
