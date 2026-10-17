// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

func lanesFastPolling(t *testing.T) {
	t.Helper()
	previousNodeGroup, previousPlayground := nodeGroupPollInterval, playgroundPollInterval
	nodeGroupPollInterval, playgroundPollInterval = 10*time.Millisecond, 10*time.Millisecond
	t.Cleanup(func() { nodeGroupPollInterval, playgroundPollInterval = previousNodeGroup, previousPlayground })
}

func lanesCheck(check func() error) resource.TestCheckFunc {
	return func(*terraform.State) error { return check() }
}

func lanesExpect(field string, got, want any) error {
	if !reflect.DeepEqual(got, want) {
		return fmt.Errorf("%s: got %#v, want %#v", field, got, want)
	}
	return nil
}

// TestAccLaneClusters covers the four provisioned lanes: the create body
// carries the contract's field names and defaults, the platform's state and
// kind land in state, import verifies, a cluster deleted outside Terraform is
// recreated, and destroy sends the deprovision flags.
func TestAccLaneClusters(t *testing.T) {
	lanes := []struct {
		resourceType string
		lane         string
		arguments    string
		wantBody     map[string]any
		wantDelete   string
	}{
		{"ankra_aws_cluster", "aws", `
  credential_id         = "cred-1"
  ssh_key_credential_id = "ssh-1"
  region                = "eu-central-1"
  bastion_allowed_ips   = ["203.0.113.0/24"]
  accept_volume_data_loss = true`, map[string]any{
			"region": "eu-central-1", "bastion_allowed_ips": []any{"203.0.113.0/24"},
			"bastion_instance_type": "t3.small", "control_plane_type": "t3.medium", "worker_type": "t3.medium",
			"architecture": "amd64", "root_volume_gib": float64(40), "retention_policy": "retain",
			"gitops_branch": "master", "cni": "cilium", "distribution": "kubeadm",
		}, "accept_volume_data_loss=true&force=true"},
		{"ankra_proxmox_cluster", "proxmox", `
  credential_id         = "cred-1"
  ssh_key_credential_id = "ssh-1"
  node                  = "pve1"
  bridge                = "vmbr0"
  force_destroy         = false`, map[string]any{
			"node": "pve1", "bridge": "vmbr0", "storage": "local-lvm",
			"bastion_instance_type": "px-small", "control_plane_instance_type": "px-medium",
			"include_dns": true, "include_networking": true,
		}, "force=false"},
		{"ankra_morpheus_cluster", "morpheus", `
  credential_id         = "cred-1"
  ssh_key_credential_id = "ssh-1"
  group_id              = 1
  cloud_id              = 2
  network_id            = 3
  layout_id             = 4
  bastion_plan_id       = 5
  control_plane_plan_id = 6
  worker_plan_id        = 7`, map[string]any{
			"group_id": float64(1), "layout_id": float64(4), "worker_plan_id": float64(7), "etcd_node_count": float64(3),
		}, "force=true"},
		{"ankra_ankracloud_cluster", "ankracloud", `
  zone               = "se-sto1"
  bastion_plan       = "small"
  control_plane_plan = "medium"
  worker_plan        = "medium"`, map[string]any{
			"zone": "se-sto1", "bastion_plan": "small", "control_plane_plan": "medium", "worker_plan": "medium",
			"template": "debian-13", "network_mode": "ipv4_nat", "retention_policy": "retain",
		}, "force=true"},
	}

	for _, lane := range lanes {
		t.Run(lane.resourceType, func(subtest *testing.T) {
			fake := newLanesFake(subtest)
			address := lane.resourceType + ".test"
			config := fake.providerBlock() + fmt.Sprintf(`
resource %q "test" {
  name = "tf-%s"%s
}
`, lane.resourceType, lane.lane, lane.arguments)

			resource.Test(subtest, resource.TestCase{
				ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
				Steps: []resource.TestStep{
					{
						Config: config,
						Check: resource.ComposeAggregateTestCheckFunc(
							resource.TestCheckResourceAttrSet(address, "cluster_id"),
							resource.TestCheckResourceAttr(address, "state", "running"),
							resource.TestCheckResourceAttr(address, "kind", lane.lane),
							lanesCheck(func() error {
								bodies := fake.createBodies[lane.lane]
								if len(bodies) != 1 {
									return fmt.Errorf("sent %d creates to %s, want 1", len(bodies), lane.lane)
								}
								for field, want := range lane.wantBody {
									if err := lanesExpect(field, bodies[0][field], want); err != nil {
										return err
									}
								}
								if _, sent := bodies[0]["external_cloud_provider"]; sent {
									return fmt.Errorf("external_cloud_provider sent; this lane refuses or ignores it")
								}
								return nil
							}),
						),
					},
					{
						ResourceName:      address,
						ImportState:       true,
						ImportStateVerify: true,
						ImportStateVerifyIgnore: []string{
							"credential_id", "ssh_key_credential_id", "region", "bastion_allowed_ips", "node", "bridge",
							"group_id", "cloud_id", "network_id", "layout_id", "bastion_plan_id", "control_plane_plan_id",
							"worker_plan_id", "zone", "bastion_plan", "control_plane_plan", "worker_plan",
							"distribution", "cni", "control_plane_count", "worker_count", "etcd_topology", "etcd_node_count",
							"include_networking", "include_dns", "bastion_instance_type", "control_plane_type", "worker_type",
							"etcd_type", "ubuntu_series", "architecture", "root_volume_gib", "gitops_branch", "retention_policy",
							"nat_gateway_single_zone", "accept_volume_data_loss", "storage", "control_plane_instance_type",
							"worker_instance_type", "etcd_instance_type", "template", "network_mode", "force_destroy", "timeouts",
						},
					},
					{
						// Deleted outside Terraform: the next apply recreates it.
						PreConfig: func() {
							for id := range fake.clusters {
								delete(fake.clusters, id)
							}
						},
						Config: config,
						Check: lanesCheck(func() error {
							return lanesExpect("creates", len(fake.createBodies[lane.lane]), 2)
						}),
					},
				},
				CheckDestroy: func(*terraform.State) error {
					if len(fake.clusters) != 0 {
						return fmt.Errorf("%d clusters left after destroy", len(fake.clusters))
					}
					if len(fake.deprovisionQuery) != 1 {
						return fmt.Errorf("deprovision calls %v, want one", fake.deprovisionQuery)
					}
					return lanesExpect("deprovision query", fake.deprovisionQuery[0], lane.wantDelete)
				},
			})
		})
	}
}

