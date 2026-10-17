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

// After an import the anchoring occurrence is unknown. The first apply that
// records one must prove, through the preview, that it derives the stored
// selector; any other occurrence is refused rather than recorded.
func TestAccSecurityDispositionImportChecksOccurrence(t *testing.T) {
	fake := newSecurityFake(t)
	disposition := func(name, occurrenceID string) string {
		return `
resource "ankra_security_disposition" "` + name + `" {
  occurrence_id = "` + occurrenceID + `"
  scope         = "workload"
  disposition   = "acknowledged"
  reason        = "Not reachable from outside the cluster"
}
`
	}
	const anchor = "11111111-1111-4111-8111-111111111111"
	const other = "99999999-9999-4999-8999-999999999999"
	seed := fake.providerBlock() + disposition("seed", anchor)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{Config: seed},
			{
				Config:             seed + disposition("adopted", other),
				ResourceName:       "ankra_security_disposition.adopted",
				ImportState:        true,
				ImportStatePersist: true,
				ImportStateIdFunc: func(state *terraform.State) (string, error) {
					return state.RootModule().Resources["ankra_security_disposition.seed"].Primary.ID, nil
				},
			},
			{
				Config:      seed + disposition("adopted", other),
				ExpectError: regexp.MustCompile("Occurrence does not match the imported disposition"),
			},
			{
				Config: seed + disposition("adopted", anchor),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ankra_security_disposition.adopted", "occurrence_id", anchor),
					func(_ *terraform.State) error {
						last := fake.previews[len(fake.previews)-1]
						if last["occurrence_id"] != anchor || last["scope"] != "workload" || last["disposition"] != "acknowledged" {
							return fmt.Errorf("preview sent %v", last)
						}
						return nil
					},
				),
			},
		},
	})
}
