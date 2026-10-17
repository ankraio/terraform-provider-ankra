// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ datasource.DataSource              = (*mcpServersDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*mcpServersDataSource)(nil)
)

type mcpServersDataSource struct {
	configuredDataSourceClient
}

type mcpServerItemModel struct {
	ID              types.String `tfsdk:"id"`
	Name            types.String `tfsdk:"name"`
	Transport       types.String `tfsdk:"transport"`
	Enabled         types.Bool   `tfsdk:"enabled"`
	PermissionTier  types.String `tfsdk:"permission_tier"`
	AdapterKey      types.String `tfsdk:"adapter_key"`
	LastError       types.String `tfsdk:"last_error"`
	ToolGrantsCount types.Int64  `tfsdk:"tool_grants_count"`
}

type mcpServersDataSourceModel struct {
	ID      types.String         `tfsdk:"id"`
	Servers []mcpServerItemModel `tfsdk:"servers"`
}

// NewMCPServersDataSource returns the ankra_mcp_servers data source, the
// counterpart of `ankra org mcp-servers list`.
func NewMCPServersDataSource() datasource.DataSource {
	return &mcpServersDataSource{}
}

func (servers *mcpServersDataSource) Metadata(_ context.Context, request datasource.MetadataRequest, response *datasource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_mcp_servers"
}

func (servers *mcpServersDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, response *datasource.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: "Lists the organisation's MCP tool servers. The counterpart of `ankra org mcp-servers list`.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{MarkdownDescription: "Placeholder identifier for the data source.", Computed: true},
			"servers": schema.ListNestedAttribute{
				MarkdownDescription: "Registered servers.",
				Computed:            true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id":                schema.StringAttribute{MarkdownDescription: "Server id.", Computed: true},
						"name":              schema.StringAttribute{MarkdownDescription: "Server name.", Computed: true},
						"transport":         schema.StringAttribute{MarkdownDescription: "`http`, `sse` or `stdio`.", Computed: true},
						"enabled":           schema.BoolAttribute{MarkdownDescription: "Whether agent runs may use it.", Computed: true},
						"permission_tier":   schema.StringAttribute{MarkdownDescription: "`read_only` or `read_write`.", Computed: true},
						"adapter_key":       schema.StringAttribute{MarkdownDescription: "Curated adapter, if any.", Computed: true},
						"last_error":        schema.StringAttribute{MarkdownDescription: "Last connection error, if any.", Computed: true},
						"tool_grants_count": schema.Int64Attribute{MarkdownDescription: "Number of tool grants.", Computed: true},
					},
				},
			},
		},
	}
}

func (servers *mcpServersDataSource) Read(ctx context.Context, _ datasource.ReadRequest, response *datasource.ReadResponse) {
	apiClient := servers.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	listing, err := apiClient.ListMCPServers(ctx)
	if err != nil {
		response.Diagnostics.AddError("Unable to list MCP servers", err.Error())
		return
	}
	state := mcpServersDataSourceModel{
		ID:      types.StringValue("ankra_mcp_servers"),
		Servers: make([]mcpServerItemModel, 0, len(listing)),
	}
	for _, server := range listing {
		state.Servers = append(state.Servers, mcpServerItemModel{
			ID:              types.StringValue(server.ID),
			Name:            types.StringValue(server.Name),
			Transport:       types.StringValue(server.Transport),
			Enabled:         types.BoolValue(server.Enabled),
			PermissionTier:  types.StringValue(server.PermissionTier),
			AdapterKey:      types.StringPointerValue(server.AdapterKey),
			LastError:       types.StringPointerValue(server.LastError),
			ToolGrantsCount: types.Int64Value(int64(server.ToolGrantsCount)),
		})
	}
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
}