func TestAccLaneClusterRejectsUnknownEnum(t *testing.T) {
	fake := newLanesFake(t)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config: fake.providerBlock() + `
resource "ankra_aws_cluster" "test" {
  name                  = "tf-aws"
  credential_id         = "cred-1"
  ssh_key_credential_id = "ssh-1"
  region                = "eu-central-1"
  bastion_allowed_ips   = ["203.0.113.0/24"]
  retention_policy      = "keep"
}
`,
			ExpectError: regexp.MustCompile(`"keep" is not accepted`),
		}},
	})
	if served := fake.served(); len(served) != 0 {
		t.Errorf("an invalid plan reached the API: %v", served)
	}
}

// TestAccNodeGroupLifecycle proves node groups size in place: count,
// instance type, labels, taints and autoscaling change without replacing the
// group, each submitted with wait=false and waited for.
func TestAccNodeGroupLifecycle(t *testing.T) {
	lanesFastPolling(t)
	fake := newLanesFake(t)
	cluster := fake.addCluster("hetzner", "tf-hetzner", "running")

	config := func(body string) string {
		return fake.providerBlock() + fmt.Sprintf(`
resource "ankra_node_group" "gpu" {
  cluster_id = %q
  name       = "gpu"
%s
}

data "ankra_node_groups" "all" {
  cluster_id = %q
  depends_on = [ankra_node_group.gpu]
}
`, cluster.id, body, cluster.id)
	}
	initial := config(`
  instance_type = "cx32"
  node_count    = 2
  labels        = { pool = "gpu" }`)
	resized := config(`
  instance_type = "cx42"
  node_count    = 3
  labels        = { pool = "gpu", tier = "a" }
  taints = [{ key = "gpu", effect = "NoSchedule" }]
  autoscaling = { enabled = true, min_count = 1, max_count = 5 }`)
	cleared := config(`
  instance_type = "cx42"
  node_count    = 3`)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: initial,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ankra_node_group.gpu", "id", cluster.id+"/gpu"),
					resource.TestCheckResourceAttr("ankra_node_group.gpu", "cluster_kind", "hetzner"),
					resource.TestCheckResourceAttr("ankra_node_group.gpu", "node_count", "2"),
					resource.TestCheckResourceAttr("data.ankra_node_groups.all", "node_groups.#", "1"),
					resource.TestCheckResourceAttr("data.ankra_node_groups.all", "node_groups.0.instance_type", "cx32"),
					lanesCheck(func() error {
						body := fake.nodeGroupBodies["add gpu"]
						if err := lanesExpect("count", body["count"], float64(2)); err != nil {
							return err
						}
						return lanesExpect("labels", body["labels"], map[string]any{"pool": "gpu"})
					}),
				),
			},
			{
				Config: resized,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ankra_node_group.gpu", "instance_type", "cx42"),
					resource.TestCheckResourceAttr("ankra_node_group.gpu", "node_count", "3"),
					resource.TestCheckResourceAttr("ankra_node_group.gpu", "taints.0.value", ""),
					resource.TestCheckResourceAttr("ankra_node_group.gpu", "autoscaling.max_count", "5"),
					lanesCheck(func() error {
						want := []string{"add gpu", "instance-type gpu", "autoscaling gpu", "scale gpu", "labels gpu", "taints gpu"}
						if err := lanesExpect("writes", fake.nodeGroupWrites, want); err != nil {
							return err
						}
						if err := lanesExpect("scale body", fake.nodeGroupBodies["scale gpu"], map[string]any{"count": float64(3)}); err != nil {
							return err
						}
						return lanesExpect("taints body", fake.nodeGroupBodies["taints gpu"]["taints"],
							[]any{map[string]any{"key": "gpu", "value": "", "effect": "NoSchedule"}})
					}),
				),
			},
			{
				// Removing labels, taints and autoscaling clears them on the platform.
				Config: cleared,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckNoResourceAttr("ankra_node_group.gpu", "labels.%"),
					resource.TestCheckNoResourceAttr("ankra_node_group.gpu", "autoscaling.enabled"),
					lanesCheck(func() error {
						if err := lanesExpect("labels cleared", fake.nodeGroupBodies["labels gpu"]["labels"], map[string]any{}); err != nil {
							return err
						}
						if err := lanesExpect("taints cleared", fake.nodeGroupBodies["taints gpu"]["taints"], []any{}); err != nil {
							return err
						}
						return lanesExpect("autoscaling disabled", fake.nodeGroupBodies["autoscaling gpu"]["enabled"], false)
					}),
				),
			},
			{
				ResourceName:            "ankra_node_group.gpu",
				ImportState:             true,
				ImportStateId:           cluster.id + "/gpu",
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"timeouts"},
			},
			{
				PreConfig: func() { delete(cluster.groups, "gpu") },
				Config:    cleared,
				Check: lanesCheck(func() error {
					if cluster.groups["gpu"] == nil {
						return fmt.Errorf("node group deleted outside Terraform was not recreated")
					}
					return nil
				}),
			},
		},
		CheckDestroy: func(*terraform.State) error {
			if _, exists := cluster.groups["gpu"]; exists {
				return fmt.Errorf("node group still exists after destroy")
			}
			return nil
		},
	})
}

