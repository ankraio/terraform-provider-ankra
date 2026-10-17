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
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

func lanesFastPolling(t *testing.T) {
	t.Helper()
	previous := []time.Duration{nodeGroupPollInterval, playgroundPollInterval, clusterGonePollInterval,
		managedWaitPollInterval, managedPoolPollInterval, managedStepErrorPatience}
	nodeGroupPollInterval, playgroundPollInterval = 10*time.Millisecond, 10*time.Millisecond
	clusterGonePollInterval, managedWaitPollInterval = 10*time.Millisecond, 10*time.Millisecond
	managedPoolPollInterval, managedStepErrorPatience = 10*time.Millisecond, 0
	t.Cleanup(func() {
		nodeGroupPollInterval, playgroundPollInterval = previous[0], previous[1]
		clusterGonePollInterval, managedWaitPollInterval = previous[2], previous[3]
		managedPoolPollInterval, managedStepErrorPatience = previous[4], previous[5]
	})
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

// lanesImportAdoptionSteps imports a resource, then proves the first plan
// after the import adopts the configuration in place (no replacement) and
// the plan after that apply is empty.
func lanesImportAdoptionSteps(address, importID, config string) []resource.TestStep {
	return []resource.TestStep{
		{
			Config:             config,
			ResourceName:       address,
			ImportState:        true,
			ImportStateId:      importID,
			ImportStatePersist: true,
		},
		{
			Config: config,
			ConfigPlanChecks: resource.ConfigPlanChecks{
				PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction(address, plancheck.ResourceActionUpdate),
				},
				PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
			},
		},
	}
}

type laneCase struct {
	resourceType string
	lane         string
	arguments    string
	wantBody     map[string]any
	wantDelete   string
}

var newLaneCases = []laneCase{
	{"ankra_aws_cluster", "aws", `
  credential_id           = "cred-1"
  ssh_key_credential_id   = "ssh-1"
  region                  = "eu-central-1"
  bastion_allowed_ips     = ["203.0.113.0/24"]
  accept_volume_data_loss = true`, map[string]any{
		"region": "eu-central-1", "bastion_allowed_ips": []any{"203.0.113.0/24"},
		"bastion_instance_type": "t3.small", "control_plane_type": "t3.medium", "worker_type": "t3.medium",
		"architecture": "amd64", "root_volume_gib": float64(40), "retention_policy": "retain",
		"gitops_branch": "master", "cni": "cilium", "distribution": "kubeadm",
	}, "accept_volume_data_loss=true&force=false"},
	{"ankra_proxmox_cluster", "proxmox", `
  credential_id         = "cred-1"
  ssh_key_credential_id = "ssh-1"
  node                  = "pve1"
  bridge                = "vmbr0"
  force_destroy         = true`, map[string]any{
		"node": "pve1", "bridge": "vmbr0", "storage": "local-lvm",
		"bastion_instance_type": "px-small", "control_plane_instance_type": "px-medium",
		"include_dns": true, "include_networking": true,
	}, "force=true"},
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
	}, "force=false"},
	{"ankra_ankracloud_cluster", "ankracloud", `
  zone               = "se-sto1"
  bastion_plan       = "small"
  control_plane_plan = "medium"
  worker_plan        = "medium"`, map[string]any{
		"zone": "se-sto1", "bastion_plan": "small", "control_plane_plan": "medium", "worker_plan": "medium",
		"template": "debian-13", "network_mode": "ipv4_nat", "retention_policy": "retain",
	}, "force=false"},
}

