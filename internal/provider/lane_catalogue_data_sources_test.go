// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"net/http"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

// newLaneCatalogueFake answers the catalogue routes with the bodies the
// platform sends (cluster go/internal/providerapi aws.go, ankracloud.go and
// clusters.go, usecase/providers *_catalogs.go): AWS and Ankra Cloud wrap
// the list in an envelope, Proxmox VE and HPE Morpheus answer a bare array.
// Every route checks the query it was sent, and records it.
func newLaneCatalogueFake(t *testing.T) (*routeFake, map[string]string) {
	fake := newRouteFake(t)
	queries := map[string]string{}
	route := func(pattern string, wantQuery string, body any) {
		fake.handle("GET "+pattern, func(writer http.ResponseWriter, request *http.Request) {
			queries[pattern] = request.URL.RawQuery
			if request.URL.RawQuery != wantQuery {
				writeJSON(t, writer, http.StatusUnprocessableEntity, map[string]any{"detail": "unexpected query " + request.URL.RawQuery})
				return
			}
			writeJSON(t, writer, http.StatusOK, body)
		})
	}

	route("/api/v1/clusters/aws/regions", "credential_id=aws-1", map[string]any{
		"regions":          []any{map[string]any{"slug": "eu-central-1", "name": "Europe (Frankfurt)"}},
		"pricing_complete": true,
	})
	route("/api/v1/clusters/aws/instance-types", "credential_id=aws-1&region=eu-central-1", map[string]any{
		"instance_types": []any{
			map[string]any{"name": "m7i.large", "vcpus": 2, "memory_gib": 8.0, "architecture": "x86_64", "category": "general",
				"hourly_price_usd": 0.1155, "monthly_price_usd": 84.32, "current_generation": true},
			map[string]any{"name": "t2.micro", "vcpus": 1, "memory_gib": 1.0, "architecture": "x86_64", "category": "burstable",
				"hourly_price_usd": nil, "monthly_price_usd": nil, "current_generation": false},
		},
		"pricing_complete": false, "incomplete_reasons": []any{"t2.micro has no price"},
	})
	route("/api/v1/clusters/aws/vpcs", "credential_id=aws-1&region=eu-central-1", map[string]any{
		"vpcs": []any{map[string]any{"id": "vpc-1", "name": "main", "cidr": "10.0.0.0/16", "is_default": false,
			"dhcp_domain_name": nil, "dhcp_domain_name_state": "empty"}},
	})
	route("/api/v1/clusters/aws/subnets", "credential_id=aws-1&region=eu-central-1&vpc_id=vpc-1", map[string]any{
		"subnets": []any{map[string]any{"id": "subnet-1", "name": "private-a", "cidr": "10.0.1.0/24", "availability_zone": "eu-central-1a",
			"map_public_ip_on_launch": false, "egress": map[string]any{"kind": "nat_gateway"}, "foreign_instance_count": nil}},
	})
	route("/api/v1/clusters/proxmox/nodes", "credential_id=pve-1", []any{
		map[string]any{"node": "pve1", "status": "online", "cpu_count": 32, "memory_bytes": int64(137438953472)},
	})
	route("/api/v1/clusters/proxmox/bridges", "credential_id=pve-1&node=pve1", []any{
		map[string]any{"name": "vmbr0", "active": true, "cidr": "192.168.1.0/24", "comments": nil, "source": "bridge"},
		map[string]any{"name": "ankra1", "active": true, "cidr": nil, "comments": nil, "source": "sdn"},
	})
	route("/api/v1/clusters/proxmox/storages", "credential_id=pve-1&node=pve1", []any{
		map[string]any{"storage": "local-lvm", "type": "lvmthin", "content": []any{"images", "rootdir"}, "active": true, "shared": false,
			"available_bytes": int64(500000000000), "total_bytes": int64(1000000000000)},
	})
	route("/api/v1/clusters/proxmox/templates", "credential_id=pve-1&node=pve1", []any{})
	route("/api/v1/clusters/morpheus/groups", "credential_id=mph-1", []any{map[string]any{"id": 3, "name": "lab", "location": nil}})
	route("/api/v1/clusters/morpheus/clouds", "credential_id=mph-1", []any{map[string]any{"id": 5, "name": "vmware", "cloud_type": "vmware"}})
	route("/api/v1/clusters/morpheus/plans", "credential_id=mph-1", []any{
		map[string]any{"id": 11, "name": "2 CPU, 8GB", "code": "vm-8192", "max_cores": 2, "max_memory_bytes": int64(8589934592)}})
	route("/api/v1/clusters/morpheus/layouts", "credential_id=mph-1", []any{
		map[string]any{"id": 21, "name": "Ubuntu 24.04", "instance_type_id": 7, "instance_type_name": "Ubuntu", "provision_type": nil}})
	route("/api/v1/clusters/morpheus/networks", "credential_id=mph-1", []any{
		map[string]any{"id": 31, "name": "vlan-10", "cidr": "10.10.0.0/24", "cloud_id": 5}})
	route("/api/v1/clusters/ankracloud/zones", "", map[string]any{
		"zones": []any{map[string]any{"name": "se-sto1", "region": "se", "display_name": "Stockholm 1", "country": "SE",
			"position": 1, "customer_visible": true}},
		"pricing_complete": true,
	})
	route("/api/v1/clusters/ankracloud/plans", "credential_id=ac-1", map[string]any{
		"plans": []any{map[string]any{"name": "g1.small", "family": "g1", "category": "General purpose", "cores": 2,
			"memory_mebibytes": 4096, "storage_gibibytes": 40, "price_monthly_cents": 1200, "price_hourly_millicents": 1644, "available": true}},
		"pricing_complete": true,
	})
	return fake, queries
}

