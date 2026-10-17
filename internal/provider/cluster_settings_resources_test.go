// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

const (
	clusterSettingsTestCluster = "0b6f1d2e-1111-4222-8333-944445555666"
	clusterSettingsOtherMember = "member-2"
)

// clusterSettingsFake models the cluster settings routes the way the
// platform answers them (cluster go/internal/kubeproxyapi, providerapi,
// importedapi and cliapi): grant creation 200 with a re-time when the body
// names a matching grant and an expiry, 409 without one; revoke 200
// {"deleted":true} or 404; zones normalised and listed with inherited
// organisation zones; power schedule writes answer the full listing and an
// update refuses an omitted enabled, stop_mode or cron timezone; mesh create
// 201, delete 409 while it has members.
type clusterSettingsFake struct {
	*routeFake
	grants        []map[string]any
	sshKeys       []string
	zones         []map[string]any
	schedules     []map[string]any
	disableUpdate bool
	ciSettings    map[string]any
	shipping      bool
	meshes        map[string]map[string]any
	created       int
	hasAgent      bool
	// clusterHidden makes the cluster-scoped routes answer 404 "Cluster not
	// found", as ClusterIsInScope does for a cluster outside the project scope.
	clusterHidden bool
	// duplicateCreate makes a schedule create commit two identical rows.
	duplicateCreate bool
	// joinFailsAfterCommit records a mesh membership and then answers 400.
	joinFailsAfterCommit bool
}

func (fake *clusterSettingsFake) answerHidden(t *testing.T, writer http.ResponseWriter) bool {
	if !fake.clusterHidden {
		return false
	}
	writeJSON(t, writer, http.StatusNotFound, map[string]any{"detail": "Cluster not found"})
	return true
}

func (fake *clusterSettingsFake) nextID(prefix string) string {
	fake.created++
	return fmt.Sprintf("%s-%d", prefix, fake.created)
}

