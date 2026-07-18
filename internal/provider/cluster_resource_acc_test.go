// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

// mockPlatform is an in-memory stand-in for the Ankra platform API, sufficient
// to exercise the cluster resource lifecycle without live credentials.
type mockPlatform struct {
	mutex    sync.Mutex
	clusters map[string]string // id -> name
	nextID   int
	deleted  bool
}

func newMockPlatformServer(t *testing.T) *httptest.Server {
	platform := &mockPlatform{clusters: map[string]string{}}
	return httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		platform.mutex.Lock()
		defer platform.mutex.Unlock()
		writer.Header().Set("Content-Type", "application/json")

		switch {
		case request.Method == http.MethodPost && request.URL.Path == "/api/v1/clusters/import":
			platform.nextID++
			id := fmt.Sprintf("cluster-%d", platform.nextID)
			platform.clusters[id] = "dev"
			platform.deleted = false
			_, _ = fmt.Fprintf(writer, `{"cluster_id":%q,"import_command":"helm install ankra-agent"}`, id)
		case request.Method == http.MethodGet && request.URL.Path == "/api/v1/clusters":
			_, _ = writer.Write([]byte(platform.listBody()))
		case request.Method == http.MethodDelete:
			platform.clusters = map[string]string{}
			platform.deleted = true
			writer.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected request: %s %s", request.Method, request.URL.Path)
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
}

func (platform *mockPlatform) listBody() string {
	body := `{"clusters":[`
	first := true
	for id, name := range platform.clusters {
		if !first {
			body += ","
		}
		body += fmt.Sprintf(`{"id":%q,"name":%q}`, id, name)
		first = false
	}
	return body + "]}"
}

func TestAccClusterResourceLifecycle(t *testing.T) {
	server := newMockPlatformServer(t)
	defer server.Close()

	config := fmt.Sprintf(`
provider "ankra" {
  token    = "test-token"
  base_url = %q
}

resource "ankra_cluster" "test" {
  cluster_name           = "dev"
  github_credential_name = "cred"
  github_branch          = "main"
  github_repository      = "ankra-io/repo"

  stacks {
    name = "base"
    manifests {
      name            = "ns"
      manifest_base64 = "YmFzZTY0"
    }
  }
}

data "ankra_clusters" "all" {
  depends_on = [ankra_cluster.test]
}
`, server.URL)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: config,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("ankra_cluster.test", "cluster_id"),
					resource.TestCheckResourceAttr("ankra_cluster.test", "helm_command", "helm install ankra-agent"),
					resource.TestCheckResourceAttr("ankra_cluster.test", "cluster_name", "dev"),
					resource.TestCheckResourceAttrSet("data.ankra_clusters.all", "clusters.0.id"),
				),
			},
		},
	})
}
