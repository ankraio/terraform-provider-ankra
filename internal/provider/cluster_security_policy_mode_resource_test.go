// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"fmt"
	"net/http"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

const policyModeFakeClusterID = "c1000000-0000-0000-0000-000000000001"

// policyModeFake mirrors the stored policy-mode routes
// (go/internal/securityapi): GET answers the stored mode, the last requested
// mode and whether a policy engine is installed; audit on a cluster whose
// engine was retired still stores off.
type policyModeFake struct {
	*routeFake
	requested       *string
	engineInstalled bool
	hasBaseline     bool
	sets            int
}

func newPolicyModeFake(t *testing.T) *policyModeFake {
	fake := &policyModeFake{routeFake: newRouteFake(t), engineInstalled: true, hasBaseline: true}
	path := "/api/v1/org/clusters/imported/{cluster_id}/security/policy-mode"
	fake.handle("GET "+path, func(writer http.ResponseWriter, request *http.Request) {
		if request.PathValue("cluster_id") != policyModeFakeClusterID {
			writeJSON(t, writer, http.StatusNotFound, map[string]any{"detail": "Cluster not found"})
			return
		}
		writeJSON(t, writer, http.StatusOK, map[string]any{
			"cluster_id": policyModeFakeClusterID, "policy_mode": fake.storedMode(), "requested_mode": fake.requested,
			"policy_engine_installed": fake.engineInstalled,
		})
	})
	fake.handle("POST "+path, func(writer http.ResponseWriter, request *http.Request) {
		if !fake.hasBaseline {
			writeJSON(t, writer, http.StatusNotFound, map[string]any{"detail": "The security baseline policy addon is not installed on this cluster."})
			return
		}
		var body struct {
			Mode string `json:"mode"`
		}
		decodeJSON(t, request, &body)
		fake.requested = &body.Mode
		if body.Mode == "off" {
			fake.engineInstalled = false
		}
		fake.sets++
		writeJSON(t, writer, http.StatusOK, map[string]any{"valid": true, "mode": body.Mode, "commit_sha": "abc123", "commit_url": nil})
	})
	return fake
}

func (fake *policyModeFake) storedMode() *string {
	if !fake.hasBaseline && fake.requested == nil {
		return nil
	}
	stored := "audit"
	switch {
	case fake.requested != nil && *fake.requested == "enforce":
		stored = "enforce"
	case !fake.engineInstalled:
		stored = "off"
	}
	return &stored
}

func TestAccClusterSecurityPolicyModeLifecycle(t *testing.T) {
	fake := newPolicyModeFake(t)
	config := func(mode string) string {
		return fake.providerBlock() + fmt.Sprintf(`
resource "ankra_cluster_security_policy_mode" "baseline" {
  cluster_id = %q
  mode       = %q
}
`, policyModeFakeClusterID, mode)
	}
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy: func(_ *terraform.State) error {
			if fake.requested == nil || *fake.requested != "audit" {
				return fmt.Errorf("destroy must leave the mode as it is")
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: config("enforce"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ankra_cluster_security_policy_mode.baseline", "id", policyModeFakeClusterID),
					resource.TestCheckResourceAttr("ankra_cluster_security_policy_mode.baseline", "policy_mode", "enforce"),
					resource.TestCheckResourceAttr("ankra_cluster_security_policy_mode.baseline", "policy_engine_installed", "true"),
					resource.TestCheckResourceAttr("ankra_cluster_security_policy_mode.baseline", "commit_sha", "abc123"),
				),
			},
			{
				Config: config("off"),
				Check:  resource.TestCheckResourceAttr("ankra_cluster_security_policy_mode.baseline", "policy_engine_installed", "false"),
			},
			{
				Config: config("audit"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ankra_cluster_security_policy_mode.baseline", "mode", "audit"),
					resource.TestCheckResourceAttr("ankra_cluster_security_policy_mode.baseline", "policy_mode", "off"),
				),
			},
			{
				ResourceName:            "ankra_cluster_security_policy_mode.baseline",
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"commit_sha"},
			},
			{
				PreConfig: func() {
					enforce := "enforce"
					fake.requested = &enforce
				},
				Config: config("audit"),
				Check: func(_ *terraform.State) error {
					if *fake.requested != "audit" || fake.sets != 4 {
						return fmt.Errorf("a mode changed outside Terraform must be set back: requested %s after %d sets", *fake.requested, fake.sets)
					}
					return nil
				},
			},
		},
	})
}

func TestAccClusterSecurityPolicyModeWithoutBaseline(t *testing.T) {
	fake := newPolicyModeFake(t)
	fake.hasBaseline = false
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: fake.providerBlock() + fmt.Sprintf(`
resource "ankra_cluster_security_policy_mode" "baseline" {
  cluster_id = %q
  mode       = "enforce"
}
`, policyModeFakeClusterID),
				ExpectError: regexp.MustCompile(`(?s)Unable to set the security policy mode.*not installed`),
			},
			{
				Config: fake.providerBlock() + `
resource "ankra_cluster_security_policy_mode" "baseline" {
  cluster_id = "c1000000-0000-0000-0000-000000000001"
  mode       = "strict"
}
`,
				ExpectError: regexp.MustCompile(`Invalid value`),
			},
		},
	})
}
