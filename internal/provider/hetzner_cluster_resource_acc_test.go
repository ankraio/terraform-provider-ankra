// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

// hetznerFake records what the provider sent so a test can assert on the
// destroy contract as well as the create one.
type hetznerFake struct {
	server        *httptest.Server
	mutex         sync.Mutex
	observedForce string
}

// forceOnDestroy reports the force query value seen on the last deprovision.
func (fake *hetznerFake) forceOnDestroy() string {
	fake.mutex.Lock()
	defer fake.mutex.Unlock()
	return fake.observedForce
}

func newMockHetznerServer(t *testing.T) *hetznerFake {
	fake := &hetznerFake{}
	clusters := map[string]string{} // id -> name
	created := 0

	listBody := func(query url.Values) string {
		return writeClusterListing(clusters, query)
	}

	fake.server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		fake.mutex.Lock()
		defer fake.mutex.Unlock()
		writer.Header().Set("Content-Type", "application/json")

		switch {
		case request.Method == http.MethodPost && request.URL.Path == "/api/v1/clusters/hetzner":
			created++
			id := fmt.Sprintf("hz-%d", created)
			clusters[id] = "tf-hz"
			_, _ = fmt.Fprintf(writer, `{"cluster_id":%q,"name":"tf-hz"}`, id)
		case request.Method == http.MethodGet && request.URL.Path == "/api/v1/clusters":
			_, _ = writer.Write([]byte(listBody(request.URL.Query())))
		case request.Method == http.MethodDelete && strings.HasPrefix(request.URL.Path, "/api/v1/clusters/hetzner/"):
			fake.observedForce = request.URL.Query().Get("force")
			id := strings.TrimPrefix(request.URL.Path, "/api/v1/clusters/hetzner/")
			delete(clusters, id)
			writer.WriteHeader(http.StatusOK)
		default:
			t.Errorf("unexpected request: %s %s", request.Method, request.URL.Path)
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
	return fake
}

func TestAccHetznerClusterResourceLifecycle(t *testing.T) {
	fake := newMockHetznerServer(t)
	defer fake.server.Close()

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
`, fake.server.URL)

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

// TestAccHetznerClusterHonoursForceDestroy covers the destroy contract: the
// provider used to hardcode force=true, tearing down cloud resources without
// the platform's guards and giving the operator no way to opt out.
func TestAccHetznerClusterHonoursForceDestroy(t *testing.T) {
	fake := newMockHetznerServer(t)
	defer fake.server.Close()

	config := fmt.Sprintf(`
provider "ankra" {
  token    = "test-token"
  base_url = %q
}

resource "ankra_hetzner_cluster" "guarded" {
  name          = "tf-hz"
  credential_id = "cred-1"
  location      = "fsn1"
  force_destroy = false
}
`, fake.server.URL)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: config,
				Check: resource.TestCheckResourceAttr(
					"ankra_hetzner_cluster.guarded", "force_destroy", "false"),
			},
		},
		CheckDestroy: func(*terraform.State) error {
			if got := fake.forceOnDestroy(); got != "false" {
				return fmt.Errorf("force on destroy = %q, want \"false\"", got)
			}
			return nil
		},
	})
}

// TestAccHetznerClusterDefaultsToForceDestroy pins the backwards-compatible
// default, so the opt-out cannot silently change existing behaviour.
func TestAccHetznerClusterDefaultsToForceDestroy(t *testing.T) {
	fake := newMockHetznerServer(t)
	defer fake.server.Close()

	config := fmt.Sprintf(`
provider "ankra" {
  token    = "test-token"
  base_url = %q
}

resource "ankra_hetzner_cluster" "default" {
  name          = "tf-hz"
  credential_id = "cred-1"
  location      = "fsn1"
}
`, fake.server.URL)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: config,
				Check: resource.TestCheckResourceAttr(
					"ankra_hetzner_cluster.default", "force_destroy", "true"),
			},
		},
		CheckDestroy: func(*terraform.State) error {
			if got := fake.forceOnDestroy(); got != "true" {
				return fmt.Errorf("force on destroy = %q, want \"true\"", got)
			}
			return nil
		},
	})
}
