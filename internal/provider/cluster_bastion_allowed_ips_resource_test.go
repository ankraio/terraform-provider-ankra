// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

const (
	bastionFakeClusterID      = "0b6f1d2e-1111-4222-8333-944445555666"
	bastionFakeAWSClusterID   = "0b6f1d2e-1111-4222-8333-944445555777"
	bastionFakeOtherProjectID = "bbbbbbbb-2222-4222-8222-222222222222"
)

// bastionFakeCluster is one cluster and the allowlist its bastion holds:
// nil is never configured, an empty slice is cleared.
type bastionFakeCluster struct {
	kind       string
	project    string
	allowedIPs []string
	hasBastion bool
}

type bastionFake struct {
	*routeFake
	clusters map[string]*bastionFakeCluster
	// writes records every list the provider stored.
	writes [][]string
	// readMissing answers the read 405, as a platform from before
	// 2026-10-18 does: only the PUT is mounted there.
	readMissing bool
	// runningReads is how many reads of a write's operation answer
	// "running" before it ends with finalStatus ("success" when empty);
	// operationsMissing answers every operation read 404.
	runningReads      int
	finalStatus       string
	operationsMissing bool
	// operationReads counts reads of each operation; pendingReads is what
	// is left of runningReads for each.
	operationReads map[string]int
	pendingReads   map[string]int
}

func newBastionFake(t *testing.T) *bastionFake {
	previousInterval := bastionAllowedIPsPollInterval
	bastionAllowedIPsPollInterval = 10 * time.Millisecond
	t.Cleanup(func() { bastionAllowedIPsPollInterval = previousInterval })
	fake := &bastionFake{routeFake: newRouteFake(t), operationReads: map[string]int{}, pendingReads: map[string]int{}, clusters: map[string]*bastionFakeCluster{
		bastionFakeClusterID:    {kind: "hetzner", hasBastion: true},
		bastionFakeAWSClusterID: {kind: "aws", hasBastion: true},
	}}
	visible := func(cluster *bastionFakeCluster, request *http.Request) bool {
		return cluster.project == "" || cluster.project == request.Header.Get("X-Ankra-Project-Id")
	}
	lookup := func(writer http.ResponseWriter, request *http.Request) *bastionFakeCluster {
		cluster, exists := fake.clusters[request.PathValue("id")]
		if !exists || !visible(cluster, request) {
			writeJSON(t, writer, http.StatusNotFound, map[string]any{"detail": "Cluster not found"})
			return nil
		}
		if cluster.kind != request.PathValue("lane") {
			writeJSON(t, writer, http.StatusConflict, map[string]any{"detail": "This cluster is not a " + request.PathValue("lane") + " cluster"})
			return nil
		}
		if !cluster.hasBastion {
			writeJSON(t, writer, http.StatusConflict, map[string]any{"detail": "No bastion or gateway node found for this cluster"})
			return nil
		}
		return cluster
	}
	fake.handle("GET /api/v1/clusters", func(writer http.ResponseWriter, request *http.Request) {
		wanted := request.URL.Query().Get("cluster_id")
		rows := []map[string]any{}
		for id, cluster := range fake.clusters {
			if (wanted != "" && wanted != id) || !visible(cluster, request) {
				continue
			}
			rows = append(rows, map[string]any{"id": id, "name": "cluster-" + cluster.kind, "kind": cluster.kind, "state": "running"})
		}
		writeJSON(t, writer, http.StatusOK, map[string]any{
			"result":     rows,
			"pagination": map[string]any{"total_count": len(rows), "total_pages": 1, "page": 1, "page_size": 100},
		})
	})
	fake.handle("GET /api/v1/clusters/{lane}/{id}/bastion/allowed-ips", func(writer http.ResponseWriter, request *http.Request) {
		if fake.readMissing {
			writeJSON(t, writer, http.StatusMethodNotAllowed, map[string]any{"detail": "Method Not Allowed"})
			return
		}
		cluster := lookup(writer, request)
		if cluster == nil {
			return
		}
		listed := cluster.allowedIPs
		if listed == nil {
			listed = []string{}
		}
		writeJSON(t, writer, http.StatusOK, map[string]any{
			"node_id": "bastion-1", "kind": cluster.kind + "_bastion", "name": "bastion",
			"bastion_allowed_ips": listed, "configured": cluster.allowedIPs != nil,
		})
	})
	fake.handle("PUT /api/v1/clusters/{lane}/{id}/bastion/allowed-ips", func(writer http.ResponseWriter, request *http.Request) {
		cluster := lookup(writer, request)
		if cluster == nil {
			return
		}
		var body struct {
			BastionAllowedIPs []string `json:"bastion_allowed_ips"`
		}
		decodeJSON(t, request, &body)
		if body.BastionAllowedIPs == nil {
			writeJSON(t, writer, http.StatusUnprocessableEntity, map[string]any{"detail": "bastion_allowed_ips is required"})
			return
		}
		var operationID any
		if cluster.allowedIPs == nil || !slices.Equal(cluster.allowedIPs, body.BastionAllowedIPs) {
			identifier := fmt.Sprintf("operation-%d", len(fake.writes)+1)
			fake.pendingReads[identifier] = fake.runningReads
			operationID = identifier
		}
		cluster.allowedIPs = append([]string{}, body.BastionAllowedIPs...)
		fake.writes = append(fake.writes, cluster.allowedIPs)
		writeJSON(t, writer, http.StatusOK, map[string]any{
			"node_id": "bastion-1", "kind": cluster.kind + "_bastion", "name": "bastion",
			"bastion_allowed_ips": cluster.allowedIPs, "operation_id": operationID,
		})
	})
	fake.handle("GET /api/v1/org/executions/{id}", func(writer http.ResponseWriter, request *http.Request) {
		identifier := request.PathValue("id")
		fake.operationReads[identifier]++
		pending, known := fake.pendingReads[identifier]
		if fake.operationsMissing || !known {
			writeJSON(t, writer, http.StatusNotFound, map[string]any{"detail": "Execution not found"})
			return
		}
		status := "running"
		if pending > 0 {
			fake.pendingReads[identifier] = pending - 1
		} else {
			status = fake.finalStatus
			if status == "" {
				status = "success"
			}
		}
		execution := map[string]any{"id": identifier, "name": "update_bastion_allowed_ips", "status": status, "error_excerpt": nil}
		if status == "failed" {
			execution["error_excerpt"] = "the provider firewall refused the update"
		}
		writeJSON(t, writer, http.StatusOK, map[string]any{"execution": execution})
	})
	return fake
}

