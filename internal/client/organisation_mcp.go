// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package client

import (
	"context"
	"net/http"
	"net/url"
)

const mcpServersPath = "/api/v1/org/mcp-servers"

// MCPServer is an organisation MCP tool server registration. EnvKeys lists
// the configured env and header names only: values are never echoed.
type MCPServer struct {
	ID               string   `json:"id"`
	Name             string   `json:"name"`
	Description      string   `json:"description"`
	Transport        string   `json:"transport"`
	URL              string   `json:"url"`
	EnvKeys          []string `json:"env_keys"`
	AllowedTools     []string `json:"allowed_tools"`
	Enabled          bool     `json:"enabled"`
	PermissionTier   string   `json:"permission_tier"`
	AdapterKey       *string  `json:"adapter_key"`
	ClusterAllowList []string `json:"cluster_allow_list"`
	LastConnectedAt  *string  `json:"last_connected_at"`
	LastError        *string  `json:"last_error"`
}

// MCPServerSummary is one row of the server listing.
type MCPServerSummary struct {
	ID              string  `json:"id"`
	Name            string  `json:"name"`
	Transport       string  `json:"transport"`
	Enabled         bool    `json:"enabled"`
	PermissionTier  string  `json:"permission_tier"`
	AdapterKey      *string `json:"adapter_key"`
	LastConnectedAt *string `json:"last_connected_at"`
	LastError       *string `json:"last_error"`
	ToolGrantsCount int     `json:"tool_grants_count"`
}

// MCPServerCreate is the register body. Env values under sensitive-looking
// keys must be ${SECRET_SLOT:<id>} or ${VAULT:<path>} markers: the platform
// refuses plaintext there. Nil AllowedTools allows every tool; nil
// ClusterAllowList makes the server organisation-wide.
type MCPServerCreate struct {
	Name             string            `json:"name"`
	Description      string            `json:"description"`
	Transport        string            `json:"transport"`
	URL              string            `json:"url"`
	Env              map[string]string `json:"env"`
	AllowedTools     []string          `json:"allowed_tools"`
	Enabled          bool              `json:"enabled"`
	PermissionTier   string            `json:"permission_tier"`
	AdapterKey       *string           `json:"adapter_key"`
	ClusterAllowList []string          `json:"cluster_allow_list"`
}

// MCPServerUpdate is the update body. Every member is sent: an absent key
// would keep the stored value, so a field removed from configuration would
// never clear. A null allowed_tools, adapter_key or cluster_allow_list
// clears it; an empty description clears the description. Env replaces the
// whole env map.
type MCPServerUpdate struct {
	Description      string            `json:"description"`
	URL              string            `json:"url"`
	Env              map[string]string `json:"env"`
	AllowedTools     []string          `json:"allowed_tools"`
	Enabled          bool              `json:"enabled"`
	PermissionTier   string            `json:"permission_tier"`
	AdapterKey       *string           `json:"adapter_key"`
	ClusterAllowList []string          `json:"cluster_allow_list"`
}

// MCPToolGrant grants one tool of a server to one organisation role.
type MCPToolGrant struct {
	ID             string `json:"id"`
	OrgMCPServerID string `json:"org_mcp_server_id"`
	ToolName       string `json:"tool_name"`
	AllowedToRole  string `json:"allowed_to_role"`
	CreatedAt      string `json:"created_at"`
}

type mcpToolGrantCreate struct {
	ToolName      string `json:"tool_name"`
	AllowedToRole string `json:"allowed_to_role"`
}

func mcpServerPath(serverID string) string {
	return mcpServersPath + "/" + url.PathEscape(serverID)
}

// ListMCPServers lists the organisation's MCP servers.
func (client *Client) ListMCPServers(ctx context.Context) ([]MCPServerSummary, error) {
	var servers []MCPServerSummary
	if err := client.doRequest(ctx, http.MethodGet, mcpServersPath, nil, &servers); err != nil {
		return nil, err
	}
	return servers, nil
}

// GetMCPServer reads one server.
func (client *Client) GetMCPServer(ctx context.Context, serverID string) (*MCPServer, error) {
	var server MCPServer
	if err := client.doRequest(ctx, http.MethodGet, mcpServerPath(serverID), nil, &server); err != nil {
		return nil, err
	}
	return &server, nil
}

// CreateMCPServer registers a server.
func (client *Client) CreateMCPServer(ctx context.Context, request MCPServerCreate) (*MCPServer, error) {
	var server MCPServer
	if err := client.doRequest(ctx, http.MethodPost, mcpServersPath, request, &server); err != nil {
		return nil, err
	}
	return &server, nil
}

// UpdateMCPServer rewrites a server's mutable fields.
func (client *Client) UpdateMCPServer(ctx context.Context, serverID string, request MCPServerUpdate) (*MCPServer, error) {
	var server MCPServer
	if err := client.doRequest(ctx, http.MethodPut, mcpServerPath(serverID), request, &server); err != nil {
		return nil, err
	}
	return &server, nil
}

// DeleteMCPServer removes a server and its grants. A server already gone is
// not an error.
func (client *Client) DeleteMCPServer(ctx context.Context, serverID string) error {
	return client.doRequest(ctx, http.MethodDelete, mcpServerPath(serverID), nil, nil, http.StatusNotFound)
}

// ListMCPToolGrants lists a server's tool grants. A server that is gone is a
// NotFound APIError.
func (client *Client) ListMCPToolGrants(ctx context.Context, serverID string) ([]MCPToolGrant, error) {
	var grants []MCPToolGrant
	if err := client.doRequest(ctx, http.MethodGet, mcpServerPath(serverID)+"/grants", nil, &grants); err != nil {
		return nil, err
	}
	return grants, nil
}

// CreateMCPToolGrant grants one tool to one role.
func (client *Client) CreateMCPToolGrant(ctx context.Context, serverID, toolName, role string) (*MCPToolGrant, error) {
	var grant MCPToolGrant
	body := mcpToolGrantCreate{ToolName: toolName, AllowedToRole: role}
	if err := client.doRequest(ctx, http.MethodPost, mcpServerPath(serverID)+"/grants", body, &grant); err != nil {
		return nil, err
	}
	return &grant, nil
}

// DeleteMCPToolGrant revokes one tool from one role. A grant already gone is
// not an error.
func (client *Client) DeleteMCPToolGrant(ctx context.Context, serverID, toolName, role string) error {
	grantPath := mcpServerPath(serverID) + "/grants/" + url.PathEscape(toolName) + "/" + url.PathEscape(role)
	return client.doRequest(ctx, http.MethodDelete, grantPath, nil, nil, http.StatusNotFound)
}
