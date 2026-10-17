// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ resource.Resource                = (*mcpToolGrantResource)(nil)
	_ resource.ResourceWithConfigure   = (*mcpToolGrantResource)(nil)
	_ resource.ResourceWithImportState = (*mcpToolGrantResource)(nil)
)

type mcpToolGrantResource struct {
	configuredClient
}

type mcpToolGrantModel struct {
	ID       types.String `tfsdk:"id"`
	ServerID types.String `tfsdk:"server_id"`
	ToolName types.String `tfsdk:"tool_name"`
	Role     types.String `tfsdk:"role"`
}

// NewMCPToolGrantResource returns the ankra_mcp_tool_grant resource, the
// counterpart of `ankra org mcp-servers grant` and `revoke-grant`.
func NewMCPToolGrantResource() resource.Resource {
	return &mcpToolGrantResource{}
}

func (grant *mcpToolGrantResource) Metadata(_ context.Context, request resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_mcp_tool_grant"
}

func (grant *mcpToolGrantResource) Schema(_ context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: "Grants one tool of an MCP server to one organisation role. The counterpart of " +
			"`ankra org mcp-servers grant` and `revoke-grant`. Changes need the `ai.manage` permission.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Grant id.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"server_id": schema.StringAttribute{
				MarkdownDescription: "MCP server id.",
				Required:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"tool_name": schema.StringAttribute{
				MarkdownDescription: "Tool name as the server lists it, at most 160 characters, without `/`.",
				Required:            true,
				Validators:          []validator.String{mcpToolNameValidator{}},
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"role": schema.StringAttribute{
				MarkdownDescription: "Organisation role the tool is granted to. Defaults to `member`.",
				Optional:            true,
				Computed:            true,
				Default:             stringdefault.StaticString("member"),
				Validators:          []validator.String{mcpGrantRoleValidator{}},
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
		},
	}
}

func (grant *mcpToolGrantResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	apiClient := grant.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var plan mcpToolGrantModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}
	created, err := apiClient.CreateMCPToolGrant(ctx, plan.ServerID.ValueString(), plan.ToolName.ValueString(), plan.Role.ValueString())
	if err != nil {
		response.Diagnostics.AddError("Unable to grant MCP tool", err.Error())
		return
	}
	plan.ID = types.StringValue(created.ID)
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
}

func (grant *mcpToolGrantResource) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	apiClient := grant.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state mcpToolGrantModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}

	grants, err := apiClient.ListMCPToolGrants(ctx, state.ServerID.ValueString())
	if removeWhenNotFound(ctx, err, response) {
		return
	}
	if err != nil {
		response.Diagnostics.AddError("Unable to read MCP tool grants", err.Error())
		return
	}
	for _, listed := range grants {
		if listed.ToolName != state.ToolName.ValueString() || listed.AllowedToRole != state.Role.ValueString() {
			continue
		}
		if readTargetChanged(state.ID, listed.ID) {
			break
		}
		state.ID = types.StringValue(listed.ID)
		response.Diagnostics.Append(response.State.Set(ctx, &state)...)
		return
	}
	response.State.RemoveResource(ctx)
}

// Update is never reached with a change: every configurable attribute
// forces replacement.
func (grant *mcpToolGrantResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	if grant.requireClient(&response.Diagnostics) == nil {
		return
	}
	var plan mcpToolGrantModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
}

func (grant *mcpToolGrantResource) Delete(ctx context.Context, request resource.DeleteRequest, response *resource.DeleteResponse) {
	apiClient := grant.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state mcpToolGrantModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	if err := apiClient.DeleteMCPToolGrant(ctx, state.ServerID.ValueString(), state.ToolName.ValueString(), state.Role.ValueString()); err != nil {
		response.Diagnostics.AddError("Unable to revoke MCP tool grant", err.Error())
	}
}

// ImportState imports by "<server_id>/<role>/<tool_name>".
func (grant *mcpToolGrantResource) ImportState(ctx context.Context, request resource.ImportStateRequest, response *resource.ImportStateResponse) {
	parts, err := organisationSplitImportID(request.ID, 3, "<server_id>/<role>/<tool_name>")
	if err != nil {
		response.Diagnostics.AddError("Invalid import id", err.Error())
		return
	}
	response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("server_id"), parts[0])...)
	response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("role"), parts[1])...)
	response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("tool_name"), parts[2])...)
}

// mcpToolNameValidator refuses names the revoke route cannot address: the
// tool name is a path segment there, and an escaped "/" is not routed.
type mcpToolNameValidator struct{}

func (mcpToolNameValidator) Description(_ context.Context) string {
	return "tool_name must be 1 to 160 characters without \"/\""
}

func (nameValidator mcpToolNameValidator) MarkdownDescription(ctx context.Context) string {
	return nameValidator.Description(ctx)
}

func (nameValidator mcpToolNameValidator) ValidateString(ctx context.Context, request validator.StringRequest, response *validator.StringResponse) {
	if request.ConfigValue.IsNull() || request.ConfigValue.IsUnknown() {
		return
	}
	name := request.ConfigValue.ValueString()
	if strings.TrimSpace(name) == "" || len([]rune(name)) > 160 || strings.Contains(name, "/") || name != strings.TrimSpace(name) {
		response.Diagnostics.AddAttributeError(request.Path, "Invalid tool name", nameValidator.Description(ctx)+", and without surrounding whitespace.")
	}
}

// mcpGrantRoleValidator mirrors the platform's 32-character role limit and
// refuses "/", which the revoke route cannot address.
type mcpGrantRoleValidator struct{}

func (mcpGrantRoleValidator) Description(_ context.Context) string {
	return "role must be 1 to 32 characters without \"/\""
}

func (roleValidator mcpGrantRoleValidator) MarkdownDescription(ctx context.Context) string {
	return roleValidator.Description(ctx)
}

func (roleValidator mcpGrantRoleValidator) ValidateString(ctx context.Context, request validator.StringRequest, response *validator.StringResponse) {
	if request.ConfigValue.IsNull() || request.ConfigValue.IsUnknown() {
		return
	}
	role := request.ConfigValue.ValueString()
	if role == "" || len([]rune(role)) > 32 || strings.Contains(role, "/") {
		response.Diagnostics.AddAttributeError(request.Path, "Invalid role", roleValidator.Description(ctx)+".")
	}
}
