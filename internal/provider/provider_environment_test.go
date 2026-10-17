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

// TestAccProviderOrganisationFromEnvironment pins the environment variable
// the provider reads for organisation_id: ANKRA_ORG, the CLI's, and not
// ANKRA_ORGANISATION_ID, which Ankra Pipelines sets in every step to the
// run's own organisation. Terraform run inside a pipeline must not silently
// act in the pipeline's organisation.
func TestAccProviderOrganisationFromEnvironment(t *testing.T) {
	t.Setenv("ANKRA_ORGANISATION_ID", "99999999-9999-9999-9999-999999999999")
	t.Setenv("ANKRA_ORG", testOrganisationID)

	fake := newRouteFake(t)
	fake.handle("GET /api/v1/clusters", func(writer http.ResponseWriter, request *http.Request) {
		_, _ = writer.Write([]byte(writeClusterListing(map[string]string{"cl-1": "alpha"}, request.URL.Query())))
	})

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: fake.providerBlock() + `
data "ankra_clusters" "all" {}
`,
				Check: func(_ *terraform.State) error {
					fake.mutex.Lock()
					defer fake.mutex.Unlock()
					for index, header := range fake.organisationHeaders {
						if header != testOrganisationID {
							return fmt.Errorf("request %q carried organisation %q, want ANKRA_ORG's %q", fake.requests[index], header, testOrganisationID)
						}
					}
					return nil
				},
			},
		},
	})
}