func TestAccLaneCatalogueDataSources(t *testing.T) {
	fake, queries := newLaneCatalogueFake(t)
	config := fake.providerBlock() + `
data "ankra_aws_regions" "all" {
  credential_id = "aws-1"
}

data "ankra_aws_instance_types" "frankfurt" {
  credential_id = "aws-1"
  region        = data.ankra_aws_regions.all.regions[0].slug
}

data "ankra_aws_vpcs" "frankfurt" {
  credential_id = "aws-1"
  region        = "eu-central-1"
}

data "ankra_aws_subnets" "main" {
  credential_id = "aws-1"
  region        = "eu-central-1"
  vpc_id        = data.ankra_aws_vpcs.frankfurt.vpcs[0].id
}

data "ankra_proxmox_hosts" "lab" {
  credential_id = "pve-1"
}

data "ankra_proxmox_bridges" "pve1" {
  credential_id = "pve-1"
  node          = data.ankra_proxmox_hosts.lab.hosts[0].node
}

data "ankra_proxmox_storages" "pve1" {
  credential_id = "pve-1"
  node          = "pve1"
}

data "ankra_proxmox_templates" "pve1" {
  credential_id = "pve-1"
  node          = "pve1"
}

data "ankra_morpheus_groups" "all" {
  credential_id = "mph-1"
}

data "ankra_morpheus_clouds" "all" {
  credential_id = "mph-1"
}

data "ankra_morpheus_plans" "all" {
  credential_id = "mph-1"
}

data "ankra_morpheus_layouts" "all" {
  credential_id = "mph-1"
}

data "ankra_morpheus_networks" "all" {
  credential_id = "mph-1"
}

data "ankra_ankracloud_zones" "builtin" {}

data "ankra_ankracloud_plans" "named" {
  credential_id = "ac-1"
}
`
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config: config,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("data.ankra_aws_regions.all", "regions.#", "1"),
				resource.TestCheckResourceAttr("data.ankra_aws_regions.all", "regions.0.name", "Europe (Frankfurt)"),
				resource.TestCheckResourceAttr("data.ankra_aws_instance_types.frankfurt", "instance_types.#", "2"),
				resource.TestCheckResourceAttr("data.ankra_aws_instance_types.frankfurt", "instance_types.0.vcpus", "2"),
				resource.TestCheckResourceAttr("data.ankra_aws_instance_types.frankfurt", "instance_types.0.memory_gib", "8"),
				resource.TestCheckResourceAttr("data.ankra_aws_instance_types.frankfurt", "instance_types.0.hourly_price_usd", "0.1155"),
				resource.TestCheckNoResourceAttr("data.ankra_aws_instance_types.frankfurt", "instance_types.1.hourly_price_usd"),
				resource.TestCheckResourceAttr("data.ankra_aws_instance_types.frankfurt", "instance_types.1.current_generation", "false"),
				resource.TestCheckResourceAttr("data.ankra_aws_vpcs.frankfurt", "vpcs.0.cidr", "10.0.0.0/16"),
				resource.TestCheckResourceAttr("data.ankra_aws_subnets.main", "subnets.0.egress_kind", "nat_gateway"),
				resource.TestCheckResourceAttr("data.ankra_aws_subnets.main", "subnets.0.availability_zone", "eu-central-1a"),
				resource.TestCheckResourceAttr("data.ankra_proxmox_hosts.lab", "hosts.0.memory_bytes", "137438953472"),
				resource.TestCheckResourceAttr("data.ankra_proxmox_bridges.pve1", "bridges.#", "2"),
				resource.TestCheckResourceAttr("data.ankra_proxmox_bridges.pve1", "bridges.1.source", "sdn"),
				resource.TestCheckNoResourceAttr("data.ankra_proxmox_bridges.pve1", "bridges.1.cidr"),
				resource.TestCheckResourceAttr("data.ankra_proxmox_storages.pve1", "storages.0.content.#", "2"),
				resource.TestCheckResourceAttr("data.ankra_proxmox_storages.pve1", "storages.0.content.1", "rootdir"),
				resource.TestCheckResourceAttr("data.ankra_proxmox_templates.pve1", "templates.#", "0"),
				resource.TestCheckResourceAttr("data.ankra_morpheus_groups.all", "groups.0.id", "3"),
				resource.TestCheckNoResourceAttr("data.ankra_morpheus_groups.all", "groups.0.location"),
				resource.TestCheckResourceAttr("data.ankra_morpheus_clouds.all", "clouds.0.cloud_type", "vmware"),
				resource.TestCheckResourceAttr("data.ankra_morpheus_plans.all", "plans.0.max_memory_bytes", "8589934592"),
				resource.TestCheckResourceAttr("data.ankra_morpheus_layouts.all", "layouts.0.instance_type_id", "7"),
				resource.TestCheckResourceAttr("data.ankra_morpheus_networks.all", "networks.0.cloud_id", "5"),
				resource.TestCheckResourceAttr("data.ankra_ankracloud_zones.builtin", "zones.0.name", "se-sto1"),
				resource.TestCheckNoResourceAttr("data.ankra_ankracloud_zones.builtin", "credential_id"),
				resource.TestCheckResourceAttr("data.ankra_ankracloud_plans.named", "plans.0.price_monthly_cents", "1200"),
				resource.TestCheckResourceAttr("data.ankra_ankracloud_plans.named", "credential_id", "ac-1"),
				func(_ *terraform.State) error {
					return lanesExpect("ankracloud zones query", queries["/api/v1/clusters/ankracloud/zones"], "")
				},
			),
		}},
	})
}

