// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

// TestAccManagedPoolRenameUnverifiableKeepsOldPool pins that a provider whose
// pool nodes Ankra cannot find (UKS) never has its old pool deleted on a
// rename: the new pool is added, the old one kept, and the apply says why.
func TestAccManagedPoolRenameUnverifiableKeepsOldPool(t *testing.T) {
	lanesFastPolling(t)
	fake := newLanesFake(t)
	config := func(pool string) string {
		return fake.providerBlock() + fmt.Sprintf(`
resource "ankra_managed_cluster" "uks" {
  provider_name = "uks"
  name          = "tf-uks"
  credential_id = "cred-1"
  location      = "de-fra1"
  node_pools    = { %s = { size = "2xCPU-4GB", node_count = 1 } }
}
`, pool)
	}
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{Config: config("default")},
			{Config: config("general"), ExpectError: regexp.MustCompile(`Removed node pools were kept`)},
			{
				// The old pool is still in state, so the delete is planned again.
				Config:             config("general"),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
		},
	})
	if len(fake.poolDeletes) != 0 {
		t.Errorf("the old pool was deleted although its replacement could not be verified: %v", fake.events)
	}
}

// TestAccManagedClusterStuckStepWarnsAndWaits pins that a build step failing
// for longer than the patience only warns: the wait goes on, so an apply
// whose cluster finishes in time does not end in a tainted, paid cluster.
func TestAccManagedClusterStuckStepWarnsAndWaits(t *testing.T) {
	lanesFastPolling(t)
	fake := newLanesFake(t)
	fake.managedStepErrorReads = 4
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config: fake.providerBlock() + `
resource "ankra_managed_cluster" "doks" {
  provider_name = "doks"
  name          = "tf-doks"
  credential_id = "cred-1"
  location      = "fra1"
  node_pools    = { default = { size = "s-2vcpu-4gb" } }
}
`,
			Check: resource.TestCheckResourceAttr("ankra_managed_cluster.doks", "state", "provider_without_access"),
		}},
	})
}

// TestAccLaneDeleteSkipsRunningDeprovision pins that a destroy finding the
// cluster already deprovisioning (an earlier destroy timed out) only waits:
// re-sending the delete would cancel and re-dispatch the platform's jobs.
func TestAccLaneDeleteSkipsRunningDeprovision(t *testing.T) {
	lanesFastPolling(t)
	fake := newLanesFake(t)
	config := fake.providerBlock() + `
resource "ankra_proxmox_cluster" "lab" {
  name                  = "tf-proxmox"
  credential_id         = "cred-1"
  ssh_key_credential_id = "ssh-1"
  node                  = "pve1"
  bridge                = "vmbr0"
}
`
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{Config: config},
			{
				PreConfig: func() {
					for _, cluster := range fake.clusters {
						cluster.state = "deprovisioning"
						cluster.deprovisionReads = 3
					}
				},
				Config:  config,
				Destroy: true,
			},
		},
		CheckDestroy: func(*terraform.State) error {
			if len(fake.deprovisionQuery) != 0 {
				return fmt.Errorf("a delete was re-sent for a cluster already deprovisioning: %v", fake.deprovisionQuery)
			}
			if len(fake.clusters) != 0 {
				return fmt.Errorf("destroy returned before the cluster was gone")
			}
			return nil
		},
	})
}

// TestAccNodeGroupOfDeletedClusterIsDropped pins that "Cluster not found"
// from the node-group routes, while the provider's scope has not moved,
// means the cluster is gone: the group leaves state instead of erroring on
// every plan.
func TestAccNodeGroupOfDeletedClusterIsDropped(t *testing.T) {
	lanesFastPolling(t)
	fake := newLanesFake(t)
	cluster := fake.addCluster("hetzner", "tf-hetzner", "running")
	config := fake.providerBlock() + fmt.Sprintf(`
resource "ankra_node_group" "extra" {
  cluster_id    = %q
  name          = "extra"
  instance_type = "cx32"
}
`, cluster.id)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{Config: config},
			{
				PreConfig:          func() { delete(fake.clusters, cluster.id) },
				Config:             config,
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
		},
	})
}
