// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

// originalLaneConfigurations are minimal configurations of the five
// original lanes, keyed by resource type.
var originalLaneConfigurations = []struct {
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

// TestAccOriginalLanesVolumeConsentAndDestroyWait pins, on each original
// lane, that a destroy refused because the cluster still holds volumes fails
// with the platform's reason and keeps the cluster in state; that
// accept_volume_data_loss changes in place and is sent on the next destroy,
// alongside force=true (the backwards-compatible default); and that destroy
// returns only once the cluster has left the listing.
func TestAccOriginalLanesVolumeConsentAndDestroyWait(t *testing.T) {
	lanesFastPolling(t)
	for _, lane := range originalLaneConfigurations {
		t.Run(lane.resourceType, func(subtest *testing.T) {
			fake := newLanesFake(subtest)
			fake.volumeConsentRequired = true
			config := func(consent string) string {
				return fake.providerBlock() + fmt.Sprintf(`
resource %q "test" {
  name = "tf-%s"%s%s
}
`, lane.resourceType, lane.lane, lane.arguments, consent)
			}
			resource.Test(subtest, resource.TestCase{
				ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
				CheckDestroy: func(_ *terraform.State) error {
					if len(fake.clusters) != 0 {
						return fmt.Errorf("destroy returned while the cluster was still listed: %v", fake.served())
					}
					return lanesExpect("deprovision queries", fake.deprovisionQuery,
						[]string{"force=true", "accept_volume_data_loss=true&force=true"})
				},
				Steps: []resource.TestStep{
					{Config: config("")},
					{
						Config:      config(""),
						Destroy:     true,
						ExpectError: regexp.MustCompile(`accept_volume_data_loss=true to proceed`),
					},
					{
						// The refused destroy left the cluster in state, and the
						// consent is an in-place change.
						Config: config("\n  accept_volume_data_loss = true"),
						Check:  resource.TestCheckResourceAttr(lane.resourceType+".test", "accept_volume_data_loss", "true"),
					},
				},
			})
		})
	}
}

// TestAccOriginalLanesUpgradeFromPreviousReleasePlansNoChange pins the
// upgrade path: a cluster created by the last release (state without
// accept_volume_data_loss and without a recorded scope) plans no change once
// this provider reads it, and the attribute stays unset.
func TestAccOriginalLanesUpgradeFromPreviousReleasePlansNoChange(t *testing.T) {
	lanesFastPolling(t)
	for _, lane := range originalLaneConfigurations {
		t.Run(lane.resourceType, func(subtest *testing.T) {
			fake := newLanesFake(subtest)
			config := fake.providerBlock() + fmt.Sprintf(`
resource %q "test" {
  name = "tf-%s"%s
}
`, lane.resourceType, lane.lane, lane.arguments)
			resource.Test(subtest, resource.TestCase{
				Steps: []resource.TestStep{
					{
						ExternalProviders: map[string]resource.ExternalProvider{
							"ankra": {Source: "ankraio/ankra", VersionConstraint: "0.3.0"},
						},
						Config: config,
					},
					{
						ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
						Config:                   config,
						PlanOnly:                 true,
					},
					{
						ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
						Config:                   config,
						Check:                    resource.TestCheckNoResourceAttr(lane.resourceType+".test", "accept_volume_data_loss"),
					},
				},
			})
		})
	}
}

// TestAccOriginalLanesScopeMoveKeepsCluster pins that moving the provider to
// another project, where the listing no longer shows the cluster, neither
// drops it from state (which would plan a paid duplicate) nor lets destroy
// report it gone.
func TestAccOriginalLanesScopeMoveKeepsCluster(t *testing.T) {
	lanesFastPolling(t)
	fake := newLanesFake(t)
	inProject := func(project string) string {
		return fmt.Sprintf(`
provider "ankra" {
  token      = "test-token"
  base_url   = %q
  project_id = %q
}

resource "ankra_upcloud_cluster" "test" {
  name                  = "tf-upcloud"
  credential_id         = "cred-1"
  zone                  = "de-fra1"
  ssh_key_credential_id = "ssh-1"
}
`, fake.server.URL, project)
	}
	notVisible := regexp.MustCompile(`not visible in the provider's current scope`)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{Config: inProject(lanesTestProject)},
			{Config: inProject(lanesOtherTestProject), ExpectError: notVisible},
			{Config: inProject(lanesOtherTestProject), Destroy: true, ExpectError: notVisible},
			{
				Config: inProject(lanesTestProject),
				Check: lanesCheck(func() error {
					if err := lanesExpect("upcloud creates", len(fake.createBodies["upcloud"]), 1); err != nil {
						return err
					}
					return lanesExpect("deprovisions", len(fake.deprovisionQuery), 0)
				}),
			},
		},
	})
}

// TestAccOriginalLanesDestroyOfDeprovisioningClusterOnlyWaits pins that a
// destroy finding the cluster already deprovisioning (an earlier destroy
// timed out) does not send the delete again.
func TestAccOriginalLanesDestroyOfDeprovisioningClusterOnlyWaits(t *testing.T) {
	lanesFastPolling(t)
	fake := newLanesFake(t)
	config := fake.providerBlock() + `
resource "ankra_scaleway_cluster" "test" {
  name                  = "tf-scaleway"
  credential_id         = "cred-1"
  region                = "fr-par"
  zone                  = "fr-par-1"
  ssh_key_credential_id = "ssh-1"
}
`
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy: func(_ *terraform.State) error {
			if len(fake.clusters) != 0 {
				return fmt.Errorf("destroy returned while the cluster was still listed")
			}
			return lanesExpect("deprovision queries", len(fake.deprovisionQuery), 0)
		},
		Steps: []resource.TestStep{{
			Config: config,
			Check: func(_ *terraform.State) error {
				for _, cluster := range fake.clusters {
					if strings.HasPrefix(cluster.name, "tf-scaleway") {
						cluster.state = "deprovisioning"
						cluster.deprovisionReads = 5
					}
				}
				return nil
			},
		}},
	})
}