// TestAccLaneClusters covers the four provisioned lanes: the create body
// carries the contract's field names and defaults, an import adopts the
// configuration without a replacement, a replacement waits for the old
// cluster to leave the listing before creating the new one under the same
// name, a cluster deleted outside Terraform is recreated, and destroy sends
// the deprovision flags (force defaults to false).
func TestAccLaneClusters(t *testing.T) {
	lanesFastPolling(t)
	for _, lane := range newLaneCases {
		t.Run(lane.resourceType, func(subtest *testing.T) {
			fake := newLanesFake(subtest)
			address := lane.resourceType + ".test"
			config := func(description string) string {
				return fake.providerBlock() + fmt.Sprintf(`
resource %q "test" {
  name        = "tf-%s"
  description = %q%s
}
`, lane.resourceType, lane.lane, description, lane.arguments)
			}
			clusterID := func() string {
				for id, cluster := range fake.clusters {
					if cluster.state != "deprovisioning" {
						return id
					}
				}
				return ""
			}

			steps := []resource.TestStep{
				{
					Config: config("first"),
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
					// A replacement reuses the name: the create only succeeds
					// because destroy waited for the old cluster to go.
					Config: config("second"),
					ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(address, plancheck.ResourceActionDestroyBeforeCreate),
					}},
					Check: lanesCheck(func() error { return lanesExpect("creates", len(fake.createBodies[lane.lane]), 2) }),
				},
			}
			steps = append(steps, resource.TestStep{
				Config: config("second"),
				Check: func(state *terraform.State) error {
					return lanesExpect("cluster id", state.RootModule().Resources[address].Primary.ID, clusterID())
				},
			}, resource.TestStep{
				// Deleted outside Terraform: the next apply recreates it.
				PreConfig: func() {
					for id := range fake.clusters {
						delete(fake.clusters, id)
					}
				},
				Config: config("second"),
				Check:  lanesCheck(func() error { return lanesExpect("creates", len(fake.createBodies[lane.lane]), 3) }),
			})

			resource.Test(subtest, resource.TestCase{
				ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
				Steps:                    steps,
				CheckDestroy: func(*terraform.State) error {
					if len(fake.clusters) != 0 {
						return fmt.Errorf("%d clusters left after destroy", len(fake.clusters))
					}
					last := fake.deprovisionQuery[len(fake.deprovisionQuery)-1]
					return lanesExpect("deprovision query", last, lane.wantDelete)
				},
			})
		})
	}
}

// TestAccLaneClustersImportAdopts imports an existing cluster of each new
// lane: the first apply adopts the configured build arguments in place, and
// the plan after it is empty.
func TestAccLaneClustersImportAdopts(t *testing.T) {
	lanesFastPolling(t)
	for _, lane := range newLaneCases {
		t.Run(lane.resourceType, func(subtest *testing.T) {
			fake := newLanesFake(subtest)
			existing := fake.addCluster(lane.lane, "tf-"+lane.lane, "running")
			config := fake.providerBlock() + fmt.Sprintf(`
resource %q "test" {
  name = "tf-%s"%s
}
`, lane.resourceType, lane.lane, lane.arguments)
			resource.Test(subtest, resource.TestCase{
				ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
				Steps:                    lanesImportAdoptionSteps(lane.resourceType+".test", existing.id, config),
			})
		})
	}
}

// TestAccOriginalLanesImportAdopts covers the five original lanes: an
// import no longer plans a destroy-and-recreate for the build arguments the
// platform does not return.
func TestAccOriginalLanesImportAdopts(t *testing.T) {
	lanesFastPolling(t)
	lanes := []struct {
		resourceType string
		lane         string
		arguments    string
	}{
		{"ankra_hetzner_cluster", "hetzner", `
  credential_id = "cred-1"
  location      = "fsn1"`},
		{"ankra_digitalocean_cluster", "digitalocean", `
  credential_id         = "cred-1"
  region                = "fra1"
  ssh_key_credential_id = "ssh-1"`},
		{"ankra_upcloud_cluster", "upcloud", `
  credential_id         = "cred-1"
  zone                  = "de-fra1"
  ssh_key_credential_id = "ssh-1"`},
		{"ankra_ovh_cluster", "ovh", `
  credential_id         = "cred-1"
  region                = "GRA11"
  ssh_key_credential_id = "ssh-1"`},
		{"ankra_scaleway_cluster", "scaleway", `
  credential_id         = "cred-1"
  region                = "fr-par"
  zone                  = "fr-par-1"
  ssh_key_credential_id = "ssh-1"`},
	}
	for _, lane := range lanes {
		t.Run(lane.resourceType, func(subtest *testing.T) {
			fake := newLanesFake(subtest)
			existing := fake.addCluster(lane.lane, "tf-original", "running")
			config := fake.providerBlock() + fmt.Sprintf(`
resource %q "test" {
  name = "tf-original"%s
}
`, lane.resourceType, lane.arguments)
			resource.Test(subtest, resource.TestCase{
				ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
				Steps:                    lanesImportAdoptionSteps(lane.resourceType+".test", existing.id, config),
			})
		})
	}
}

