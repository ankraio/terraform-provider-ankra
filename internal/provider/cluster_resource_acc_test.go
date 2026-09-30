// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

// mockPlatform is an in-memory stand-in for the Ankra platform API that
// follows the import route's current contract
// (cluster go/internal/cliapi/imports.go):
//
//   - without wait=true it registers the cluster in the background and
//     answers 202 {"status":"accepted"} with no cluster_id;
//   - with wait=true it answers 200 with CreateOrUpdateImportClusterResult,
//     whose import_command is only issued when the cluster is new;
//   - a spec in a shape importedapi/specparse.go refuses is a 422.
type mockPlatform struct {
	mutex    sync.Mutex
	clusters map[string]string // id -> name
	nextID   int

	// ignoreWait answers every import 202, as a platform that queued it.
	ignoreWait bool
	// failAfterRegister registers the cluster and then answers 502, the way
	// a proxy does when the connection drops mid-import.
	failAfterRegister bool

	imports      int
	deletes      int
	lastQueryRaw string
}

func newMockPlatform(t *testing.T) (*mockPlatform, *httptest.Server) {
	platform := &mockPlatform{clusters: map[string]string{}}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		platform.mutex.Lock()
		defer platform.mutex.Unlock()
		writer.Header().Set("Content-Type", "application/json")

		switch {
		case request.Method == http.MethodPost && request.URL.Path == "/api/v1/clusters/import":
			platform.handleImport(writer, request)
		case request.Method == http.MethodGet && request.URL.Path == "/api/v1/clusters":
			_, _ = writer.Write([]byte(writeClusterListing(platform.clusters, request.URL.Query())))
		case request.Method == http.MethodDelete && strings.HasPrefix(request.URL.Path, "/api/v1/clusters/"):
			name := strings.TrimPrefix(request.URL.Path, "/api/v1/clusters/")
			for id, clusterName := range platform.clusters {
				if clusterName == name {
					delete(platform.clusters, id)
				}
			}
			platform.deletes++
			writer.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected request: %s %s", request.Method, request.URL.Path)
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
	return platform, server
}

func (platform *mockPlatform) handleImport(writer http.ResponseWriter, request *http.Request) {
	platform.imports++
	platform.lastQueryRaw = request.URL.RawQuery
	var body map[string]any
	decoder := json.NewDecoder(request.Body)
	decoder.UseNumber()
	if err := decoder.Decode(&body); err != nil {
		writer.WriteHeader(http.StatusUnprocessableEntity)
		return
	}
	if problem := specShapeProblem(body); problem != "" {
		writer.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = fmt.Fprintf(writer, `{"detail":[{"msg":%q}]}`, problem)
		return
	}

	name, _ := body["name"].(string)
	id, isNew := platform.register(name)

	if platform.failAfterRegister {
		writer.WriteHeader(http.StatusBadGateway)
		_, _ = writer.Write([]byte(`<html>502 Bad Gateway</html>`))
		return
	}
	wait := request.URL.Query().Get("wait") == "true"
	if !wait || platform.ignoreWait {
		writer.WriteHeader(http.StatusAccepted)
		_, _ = writer.Write([]byte(`{"status":"accepted"}`))
		return
	}
	importCommand := ""
	if isNew {
		importCommand = "helm install ankra-agent --set token=" + id
	}
	_, _ = fmt.Fprintf(writer, `{"name":%q,"cluster_id":%q,"organisation_id":"org-1","import_command":%q,`+
		`"errors":[],"commit_sha":null,"commit_url":null,"warnings":[]}`, name, id, importCommand)
}

func (platform *mockPlatform) register(name string) (string, bool) {
	for id, clusterName := range platform.clusters {
		if clusterName == name {
			return id, false
		}
	}
	platform.nextID++
	id := fmt.Sprintf("cluster-%d", platform.nextID)
	platform.clusters[id] = name
	return id, true
}

func (platform *mockPlatform) counts() (int, int) {
	platform.mutex.Lock()
	defer platform.mutex.Unlock()
	return platform.imports, platform.deletes
}

func (platform *mockPlatform) setFailAfterRegister(fail bool) {
	platform.mutex.Lock()
	defer platform.mutex.Unlock()
	platform.failAfterRegister = fail
}

// specShapeProblem applies the member-shape rules of the platform's spec
// parser: parents are {name, kind} objects, an addon carries registry_name
// and registry_url, and job_configuration is an object.
func specShapeProblem(body map[string]any) string {
	spec, _ := body["spec"].(map[string]any)
	stacks, _ := spec["stacks"].([]any)
	for _, rawStack := range stacks {
		stack, _ := rawStack.(map[string]any)
		members := []any{}
		if manifests, ok := stack["manifests"].([]any); ok {
			members = append(members, manifests...)
		}
		addons, _ := stack["addons"].([]any)
		for _, rawAddon := range addons {
			addon, _ := rawAddon.(map[string]any)
			for _, key := range []string{"name", "chart_name", "chart_version", "registry_name", "registry_url", "namespace"} {
				if value, _ := addon[key].(string); value == "" {
					return "addon field required: " + key
				}
			}
			if raw, present := addon["job_configuration"]; present {
				if _, isObject := raw.(map[string]any); !isObject {
					return "job_configuration: Input should be a valid dictionary"
				}
			}
			if raw, present := addon["configuration"]; present {
				if _, isObject := raw.(map[string]any); !isObject {
					return "configuration: Input should be a valid dictionary"
				}
			}
		}
		members = append(members, addons...)
		for _, rawMember := range members {
			member, _ := rawMember.(map[string]any)
			parents, present := member["parents"]
			if !present {
				continue
			}
			list, isList := parents.([]any)
			if !isList {
				return "parents: Input should be a valid list"
			}
			for _, rawParent := range list {
				parent, isObject := rawParent.(map[string]any)
				if !isObject {
					return "parents: Input should be a valid dictionary or object to extract fields from"
				}
				if kind, _ := parent["kind"].(string); kind != "manifest" && kind != "addon" {
					return "parents.kind invalid"
				}
				if name, _ := parent["name"].(string); name == "" {
					return "parents.name required"
				}
			}
		}
	}
	return ""
}

func clusterConfig(baseURL, token, chartVersion string) string {
	return fmt.Sprintf(`
provider "ankra" {
  token    = "provider-token"
  base_url = %q
}

resource "ankra_cluster" "test" {
  cluster_name           = "dev"
  github_credential_name = "cred"
  github_branch          = "main"
  github_repository      = "ankra-io/repo"
  ankra_token            = %q

  stacks {
    name = "base"
    manifests {
      name            = "ns"
      manifest_base64 = "YmFzZTY0"
    }
    addons {
      name              = "podinfo"
      chart_name        = "podinfo"
      chart_version     = %q
      repository_url    = "https://stefanprodan.github.io/podinfo"
      namespace         = "podinfo"
      configuration     = "replicaCount: 2"
      parents           = ["ns"]
      job_configuration = jsonencode({ create_job_timeout = 600 })
    }
  }
}

data "ankra_clusters" "all" {
  depends_on = [ankra_cluster.test]
}
`, baseURL, token, chartVersion)
}

// TestAccClusterResourceLifecycle creates, updates, re-tokens and destroys a
// cluster against the current import contract, using a 0.1.x-style
// configuration (repository_url, bare-string parents).
func TestAccClusterResourceLifecycle(t *testing.T) {
	platform, server := newMockPlatform(t)
	defer server.Close()

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: clusterConfig(server.URL, "token-a", "6.0.0"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ankra_cluster.test", "cluster_id", "cluster-1"),
					resource.TestCheckResourceAttr("ankra_cluster.test", "helm_command",
						"helm install ankra-agent --set token=cluster-1"),
					resource.TestCheckResourceAttr("ankra_cluster.test", "cluster_name", "dev"),
					resource.TestCheckResourceAttrSet("data.ankra_clusters.all", "clusters.0.id"),
					func(_ *terraform.State) error {
						platform.mutex.Lock()
						defer platform.mutex.Unlock()
						if platform.lastQueryRaw != "wait=true" {
							return fmt.Errorf("import query = %q, want wait=true", platform.lastQueryRaw)
						}
						return nil
					},
				),
			},
			{
				// A spec change re-imports in place; the re-import returns no
				// import_command, and the stored one must survive.
				Config: clusterConfig(server.URL, "token-a", "6.1.0"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("ankra_cluster.test", plancheck.ResourceActionUpdate),
					},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ankra_cluster.test", "cluster_id", "cluster-1"),
					resource.TestCheckResourceAttr("ankra_cluster.test", "helm_command",
						"helm install ankra-agent --set token=cluster-1"),
					func(_ *terraform.State) error {
						if imports, _ := platform.counts(); imports != 2 {
							return fmt.Errorf("imports = %d, want 2", imports)
						}
						return nil
					},
				),
			},
			{
				// Rotating the deprecated per-resource token is an in-place
				// update that does not even call the import route.
				Config: clusterConfig(server.URL, "token-b", "6.1.0"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("ankra_cluster.test", plancheck.ResourceActionUpdate),
					},
				},
				Check: func(_ *terraform.State) error {
					imports, deletes := platform.counts()
					if imports != 2 || deletes != 0 {
						return fmt.Errorf("imports = %d, deletes = %d; want 2 and 0", imports, deletes)
					}
					return nil
				},
			},
		},
	})
}

