// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

// TestAccProviderOrganisationOverride pins that organisation_id reaches the
// wire as the same header the CLI's --org flag sends.
func TestAccProviderOrganisationOverride(t *testing.T) {
	fake := newRouteFake(t)
	fake.handle("GET /api/v1/clusters", func(writer http.ResponseWriter, request *http.Request) {
		_, _ = writer.Write([]byte(writeClusterListing(map[string]string{"cl-1": "alpha"}, request.URL.Query())))
	})

	config := fmt.Sprintf(`
provider "ankra" {
  token           = "test-token"
  base_url        = %q
  organisation_id = "org-other"
}

data "ankra_clusters" "all" {}
`, fake.server.URL)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: config,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.ankra_clusters.all", "clusters.0.name", "alpha"),
					func(_ *terraform.State) error {
						fake.mutex.Lock()
						defer fake.mutex.Unlock()
						for index, header := range fake.organisationHeaders {
							if header != "org-other" {
								return fmt.Errorf("request %q carried organisation %q, want org-other", fake.requests[index], header)
							}
						}
						return nil
					},
				),
			},
		},
	})
}
