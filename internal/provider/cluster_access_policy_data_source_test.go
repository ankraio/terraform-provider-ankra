// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"net/http"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

// TestAccClusterAccessPolicyDataSource reads a configured policy and the
// default one (no policy row: no bounds, null in state), as the bearer policy
// route answers them (go/internal/kubeproxyapi/policy.go).
func TestAccClusterAccessPolicyDataSource(t *testing.T) {
	fake := newRouteFake(t)
	isConfigured := true
	fake.handle("GET /api/v1/org/cluster-access-policy", func(writer http.ResponseWriter, _ *http.Request) {
		policy := map[string]any{
			"creator_grant_role": "cluster-admin", "max_grant_role": "cluster-admin",
			"elevated_max_ttl_seconds": nil, "require_reason_from_role": nil, "is_configured": false,
		}
		if isConfigured {
			policy = map[string]any{
				"creator_grant_role": "view", "max_grant_role": "edit",
				"elevated_max_ttl_seconds": 14400, "require_reason_from_role": "edit", "is_configured": true,
			}
		}
		writeJSON(t, writer, http.StatusOK, map[string]any{
			"policy": policy, "organisation_id": "0a0a0a0a-0000-0000-0000-000000000001", "organisation_name": "Acme",
		})
	})
	config := fake.providerBlock() + `data "ankra_cluster_access_policy" "this" {}`

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: config,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.ankra_cluster_access_policy.this", "id", "cluster_access_policy"),
					resource.TestCheckResourceAttr("data.ankra_cluster_access_policy.this", "organisation_name", "Acme"),
					resource.TestCheckResourceAttr("data.ankra_cluster_access_policy.this", "is_configured", "true"),
					resource.TestCheckResourceAttr("data.ankra_cluster_access_policy.this", "creator_grant_role", "view"),
					resource.TestCheckResourceAttr("data.ankra_cluster_access_policy.this", "max_grant_role", "edit"),
					resource.TestCheckResourceAttr("data.ankra_cluster_access_policy.this", "elevated_max_ttl_seconds", "14400"),
					resource.TestCheckResourceAttr("data.ankra_cluster_access_policy.this", "require_reason_from_role", "edit"),
				),
			},
			{
				PreConfig: func() { isConfigured = false },
				Config:    config,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.ankra_cluster_access_policy.this", "is_configured", "false"),
					resource.TestCheckResourceAttr("data.ankra_cluster_access_policy.this", "max_grant_role", "cluster-admin"),
					resource.TestCheckNoResourceAttr("data.ankra_cluster_access_policy.this", "elevated_max_ttl_seconds"),
					resource.TestCheckNoResourceAttr("data.ankra_cluster_access_policy.this", "require_reason_from_role"),
				),
			},
		},
	})
}

func TestAccClusterAccessPolicyDataSourceGatewayDisabled(t *testing.T) {
	fake := newRouteFake(t)
	fake.handle("GET /api/v1/org/cluster-access-policy", func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(t, writer, http.StatusNotFound, map[string]any{"detail": "Cluster gateway is not enabled"})
	})
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:      fake.providerBlock() + `data "ankra_cluster_access_policy" "this" {}`,
				ExpectError: regexp.MustCompile("Unable to read the cluster access policy"),
			},
		},
	})
}