// lastOperation is the operation of the provider's last write.
func (fake *bastionFake) lastOperation() string {
	return fmt.Sprintf("operation-%d", len(fake.writes))
}

func bastionFakeConfiguration(fake *bastionFake, clusterID string, cidrs string) string {
	return fake.providerBlock() + fmt.Sprintf(`
resource "ankra_cluster_bastion_allowed_ips" "main" {
  cluster_id = %q
  cidrs      = [%s]
}
`, clusterID, cidrs)
}

func bastionFakeExpect(fake *bastionFake, expected ...string) resource.TestCheckFunc {
	return func(_ *terraform.State) error {
		stored := fake.clusters[bastionFakeClusterID].allowedIPs
		sorted := append([]string{}, stored...)
		slices.Sort(sorted)
		if stored == nil || !slices.Equal(sorted, expected) {
			return fmt.Errorf("the bastion holds %v, want %v", stored, expected)
		}
		return nil
	}
}

// TestAccClusterBastionAllowedIPsLifecycle pins create, in-place change,
// drift from a change made outside Terraform, import, and a destroy that
// clears the list.
func TestAccClusterBastionAllowedIPsLifecycle(t *testing.T) {
	fake := newBastionFake(t)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy: func(_ *terraform.State) error {
			stored := fake.clusters[bastionFakeClusterID].allowedIPs
			if stored == nil || len(stored) != 0 {
				return fmt.Errorf("destroy left the bastion holding %v, want a cleared list", stored)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: bastionFakeConfiguration(fake, bastionFakeClusterID, `"203.0.113.0/24", "198.51.100.7/32"`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ankra_cluster_bastion_allowed_ips.main", "id", bastionFakeClusterID),
					resource.TestCheckResourceAttr("ankra_cluster_bastion_allowed_ips.main", "cluster_kind", "hetzner"),
					resource.TestCheckResourceAttr("ankra_cluster_bastion_allowed_ips.main", "cidrs.#", "2"),
					bastionFakeExpect(fake, "198.51.100.7/32", "203.0.113.0/24"),
				),
			},
			{
				Config: bastionFakeConfiguration(fake, bastionFakeClusterID, `"203.0.113.0/24"`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("ankra_cluster_bastion_allowed_ips.main", plancheck.ResourceActionUpdate),
					},
				},
				Check: bastionFakeExpect(fake, "203.0.113.0/24"),
			},
			{
				// Changed outside Terraform: the read shows it and the apply
				// sets the configured list back.
				PreConfig: func() { fake.clusters[bastionFakeClusterID].allowedIPs = []string{"192.0.2.0/24"} },
				Config:    bastionFakeConfiguration(fake, bastionFakeClusterID, `"203.0.113.0/24"`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("ankra_cluster_bastion_allowed_ips.main", plancheck.ResourceActionUpdate),
					},
				},
				Check: bastionFakeExpect(fake, "203.0.113.0/24"),
			},
			{
				ResourceName:      "ankra_cluster_bastion_allowed_ips.main",
				ImportState:       true,
				ImportStateId:     bastionFakeClusterID,
				ImportStateVerify: true,
			},
			{
				// An empty set clears the list in place.
				Config: bastionFakeConfiguration(fake, bastionFakeClusterID, ``),
				Check: func(_ *terraform.State) error {
					stored := fake.clusters[bastionFakeClusterID].allowedIPs
					if stored == nil || len(stored) != 0 {
						return fmt.Errorf("the bastion holds %v, want a cleared list", stored)
					}
					return nil
				},
			},
		},
	})
}