func newClusterSettingsFake(t *testing.T) *clusterSettingsFake {
	fake := &clusterSettingsFake{
		routeFake:  newRouteFake(t),
		hasAgent:   true,
		ciSettings: map[string]any{"ci_worker_count": 0, "ci_storage_class": "", "ci_run_reservation": "off", "ci_image_prepull": "off", "ci_build_apparmor_profile": ""},
		meshes:     map[string]map[string]any{},
	}
	clusterPrefix := "/api/v1/clusters/" + clusterSettingsTestCluster
	importedPrefix := "/api/v1/org/clusters/imported/" + clusterSettingsTestCluster
	organisationPrefix := "/api/v1/org/clusters/" + clusterSettingsTestCluster

	fake.handle("GET /api/v1/clusters", func(writer http.ResponseWriter, request *http.Request) {
		rows := []map[string]any{}
		if request.URL.Query().Get("cluster_id") == clusterSettingsTestCluster || request.URL.Query().Get("cluster_name") == "prod" {
			rows = append(rows, map[string]any{"id": clusterSettingsTestCluster, "name": "prod", "kind": "hetzner", "state": "up"})
		}
		writeJSON(t, writer, http.StatusOK, map[string]any{"result": rows, "pagination": map[string]any{"total_count": len(rows), "total_pages": 1, "page": 1, "page_size": 1}})
	})

	fake.handle("GET "+clusterPrefix+"/access/grants", func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(t, writer, http.StatusOK, map[string]any{"result": fake.grants})
	})
	fake.handle("POST "+clusterPrefix+"/access/grants", func(writer http.ResponseWriter, request *http.Request) {
		var body map[string]any
		decodeJSON(t, request, &body)
		if _, present := body["expires_in"]; present {
			t.Errorf("grant body carried expires_in; the provider writes expires_at only")
		}
		if expiresAt, present := body["expires_at"].(string); present {
			parsed, err := time.Parse(time.RFC3339, expiresAt)
			if err != nil {
				writeJSON(t, writer, http.StatusUnprocessableEntity, map[string]any{"detail": "bad expires_at"})
				return
			}
			body["expires_at"] = parsed.UTC().Format("2006-01-02T15:04:05") + "Z"
		}
		for _, grant := range fake.grants {
			if grant["user_email"] == body["user_email"] && grant["scope"] == body["scope"] && grant["role"] == body["role"] && grant["namespace"] == body["namespace"] {
				if body["expires_at"] == nil {
					writeJSON(t, writer, http.StatusConflict, map[string]any{"detail": "A matching access grant already exists"})
					return
				}
				grant["expires_at"] = body["expires_at"]
				if reason, present := body["reason"]; present && reason != nil {
					grant["reason"] = reason
				}
				writeJSON(t, writer, http.StatusOK, map[string]any{"grant": grant})
				return
			}
		}
		grant := map[string]any{
			"id": fake.nextID("grant"), "cluster_id": clusterSettingsTestCluster, "ankra_user_id": clusterSettingsOtherMember,
			"user_email": body["user_email"], "scope": body["scope"], "namespace": body["namespace"], "role": body["role"],
			"reconcile_status": "pending", "expires_at": body["expires_at"], "reason": body["reason"], "source": "manual",
		}
		fake.grants = append([]map[string]any{grant}, fake.grants...)
		writeJSON(t, writer, http.StatusOK, map[string]any{"grant": grant})
	})
	fake.handle("DELETE "+clusterPrefix+"/access/grants/{grant_id}", func(writer http.ResponseWriter, request *http.Request) {
		for index, grant := range fake.grants {
			if grant["id"] == request.PathValue("grant_id") {
				fake.grants = append(fake.grants[:index], fake.grants[index+1:]...)
				writeJSON(t, writer, http.StatusOK, map[string]any{"deleted": true})
				return
			}
		}
		writeJSON(t, writer, http.StatusNotFound, map[string]any{"detail": "Access grant not found"})
	})

	sshPath := "/api/v1/clusters/hetzner/" + clusterSettingsTestCluster + "/ssh-keys"
	fake.handle("GET "+sshPath, func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(t, writer, http.StatusOK, map[string]any{"ssh_key_credential_ids": fake.sshKeys, "available_ssh_keys": []any{}})
	})
	fake.handle("PUT "+sshPath, func(writer http.ResponseWriter, request *http.Request) {
		var body map[string][]string
		decodeJSON(t, request, &body)
		keys, present := body["ssh_key_credential_ids"]
		if !present || keys == nil {
			writeJSON(t, writer, http.StatusUnprocessableEntity, map[string]any{"detail": "ssh_key_credential_ids is required"})
			return
		}
		fake.sshKeys = keys
		writeJSON(t, writer, http.StatusOK, map[string]any{"ssh_key_credential_ids": keys})
	})

	fake.handle("GET "+clusterPrefix+"/custom-dns-zones", func(writer http.ResponseWriter, _ *http.Request) {
		listing := append([]map[string]any{{"zone": "inherited.example.com", "credential_name": "org-dns", "source": "organisation", "provider": "webhook", "policy": "sync"}}, fake.zones...)
		writeJSON(t, writer, http.StatusOK, map[string]any{"success": true, "zones": listing})
	})
	fake.handle("POST "+clusterPrefix+"/custom-dns-zones", func(writer http.ResponseWriter, request *http.Request) {
		var body map[string]any
		decodeJSON(t, request, &body)
		requestedZone, _ := body["zone"].(string)
		credentialName, _ := body["credential_name"].(string)
		zone := clusterSettingsNormaliseZone(requestedZone)
		family := "webhook"
		if strings.HasPrefix(credentialName, "cf-") {
			family = "cloudflare"
		}
		if provider, present := body["provider"]; present && provider != family {
			writeJSON(t, writer, http.StatusBadRequest, map[string]any{"detail": "credential kind mismatch"})
			return
		}
		policy := "sync"
		if family == "cloudflare" {
			policy = "upsert-only"
		}
		binding := map[string]any{"zone": zone, "credential_name": body["credential_name"], "source": "cluster", "provider": family, "policy": policy}
		for index, existing := range fake.zones {
			if existing["zone"] == zone {
				fake.zones[index] = binding
				writeJSON(t, writer, http.StatusOK, binding)
				return
			}
		}
		fake.zones = append(fake.zones, binding)
		writeJSON(t, writer, http.StatusOK, binding)
	})
	fake.handle("DELETE "+clusterPrefix+"/custom-dns-zones/{zone}", func(writer http.ResponseWriter, request *http.Request) {
		zone := clusterSettingsNormaliseZone(request.PathValue("zone"))
		for index, existing := range fake.zones {
			if existing["zone"] == zone {
				fake.zones = append(fake.zones[:index], fake.zones[index+1:]...)
				writeJSON(t, writer, http.StatusOK, map[string]any{"success": true, "zone": zone})
				return
			}
		}
		writeJSON(t, writer, http.StatusNotFound, map[string]any{"detail": "custom dns zone not found"})
	})

	fake.handle("GET "+importedPrefix+"/power-schedules", func(writer http.ResponseWriter, _ *http.Request) {
		if fake.answerHidden(t, writer) {
			return
		}
		writeJSON(t, writer, http.StatusOK, map[string]any{"schedules": fake.schedules})
	})
	fake.handle("POST "+importedPrefix+"/power-schedules", func(writer http.ResponseWriter, request *http.Request) {
		if fake.answerHidden(t, writer) {
			return
		}
		row, refusal := clusterSettingsDecodeSchedule(request, false)
		if refusal != "" {
			writeJSON(t, writer, http.StatusUnprocessableEntity, map[string]any{"detail": refusal})
			return
		}
		row["id"] = fake.nextID("schedule")
		fake.schedules = append([]map[string]any{row}, fake.schedules...)
		if fake.duplicateCreate {
			twin := map[string]any{}
			for key, value := range row {
				twin[key] = value
			}
			twin["id"] = fake.nextID("schedule")
			fake.schedules = append([]map[string]any{twin}, fake.schedules...)
		}
		writeJSON(t, writer, http.StatusOK, map[string]any{"schedules": fake.schedules})
	})
	fake.handle("PUT "+importedPrefix+"/power-schedules/{schedule_id}", func(writer http.ResponseWriter, request *http.Request) {
		if fake.answerHidden(t, writer) {
			return
		}
		row, refusal := clusterSettingsDecodeSchedule(request, true)
		if refusal != "" {
			t.Errorf("update refused: %s", refusal)
			writeJSON(t, writer, http.StatusUnprocessableEntity, map[string]any{"detail": refusal})
			return
		}
		for index, existing := range fake.schedules {
			if existing["id"] == request.PathValue("schedule_id") {
				row["id"] = existing["id"]
				fake.schedules[index] = row
				writeJSON(t, writer, http.StatusOK, map[string]any{"schedules": fake.schedules})
				return
			}
		}
		writeJSON(t, writer, http.StatusNotFound, map[string]any{"detail": "Power schedule not found"})
	})
	fake.handle("DELETE "+importedPrefix+"/power-schedules/{schedule_id}", func(writer http.ResponseWriter, request *http.Request) {
		if fake.answerHidden(t, writer) {
			return
		}
		for index, existing := range fake.schedules {
			if existing["id"] == request.PathValue("schedule_id") {
				fake.schedules = append(fake.schedules[:index], fake.schedules[index+1:]...)
				writeJSON(t, writer, http.StatusOK, map[string]any{"deleted": true})
				return
			}
		}
		writeJSON(t, writer, http.StatusNotFound, map[string]any{"detail": "Power schedule not found"})
	})

	fake.handle("GET "+importedPrefix+"/agent", func(writer http.ResponseWriter, _ *http.Request) {
		if fake.answerHidden(t, writer) {
			return
		}
		if !fake.hasAgent {
			writeJSON(t, writer, http.StatusNotFound, map[string]any{"detail": "This cluster doesn't have an agent registered."})
			return
		}
		writeJSON(t, writer, http.StatusOK, map[string]any{"agent_version": "1.2.3", "disable_auto_upgrade": fake.disableUpdate, "is_online": true})
	})
	fake.handle("PATCH "+importedPrefix+"/agent/settings", func(writer http.ResponseWriter, request *http.Request) {
		if fake.answerHidden(t, writer) {
			return
		}
		var body map[string]*bool
		decodeJSON(t, request, &body)
		if body["disable_auto_upgrade"] == nil {
			t.Errorf("agent settings body omitted disable_auto_upgrade, which changes nothing")
		} else {
			fake.disableUpdate = *body["disable_auto_upgrade"]
		}
		writeJSON(t, writer, http.StatusOK, map[string]any{"success": true, "message": "Cluster agent settings updated successfully"})
	})

	fake.handle("GET "+organisationPrefix+"/agent/ci-settings", func(writer http.ResponseWriter, _ *http.Request) {
		if fake.answerHidden(t, writer) {
			return
		}
		writeJSON(t, writer, http.StatusOK, fake.ciSettingsAnswer())
	})
	fake.handle("PUT "+organisationPrefix+"/agent/ci-settings", func(writer http.ResponseWriter, request *http.Request) {
		if fake.answerHidden(t, writer) {
			return
		}
		var body map[string]json.RawMessage
		decodeJSON(t, request, &body)
		for key, raw := range body {
			if string(raw) == "null" {
				writeJSON(t, writer, http.StatusUnprocessableEntity, map[string]any{"detail": key + " may not be null"})
				return
			}
			var value any
			_ = json.Unmarshal(raw, &value)
			fake.ciSettings[key] = value
		}
		writeJSON(t, writer, http.StatusOK, fake.ciSettingsAnswer())
	})

	fake.handle("GET "+organisationPrefix+"/hosted-logs", func(writer http.ResponseWriter, _ *http.Request) {
		if fake.answerHidden(t, writer) {
			return
		}
		writeJSON(t, writer, http.StatusOK, fake.shippingAnswer())
	})
	fake.handle("PUT "+organisationPrefix+"/hosted-logs", func(writer http.ResponseWriter, request *http.Request) {
		if fake.answerHidden(t, writer) {
			return
		}
		var body map[string]any
		decodeJSON(t, request, &body)
		enabled, isBool := body["shipping_enabled"].(bool)
		if !isBool {
			writeJSON(t, writer, http.StatusUnprocessableEntity, map[string]any{"detail": "shipping_enabled must be a boolean"})
			return
		}
		fake.shipping = enabled
		writeJSON(t, writer, http.StatusOK, fake.shippingAnswer())
	})

	fake.handle("GET /api/v1/org/cluster-meshes", func(writer http.ResponseWriter, _ *http.Request) {
		listing := []map[string]any{}
		for _, mesh := range fake.meshes {
			listing = append(listing, mesh)
		}
		writeJSON(t, writer, http.StatusOK, map[string]any{"success": true, "cluster_meshes": listing})
	})
	fake.handle("POST /api/v1/org/cluster-meshes", func(writer http.ResponseWriter, request *http.Request) {
		var body map[string]string
		decodeJSON(t, request, &body)
		identifier := fake.nextID("mesh")
		fake.meshes[identifier] = map[string]any{"id": identifier, "slug": strings.ToLower(body["name"]), "name": body["name"], "status": "active", "members": []map[string]any{}}
		writeJSON(t, writer, http.StatusCreated, map[string]any{"success": true, "cluster_mesh": map[string]any{
			"id": identifier, "slug": strings.ToLower(body["name"]), "name": body["name"], "status": "active",
		}})
	})
	fake.handle("GET /api/v1/org/cluster-meshes/{mesh_id}", func(writer http.ResponseWriter, request *http.Request) {
		mesh, exists := fake.meshes[request.PathValue("mesh_id")]
		if !exists {
			writeJSON(t, writer, http.StatusNotFound, map[string]any{"detail": "Cluster mesh not found"})
			return
		}
		writeJSON(t, writer, http.StatusOK, map[string]any{"success": true, "cluster_mesh": mesh})
	})
	fake.handle("DELETE /api/v1/org/cluster-meshes/{mesh_id}", func(writer http.ResponseWriter, request *http.Request) {
		mesh, exists := fake.meshes[request.PathValue("mesh_id")]
		if !exists {
			writeJSON(t, writer, http.StatusNotFound, map[string]any{"detail": "Cluster mesh not found"})
			return
		}
		if len(clusterSettingsMeshMembers(mesh)) > 0 {
			writeJSON(t, writer, http.StatusConflict, map[string]any{"detail": "cluster mesh still has members"})
			return
		}
		delete(fake.meshes, request.PathValue("mesh_id"))
		writeJSON(t, writer, http.StatusOK, map[string]any{"success": true})
	})
	fake.handle("POST /api/v1/org/cluster-meshes/{mesh_id}/members", func(writer http.ResponseWriter, request *http.Request) {
		mesh, exists := fake.meshes[request.PathValue("mesh_id")]
		if !exists {
			writeJSON(t, writer, http.StatusBadRequest, map[string]any{"detail": "cluster mesh not found"})
			return
		}
		var body map[string]string
		decodeJSON(t, request, &body)
		members := clusterSettingsMeshMembers(mesh)
		for _, member := range members {
			if member["cluster_id"] == body["cluster_id"] {
				writeJSON(t, writer, http.StatusConflict, map[string]any{"detail": "cluster is already in a mesh"})
				return
			}
		}
		mesh["members"] = append(members, map[string]any{"cluster_id": body["cluster_id"], "cilium_cluster_id": len(members) + 1, "cilium_cluster_name": "prod", "status": "joining"})
		if fake.joinFailsAfterCommit {
			writeJSON(t, writer, http.StatusBadRequest, map[string]any{"detail": "writing the mesh peer secret failed"})
			return
		}
		writeJSON(t, writer, http.StatusOK, map[string]any{"success": true})
	})
	fake.handle("DELETE /api/v1/org/cluster-meshes/{mesh_id}/members/{cluster_id}", func(writer http.ResponseWriter, request *http.Request) {
		if mesh, exists := fake.meshes[request.PathValue("mesh_id")]; exists {
			kept := []map[string]any{}
			for _, member := range clusterSettingsMeshMembers(mesh) {
				if member["cluster_id"] != request.PathValue("cluster_id") {
					kept = append(kept, member)
				}
			}
			mesh["members"] = kept
		}
		writeJSON(t, writer, http.StatusOK, map[string]any{"success": true})
	})
	return fake
}

