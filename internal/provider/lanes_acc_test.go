// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

// laneFake stands in for the platform across every provisioned cloud lane. It
// records the create payload so a test can assert what the provider sent, and
// serves the listing through the shared contract renderer.
type laneFake struct {
	server        *httptest.Server
	mutex         sync.Mutex
	clusters      map[string]string
	createdPath   string
	observedForce string
	// state is what the listing reports; "running" lets a create settle at once.
	state string
}

func newLaneFake(t *testing.T, state string) *laneFake {
	t.Helper()
	fake := &laneFake{clusters: map[string]string{}, state: state}
	created := 0

	fake.server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		fake.mutex.Lock()
		defer fake.mutex.Unlock()
		writer.Header().Set("Content-Type", "application/json")

		switch {
		case request.Method == http.MethodPost && strings.HasPrefix(request.URL.Path, "/api/v1/clusters/"):
			created++
			id := fmt.Sprintf("cl-%d", created)
			fake.createdPath = request.URL.Path
			fake.clusters[id] = "tf-lane"
			_, _ = fmt.Fprintf(writer, `{"cluster_id":%q}`, id)
		case request.Method == http.MethodGet && request.URL.Path == "/api/v1/clusters":
			_, _ = writer.Write([]byte(writeClusterListingWithState(fake.clusters, request.URL.Query(), fake.state)))
		case request.Method == http.MethodDelete:
			fake.observedForce = request.URL.Query().Get("force")
			fake.clusters = map[string]string{}
			writer.WriteHeader(http.StatusOK)
		default:
			t.Errorf("unexpected request: %s %s", request.Method, request.URL.Path)
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
	return fake
}

func (fake *laneFake) path() string {
	fake.mutex.Lock()
	defer fake.mutex.Unlock()
	return fake.createdPath
}

// TestAccProvisionedLanes exercises every cloud lane the provider exposes: a
// minimal config applies, the provider posts to the lane's own endpoint, and
// the platform-reported state and kind land in state.
func TestAccProvisionedLanes(t *testing.T) {
	lanes := []struct {
		resourceType string
		wantPath     string
		arguments    string
	}{
		{"ankra_hetzner_cluster", "/api/v1/clusters/hetzner", `
  credential_id = "cred-1"
  location      = "fsn1"`},
		{"ankra_digitalocean_cluster", "/api/v1/clusters/digitalocean", `
  credential_id         = "cred-1"
  region                = "fra1"
  ssh_key_credential_id = "ssh-1"`},
		{"ankra_upcloud_cluster", "/api/v1/clusters/upcloud", `
  credential_id         = "cred-1"
  zone                  = "de-fra1"
  ssh_key_credential_id = "ssh-1"`},
		{"ankra_ovh_cluster", "/api/v1/clusters/ovh", `
  credential_id         = "cred-1"
  region                = "GRA11"
  ssh_key_credential_id = "ssh-1"`},
		{"ankra_scaleway_cluster", "/api/v1/clusters/scaleway", `
  credential_id         = "cred-1"
  region                = "fr-par"
  zone                  = "fr-par-1"
  ssh_key_credential_id = "ssh-1"`},
	}

	for _, lane := range lanes {
		t.Run(lane.resourceType, func(subtest *testing.T) {
			fake := newLaneFake(subtest, "running")
			defer fake.server.Close()

			address := lane.resourceType + ".test"
			config := fmt.Sprintf(`
provider "ankra" {
  token    = "test-token"
  base_url = %q
}

resource %q "test" {
  name = "tf-lane"%s
}
`, fake.server.URL, lane.resourceType, lane.arguments)

			resource.Test(subtest, resource.TestCase{
				ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
				Steps: []resource.TestStep{
					{
						Config: config,
						Check: resource.ComposeAggregateTestCheckFunc(
							resource.TestCheckResourceAttrSet(address, "cluster_id"),
							resource.TestCheckResourceAttr(address, "distribution", "kubeadm"),
							resource.TestCheckResourceAttr(address, "cni", "cilium"),
							resource.TestCheckResourceAttr(address, "control_plane_count", "1"),
							resource.TestCheckResourceAttr(address, "worker_count", "1"),
							resource.TestCheckResourceAttr(address, "force_destroy", "true"),
							resource.TestCheckResourceAttr(address, "wait_for_ready", "true"),
							// Refreshed from the platform: this is the drift signal the
							// resource previously had no way to surface.
							resource.TestCheckResourceAttr(address, "state", "running"),
							resource.TestCheckResourceAttr(address, "kind", "hetzner"),
							func(*terraform.State) error {
								if got := fake.path(); got != lane.wantPath {
									return fmt.Errorf("created via %q, want %q", got, lane.wantPath)
								}
								return nil
							},
						),
					},
				},
			})
		})
	}
}

// TestAccWaitForReadyCanBeDisabled covers the escape hatch: an operator who
// does not need a ready cluster should not be held for the provisioning wait.
func TestAccWaitForReadyCanBeDisabled(t *testing.T) {
	fake := newLaneFake(t, "creating")
	defer fake.server.Close()

	config := fmt.Sprintf(`
provider "ankra" {
  token    = "test-token"
  base_url = %q
}

resource "ankra_hetzner_cluster" "nowait" {
  name           = "tf-lane"
  credential_id  = "cred-1"
  location       = "fsn1"
  wait_for_ready = false
}
`, fake.server.URL)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: config,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("ankra_hetzner_cluster.nowait", "cluster_id"),
					resource.TestCheckResourceAttr("ankra_hetzner_cluster.nowait", "state", "creating"),
				),
			},
		},
	})
}

// TestAccCreateTimeoutSurfacesAsError proves the configurable timeout is wired
// through to the readiness wait: a cluster that never leaves "creating" must
// fail the apply rather than hang.
func TestAccCreateTimeoutSurfacesAsError(t *testing.T) {
	fake := newLaneFake(t, "creating")
	defer fake.server.Close()

	config := fmt.Sprintf(`
provider "ankra" {
  token    = "test-token"
  base_url = %q
}

resource "ankra_hetzner_cluster" "slow" {
  name          = "tf-lane"
  credential_id = "cred-1"
  location      = "fsn1"

  timeouts {
    create = "2s"
  }
}
`, fake.server.URL)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:      config,
				ExpectError: regexp.MustCompile("(?s)timed out waiting for cluster"),
			},
		},
	})
}
