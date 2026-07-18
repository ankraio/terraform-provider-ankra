// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func newMockHetznerServer(t *testing.T) *httptest.Server {
	var mutex sync.Mutex
	clusters := map[string]string{} // id -> name
	created := 0

	listBody := func() string {
		body := `{"clusters":[`
		first := true
		for id, name := range clusters {
			if !first {
				body += ","
			}
			body += fmt.Sprintf(`{"id":%q,"name":%q}`, id, name)
			first = false
		}
		return body + "]}"
	}

	return httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		mutex.Lock()
		defer mutex.Unlock()
		writer.Header().Set("Content-Type", "application/json")

		switch {
		case request.Method == http.MethodPost && request.URL.Path == "/api/v1/clusters/hetzner":
			created++
			id := fmt.Sprintf("hz-%d", created)
			clusters[id] = "tf-hz"
			_, _ = fmt.Fprintf(writer, `{"cluster_id":%q,"name":"tf-hz"}`, id)
		case request.Method == http.MethodGet && request.URL.Path == "/api/v1/clusters":
			_, _ = writer.Write([]byte(listBody()))
		case request.Method == http.MethodDelete && strings.HasPrefix(request.URL.Path, "/api/v1/clusters/hetzner/"):
			if request.URL.Query().Get("force") != "true" {
				t.Errorf("expected force=true on delete, got %q", request.URL.RawQuery)
			}
			id := strings.TrimPrefix(request.URL.Path, "/api/v1/clusters/hetzner/")
			delete(clusters, id)
			writer.WriteHeader(http.StatusOK)
		default:
			t.Errorf("unexpected request: %s %s", request.Method, request.URL.Path)
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
}

func TestAccHetznerClusterResourceLifecycle(t *testing.T) {
	server := newMockHetznerServer(t)
	defer server.Close()

	config := fmt.Sprintf(`
provider "ankra" {
  token    = "test-token"
  base_url = %q
}

resource "ankra_hetzner_cluster" "test" {
  name          = "tf-hz"
  credential_id = "cred-1"
  location      = "fsn1"
  worker_count  = 2
}
`, server.URL)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: config,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("ankra_hetzner_cluster.test", "cluster_id"),
					resource.TestCheckResourceAttr("ankra_hetzner_cluster.test", "distribution", "k3s"),
					resource.TestCheckResourceAttr("ankra_hetzner_cluster.test", "worker_count", "2"),
					resource.TestCheckResourceAttr("ankra_hetzner_cluster.test", "control_plane_count", "1"),
					resource.TestCheckResourceAttr("ankra_hetzner_cluster.test", "external_cloud_provider", "true"),
				),
			},
		},
	})
}