func (fake *clusterSettingsFake) ciSettingsAnswer() map[string]any {
	answer := map[string]any{"agent_version": "1.2.3", "supports_pipeline_steps": true, "apply_state": "applied", "updated_at": nil, "ci_placement": nil}
	for key, value := range fake.ciSettings {
		answer[key] = value
	}
	return answer
}

func (fake *clusterSettingsFake) shippingAnswer() map[string]any {
	return map[string]any{"cluster_id": clusterSettingsTestCluster, "shipping_enabled": fake.shipping, "available": true, "agent_supports_switch": true, "changed_at": nil}
}

// clusterSettingsDecodeSchedule applies the platform's power schedule body
// rules (importedapi DecodePowerScheduleRequest) that the provider must meet.
func clusterSettingsDecodeSchedule(request *http.Request, isUpdate bool) (map[string]any, string) {
	var body map[string]any
	if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
		return nil, "body is not JSON"
	}
	if isUpdate && body["enabled"] == nil {
		return nil, "Enabled must be provided when updating a schedule"
	}
	if isUpdate && body["action"] == "stop" && body["stop_mode"] == nil {
		return nil, "Stop mode must be provided when updating a stop schedule"
	}
	if isUpdate && body["schedule_kind"] == "cron" && body["timezone"] == nil {
		return nil, "Timezone must be provided when updating a repeated schedule"
	}
	if body["action"] != "stop" && body["stop_mode"] != nil && body["stop_mode"] != "delete_resources" {
		return nil, "Stop mode only applies to stop schedules"
	}
	timezone, _ := body["timezone"].(string)
	if timezone == "" {
		timezone = "UTC"
	}
	row := map[string]any{
		"action": body["action"], "stop_mode": body["stop_mode"], "preserve_state": body["preserve_state"], "schedule_kind": body["schedule_kind"],
		"run_at": nil, "cron_expression": nil, "timezone": timezone, "enabled": body["enabled"], "next_run_at": nil,
		"last_run_at": nil, "last_run_status": nil, "created_at": "2026-10-17T00:00:00Z", "updated_at": "2026-10-17T00:00:00Z",
	}
	if body["schedule_kind"] == "once" {
		runAt, err := time.Parse(time.RFC3339, fmt.Sprint(body["run_at"]))
		if err != nil {
			return nil, "Run at must be an RFC 3339 timestamp"
		}
		row["run_at"] = runAt.UTC().Format(time.RFC3339)
	} else {
		row["cron_expression"] = body["cron_expression"]
	}
	if body["enabled"] == true {
		row["next_run_at"] = "2030-01-01T18:00:00Z"
	}
	return row, ""
}