// TestAccClusterBastionAllowedIPsNeverConfigured pins that a bastion whose
// list was never set (for example one rebuilt since) plans the list again.
func TestAccClusterBastionAllowedIPsNeverConfigured(t *testing.T) {
	fake := newBastionFake(t)
	configuration := bastionFakeConfiguration(fake, bastionFakeClusterID, `"203.0.113.0/24"`)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{Config: configuration},
			{
				PreConfig: func() { fake.clusters[bastionFakeClusterID].allowedIPs = nil },
				Config:    configuration,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("ankra_cluster_bastion_allowed_ips.main", plancheck.ResourceActionCreate),
					},
				},
				Check: bastionFakeExpect(fake, "203.0.113.0/24"),
			},
		},
	})
}

// TestAccClusterBastionAllowedIPsScopeMove pins scope tracking: after the
// provider's project changes, the 404 the platform answers keeps the
// resource in state with an error, and destroy does not report success.
func TestAccClusterBastionAllowedIPsScopeMove(t *testing.T) {
	fake := newBastionFake(t)
	inProject := func(project string) string {
		return fmt.Sprintf(`
provider "ankra" {
  token      = "test-token"
  base_url   = %q
  project_id = %q
}

resource "ankra_cluster_bastion_allowed_ips" "main" {
  cluster_id = %q
  cidrs      = ["203.0.113.0/24"]
}
`, fake.server.URL, project, bastionFakeClusterID)
	}
	const firstProject = "aaaaaaaa-1111-4111-8111-111111111111"
	fake.clusters[bastionFakeClusterID].project = firstProject
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{Config: inProject(firstProject)},
			{
				Config:      inProject(bastionFakeOtherProjectID),
				ExpectError: regexp.MustCompile(`Object not visible in the provider's current scope`),
			},
			{
				Config:      inProject(bastionFakeOtherProjectID),
				Destroy:     true,
				ExpectError: regexp.MustCompile(`Object not visible in the provider's current scope`),
			},
			{
				Config: inProject(firstProject),
				Check:  bastionFakeExpect(fake, "203.0.113.0/24"),
			},
		},
	})
}

// TestAccClusterBastionAllowedIPsRefusals pins the plan-time and create-time
// refusals: entries the platform refuses or would respell, a cluster kind
// without the allowlist, and a platform without the read.
func TestAccClusterBastionAllowedIPsRefusals(t *testing.T) {
	for name, testCase := range map[string]struct {
		cidrs    string
		expected string
	}{
		"IPv6":             {`"2001:db8::/32"`, "is IPv6"},
		"every source":     {`"0.0.0.0/0"`, "allows every source"},
		"bare address":     {`"203.0.113.7"`, `stored by the platform as "203.0.113.7/32"`},
		"host bits":        {`"203.0.113.7/24"`, `stored by the platform as "203.0.113.0/24"`},
		"not an address":   {`"bastion.example"`, "is not an IPv4 address or CIDR"},
		"surrounding text": {`" 203.0.113.0/24"`, "canonical form"},
	} {
		t.Run(name, func(t *testing.T) {
			fake := newBastionFake(t)
			resource.Test(t, resource.TestCase{
				ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
				Steps: []resource.TestStep{
					{
						Config:      bastionFakeConfiguration(fake, bastionFakeClusterID, testCase.cidrs),
						PlanOnly:    true,
						ExpectError: regexp.MustCompile(regexp.QuoteMeta(testCase.expected)),
					},
				},
			})
			if served := fake.served(); len(served) != 0 {
				t.Errorf("an invalid configuration reached the platform: %v", served)
			}
		})
	}

	t.Run("cluster kind without the allowlist", func(t *testing.T) {
		fake := newBastionFake(t)
		resource.Test(t, resource.TestCase{
			ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config:      bastionFakeConfiguration(fake, bastionFakeAWSClusterID, `"203.0.113.0/24"`),
					ExpectError: regexp.MustCompile(`(?s)Cluster has no bastion SSH allowlist.*bastion_allowed_ips`),
				},
			},
		})
		if len(fake.writes) != 0 {
			t.Errorf("an AWS cluster got writes %v", fake.writes)
		}
	})

	t.Run("platform without the read", func(t *testing.T) {
		fake := newBastionFake(t)
		fake.readMissing = true
		resource.Test(t, resource.TestCase{
			ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config:      bastionFakeConfiguration(fake, bastionFakeClusterID, `"203.0.113.0/24"`),
					ExpectError: regexp.MustCompile(`(?s)platform\s+from\s+2026-10-18\s+or\s+later`),
				},
			},
		})
		if len(fake.writes) != 0 {
			t.Errorf("a platform without the read got writes %v", fake.writes)
		}
	})
}