func TestAccLaneClusterPlanTimeRefusals(t *testing.T) {
	fake := newLanesFake(t)
	aws := func(retention string) string {
		return fake.providerBlock() + fmt.Sprintf(`
resource "ankra_aws_cluster" "test" {
  name                  = "tf-aws"
  credential_id         = "cred-1"
  ssh_key_credential_id = "ssh-1"
  region                = "eu-central-1"
  bastion_allowed_ips   = ["203.0.113.0/24"]
  retention_policy      = %q
}
`, retention)
	}
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{Config: aws("keep"), ExpectError: regexp.MustCompile(`"keep" is not accepted`)},
			{Config: aws("retain")},
			{
				// The platform records the policy at create and cannot change it.
				Config:      aws("delete"),
				ExpectError: regexp.MustCompile(`Cannot change after creation`),
			},
		},
	})
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
						if err := lanesExpect("roll followed", fake.events, []string{"nodes gpu updating", "nodes gpu updating"}); err != nil {
							return err
						}
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

func TestAccNodeGroupRefusals(t *testing.T) {
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

// TestAccManagedClusterLifecycle covers a Kapsule cluster: provider_options
// go under the provider's key and compare as JSON; the provider-reported pool
// placement (zone, root volume) does not plan a change; a pool's count and
// autoscaling change in place; a renamed pool key adds the new pool and then
// deletes the old one without touching the cluster; a pool change the
// provider cannot apply is refused at plan time; a pool deleted outside
// Terraform is re-added; and destroy deletes through the explicit provider
// delete.
func TestAccManagedClusterLifecycle(t *testing.T) {
	lanesFastPolling(t)
	fake := newLanesFake(t)
	config := func(pools, extraCount string) string {
		return fake.providerBlock() + fmt.Sprintf(`
resource "ankra_managed_cluster" "kapsule" {
  provider_name    = "kapsule"
  name             = "tf-kapsule"
  credential_id    = "cred-1"
  location         = "fr-par"
  destroy_mode     = "delete_provider_cluster"
  provider_options = "{\"private_network_id\":   \"pn-1\"}"
  node_pools = {
%s
  }
}

resource "ankra_managed_node_pool" "extra" {
  provider_name = "kapsule"
  cluster_id    = ankra_managed_cluster.kapsule.cluster_id
  name          = "extra"
  size          = "dev1-l"
  node_count    = %s
}
`, pools, extraCount)
	}
	initial := config(`    default = { size = "DEV1-M", node_count = 2 }`, "1")
	scaled := config(`    default = {
      size        = "DEV1-M"
      node_count  = 3
      autoscaling = { enabled = true, min_count = 2, max_count = 6 }
    }`, "2")
	renamed := config(`    general = { size = "DEV1-L", node_count = 2 }`, "2")
	relabelled := config(`    general = { size = "DEV1-L", node_count = 2, labels = { role = "x" } }`, "2")

	var clusterID string
	steps := []resource.TestStep{
		{
			Config: initial,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("ankra_managed_cluster.kapsule", "state", "provider_without_access"),
				resource.TestCheckResourceAttr("ankra_managed_cluster.kapsule", "node_pools.default.zone", "fr-par-1"),
				resource.TestCheckResourceAttr("ankra_managed_cluster.kapsule", "node_pools.default.root_volume_size_gb", "20"),
				resource.TestCheckResourceAttr("ankra_managed_node_pool.extra", "node_count", "1"),
				resource.TestCheckResourceAttr("ankra_managed_node_pool.extra", "root_volume_type", "b_ssd"),
				resource.TestCheckResourceAttrWith("ankra_managed_cluster.kapsule", "cluster_id", func(value string) error {
					clusterID = value
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
			// The provider-reported placement and a reformatted options
			// document plan nothing.
			Config:   initial,
			PlanOnly: true,
		},
		{
			Config: scaled,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttrWith("ankra_managed_cluster.kapsule", "cluster_id", func(value string) error {
					return lanesExpect("cluster replaced", value, clusterID)
				}),
				lanesCheck(func() error {
					return lanesExpect("patches", fake.poolPatches, []map[string]any{
						{"pool": "default", "count": float64(3), "autoscaling_enabled": true, "autoscaling_min": float64(2), "autoscaling_max": float64(6)},
						{"pool": "extra", "count": float64(2)},
					})
				}),
			),
		},
		{
			// A rename deletes the old pool only after the new one's nodes
			// are Ready.
			PreConfig: func() { fake.events = nil },
			Config:    renamed,
			ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
				plancheck.ExpectResourceAction("ankra_managed_cluster.kapsule", plancheck.ResourceActionUpdate),
			}},
			Check: lanesCheck(func() error {
				if err := lanesExpect("pool order", fake.events, []string{"add pool general", "nodes general ready=False", "nodes general ready=True", "delete pool default"}); err != nil {
					return err
				}
				return lanesExpect("general added", len(fake.createBodies["pool general"]), 1)
			}),
		},
		{
			Config:      relabelled,
			ExpectError: regexp.MustCompile(`Node pool cannot change in place`),
		},
		{
			// A pool deleted outside Terraform is re-added, the cluster kept.
			PreConfig: func() { delete(fake.clusters[clusterID].pools, "general") },
			Config:    renamed,
			ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
				plancheck.ExpectResourceAction("ankra_managed_cluster.kapsule", plancheck.ResourceActionUpdate),
			}},
			Check: lanesCheck(func() error { return lanesExpect("general re-added", len(fake.createBodies["pool general"]), 2) }),
		},
		{
			ResourceName:      "ankra_managed_node_pool.extra",
			ImportState:       true,
			ImportStateIdFunc: func(*terraform.State) (string, error) { return "kapsule/" + clusterID + "/extra", nil },
			ImportStateVerify: true,
			// An import records the provider's spelling of the size and every label
			// it reports; the first apply adopts the configured ones.
			ImportStateVerifyIgnore: []string{"size", "labels"},
		},
	}
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps:                    steps,
		CheckDestroy: func(*terraform.State) error {
			if len(fake.managedDeletes) != 1 {
				return fmt.Errorf("managed deletes %v, want one", fake.managedDeletes)
			}
			if call := fake.managedDeletes[0]; !strings.HasPrefix(call, "POST ") || !strings.Contains(call, "/delete-provider-cluster") {
				return fmt.Errorf("kapsule deleted through %q", call)
			}
			if len(fake.clusters) != 0 {
				return fmt.Errorf("%d clusters left after destroy", len(fake.clusters))
			}
			return nil
		},
	})
}