func TestAccClusterAccessGrantLifecycle(t *testing.T) {
	fake := newClusterSettingsFake(t)
	config := func(expiresAt, reason string) string {
		return fake.providerBlock() + fmt.Sprintf(`
resource "ankra_cluster_access_grant" "ops" {
  cluster_id = %q
  user_email = "ops@example.com"
  role       = "edit"
  expires_at = %q
  reason     = %q
}

data "ankra_cluster_access_grants" "all" {
  cluster_id = %q
  depends_on = [ankra_cluster_access_grant.ops]
}
`, clusterSettingsTestCluster, expiresAt, reason, clusterSettingsTestCluster)
	}
	var firstID string
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: config("2030-01-01T10:00:00+02:00", "migration night"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ankra_cluster_access_grant.ops", "scope", "cluster"),
					resource.TestCheckResourceAttr("ankra_cluster_access_grant.ops", "expires_at", "2030-01-01T10:00:00+02:00"),
					resource.TestCheckResourceAttr("ankra_cluster_access_grant.ops", "source", "manual"),
					resource.TestCheckResourceAttr("data.ankra_cluster_access_grants.all", "grants.#", "1"),
					resource.TestCheckResourceAttr("data.ankra_cluster_access_grants.all", "grants.0.expires_at", "2030-01-01T08:00:00Z"),
					func(state *terraform.State) error {
						firstID = state.RootModule().Resources["ankra_cluster_access_grant.ops"].Primary.ID
						return nil
					},
				),
			},
			{
				// A new expiry and reason re-time the grant in place.
				Config: config("2030-02-01T00:00:00Z", "extended"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ankra_cluster_access_grant.ops", "reason", "extended"),
					func(state *terraform.State) error {
						if identifier := state.RootModule().Resources["ankra_cluster_access_grant.ops"].Primary.ID; identifier != firstID {
							return fmt.Errorf("grant was replaced (%s -> %s) instead of re-timed", firstID, identifier)
						}
						if len(fake.grants) != 1 || fake.grants[0]["reason"] != "extended" {
							return fmt.Errorf("platform holds %v", fake.grants)
						}
						return nil
					},
				),
			},
			{
				ResourceName:            "ankra_cluster_access_grant.ops",
				ImportState:             true,
				ImportStateIdFunc:       func(*terraform.State) (string, error) { return clusterSettingsTestCluster + "/" + firstID, nil },
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"expires_at", "reconcile_status"},
			},
			{
				PreConfig: func() { fake.grants = nil },
				Config:    config("2030-02-01T00:00:00Z", "extended"),
				Check: func(*terraform.State) error {
					if len(fake.grants) != 1 {
						return fmt.Errorf("grant revoked outside Terraform was not recreated")
					}
					return nil
				},
			},
		},
	})
}

