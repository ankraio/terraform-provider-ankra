// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

// mcpFake holds MCP servers the way the token surface does
// (go/internal/cliapi/mcpservers.go, usecase/chatcrud/mcp.go): register
// answers 200 and refuses plaintext under sensitive env keys (422), reads
// never echo env values (only env_keys), an update keeps absent keys and a
// null env, clears allowed_tools, adapter_key and cluster_allow_list on an
// explicit null, delete answers 200 {"server_id","status"} or 404, and grants
// are listed per server (404 once the server is gone), created with 200 or
// 409, and revoked by tool and role with 200 or 404.
type mcpFake struct {
	*routeFake
	servers map[string]map[string]any
	env     map[string]map[string]any
	grants  map[string][]map[string]any
	updates []map[string]json.RawMessage
	created int
}

func newMCPFake(t *testing.T) *mcpFake {
	fake := &mcpFake{routeFake: newRouteFake(t), servers: map[string]map[string]any{}, env: map[string]map[string]any{}, grants: map[string][]map[string]any{}}

	fake.handle("GET /api/v1/org/mcp-servers", func(writer http.ResponseWriter, _ *http.Request) {
		identifiers := make([]string, 0, len(fake.servers))
		for identifier := range fake.servers {
			identifiers = append(identifiers, identifier)
		}
		sort.Strings(identifiers)
		listing := []map[string]any{}
		for _, identifier := range identifiers {
			server := fake.servers[identifier]
			listing = append(listing, map[string]any{
				"id": identifier, "name": server["name"], "transport": server["transport"], "enabled": server["enabled"],
				"permission_tier": server["permission_tier"], "adapter_key": server["adapter_key"],
				"last_connected_at": nil, "last_error": nil, "tool_grants_count": len(fake.grants[identifier]),
			})
		}
		writeJSON(t, writer, http.StatusOK, listing)
	})
	fake.handle("POST /api/v1/org/mcp-servers", func(writer http.ResponseWriter, request *http.Request) {
		var body map[string]any
		decodeJSON(t, request, &body)
		env, _ := body["env"].(map[string]any)
		if refusal := mcpFakeEnvRefusal(env); refusal != "" {
			writeJSON(t, writer, http.StatusUnprocessableEntity, map[string]any{"detail": []map[string]any{{"loc": []string{"body", "env"}, "msg": refusal}}})
			return
		}
		for _, server := range fake.servers {
			if server["name"] == body["name"] {
				writeJSON(t, writer, http.StatusConflict, map[string]any{"detail": "An MCP server with that name is already registered for this organisation"})
				return
			}
		}
		fake.created++
		identifier := fmt.Sprintf("99999999-0000-0000-0000-%012d", fake.created)
		if env == nil {
			env = map[string]any{}
		}
		fake.env[identifier] = env
		delete(body, "env")
		body["id"] = identifier
		body["organisation"] = request.Header.Get("X-Ankra-Organisation-Id")
		fake.servers[identifier] = body
		writeJSON(t, writer, http.StatusOK, fake.public(identifier))
	})
	fake.handle("GET /api/v1/org/mcp-servers/{server_id}", func(writer http.ResponseWriter, request *http.Request) {
		if !fake.visible(request) {
			writeJSON(t, writer, http.StatusNotFound, map[string]any{"detail": "MCP server not found"})
			return
		}
		writeJSON(t, writer, http.StatusOK, fake.public(request.PathValue("server_id")))
	})
	fake.handle("PUT /api/v1/org/mcp-servers/{server_id}", func(writer http.ResponseWriter, request *http.Request) {
		identifier := request.PathValue("server_id")
		server := fake.servers[identifier]
		if !fake.visible(request) {
			writeJSON(t, writer, http.StatusNotFound, map[string]any{"detail": "MCP server not found"})
			return
		}
		var body map[string]json.RawMessage
		decodeJSON(t, request, &body)
		fake.updates = append(fake.updates, body)
		for key, raw := range body {
			var value any
			_ = json.Unmarshal(raw, &value)
			switch key {
			case "env":
				t.Errorf("the provider must patch env, never replace it: a replacement wipes keys set outside Terraform")
			case "env_patch":
				patch, _ := value.(map[string]any)
				if refusal := mcpFakeEnvRefusal(patch); refusal != "" {
					writeJSON(t, writer, http.StatusUnprocessableEntity, map[string]any{"detail": refusal})
					return
				}
				for key, patched := range patch {
					fake.env[identifier][key] = patched
				}
			case "description", "url", "enabled", "permission_tier":
				if value != nil {
					server[key] = value
				}
			case "allowed_tools", "adapter_key", "cluster_allow_list":
				server[key] = value
			}
		}
		writeJSON(t, writer, http.StatusOK, fake.public(identifier))
	})
	fake.handle("DELETE /api/v1/org/mcp-servers/{server_id}", func(writer http.ResponseWriter, request *http.Request) {
		identifier := request.PathValue("server_id")
		if !fake.visible(request) {
			writeJSON(t, writer, http.StatusNotFound, map[string]any{"detail": "MCP server not found"})
			return
		}
		delete(fake.servers, identifier)
		delete(fake.grants, identifier)
		writeJSON(t, writer, http.StatusOK, map[string]any{"server_id": identifier, "status": "deleted"})
	})
	fake.handle("GET /api/v1/org/mcp-servers/{server_id}/grants", func(writer http.ResponseWriter, request *http.Request) {
		identifier := request.PathValue("server_id")
		if !fake.visible(request) {
			writeJSON(t, writer, http.StatusNotFound, map[string]any{"detail": "MCP server not found"})
			return
		}
		grants := fake.grants[identifier]
		if grants == nil {
			grants = []map[string]any{}
		}
		writeJSON(t, writer, http.StatusOK, grants)
	})
	fake.handle("POST /api/v1/org/mcp-servers/{server_id}/grants", func(writer http.ResponseWriter, request *http.Request) {
		identifier := request.PathValue("server_id")
		if !fake.visible(request) {
			writeJSON(t, writer, http.StatusNotFound, map[string]any{"detail": "MCP server not found"})
			return
		}
		var body map[string]any
		decodeJSON(t, request, &body)
		for _, grant := range fake.grants[identifier] {
			if grant["tool_name"] == body["tool_name"] && grant["allowed_to_role"] == body["allowed_to_role"] {
				writeJSON(t, writer, http.StatusConflict, map[string]any{"detail": "That tool is already granted for the requested role"})
				return
			}
		}
		fake.created++
		grant := map[string]any{
			"id": fmt.Sprintf("88888888-0000-0000-0000-%012d", fake.created), "org_mcp_server_id": identifier,
			"tool_name": body["tool_name"], "allowed_to_role": body["allowed_to_role"], "created_at": "2026-10-17T09:00:00Z",
		}
		fake.grants[identifier] = append(fake.grants[identifier], grant)
		writeJSON(t, writer, http.StatusOK, grant)
	})
	fake.handle("DELETE /api/v1/org/mcp-servers/{server_id}/grants/{tool_name}/{role}", func(writer http.ResponseWriter, request *http.Request) {
		identifier := request.PathValue("server_id")
		for index, grant := range fake.grants[identifier] {
			if grant["tool_name"] == request.PathValue("tool_name") && grant["allowed_to_role"] == request.PathValue("role") {
				fake.grants[identifier] = append(fake.grants[identifier][:index], fake.grants[identifier][index+1:]...)
				writeJSON(t, writer, http.StatusOK, map[string]any{"status": "revoked"})
				return
			}
		}
		writeJSON(t, writer, http.StatusNotFound, map[string]any{"detail": "MCP tool grant not found"})
	})
	fake.handle("GET /api/v1/org/organisation/roles", func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(t, writer, http.StatusOK, map[string]any{"roles": []map[string]any{
			{"slug": "owner", "builtin": true}, {"slug": "admin", "builtin": true}, {"slug": "member", "builtin": true}, {"slug": "auditor", "builtin": false},
		}})
	})
	return fake
}