func TestAccNodeGroupRefusesManagedCluster(t *testing.T) {
	fake := newLanesFake(t)
	cluster := fake.addCluster("doks", "tf-doks", "online")
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: fake.providerBlock() + fmt.Sprintf(`
resource "ankra_node_group" "x" {
  cluster_id    = %q
  name          = "x"
  instance_type = "s-2vcpu-4gb"
}
`, cluster.id),
				ExpectError: regexp.MustCompile("ankra_managed_node_pool"),
			},
			{
				Config: fake.providerBlock() + fmt.Sprintf(`
resource "ankra_node_group" "x" {
  cluster_id    = %q
  name          = "a/b"
  instance_type = "s-2vcpu-4gb"
}
`, cluster.id),
				ExpectError: regexp.MustCompile("must not contain '/'"),
			},
		},
	})
}

// TestAccManagedClusterLifecycle covers a Kapsule cluster: provider_options go
// under the provider's key, an initial pool's count and autoscaling change in
// place through PATCH, an extra pool is its own resource, and destroy uses the
// explicit provider delete rather than the disconnect-only DELETE.
func TestAccManagedClusterLifecycle(t *testing.T) {
	fake := newLanesFake(t)
	config := func(poolBody, extraCount string) string {
		return fake.providerBlock() + fmt.Sprintf(`
resource "ankra_managed_cluster" "kapsule" {
  provider_name    = "kapsule"
  name             = "tf-kapsule"
  credential_id    = "cred-1"
  location         = "fr-par"
  provider_options = jsonencode({ private_network_id = "pn-1" })
  node_pools = [{
    name = "default"
    size = "DEV1-M"
%s
  }]
}

resource "ankra_managed_node_pool" "extra" {
  provider_name = "kapsule"
  cluster_id    = ankra_managed_cluster.kapsule.cluster_id
  name          = "extra"
  size          = "DEV1-L"
  node_count    = %s
}
`, poolBody, extraCount)
	}
	initial := config(`    node_count = 2`, "1")
	scaled := config(`    node_count = 3
    autoscaling = { enabled = true, min_count = 2, max_count = 6 }`, "2")
	resized := config(`    node_count = 3
    autoscaling = { enabled = true, min_count = 2, max_count = 6 }
    labels = { role = "general" }`, "2")

	var firstClusterID string
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: initial,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ankra_managed_cluster.kapsule", "state", "provider_without_access"),
					resource.TestCheckResourceAttr("ankra_managed_cluster.kapsule", "retention_policy", "delete"),
					resource.TestCheckResourceAttr("ankra_managed_node_pool.extra", "node_count", "1"),
					resource.TestCheckResourceAttrWith("ankra_managed_cluster.kapsule", "cluster_id", func(value string) error {
						firstClusterID = value
						return nil
					}),
					lanesCheck(func() error {
						body := fake.createBodies["kapsule"][0]
						if err := lanesExpect("kapsule options", body["kapsule"], map[string]any{"private_network_id": "pn-1"}); err != nil {
							return err
						}
						if _, sent := body["provider_options"]; sent {
							return fmt.Errorf("provider_options leaked onto the wire")
						}
						return lanesExpect("pools", body["node_pools"], []any{map[string]any{"name": "default", "size": "DEV1-M", "count": float64(2)}})
					}),
				),
			},
			{
				Config: scaled,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrWith("ankra_managed_cluster.kapsule", "cluster_id", func(value string) error {
						return lanesExpect("cluster replaced", value, firstClusterID)
					}),
					resource.TestCheckResourceAttr("ankra_managed_node_pool.extra", "node_count", "2"),
					lanesCheck(func() error {
						return lanesExpect("patches", fake.poolPatches, []map[string]any{
							{"pool": "default", "count": float64(3), "autoscaling_enabled": true, "autoscaling_min": float64(2), "autoscaling_max": float64(6)},
							{"pool": "extra", "count": float64(2)},
						})
					}),
				),
			},
			{
				// A pool label cannot change in place, so the cluster is replaced.
				Config: resized,
				Check: resource.TestCheckResourceAttrWith("ankra_managed_cluster.kapsule", "cluster_id", func(value string) error {
					if value == firstClusterID {
						return fmt.Errorf("a pool label change did not replace the cluster")
					}
					return nil
				}),
			},
			{
				ResourceName: "ankra_managed_node_pool.extra",
				ImportState:  true,
				ImportStateIdFunc: func(state *terraform.State) (string, error) {
					return state.RootModule().Resources["ankra_managed_node_pool.extra"].Primary.ID, nil
				},
				ImportStateVerify: true,
			},
		},
		CheckDestroy: func(*terraform.State) error {
			for _, call := range fake.managedDeletes {
				if !strings.HasPrefix(call, "POST ") || !strings.Contains(call, "/delete-provider-cluster") {
					return fmt.Errorf("kapsule deleted through %q", call)
				}
			}
			if len(fake.clusters) != 0 {
				return fmt.Errorf("%d clusters left after destroy", len(fake.clusters))
			}
			return nil
		},
	})
}