func TestAccClusterAccessGrantRejectsUnknownRole(t *testing.T) {
	fake := newClusterSettingsFake(t)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config: fake.providerBlock() + fmt.Sprintf(`
resource "ankra_cluster_access_grant" "ops" {
  cluster_id = %q
  user_email = "ops@example.com"
  role       = "owner"
}
`, clusterSettingsTestCluster),
			ExpectError: regexp.MustCompile(`not accepted`),
		}},
	})
	if served := fake.served(); len(served) != 0 {
		t.Errorf("an invalid role reached the API: %v", served)
	}
}

func TestAccClusterSSHKeysAndCustomDNSZone(t *testing.T) {
	fake := newClusterSettingsFake(t)
	config := func(keys, credential string) string {
		return fake.providerBlock() + fmt.Sprintf(`
data "ankra_cluster" "prod" {
  name = "prod"
}

resource "ankra_cluster_ssh_keys" "prod" {
  cluster_id             = data.ankra_cluster.prod.id
  ssh_key_credential_ids = [%s]
}

resource "ankra_cluster_custom_dns_zone" "launch" {
  cluster_id      = data.ankra_cluster.prod.id
  zone            = "Launch.Example.com."
  credential_name = %q
}

data "ankra_cluster_custom_dns_zones" "prod" {
  cluster_id = data.ankra_cluster.prod.id
  depends_on = [ankra_cluster_custom_dns_zone.launch]
}
`, keys, credential)
	}
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: config(`"key-a", "key-b"`, "example-dns"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.ankra_cluster.prod", "kind", "hetzner"),
					resource.TestCheckResourceAttr("ankra_cluster_ssh_keys.prod", "ssh_key_credential_ids.#", "2"),
					resource.TestCheckResourceAttr("ankra_cluster_custom_dns_zone.launch", "zone", "Launch.Example.com."),
					resource.TestCheckResourceAttr("ankra_cluster_custom_dns_zone.launch", "credential_provider", "webhook"),
					resource.TestCheckResourceAttr("ankra_cluster_custom_dns_zone.launch", "policy", "sync"),
					resource.TestCheckResourceAttr("data.ankra_cluster_custom_dns_zones.prod", "zones.#", "2"),
					resource.TestCheckResourceAttr("data.ankra_cluster_custom_dns_zones.prod", "zones.0.source", "organisation"),
				),
			},
			{
				// A new credential re-points the zone in place; a key leaves the set.
				Config: config(`"key-b"`, "cf-account"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ankra_cluster_custom_dns_zone.launch", "credential_provider", "cloudflare"),
					resource.TestCheckResourceAttr("ankra_cluster_custom_dns_zone.launch", "policy", "upsert-only"),
					func(*terraform.State) error {
						if len(fake.zones) != 1 || fake.zones[0]["credential_name"] != "cf-account" {
							return fmt.Errorf("platform holds %v", fake.zones)
						}
						if strings.Join(fake.sshKeys, ",") != "key-b" {
							return fmt.Errorf("platform holds keys %v", fake.sshKeys)
						}
						return nil
					},
				),
			},
			{
				ResourceName:      "ankra_cluster_ssh_keys.prod",
				ImportState:       true,
				ImportStateId:     clusterSettingsTestCluster,
				ImportStateVerify: true,
			},
			{
				ResourceName:            "ankra_cluster_custom_dns_zone.launch",
				ImportState:             true,
				ImportStateId:           clusterSettingsTestCluster + "/launch.example.com",
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"zone"},
			},
			{
				// Withdrawn and keys changed outside Terraform: both come back.
				PreConfig: func() { fake.zones = nil; fake.sshKeys = []string{"key-z"} },
				Config:    config(`"key-b"`, "cf-account"),
				Check: func(*terraform.State) error {
					if len(fake.zones) != 1 || strings.Join(fake.sshKeys, ",") != "key-b" {
						return fmt.Errorf("drift not corrected: zones %v keys %v", fake.zones, fake.sshKeys)
					}
					return nil
				},
			},
		},
	})
	if len(fake.zones) != 0 {
		t.Errorf("destroy left zones %v", fake.zones)
	}
	if strings.Join(fake.sshKeys, ",") != "key-b" {
		t.Errorf("destroy changed the cluster's keys to %v; it should leave them", fake.sshKeys)
	}
}

