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

// While the organisation's applications feature is off the service
// admission routes answer a bare 404. That must neither drop the policy or
// the binding from state nor let a destroy report success.
func TestAccServiceAdmissionFeatureOff(t *testing.T) {
	fake := newDeployServiceFake(t)
	configuration := fake.providerBlock() + fmt.Sprintf(`
resource "ankra_service_placement_policy" "prod" {
  cluster_id    = %q
  region        = "eu-north-1"
  data_boundary = "eu"
}

resource "ankra_service_consumer" "orders" {
  application_id = %q
  cluster_id     = ankra_service_placement_policy.prod.cluster_id
  namespace      = "orders"
  allow_planned  = true
}
`, deployFakeClusterID, deployFakeApplicationID)
	stillBound := func(_ *terraform.State) error {
		if len(fake.consumers) != 1 || fake.policy == nil {
			return fmt.Errorf("binding or policy lost: %v %v", fake.consumers, fake.policy)
		}
		return nil
	}

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{Config: configuration},
			{
				PreConfig:   func() { fake.isApplicationsOff = true },
				Config:      configuration,
				ExpectError: regexp.MustCompile("Managed services not available"),
				Check:       stillBound,
			},
			{
				Config:      configuration,
				Destroy:     true,
				ExpectError: regexp.MustCompile("Managed services not available"),
				Check:       stillBound,
			},
			{
				PreConfig: func() { fake.isApplicationsOff = false },
				Config:    configuration,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ankra_service_consumer.orders", "binding_revision", "1"),
					stillBound,
				),
			},
		},
	})
}
