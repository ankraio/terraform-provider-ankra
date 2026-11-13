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

const (
	aiTicketFakeProjectID    = "5d1c2b3a-4e5f-4a6b-8c7d-9e0f1a2b3c4d"
	aiTicketFakeConnectionID = "6e2d3c4b-5f6a-4b7c-9d8e-0f1a2b3c4d5e"
)

// aiTicketFake mirrors the ticket desk routes (go/internal/aiticketsapi):
// the settings PUT merges the keys it is sent, project channels are listed
// per project, and a sync connection never returns its credential.
type aiTicketFake struct {
	*routeFake
	settings        map[string]any
	settingsPuts    []map[string]any
	projectChannels map[string]any
	connection      map[string]any
	credentials     []map[string]any
	notAdmin        bool
}

func newAITicketFake(t *testing.T) *aiTicketFake {
	fake := &aiTicketFake{routeFake: newRouteFake(t), settings: map[string]any{
		"slack_channel_id": nil, "teams_conversation_id": nil, "reviewer_agent_task_id": nil, "require_agent_review": false,
		"auto_file_insight_severities": []string{"critical"}, "notify_mode": "all", "notify_min_priority": "low",
		"notify_source_kinds": []string{}, "notify_event_scope": "all", "notify_cluster_environments": []string{}, "updated_at": nil,
	}}
	refuse := func(writer http.ResponseWriter) bool {
		if fake.notAdmin {
			writeJSON(t, writer, http.StatusForbidden, map[string]any{"detail": "Only organisation admins can change ticket settings."})
		}
		return fake.notAdmin
	}
	fake.handle("GET /api/v1/org/ai-tickets/settings", func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(t, writer, http.StatusOK, fake.settings)
	})
	fake.handle("PUT /api/v1/org/ai-tickets/settings", func(writer http.ResponseWriter, request *http.Request) {
		if refuse(writer) {
			return
		}
		var body map[string]any
		decodeJSON(t, request, &body)
		fake.settingsPuts = append(fake.settingsPuts, body)
		for key, value := range body {
			fake.settings[key] = value
		}
		writeJSON(t, writer, http.StatusOK, fake.settings)
	})
	fake.handle("GET /api/v1/org/ai-tickets/settings/projects", func(writer http.ResponseWriter, _ *http.Request) {
		projects := []any{}
		if fake.projectChannels != nil {
			projects = append(projects, fake.projectChannels)
		}
		writeJSON(t, writer, http.StatusOK, map[string]any{"projects": projects})
	})
	fake.handle("PUT /api/v1/org/ai-tickets/settings/projects/{project_id}", func(writer http.ResponseWriter, request *http.Request) {
		if refuse(writer) {
			return
		}
		var body map[string]any
		decodeJSON(t, request, &body)
		fake.projectChannels = map[string]any{
			"project_id": request.PathValue("project_id"), "project_name": "web", "slack_channel_id": body["slack_channel_id"],
			"teams_conversation_id": body["teams_conversation_id"], "updated_at": "2026-11-13T10:00:00Z",
		}
		writeJSON(t, writer, http.StatusOK, map[string]any{"project_id": request.PathValue("project_id"), "channels": fake.projectChannels})
	})
	fake.handle("DELETE /api/v1/org/ai-tickets/settings/projects/{project_id}", func(writer http.ResponseWriter, _ *http.Request) {
		fake.projectChannels = nil
		writer.WriteHeader(http.StatusNoContent)
	})
	fake.handle("POST /api/v1/org/ai-tickets/sync/connections", func(writer http.ResponseWriter, request *http.Request) {
		if refuse(writer) {
			return
		}
		var body map[string]any
		decodeJSON(t, request, &body)
		fake.recordCredential(body)
		fake.connection = map[string]any{
			"id": aiTicketFakeConnectionID, "name": body["name"], "provider": body["provider"], "config": body["config"],
			"enabled": true, "credential_configured": body["credential"] != nil, "sync_kinds": []string{"incident"},
			"sync_min_priority": "medium", "sync_event_scope": "all", "last_synced_at": nil, "last_error": nil,
		}
		writeJSON(t, writer, http.StatusCreated, fake.connection)
	})
	fake.handle("GET /api/v1/org/ai-tickets/sync/connections/{id}", func(writer http.ResponseWriter, _ *http.Request) {
		if fake.connection == nil {
			writeJSON(t, writer, http.StatusNotFound, map[string]any{"detail": "Sync connection not found"})
			return
		}
		writeJSON(t, writer, http.StatusOK, fake.connection)
	})
	fake.handle("PUT /api/v1/org/ai-tickets/sync/connections/{id}", func(writer http.ResponseWriter, request *http.Request) {
		var body map[string]any
		decodeJSON(t, request, &body)
		if _, hasProvider := body["provider"]; hasProvider {
			t.Errorf("an update must not send the provider")
		}
		fake.recordCredential(body)
		for _, key := range []string{"name", "config", "enabled", "sync_kinds", "sync_min_priority", "sync_event_scope"} {
			if value, set := body[key]; set {
				fake.connection[key] = value
			}
		}
		writeJSON(t, writer, http.StatusOK, fake.connection)
	})
	fake.handle("DELETE /api/v1/org/ai-tickets/sync/connections/{id}", func(writer http.ResponseWriter, _ *http.Request) {
		fake.connection = nil
		writer.WriteHeader(http.StatusNoContent)
	})
	return fake
}

