// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"sort"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"ankra.io/terraform-provider-ankra/internal/client"
)

var (
	_ resource.Resource                = (*mcpServerResource)(nil)
	_ resource.ResourceWithConfigure   = (*mcpServerResource)(nil)
	_ resource.ResourceWithImportState = (*mcpServerResource)(nil)
)

type mcpServerResource struct {
	configuredClient
}

type mcpServerModel struct {
	ID               types.String `tfsdk:"id"`
	Name             types.String `tfsdk:"name"`
	Description      types.String `tfsdk:"description"`
	Transport        types.String `tfsdk:"transport"`
	URL              types.String `tfsdk:"url"`
	Env              types.Map    `tfsdk:"env"`
	AllowedTools     types.Set    `tfsdk:"allowed_tools"`
	Enabled          types.Bool   `tfsdk:"enabled"`
	PermissionTier   types.String `tfsdk:"permission_tier"`
	AdapterKey       types.String `tfsdk:"adapter_key"`
	ClusterAllowList types.Set    `tfsdk:"cluster_allow_list"`
}

// NewMCPServerResource returns the ankra_mcp_server resource, the
// counterpart of `ankra org mcp-servers add|update|remove|enable|disable`.
func NewMCPServerResource() resource.Resource {
	return &mcpServerResource{}
}

func (server *mcpServerResource) Metadata(_ context.Context, request resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_mcp_server"
}

func (server *mcpServerResource) Schema(_ context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: "An MCP tool server registered for the organisation's AI agents. The counterpart of " +
			"`ankra org mcp-servers add`, `update`, `remove`, `enable` and `disable`. Changes need the `ai.manage` permission. " +
			"Grant individual tools to roles with `ankra_mcp_tool_grant`.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Server id.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				MarkdownDescription: "Server name, unique in the organisation, without spaces or `/`. Changing it re-registers the server.",
				Required:            true,
				Validators:          []validator.String{mcpServerNameValidator{}},
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"description": schema.StringAttribute{
				MarkdownDescription: "Free-text description.",
				Optional:            true,
			},
			"transport": schema.StringAttribute{
				MarkdownDescription: "Transport: `http`, `sse` or `stdio`. Changing it re-registers the server.",
				Required:            true,
				Validators:          []validator.String{organisationStringOneOf{values: []string{"http", "sse", "stdio"}}},
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"url": schema.StringAttribute{
				MarkdownDescription: "Endpoint URL. For `http` and `sse` it must be a public http(s) endpoint; for `stdio` it is only an identifier.",
				Required:            true,
			},
			"env": schema.MapAttribute{
				MarkdownDescription: "Environment variables and headers sent to the server. A value under a key that looks sensitive " +
					"(token, secret, password, api_key, authorization and the like) must be a `${SECRET_SLOT:<id>}` or `${VAULT:<path>}` " +
					"marker: the platform refuses plaintext there. Values are never returned, so only a change to the set of keys is detected.",
				ElementType: types.StringType,
				Optional:    true,
				Sensitive:   true,
			},
			"allowed_tools": schema.SetAttribute{
				MarkdownDescription: "Tools agent runs may call. Unset allows every tool the server offers.",
				ElementType:         types.StringType,
				Optional:            true,
			},
			"enabled": schema.BoolAttribute{
				MarkdownDescription: "Whether agent runs may use the server. Defaults to `true`.",
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(true),
			},
			"permission_tier": schema.StringAttribute{
				MarkdownDescription: "`read_only` or `read_write`. Defaults to `read_only`.",
				Optional:            true,
				Computed:            true,
				Default:             stringdefault.StaticString("read_only"),
				Validators:          []validator.String{organisationStringOneOf{values: []string{"read_only", "read_write"}}},
			},
			"adapter_key": schema.StringAttribute{
				MarkdownDescription: "Curated adapter from `ankra org mcp-servers catalog` this server is an instance of.",
				Optional:            true,
			},
			"cluster_allow_list": schema.SetAttribute{
				MarkdownDescription: "Cluster ids allowed to use the server. Unset makes it available to every cluster.",
				ElementType:         types.StringType,
				Optional:            true,
			},
		},
	}
}

func (server *mcpServerResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	apiClient := server.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var plan mcpServerModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}
	env := mcpServerEnv(ctx, plan.Env, &response.Diagnostics)
	allowedTools := organisationStringsFromSet(ctx, plan.AllowedTools, &response.Diagnostics)
	clusterAllowList := organisationStringsFromSet(ctx, plan.ClusterAllowList, &response.Diagnostics)
	if response.Diagnostics.HasError() {
		return
	}

	created, err := apiClient.CreateMCPServer(ctx, client.MCPServerCreate{
		Name:             plan.Name.ValueString(),
		Description:      plan.Description.ValueString(),
		Transport:        plan.Transport.ValueString(),
		URL:              plan.URL.ValueString(),
		Env:              env,
		AllowedTools:     allowedTools,
		Enabled:          plan.Enabled.ValueBool(),
		PermissionTier:   plan.PermissionTier.ValueString(),
		AdapterKey:       stringPointer(plan.AdapterKey),
		ClusterAllowList: clusterAllowList,
	})
	if err != nil {
		response.Diagnostics.AddError("Unable to register MCP server", err.Error())
		return
	}
	plan.ID = types.StringValue(created.ID)
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
}