func TestAccClusterPowerScheduleLifecycle(t *testing.T) {
	fake := newClusterSettingsFake(t)
	config := func(cron string, enabled bool) string {
		return fake.providerBlock() + fmt.Sprintf(`
resource "ankra_cluster_power_schedule" "nightly" {
  cluster_id      = %q
  action          = "stop"
  stop_mode       = "scale_to_zero"
  schedule_kind   = "cron"
  cron_expression = %q
  timezone        = "Europe/Stockholm"
  enabled         = %t
}

resource "ankra_cluster_power_schedule" "launch" {
  cluster_id    = %q
  action        = "start"
  schedule_kind = "once"
  run_at        = "2030-01-01T09:00:00+01:00"
}

data "ankra_cluster_power_schedules" "all" {
  cluster_id = %q
  depends_on = [ankra_cluster_power_schedule.nightly, ankra_cluster_power_schedule.launch]
}
`, clusterSettingsTestCluster, cron, enabled, clusterSettingsTestCluster, clusterSettingsTestCluster)
	}
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: config("0 19 * * 1-5", true),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("ankra_cluster_power_schedule.nightly", "id"),
					resource.TestCheckResourceAttr("ankra_cluster_power_schedule.launch", "stop_mode", "delete_resources"),
					resource.TestCheckResourceAttr("ankra_cluster_power_schedule.launch", "timezone", "UTC"),
					resource.TestCheckResourceAttr("ankra_cluster_power_schedule.launch", "run_at", "2030-01-01T09:00:00+01:00"),
					resource.TestCheckResourceAttr("data.ankra_cluster_power_schedules.all", "schedules.#", "2"),
				),
			},
			{
				Config: config("0 20 * * 1-5", false),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ankra_cluster_power_schedule.nightly", "enabled", "false"),
					resource.TestCheckNoResourceAttr("ankra_cluster_power_schedule.nightly", "next_run_at"),
				),
			},
			{
				ResourceName: "ankra_cluster_power_schedule.nightly",
				ImportState:  true,
				ImportStateIdFunc: func(state *terraform.State) (string, error) {
					return clusterSettingsTestCluster + "/" + state.RootModule().Resources["ankra_cluster_power_schedule.nightly"].Primary.ID, nil
				},
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"accept_node_local_data_loss"},
			},
			{
				PreConfig: func() { fake.schedules = fake.schedules[1:] },
				Config:    config("0 20 * * 1-5", false),
				Check: func(*terraform.State) error {
					if len(fake.schedules) != 2 {
						return fmt.Errorf("schedule deleted outside Terraform was not recreated: %v", fake.schedules)
					}
					return nil
				},
			},
		},
	})
	if len(fake.schedules) != 0 {
		t.Errorf("destroy left schedules %v", fake.schedules)
	}
}