func TestAccManagedClusterRequiresCredential(t *testing.T) {
	fake := newLanesFake(t)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config: fake.providerBlock() + `
resource "ankra_managed_cluster" "doks" {
  provider_name = "doks"
  name          = "tf-doks"
  location      = "fra1"
  node_pools    = [{ name = "default", size = "s-2vcpu-4gb" }]
}
`,
			ExpectError: regexp.MustCompile("Missing credential_id"),
		}},
	})
}

// TestAccPlaygroundLifecycle orders a paid playground, resizes it in place,
// moves it to the trial (a replacement, because the platform cannot resize
// to or from the trial), and treats a destroyed environment, which the status
// route keeps answering for, as gone.
func TestAccPlaygroundLifecycle(t *testing.T) {
	lanesFastPolling(t)
	fake := newLanesFake(t)
	config := func(plan string) string {
		return fake.providerBlock() + fmt.Sprintf(`
resource "ankra_playground" "test" {
  plan = %q
}

data "ankra_playground_plans" "all" {}

data "ankra_kubernetes_versions" "kubeadm" {
  distribution = "kubeadm"
}
`, plan)
	}

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: config("starter"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ankra_playground.test", "phase", "ready"),
					resource.TestCheckResourceAttr("ankra_playground.test", "ordered_at", "2026-10-17T00:00:00Z"),
					resource.TestCheckResourceAttr("data.ankra_playground_plans.all", "plans.#", "3"),
					resource.TestCheckResourceAttr("data.ankra_playground_plans.all", "default_plan_id", "trial"),
					resource.TestCheckResourceAttr("data.ankra_playground_plans.all", "quota_limit_mib", "8192"),
					resource.TestCheckResourceAttr("data.ankra_kubernetes_versions.kubeadm", "stable_version", "v1.33.4"),
					resource.TestCheckNoResourceAttr("data.ankra_kubernetes_versions.kubeadm", "versions.1.published_at"),
				),
			},
			{
				Config: config("pro"),
				Check: lanesCheck(func() error {
					return lanesExpect("calls", fake.playgroundCalls, []string{"create starter", "resize pro"})
				}),
			},
			{
				ResourceName:            "ankra_playground.test",
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"timeouts"},
			},
			{
				Config: config("trial"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckNoResourceAttr("ankra_playground.test", "ordered_at"),
					lanesCheck(func() error {
						return lanesExpect("calls", fake.playgroundCalls,
							[]string{"create starter", "resize pro", "destroy", "create trial"})
					}),
				),
			},
			{
				// Torn down outside Terraform: the status still answers, with
				// a teardown phase, and the next apply orders a new one.
				PreConfig: func() {
					for _, cluster := range fake.clusters {
						cluster.phase = "removed"
					}
				},
				Config: config("trial"),
				Check: lanesCheck(func() error {
					return lanesExpect("last call", fake.playgroundCalls[len(fake.playgroundCalls)-1], "create trial")
				}),
			},
		},
	})
}
