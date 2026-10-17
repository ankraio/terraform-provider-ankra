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
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/setplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"ankra.io/terraform-provider-ankra/internal/client"
)

var (
	_ resource.Resource                = (*apiTokenResource)(nil)
	_ resource.ResourceWithConfigure   = (*apiTokenResource)(nil)
	_ resource.ResourceWithImportState = (*apiTokenResource)(nil)
)

// apiTokenNameConflictHint explains the one create conflict a Terraform user
// can cause without seeing it: a token revoked outside Terraform keeps its
// name until it is deleted.
const apiTokenNameConflictHint = "\n\nA revoked token keeps its name until it is deleted: delete it with `ankra tokens delete <id>` and apply again."

type apiTokenResource struct {
	configuredClient
}

type apiTokenModel struct {
	ID        types.String `tfsdk:"id"`
	Name      types.String `tfsdk:"name"`
	ExpiresAt types.String `tfsdk:"expires_at"`
	Scopes    types.Set    `tfsdk:"scopes"`
	ProjectID types.String `tfsdk:"project_id"`
	CreatedAt types.String `tfsdk:"created_at"`
	Token     types.String `tfsdk:"token"`
}

// NewAPITokenResource returns the ankra_api_token resource, the counterpart
// of `ankra tokens create`, `revoke` and `delete`.
func NewAPITokenResource() resource.Resource {
	return &apiTokenResource{}
}

func (token *apiTokenResource) Metadata(_ context.Context, request resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_api_token"
}

// apiTokenScopeAttribute is the scopes attribute shared by the personal and
// service token resources.
func apiTokenScopeAttribute() schema.SetAttribute {
	return schema.SetAttribute{
		MarkdownDescription: "Permission allowlist: `mcp:read` and/or `mcp:write` make an MCP token, which ordinary REST requests refuse. " +
			"Unset keeps the token at the full authority of its holder.",
		ElementType:   types.StringType,
		Optional:      true,
		PlanModifiers: []planmodifier.Set{setplanmodifier.RequiresReplace()},
	}
}

func (token *apiTokenResource) Schema(_ context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: "A personal API token of the provider token's user, in the current organisation. The counterpart of `ankra tokens create`; " +
			"destroy revokes and then deletes it, like `ankra tokens revoke` followed by `ankra tokens delete`.\n\n" +
			"The token value is revealed once, at creation, and kept in state as a sensitive value. Nothing about a token can be " +
			"changed in place, so every change mints a new token. A token revoked outside Terraform is treated as gone and planned for recreation.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Token id.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				MarkdownDescription: "Token name: letters, digits, spaces, `-`, `_` and `.`, at most 100 characters, unique among the user's tokens.",
				Required:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"expires_at": schema.StringAttribute{
				MarkdownDescription: "Expiry as an RFC 3339 timestamp. Defaults to 90 days after creation.",
				Optional:            true,
				Computed:            true,
				Validators:          []validator.String{organisationTimestampValidator{}},
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown(), stringplanmodifier.RequiresReplace()},
			},
			"scopes": apiTokenScopeAttribute(),
			"project_id": schema.StringAttribute{
				MarkdownDescription: "Project the token is pinned to. The platform pins an MCP-scoped token to the project requests act in.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"created_at": schema.StringAttribute{
				MarkdownDescription: "Creation time.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"token": schema.StringAttribute{
				MarkdownDescription: "The token value. Revealed only at creation; null after an import.",
				Computed:            true,
				Sensitive:           true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
		},
	}
}

func (token *apiTokenResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	apiClient := token.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var plan apiTokenModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}
	scopes := organisationStringsFromSet(ctx, plan.Scopes, &response.Diagnostics)
	if response.Diagnostics.HasError() {
		return
	}

	created, err := apiClient.CreateAPIToken(ctx, client.OrganisationAPITokenCreate{
		Name:      plan.Name.ValueString(),
		ExpiresAt: stringPointer(plan.ExpiresAt),
		Scopes:    scopes,
	})
	if err != nil {
		detail := err.Error()
		if strings.Contains(detail, "already exists") {
			detail += apiTokenNameConflictHint
		}
		response.Diagnostics.AddError("Unable to create API token", detail)
		return
	}
	plan.ID = types.StringValue(created.ID)
	plan.Token = types.StringValue(created.Token)
	plan.ExpiresAt = organisationTimestampState(plan.ExpiresAt, created.ExpiresAt)
	plan.ProjectID = types.StringNull()
	plan.CreatedAt = types.StringNull()

	listed, err := apiClient.GetAPIToken(ctx, created.ID)
	if err != nil {
		response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
		response.Diagnostics.AddError("Unable to read the created API token", err.Error())
		return
	}
	apiTokenApplyListing(&plan, listed)
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
}

func (token *apiTokenResource) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	apiClient := token.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state apiTokenModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}

	listed, err := apiClient.GetAPIToken(ctx, state.ID.ValueString())
	if removeWhenNotFound(ctx, err, response) {
		return
	}
	if err != nil {
		response.Diagnostics.AddError("Unable to read API token", err.Error())
		return
	}
	if listed.Revoked {
		response.State.RemoveResource(ctx)
		return
	}
	apiTokenApplyListing(&state, listed)
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
}

// Update is never reached with a change: every configurable attribute
// forces replacement.
func (token *apiTokenResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	if token.requireClient(&response.Diagnostics) == nil {
		return
	}
	var plan apiTokenModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
}

// Delete revokes the token, then deletes it: the platform refuses to delete
// a live token.
func (token *apiTokenResource) Delete(ctx context.Context, request resource.DeleteRequest, response *resource.DeleteResponse) {
	apiClient := token.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state apiTokenModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	if err := apiClient.RevokeAPIToken(ctx, state.ID.ValueString()); err != nil {
		response.Diagnostics.AddError("Unable to revoke API token", err.Error())
		return
	}
	if err := apiClient.DeleteAPIToken(ctx, state.ID.ValueString()); err != nil {
		response.Diagnostics.AddError("Unable to delete API token", err.Error())
	}
}

// ImportState imports by token id. The token value cannot be recovered, so
// it is null after an import.
func (token *apiTokenResource) ImportState(ctx context.Context, request resource.ImportStateRequest, response *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), request, response)
}

func apiTokenApplyListing(model *apiTokenModel, listed *client.OrganisationAPIToken) {
	model.Name = types.StringValue(listed.Name)
	model.ExpiresAt = organisationTimestampState(model.ExpiresAt, listed.ExpiresAt)
	model.Scopes = organisationOptionalSet(model.Scopes, listed.Scopes)
	model.ProjectID = types.StringPointerValue(listed.ProjectID)
	model.CreatedAt = organisationTimestampState(model.CreatedAt, listed.CreatedAt)
}