func (fake *aiTicketFake) recordCredential(body map[string]any) {
	if credential, set := body["credential"].(map[string]any); set {
		fake.credentials = append(fake.credentials, credential)
	}
}

func TestAccAITicketSettingsWritesOnlyConfiguredKeys(t *testing.T) {
	fake := newAITicketFake(t)
	config := func(channel string) string {
		return fake.providerBlock() + fmt.Sprintf(`
resource "ankra_ai_ticket_settings" "desk" {
  slack_channel_id    = %q
  notify_source_kinds = ["insight", "alert"]
}
`, channel)
	}
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy: func(_ *terraform.State) error {
			if fake.settings["slack_channel_id"] != "C0456" {
				return fmt.Errorf("destroy must leave the settings as they are")
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: config("C0123"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ankra_ai_ticket_settings.desk", "id", "ai_ticket_settings"),
					resource.TestCheckResourceAttr("ankra_ai_ticket_settings.desk", "notify_mode", "all"),
					resource.TestCheckResourceAttr("ankra_ai_ticket_settings.desk", "auto_file_insight_severities.#", "1"),
					func(_ *terraform.State) error {
						first := fake.settingsPuts[0]
						if _, sent := first["notify_mode"]; sent || len(first) != 2 {
							return fmt.Errorf("the create must write only the configured keys, wrote %v", first)
						}
						return nil
					},
				),
			},
			{
				Config: config("C0456"),
				Check:  resource.TestCheckResourceAttr("ankra_ai_ticket_settings.desk", "slack_channel_id", "C0456"),
			},
			{
				ResourceName:      "ankra_ai_ticket_settings.desk",
				ImportState:       true,
				ImportStateId:     "anything",
				ImportStateVerify: true,
			},
		},
	})
}

func TestAccAITicketProjectChannelsLifecycle(t *testing.T) {
	fake := newAITicketFake(t)
	config := fake.providerBlock() + fmt.Sprintf(`
resource "ankra_ai_ticket_project_channels" "web" {
  project_id       = %q
  slack_channel_id = "C0999"
}
`, aiTicketFakeProjectID)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy: func(_ *terraform.State) error {
			if fake.projectChannels != nil {
				return fmt.Errorf("destroy must send the project back to the organisation's channels")
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: fake.providerBlock() + fmt.Sprintf(`
resource "ankra_ai_ticket_project_channels" "web" {
  project_id = %q
}
`, aiTicketFakeProjectID),
				ExpectError: regexp.MustCompile("Set at least one of"),
			},
			{
				Config: config,
				Check:  resource.TestCheckResourceAttr("ankra_ai_ticket_project_channels.web", "id", aiTicketFakeProjectID),
			},
			{
				ResourceName:      "ankra_ai_ticket_project_channels.web",
				ImportState:       true,
				ImportStateId:     aiTicketFakeProjectID,
				ImportStateVerify: true,
			},
			{
				PreConfig: func() { fake.projectChannels = nil },
				Config:    config,
				Check: func(_ *terraform.State) error {
					if fake.projectChannels == nil {
						return fmt.Errorf("channels removed outside Terraform must be set again")
					}
					return nil
				},
			},
		},
	})
}

func TestAccAITicketSyncConnectionCredentialIsWriteOnly(t *testing.T) {
	fake := newAITicketFake(t)
	config := func(name string, version int) string {
		return fake.providerBlock() + fmt.Sprintf(`
resource "ankra_ai_ticket_sync_connection" "linear" {
  name                  = %q
  provider_name         = "linear"
  config                = { team_key = "OPS" }
  credential_wo         = { api_key = "lin_secret" }
  credential_wo_version = %d
}
`, name, version)
	}
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy: func(_ *terraform.State) error {
			if fake.connection != nil {
				return fmt.Errorf("destroy must delete the connection")
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: config("ops", 1),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ankra_ai_ticket_sync_connection.linear", "credential_configured", "true"),
					resource.TestCheckNoResourceAttr("ankra_ai_ticket_sync_connection.linear", "credential_wo"),
					resource.TestCheckResourceAttr("ankra_ai_ticket_sync_connection.linear", "sync_min_priority", "medium"),
				),
			},
			{
				Config: config("ops-board", 1),
				Check: func(_ *terraform.State) error {
					if len(fake.credentials) != 1 {
						return fmt.Errorf("a rename must not send the credential again, sent %d times", len(fake.credentials))
					}
					return nil
				},
			},
			{
				Config: config("ops-board", 2),
				Check: func(_ *terraform.State) error {
					if len(fake.credentials) != 2 || fake.credentials[1]["api_key"] != "lin_secret" {
						return fmt.Errorf("a new credential_wo_version must send the credential, got %v", fake.credentials)
					}
					return nil
				},
			},
			{
				ResourceName:            "ankra_ai_ticket_sync_connection.linear",
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"credential_wo_version"},
			},
			{
				PreConfig:   func() { fake.notAdmin = true; fake.connection = nil },
				Config:      config("ops-board", 2),
				ExpectError: regexp.MustCompile(`(?s)Unable to create the ticket sync connection.*Only organisation admins`),
			},
			{
				Config: fake.providerBlock() + `
resource "ankra_ai_ticket_sync_connection" "linear" {
  name          = "ops"
  provider_name = "linear"
  credential_wo = { api_key = "lin_secret" }
}
`,
				ExpectError: regexp.MustCompile("Missing credential_wo_version"),
			},
		},
	})
}