func TestAccClusterAgentAndLogShippingSettings(t *testing.T) {
	fake := newClusterSettingsFake(t)
	fake.ciSettings["ci_storage_class"] = "portal-ssd"
	fake.ciSettings["ci_image_prepull"] = "on"
	config := func(autoUpgrade bool, workers int, shipping bool) string {
		return fake.providerBlock() + fmt.Sprintf(`
resource "ankra_cluster_agent_auto_upgrade" "prod" {
  cluster_id = %q
  enabled    = %t
}

resource "ankra_cluster_agent_ci_settings" "prod" {
  cluster_id         = %q
  ci_worker_count    = %d
  ci_run_reservation = "shadow"
}

resource "ankra_cluster_log_shipping" "prod" {
  cluster_id = %q
  enabled    = %t
}
`, clusterSettingsTestCluster, autoUpgrade, clusterSettingsTestCluster, workers, clusterSettingsTestCluster, shipping)
	}
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: config(false, 4, true),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ankra_cluster_agent_ci_settings.prod", "ci_storage_class", "portal-ssd"),
					resource.TestCheckResourceAttr("ankra_cluster_agent_ci_settings.prod", "ci_image_prepull", "on"),
					resource.TestCheckResourceAttr("ankra_cluster_agent_ci_settings.prod", "apply_state", "applied"),
					resource.TestCheckResourceAttr("ankra_cluster_log_shipping.prod", "available", "true"),
					func(*terraform.State) error {
						if !fake.disableUpdate || !fake.shipping || fmt.Sprint(fake.ciSettings["ci_worker_count"]) != "4" {
							return fmt.Errorf("platform holds disable=%v shipping=%v ci=%v", fake.disableUpdate, fake.shipping, fake.ciSettings)
						}
						return nil
					},
				),
			},
			{
				Config: config(true, 8, false),
				Check: func(*terraform.State) error {
					if fake.disableUpdate || fake.shipping || fmt.Sprint(fake.ciSettings["ci_worker_count"]) != "8" {
						return fmt.Errorf("platform holds disable=%v shipping=%v ci=%v", fake.disableUpdate, fake.shipping, fake.ciSettings)
					}
					return nil
				},
			},
			{ResourceName: "ankra_cluster_agent_auto_upgrade.prod", ImportState: true, ImportStateId: clusterSettingsTestCluster, ImportStateVerify: true},
			{ResourceName: "ankra_cluster_agent_ci_settings.prod", ImportState: true, ImportStateId: clusterSettingsTestCluster, ImportStateVerify: true},
			{ResourceName: "ankra_cluster_log_shipping.prod", ImportState: true, ImportStateId: clusterSettingsTestCluster, ImportStateVerify: true},
			{
				// Changed outside Terraform: the next apply restores the configuration.
				PreConfig: func() { fake.disableUpdate = true; fake.shipping = true; fake.ciSettings["ci_worker_count"] = 2 },
				Config:    config(true, 8, false),
				Check: func(*terraform.State) error {
					if fake.disableUpdate || fake.shipping || fmt.Sprint(fake.ciSettings["ci_worker_count"]) != "8" {
						return fmt.Errorf("drift not corrected: disable=%v shipping=%v ci=%v", fake.disableUpdate, fake.shipping, fake.ciSettings)
					}
					return nil
				},
			},
		},
	})
	if fake.disableUpdate || fake.shipping || fmt.Sprint(fake.ciSettings["ci_worker_count"]) != "0" || fake.ciSettings["ci_run_reservation"] != "off" ||
		fake.ciSettings["ci_storage_class"] != "portal-ssd" || fake.ciSettings["ci_image_prepull"] != "on" {
		t.Errorf("destroy did not restore exactly the configured defaults: disable=%v shipping=%v ci=%v", fake.disableUpdate, fake.shipping, fake.ciSettings)
	}
}

