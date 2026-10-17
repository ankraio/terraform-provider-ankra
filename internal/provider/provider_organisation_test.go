// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	testingresource "github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"ankra.io/terraform-provider-ankra/internal/client"
)

const (
	testOrganisationID = "0f0f0f0f-1111-2222-3333-444444444444"
	testProjectID      = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
)

// TestAccProviderScopeHeaders pins that organisation_id and project_id reach
// the wire on every request, as the headers the CLI and portal send.
func TestAccProviderScopeHeaders(t *testing.T) {
	fake := newRouteFake(t)
	var projectHeaders []string
	fake.handle("GET /api/v1/clusters", func(writer http.ResponseWriter, request *http.Request) {
		projectHeaders = append(projectHeaders, request.Header.Get(client.ProjectHeader))
		_, _ = writer.Write([]byte(writeClusterListing(map[string]string{"cl-1": "alpha"}, request.URL.Query())))
	})

	config := fmt.Sprintf(`
provider "ankra" {
  token           = "test-token"
  base_url        = %q
  organisation_id = %q
  project_id      = %q
}

data "ankra_clusters" "all" {}
`, fake.server.URL, testOrganisationID, testProjectID)

	testingresource.Test(t, testingresource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []testingresource.TestStep{
			{
				Config: config,
				Check: testingresource.ComposeAggregateTestCheckFunc(
					testingresource.TestCheckResourceAttr("data.ankra_clusters.all", "clusters.0.name", "alpha"),
					func(_ *terraform.State) error {
						fake.mutex.Lock()
						defer fake.mutex.Unlock()
						for index, header := range fake.organisationHeaders {
							if header != testOrganisationID {
								return fmt.Errorf("request %q carried organisation %q", fake.requests[index], header)
							}
						}
						for _, header := range projectHeaders {
							if header != testProjectID {
								return fmt.Errorf("request carried project %q", header)
							}
						}
						return nil
					},
				),
			},
		},
	})
}

func TestAccProviderRejectsScopeSlugs(t *testing.T) {
	fake := newRouteFake(t)

	for _, attribute := range []string{"organisation_id", "project_id"} {
		testingresource.Test(t, testingresource.TestCase{
			ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
			Steps: []testingresource.TestStep{
				{
					Config: fmt.Sprintf(`
provider "ankra" {
  token    = "test-token"
  base_url = %q
  %s = "Acme Corp"
}

data "ankra_clusters" "all" {}
`, fake.server.URL, attribute),
					ExpectError: regexp.MustCompile("Invalid " + attribute),
				},
			},
		})
	}
	if served := fake.served(); len(served) != 0 {
		t.Errorf("a slug scope still reached the platform: %v", served)
	}
}

// TestUnresolvedScopeSendsNothing pins the fail-closed path for an
// organisation_id or project_id only known after apply: no request goes out
// without the header, at the client and at requireClient.
func TestUnresolvedScopeSendsNothing(t *testing.T) {
	fake := newRouteFake(t)
	apiClient := client.NewClient(fake.server.URL, "test-token", "test")
	apiClient.UnresolvedScope = "organisation_id"

	if _, err := apiClient.ListClusters(context.Background()); err == nil {
		t.Error("ListClusters succeeded with an unresolved organisation")
	}

	holder := configuredClient{client: apiClient}
	var diagnostics diag.Diagnostics
	if holder.requireClient(&diagnostics) != nil || !diagnostics.HasError() {
		t.Error("requireClient handed out a client with an unresolved organisation")
	}
	if served := fake.served(); len(served) != 0 {
		t.Errorf("sent %v with an unresolved organisation", served)
	}
}

// TestEveryTypeIsConfigurable guards the configuredClient embedding: a
// resource or data source that does not implement the matching
// ...WithConfigure interface compiles fine, but the framework never hands it a
// client, and it reports "Missing API token" with a token set.
func TestEveryTypeIsConfigurable(t *testing.T) {
	t.Parallel()

	ankra, ok := New("test")().(*ankraProvider)
	if !ok {
		t.Fatal("New did not return *ankraProvider")
	}
	for _, factory := range ankra.Resources(context.Background()) {
		instance := factory()
		if _, configurable := instance.(resource.ResourceWithConfigure); !configurable {
			t.Errorf("resource %T does not implement ResourceWithConfigure", instance)
		}
		if _, importable := instance.(resource.ResourceWithImportState); !importable {
			t.Errorf("resource %T does not implement ResourceWithImportState", instance)
		}
	}
	for _, factory := range ankra.DataSources(context.Background()) {
		instance := factory()
		if _, configurable := instance.(datasource.DataSourceWithConfigure); !configurable {
			t.Errorf("data source %T does not implement DataSourceWithConfigure", instance)
		}
	}
}