// TestAccClusterBastionAllowedIPsDestroyWaitsForTheClear pins that destroy
// returns only once the operation applying the cleared list has ended, so a
// cluster destroyed in the same run does not race it.
func TestAccClusterBastionAllowedIPsDestroyWaitsForTheClear(t *testing.T) {
	fake := newBastionFake(t)
	fake.runningReads = 3
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy: func(_ *terraform.State) error {
			stored := fake.clusters[bastionFakeClusterID].allowedIPs
			if stored == nil || len(stored) != 0 {
				return fmt.Errorf("destroy left the bastion holding %v", stored)
			}
			if reads := fake.operationReads[fake.lastOperation()]; reads != 4 {
				return fmt.Errorf("destroy read the clear operation %d times, want 4 (three running, then success)", reads)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{Config: bastionFakeConfiguration(fake, bastionFakeClusterID, `"203.0.113.0/24"`)},
		},
	})
}

// TestAccClusterBastionAllowedIPsDestroyReportsAFailedClear pins that a clear
// operation that fails, or outlasts the delete timeout, fails the destroy and
// keeps the resource. An operation the platform no longer finds counts as
// settled.
func TestAccClusterBastionAllowedIPsDestroyReportsAFailedClear(t *testing.T) {
	for name, testCase := range map[string]struct {
		prepare  func(fake *bastionFake)
		timeouts string
		expected string
	}{
		"failed": {prepare: func(fake *bastionFake) { fake.finalStatus = "failed" }, expected: `(?s)ended\s+failed.*firewall\s+refused`},
		"timed out": {
			prepare:  func(fake *bastionFake) { fake.runningReads = 1000000 },
			timeouts: "\n\n  timeouts {\n    delete = \"1s\"\n  }",
			expected: `(?s)still\s+running\s+at\s+the\s+delete\s+timeout`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			fake := newBastionFake(t)
			configuration := fake.providerBlock() + fmt.Sprintf(`
resource "ankra_cluster_bastion_allowed_ips" "main" {
  cluster_id = %q
  cidrs      = ["203.0.113.0/24"]%s
}
`, bastionFakeClusterID, testCase.timeouts)
			resource.Test(t, resource.TestCase{
				ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
				Steps: []resource.TestStep{
					{Config: configuration},
					{
						PreConfig:   func() { testCase.prepare(fake) },
						Config:      configuration,
						Destroy:     true,
						ExpectError: regexp.MustCompile(testCase.expected),
					},
					{
						// The failed destroy kept the resource: the refresh sees
						// the cleared list as drift and sets it again.
						PreConfig: func() {
							fake.finalStatus = ""
							fake.runningReads = 0
						},
						Config: configuration,
						Check:  bastionFakeExpect(fake, "203.0.113.0/24"),
					},
				},
				CheckDestroy: func(_ *terraform.State) error {
					if stored := fake.clusters[bastionFakeClusterID].allowedIPs; stored == nil || len(stored) != 0 {
						return fmt.Errorf("destroy left the bastion holding %v", stored)
					}
					return nil
				},
			})
		})
	}

	t.Run("operation no longer found", func(t *testing.T) {
		fake := newBastionFake(t)
		fake.operationsMissing = true
		resource.Test(t, resource.TestCase{
			ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
			CheckDestroy: func(_ *terraform.State) error {
				if stored := fake.clusters[bastionFakeClusterID].allowedIPs; stored == nil || len(stored) != 0 {
					return fmt.Errorf("destroy left the bastion holding %v", stored)
				}
				return nil
			},
			Steps: []resource.TestStep{
				{Config: bastionFakeConfiguration(fake, bastionFakeClusterID, `"203.0.113.0/24"`)},
			},
		})
	})
}