func TestAccClusterMeshAndMember(t *testing.T) {
	fake := newClusterSettingsFake(t)
	config := fake.providerBlock() + fmt.Sprintf(`
resource "ankra_cluster_mesh" "edge" {
  name = "Edge"
}

resource "ankra_cluster_mesh_member" "prod" {
  cluster_id = %q
  mesh_id    = ankra_cluster_mesh.edge.id
}

data "ankra_cluster_meshes" "all" {
  depends_on = [ankra_cluster_mesh_member.prod]
}
`, clusterSettingsTestCluster)
	var meshID string
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: config,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ankra_cluster_mesh.edge", "slug", "edge"),
					resource.TestCheckResourceAttr("ankra_cluster_mesh_member.prod", "cilium_cluster_id", "1"),
					resource.TestCheckResourceAttr("data.ankra_cluster_meshes.all", "meshes.0.members.#", "1"),
					func(state *terraform.State) error {
						meshID = state.RootModule().Resources["ankra_cluster_mesh.edge"].Primary.ID
						return nil
					},
				),
			},
			{ResourceName: "ankra_cluster_mesh.edge", ImportState: true, ImportStateVerify: true},
			{
				ResourceName:      "ankra_cluster_mesh_member.prod",
				ImportState:       true,
				ImportStateIdFunc: func(*terraform.State) (string, error) { return clusterSettingsTestCluster + "/" + meshID, nil },
				ImportStateVerify: true,
			},
			{
				PreConfig: func() { fake.meshes[meshID]["members"] = []map[string]any{} },
				Config:    config,
				Check: func(*terraform.State) error {
					if len(clusterSettingsMeshMembers(fake.meshes[meshID])) != 1 {
						return fmt.Errorf("cluster that left outside Terraform did not rejoin")
					}
					return nil
				},
			},
		},
	})
	if len(fake.meshes) != 0 {
		t.Errorf("destroy left meshes %v", fake.meshes)
	}
}

func TestAccClusterSettingsImportIDIsStrict(t *testing.T) {
	fake := newClusterSettingsFake(t)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config: fake.providerBlock() + fmt.Sprintf(`
resource "ankra_cluster_custom_dns_zone" "launch" {
  cluster_id      = %q
  zone            = "launch.example.com"
  credential_name = "example-dns"
}
`, clusterSettingsTestCluster),
			ResourceName:  "ankra_cluster_custom_dns_zone.launch",
			ImportState:   true,
			ImportStateId: "launch.example.com",
			ExpectError:   regexp.MustCompile(`<cluster_id>/<zone>`),
		}},
	})
}

func clusterSettingsMeshMembers(mesh map[string]any) []map[string]any {
	members, _ := mesh["members"].([]map[string]any)
	return members
}