// visible reports whether the server in the path exists in the organisation
// the request acts in: another organisation sees a 404.
func (fake *mcpFake) visible(request *http.Request) bool {
	server, exists := fake.servers[request.PathValue("server_id")]
	return exists && server["organisation"] == request.Header.Get("X-Ankra-Organisation-Id")
}

// public renders a server as GET does: env values never leave the platform.
func (fake *mcpFake) public(identifier string) map[string]any {
	row := map[string]any{"organisation_id": organisationTestCurrent, "created_at": "2026-10-17T09:00:00Z", "updated_at": "2026-10-17T09:00:00Z"}
	for key, value := range fake.servers[identifier] {
		row[key] = value
	}
	keys := []string{}
	for key := range fake.env[identifier] {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	row["env_keys"] = keys
	return row
}

func mcpFakeEnvRefusal(env map[string]any) string {
	for key, value := range env {
		text, _ := value.(string)
		lowered := strings.ToLower(key)
		if (strings.Contains(lowered, "token") || strings.Contains(lowered, "secret") || strings.Contains(lowered, "authorization")) &&
			text != "" && !strings.HasPrefix(text, "${SECRET_SLOT:") && !strings.HasPrefix(text, "${VAULT:") {
			return "Sensitive env values must be referenced via Vault or secret-slot markers"
		}
	}
	return ""
}

func TestAccMCPServerLifecycle(t *testing.T) {
	fake := newMCPFake(t)
	withTools := fake.providerBlock() + `
resource "ankra_mcp_server" "sentry" {
  name          = "sentry"
  description   = "Error tracking"
  transport     = "http"
  url           = "https://mcp.sentry.dev/mcp"
  env           = { Authorization = "$${SECRET_SLOT:slot-1}" }
  allowed_tools = ["find_issues", "get_issue"]
}

resource "ankra_mcp_tool_grant" "find_issues" {
  server_id = ankra_mcp_server.sentry.id
  tool_name = "find_issues"
}

data "ankra_mcp_servers" "all" {
  depends_on = [ankra_mcp_tool_grant.find_issues]
}
`
	withoutTools := fake.providerBlock() + `
resource "ankra_mcp_server" "sentry" {
  name      = "sentry"
  transport = "http"
  url       = "https://mcp.sentry.dev/mcp"
  enabled   = false
  env       = { Authorization = "$${SECRET_SLOT:slot-2}", Region = "eu" }
}

resource "ankra_mcp_tool_grant" "find_issues" {
  server_id = ankra_mcp_server.sentry.id
  tool_name = "find_issues"
  role      = "admin"
}
`
	plaintext := fake.providerBlock() + `
resource "ankra_mcp_server" "leaky" {
  name      = "leaky"
  transport = "http"
  url       = "https://example.com/mcp"
  env       = { API_TOKEN = "plaintext" }
}
`

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy: func(_ *terraform.State) error {
			if len(fake.servers) != 0 {
				return fmt.Errorf("servers left after destroy: %v", fake.servers)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: withTools,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ankra_mcp_server.sentry", "enabled", "true"),
					resource.TestCheckResourceAttr("ankra_mcp_server.sentry", "permission_tier", "read_only"),
					resource.TestCheckResourceAttr("ankra_mcp_tool_grant.find_issues", "role", "member"),
					resource.TestCheckResourceAttr("data.ankra_mcp_servers.all", "servers.0.tool_grants_count", "1"),
				),
			},
			{
				Config: withoutTools,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ankra_mcp_server.sentry", "enabled", "false"),
					resource.TestCheckNoResourceAttr("ankra_mcp_server.sentry", "allowed_tools"),
					resource.TestCheckResourceAttr("ankra_mcp_tool_grant.find_issues", "role", "admin"),
					func(_ *terraform.State) error {
						update := fake.updates[len(fake.updates)-1]
						if string(update["env_patch"]) != `{"Authorization":"${SECRET_SLOT:slot-2}","Region":"eu"}` {
							return fmt.Errorf("env must be patched with the configured keys, got %s", update["env_patch"])
						}
						if string(update["allowed_tools"]) != "null" || string(update["description"]) != `""` {
							return fmt.Errorf("update must send an explicit null allowed_tools and an empty description to clear them, got %s and %s",
								update["allowed_tools"], update["description"])
						}
						for _, server := range fake.servers {
							if server["allowed_tools"] != nil {
								return fmt.Errorf("platform still holds allowed_tools %v", server["allowed_tools"])
							}
						}
						return nil
					},
				),
			},
			{
				ResourceName:            "ankra_mcp_server.sentry",
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"env"},
			},
			{
				ResourceName:      "ankra_mcp_tool_grant.find_issues",
				ImportState:       true,
				ImportStateIdFunc: mcpToolGrantImportID,
				ImportStateVerify: true,
			},
			{
				// A header added in the portal is not Terraform's: no diff.
				PreConfig: func() {
					for identifier := range fake.env {
						fake.env[identifier]["X-Portal-Header"] = "${SECRET_SLOT:portal}"
					}
				},
				Config:   withoutTools,
				PlanOnly: true,
			},
			{
				// Without env in configuration nothing is patched, and the
				// portal header survives.
				Config: strings.Replace(withoutTools, `  env       = { Authorization = "$${SECRET_SLOT:slot-2}", Region = "eu" }`+"\n", "", 1),
				Check: func(_ *terraform.State) error {
					update := fake.updates[len(fake.updates)-1]
					if _, present := update["env_patch"]; !present {
						return fmt.Errorf("dropping env must blank the keys Terraform set, got %v", update)
					}
					for identifier := range fake.env {
						if fake.env[identifier]["X-Portal-Header"] != "${SECRET_SLOT:portal}" {
							return fmt.Errorf("the portal header was touched: %v", fake.env[identifier])
						}
						if fake.env[identifier]["Region"] != "" {
							return fmt.Errorf("a dropped key must be blanked, got %v", fake.env[identifier])
						}
					}
					return nil
				},
			},
			{
				Config: withoutTools,
			},
			{
				// A configured key lost on the platform is drift.
				PreConfig: func() {
					for identifier := range fake.env {
						delete(fake.env[identifier], "Region")
					}
				},
				Config:             withoutTools,
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
			{
				Config:      strings.Replace(withoutTools, `role      = "admin"`, `role      = "admn"`, 1),
				ExpectError: regexp.MustCompile("Unknown role"),
			},
			{
				PreConfig: func() {
					for identifier := range fake.servers {
						delete(fake.servers, identifier)
						delete(fake.grants, identifier)
					}
				},
				Config: withoutTools,
				Check: func(_ *terraform.State) error {
					if len(fake.servers) != 1 || len(fake.grants) != 1 {
						return fmt.Errorf("server and grant deleted outside Terraform were not recreated")
					}
					return nil
				},
			},
			{
				Config:      withoutTools + strings.TrimPrefix(plaintext, fake.providerBlock()),
				ExpectError: regexp.MustCompile("Sensitive env values"),
			},
		},
	})
}