// TestAccLaneCatalogueDataSourceErrors pins that a catalogue the platform
// refuses (an AWS lane not enabled for the organisation answers 404, a
// credential the cloud rejects 400) fails the read with the platform's
// reason, and that an empty argument is refused before any request.
func TestAccLaneCatalogueDataSourceErrors(t *testing.T) {
	fake := newRouteFake(t)
	fake.handle("GET /api/v1/clusters/aws/regions", func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(t, writer, http.StatusNotFound, map[string]any{"detail": "Not Found"})
	})
	fake.handle("GET /api/v1/clusters/morpheus/groups", func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(t, writer, http.StatusBadRequest, map[string]any{"detail": "HPE Morpheus rejected the credential"})
	})
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: fake.providerBlock() + `
data "ankra_aws_regions" "all" {
  credential_id = "aws-1"
}
`,
				ExpectError: regexp.MustCompile(`Unable to read aws_regions(.|\n)*404`),
			},
			{
				Config: fake.providerBlock() + `
data "ankra_morpheus_groups" "all" {
  credential_id = "mph-1"
}
`,
				ExpectError: regexp.MustCompile(`rejected the credential`),
			},
			{
				Config: fake.providerBlock() + `
data "ankra_proxmox_bridges" "none" {
  credential_id = "pve-1"
  node          = ""
}
`,
				ExpectError: regexp.MustCompile(`value must not be empty`),
			},
		},
	})
	for _, served := range fake.served() {
		if served == "GET /api/v1/clusters/proxmox/bridges" {
			t.Errorf("an empty node was sent to the platform")
		}
	}
}