func (server *mcpServerResource) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	apiClient := server.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state mcpServerModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}

	stored, err := apiClient.GetMCPServer(ctx, state.ID.ValueString())
	if removeWhenNotFound(ctx, err, response) {
		return
	}
	if err != nil {
		response.Diagnostics.AddError("Unable to read MCP server", err.Error())
		return
	}

	state.Name = types.StringValue(stored.Name)
	state.Description = optionalString(stored.Description)
	state.Transport = types.StringValue(stored.Transport)
	state.URL = types.StringValue(stored.URL)
	state.Env = mcpServerEnvState(ctx, state.Env, stored.EnvKeys, &response.Diagnostics)
	state.AllowedTools = organisationSetFromStrings(stored.AllowedTools)
	state.Enabled = types.BoolValue(stored.Enabled)
	state.PermissionTier = types.StringValue(stored.PermissionTier)
	state.AdapterKey = types.StringPointerValue(stored.AdapterKey)
	state.ClusterAllowList = organisationSetFromStrings(stored.ClusterAllowList)
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
}

func (server *mcpServerResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	apiClient := server.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var plan mcpServerModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}
	env := mcpServerEnv(ctx, plan.Env, &response.Diagnostics)
	allowedTools := organisationStringsFromSet(ctx, plan.AllowedTools, &response.Diagnostics)
	clusterAllowList := organisationStringsFromSet(ctx, plan.ClusterAllowList, &response.Diagnostics)
	if response.Diagnostics.HasError() {
		return
	}
	if env == nil {
		env = map[string]string{}
	}

	if _, err := apiClient.UpdateMCPServer(ctx, plan.ID.ValueString(), client.MCPServerUpdate{
		Description:      plan.Description.ValueString(),
		URL:              plan.URL.ValueString(),
		Env:              env,
		AllowedTools:     allowedTools,
		Enabled:          plan.Enabled.ValueBool(),
		PermissionTier:   plan.PermissionTier.ValueString(),
		AdapterKey:       stringPointer(plan.AdapterKey),
		ClusterAllowList: clusterAllowList,
	}); err != nil {
		response.Diagnostics.AddError("Unable to update MCP server", err.Error())
		return
	}
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
}

func (server *mcpServerResource) Delete(ctx context.Context, request resource.DeleteRequest, response *resource.DeleteResponse) {
	apiClient := server.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state mcpServerModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	if err := apiClient.DeleteMCPServer(ctx, state.ID.ValueString()); err != nil {
		response.Diagnostics.AddError("Unable to remove MCP server", err.Error())
	}
}

// ImportState imports by server id. env values cannot be read back, so an
// imported server with env keys plans an update that sets the configured
// values.
func (server *mcpServerResource) ImportState(ctx context.Context, request resource.ImportStateRequest, response *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), request, response)
}

func mcpServerEnv(ctx context.Context, env types.Map, diagnostics *diag.Diagnostics) map[string]string {
	if env.IsNull() || env.IsUnknown() {
		return nil
	}
	values := map[string]string{}
	diagnostics.Append(env.ElementsAs(ctx, &values, false)...)
	return values
}

// mcpServerEnvState keeps the state's env while the platform holds the same
// keys. When the keys differ, the stored keys are recorded with empty values,
// which plans an update that writes the configured env back.
func mcpServerEnvState(ctx context.Context, stateValue types.Map, storedKeys []string, diagnostics *diag.Diagnostics) types.Map {
	stateKeys := []string{}
	for key := range mcpServerEnv(ctx, stateValue, diagnostics) {
		stateKeys = append(stateKeys, key)
	}
	sort.Strings(stateKeys)
	platformKeys := append([]string{}, storedKeys...)
	sort.Strings(platformKeys)
	if strings.Join(stateKeys, "\x00") == strings.Join(platformKeys, "\x00") {
		return stateValue
	}
	if len(platformKeys) == 0 {
		return types.MapNull(types.StringType)
	}
	elements := make(map[string]attr.Value, len(platformKeys))
	for _, key := range platformKeys {
		elements[key] = types.StringValue("")
	}
	return types.MapValueMust(types.StringType, elements)
}

// mcpServerNameValidator mirrors the platform's name rule: 1 to 120
// characters, no whitespace and no "/".
type mcpServerNameValidator struct{}

func (mcpServerNameValidator) Description(_ context.Context) string {
	return "name must be 1 to 120 characters without whitespace or \"/\""
}

func (nameValidator mcpServerNameValidator) MarkdownDescription(ctx context.Context) string {
	return nameValidator.Description(ctx)
}

func (nameValidator mcpServerNameValidator) ValidateString(ctx context.Context, request validator.StringRequest, response *validator.StringResponse) {
	if request.ConfigValue.IsNull() || request.ConfigValue.IsUnknown() {
		return
	}
	name := request.ConfigValue.ValueString()
	if name == "" || len([]rune(name)) > 120 || strings.ContainsAny(name, "/ \t\n\r") {
		response.Diagnostics.AddAttributeError(request.Path, "Invalid MCP server name", nameValidator.Description(ctx)+".")
	}
}