// TestAccMCPServerOrganisationSwitchKeepsTheResource pins that pointing the
// provider at another organisation, where the server answers 404, neither
// drops the server nor lets destroy report success.
func TestAccMCPServerOrganisationSwitchKeepsTheResource(t *testing.T) {
	fake := newMCPFake(t)
	inOrganisation := func(organisationID string) string {
		return fmt.Sprintf(`
provider "ankra" {
  token           = "test-token"
  base_url        = %q
  organisation_id = %q
}

resource "ankra_mcp_server" "sentry" {
  name      = "sentry"
  transport = "http"
  url       = "https://mcp.sentry.dev/mcp"
}
`, fake.server.URL, organisationID)
	}

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{Config: inOrganisation(organisationTestCurrent)},
			{
				Config:      inOrganisation("aaaaaaaa-0000-0000-0000-000000000099"),
				ExpectError: regexp.MustCompile(`Object not visible in the provider's current scope`),
			},
			{
				Config: inOrganisation(organisationTestCurrent),
				Check: func(_ *terraform.State) error {
					if len(fake.servers) != 1 {
						return fmt.Errorf("the switch created or lost a server: %v", fake.servers)
					}
					return nil
				},
			},
		},
	})
}

func mcpToolGrantImportID(state *terraform.State) (string, error) {
	attributes := state.RootModule().Resources["ankra_mcp_tool_grant.find_issues"].Primary.Attributes
	return attributes["server_id"] + "/" + attributes["role"] + "/" + attributes["tool_name"], nil
}
