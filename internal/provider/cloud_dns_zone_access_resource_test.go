// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

const (
	cloudDNSFakeZoneID    = "zone-apps"
	cloudDNSFakeProjectID = "9d000000-0000-0000-0000-000000000005"
)

// cloudDNSAccessFake mirrors the Cloud DNS access routes
// (go/internal/cloudaccessapi): mode and roles are required, unknown fields
// are refused, an absent project_id keeps the zone's project limit and an
// empty one removes it.
type cloudDNSAccessFake struct {
	*routeFake
	access map[string]any
	writes []map[string]json.RawMessage
}

func newCloudDNSAccessFake(t *testing.T) *cloudDNSAccessFake {
	fake := &cloudDNSAccessFake{routeFake: newRouteFake(t), access: map[string]any{
		"mode": "public", "roles": []string{}, "organisation_id": "0a000000-0000-0000-0000-0000000000aa", "inherited_from": "zone-root",
	}}
	fake.handle("GET /api/v1/org/projects", func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(t, writer, http.StatusOK, map[string]any{"projects": []map[string]any{
			{"id": cloudDNSFakeProjectID, "name": "Platform", "slug": "platform", "is_default": true},
		}})
	})
	fake.handle("GET /api/v1/org/cloud-dns/zones", func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(t, writer, http.StatusOK, map[string]any{"items": []map[string]string{
			{"id": "zone-root", "name": "acme.ankra.app", "parent_zone_id": ""},
			{"id": cloudDNSFakeZoneID, "name": "apps.acme.ankra.app", "parent_zone_id": "zone-root"},
		}})
	})
	fake.handle("GET /api/v1/org/cloud-dns/zones/{zone_id}/access", func(writer http.ResponseWriter, request *http.Request) {
		if request.PathValue("zone_id") != cloudDNSFakeZoneID {
			writeJSON(t, writer, http.StatusNotFound, map[string]any{"detail": "zone not found"})
			return
		}
		writeJSON(t, writer, http.StatusOK, map[string]any{"access": fake.access})
	})
	fake.handle("PUT /api/v1/org/cloud-dns/zones/{zone_id}/access", func(writer http.ResponseWriter, request *http.Request) {
		var body map[string]json.RawMessage
		decodeJSON(t, request, &body)
		fake.writes = append(fake.writes, body)
		var mode string
		var roles []string
		_ = json.Unmarshal(body["mode"], &mode)
		if json.Unmarshal(body["roles"], &roles) != nil || roles == nil || mode == "" {
			writeJSON(t, writer, http.StatusBadRequest, map[string]any{"detail": "Invalid access policy."})
			return
		}
		fake.access["mode"] = mode
		fake.access["roles"] = roles
		delete(fake.access, "inherited_from")
		if raw, present := body["project_id"]; present {
			var projectID string
			_ = json.Unmarshal(raw, &projectID)
			if projectID == "" {
				delete(fake.access, "project_id")
			} else {
				fake.access["project_id"] = projectID
			}
		}
		writeJSON(t, writer, http.StatusOK, map[string]any{"access": fake.access})
	})
	return fake
}

func TestAccCloudDNSZoneAccessLifecycle(t *testing.T) {
	fake := newCloudDNSAccessFake(t)
	config := func(body string) string {
		return fake.providerBlock() + fmt.Sprintf(`
data "ankra_cloud_dns_zones" "all" {}

resource "ankra_cloud_dns_zone_access" "apps" {
  zone_id = %q
%s}
`, cloudDNSFakeZoneID, body)
	}
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy: func(_ *terraform.State) error {
			if fake.access["mode"] != "organisation" {
				return fmt.Errorf("destroy must leave the zone gated, it reads %v", fake.access)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: config("  mode       = \"organisation\"\n  roles      = [\"admin\", \"operator\"]\n  project_id = \"platform\"\n"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.ankra_cloud_dns_zones.all", "zones.#", "2"),
					resource.TestCheckNoResourceAttr("data.ankra_cloud_dns_zones.all", "zones.0.parent_zone_id"),
					resource.TestCheckResourceAttr("data.ankra_cloud_dns_zones.all", "zones.1.parent_zone_id", "zone-root"),
					resource.TestCheckResourceAttr("ankra_cloud_dns_zone_access.apps", "id", cloudDNSFakeZoneID),
					resource.TestCheckResourceAttr("ankra_cloud_dns_zone_access.apps", "project_id", "platform"),
					resource.TestCheckNoResourceAttr("ankra_cloud_dns_zone_access.apps", "inherited_from"),
					func(_ *terraform.State) error {
						if fake.access["project_id"] != cloudDNSFakeProjectID {
							return fmt.Errorf("the project slug must be sent as the project's ID, the zone holds %v", fake.access["project_id"])
						}
						return nil
					},
				),
			},
			{
				Config: config("  mode       = \"organisation\"\n  roles      = [\"admin\", \"operator\"]\n  project_id = \"platform\"\n"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
			{
				Config: config("  mode = \"organisation\"\n"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckNoResourceAttr("ankra_cloud_dns_zone_access.apps", "roles"),
					resource.TestCheckNoResourceAttr("ankra_cloud_dns_zone_access.apps", "project_id"),
					func(_ *terraform.State) error {
						last := fake.writes[len(fake.writes)-1]
						if string(last["project_id"]) != `""` || string(last["roles"]) != "[]" {
							return fmt.Errorf("an unset project and roles must be cleared, sent project_id %s roles %s", last["project_id"], last["roles"])
						}
						return nil
					},
				),
			},
			{
				ResourceName:      "ankra_cloud_dns_zone_access.apps",
				ImportState:       true,
				ImportStateId:     cloudDNSFakeZoneID,
				ImportStateVerify: true,
			},
		},
	})
}

func TestAccCloudDNSZoneAccessPublicRefusesLimits(t *testing.T) {
	fake := newCloudDNSAccessFake(t)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: fake.providerBlock() + `
resource "ankra_cloud_dns_zone_access" "apps" {
  zone_id = "zone-apps"
  mode    = "public"
  roles   = ["admin"]
}
`,
				ExpectError: regexp.MustCompile(`roles needs mode organisation`),
			},
			{
				Config: fake.providerBlock() + `
resource "ankra_cloud_dns_zone_access" "apps" {
  zone_id = "zone-apps"
  mode    = "organisation"
  roles   = []
}
`,
				ExpectError: regexp.MustCompile(`Empty roles`),
			},
		},
	})
}