// TestAccClusterResourceAsyncImport covers a platform that queues the import
// (202 accepted, no cluster_id): the cluster is resolved by name instead of
// failing with "missing cluster_id" and leaving it orphaned.
func TestAccClusterResourceAsyncImport(t *testing.T) {
	platform, server := newMockPlatform(t)
	platform.ignoreWait = true
	defer server.Close()

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: clusterConfig(server.URL, "token-a", "6.0.0"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ankra_cluster.test", "cluster_id", "cluster-1"),
					resource.TestCheckResourceAttr("ankra_cluster.test", "helm_command", ""),
				),
			},
		},
	})
}

// TestAccClusterResourceNoOrphanOnFailedImport: when the import fails after
// the platform registered the cluster, the cluster lands in state (tainted)
// instead of outside Terraform, and the next apply replaces it.
func TestAccClusterResourceNoOrphanOnFailedImport(t *testing.T) {
	platform, server := newMockPlatform(t)
	platform.failAfterRegister = true
	defer server.Close()

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:      clusterConfig(server.URL, "token-a", "6.0.0"),
				ExpectError: regexp.MustCompile(`Cluster registered but the import did not complete`),
			},
			{
				PreConfig: func() { platform.setFailAfterRegister(false) },
				Config:    clusterConfig(server.URL, "token-a", "6.0.0"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("ankra_cluster.test", plancheck.ResourceActionReplace),
					},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ankra_cluster.test", "cluster_id", "cluster-2"),
					resource.TestCheckResourceAttr("ankra_cluster.test", "helm_command",
						"helm install ankra-agent --set token=cluster-2"),
				),
			},
		},
	})
}

// TestAccClusterResourceRejectsUnresolvableParent reports a parent that names
// no declared member at plan time, before anything is sent.
func TestAccClusterResourceRejectsUnresolvableParent(t *testing.T) {
	platform, server := newMockPlatform(t)
	defer server.Close()

	config := strings.Replace(clusterConfig(server.URL, "token-a", "6.0.0"),
		`parents           = ["ns"]`, `parents           = ["nope"]`, 1)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:      config,
				ExpectError: regexp.MustCompile(`Parent "nope" is not a manifest or addon declared`),
			},
		},
	})
	if imports, _ := platform.counts(); imports != 0 {
		t.Errorf("imports = %d, want 0", imports)
	}
}