// TestAccManagedClusterImportDefaultsToDisconnect pins the safe default: a
// cluster Terraform imported (it may be a customer's own) is disconnected on
// destroy, never deleted at the provider.
func TestAccManagedClusterImportDefaultsToDisconnect(t *testing.T) {
	lanesFastPolling(t)
	fake := newLanesFake(t)
	existing := fake.addCluster("aks", "customer-aks", "online")
	existing.pools["system"] = map[string]any{
		"name": "system", "size": "Standard_D4s_v5", "count": 3, "labels": map[string]any{}, "taints": []any{},
		"zone": "1", "root_volume_type": "Managed", "root_volume_size_gb": 128,
	}
	config := fake.providerBlock() + `
resource "ankra_managed_cluster" "aks" {
  provider_name = "aks"
  name          = "customer-aks"
  credential_id = "cred-1"
  location      = "westeurope"
  node_pools    = { system = { size = "Standard_D4s_v5", node_count = 3 } }
}
`
	steps := lanesImportAdoptionSteps("ankra_managed_cluster.aks", "aks/"+existing.id, config)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps:                    steps,
		CheckDestroy: func(*terraform.State) error {
			if len(fake.managedDeletes) != 1 || !strings.Contains(fake.managedDeletes[0], "/disconnect") {
				return fmt.Errorf("imported cluster destroyed through %v, want disconnect", fake.managedDeletes)
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
  node_pools    = { default = { size = "s-2vcpu-4gb" } }
}
`,
			ExpectError: regexp.MustCompile("Missing credential_id"),
		}},
	})
}

// TestAccPlaygroundLifecycle orders a paid playground, resizes it in place,
// refuses a move to the trial until it is explicitly allowed (it destroys the
// paid playground), then replaces it, waiting for the old one to be removed,
// and treats a destroyed environment, which the status route keeps answering
// for, as gone.
func TestAccPlaygroundLifecycle(t *testing.T) {
	lanesFastPolling(t)
	fake := newLanesFake(t)
	config := func(plan string, allowDowngrade bool) string {
		return fake.providerBlock() + fmt.Sprintf(`
resource "ankra_playground" "test" {
  plan                  = %q
  allow_trial_downgrade = %t
}

data "ankra_playground_plans" "all" {}

data "ankra_kubernetes_versions" "kubeadm" {
  distribution = "kubeadm"
}
`, plan, allowDowngrade)
	}

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: config("starter", false),
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
				Config: config("pro", false),
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
				Config:      config("trial", false),
				ExpectError: regexp.MustCompile(`Moving a paid playground to the trial destroys it`),
			},
			{
				Config: config("trial", true),
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
				Config: config("trial", true),
				Check: lanesCheck(func() error {
					return lanesExpect("last call", fake.playgroundCalls[len(fake.playgroundCalls)-1], "create trial")
				}),
			},
		},
	})
}

const (
	lanesTestProject      = "11111111-2222-3333-4444-555555555555"
	lanesOtherTestProject = "99999999-8888-7777-6666-555555555555"
)

// TestAccClusterLanesScopeMoveKeepsResources pins that moving the provider to
// another project, where the platform no longer shows the clusters, neither
// drops a lane cluster, a node group or a managed cluster from state (which
// would plan paid duplicates) nor lets destroy report them gone.
func TestAccClusterLanesScopeMoveKeepsResources(t *testing.T) {
	lanesFastPolling(t)
	fake := newLanesFake(t)
	inProject := func(project string) string {
		return fmt.Sprintf(`
provider "ankra" {
  token      = "test-token"
  base_url   = %q
  project_id = %q
}

resource "ankra_proxmox_cluster" "lab" {
  name                  = "tf-proxmox"
  credential_id         = "cred-1"
  ssh_key_credential_id = "ssh-1"
  node                  = "pve1"
  bridge                = "vmbr0"
}

resource "ankra_node_group" "extra" {
  cluster_id    = ankra_proxmox_cluster.lab.cluster_id
  name          = "extra"
  instance_type = "px-large"
}

resource "ankra_managed_cluster" "doks" {
  provider_name = "doks"
  name          = "tf-doks"
  credential_id = "cred-1"
  location      = "fra1"
  node_pools    = { default = { size = "s-2vcpu-4gb" } }
}
`, fake.server.URL, project)
	}
	notVisible := regexp.MustCompile(`not visible in the provider's current scope|Cluster not in scope`)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{Config: inProject(lanesTestProject)},
			{Config: inProject(lanesOtherTestProject), ExpectError: notVisible},
			{Config: inProject(lanesOtherTestProject), Destroy: true, ExpectError: notVisible},
			{
				Config: inProject(lanesTestProject),
				Check: lanesCheck(func() error {
					if err := lanesExpect("proxmox creates", len(fake.createBodies["proxmox"]), 1); err != nil {
						return err
					}
					if err := lanesExpect("doks creates", len(fake.createBodies["doks"]), 1); err != nil {
						return err
					}
					if err := lanesExpect("deprovisions", len(fake.deprovisionQuery)+len(fake.managedDeletes), 0); err != nil {
						return err
					}
					return lanesExpect("node group writes", fake.nodeGroupWrites, []string{"add extra"})
				}),
			},
		},
	})
}
