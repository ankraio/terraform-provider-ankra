// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"ankra.io/terraform-provider-ankra/internal/client"
)

var (
	_ resource.Resource                = (*serviceTokenResource)(nil)
	_ resource.ResourceWithConfigure   = (*serviceTokenResource)(nil)
	_ resource.ResourceWithImportState = (*serviceTokenResource)(nil)
)

type serviceTokenResource struct {
	configuredClient
}

type serviceTokenModel struct {
	ID        types.String `tfsdk:"id"`
	Name      types.String `tfsdk:"name"`
	RoleSlug  types.String `tfsdk:"role_slug"`
	Label     types.String `tfsdk:"label"`
	ExpiresAt types.String `tfsdk:"expires_at"`
	Scopes    types.Set    `tfsdk:"scopes"`
	AccountID types.String `tfsdk:"account_id"`
	ProjectID types.String `tfsdk:"project_id"`
	Token     types.String `tfsdk:"token"`
}

// NewServiceTokenResource returns the ankra_service_token resource, the
// counterpart of `ankra tokens create --service`.
func NewServiceTokenResource() resource.Resource {
	return &serviceTokenResource{}
}

func (token *serviceTokenResource) Metadata(_ context.Context, request resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_service_token"
}

func (token *serviceTokenResource) Schema(_ context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: "An organisation service token: a token that belongs to a service account rather than a person, so it keeps " +
			"working when its creator leaves. The counterpart of `ankra tokens create --service`; destroy revokes and then deletes it. " +
			"Managing service tokens needs the `members.manage` permission.\n\n" +
			"The service account is created with `role_slug` when the organisation has none of that name; for an existing account " +
			"`role_slug` must be the role it already holds. The token value is revealed once, at creation, and kept in state as a " +
			"sensitive value. Every change mints a new token. A token revoked outside Terraform is treated as gone and planned for recreation.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Token id.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				MarkdownDescription: "Service account name.",
				Required:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"role_slug": schema.StringAttribute{
				MarkdownDescription: "Role the service account holds, such as `member` or a custom role's slug. It may not carry permissions the caller lacks.",
				Required:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"label": schema.StringAttribute{
				MarkdownDescription: "Name of this token among the account's tokens. Defaults to a timestamp of when it was minted.",
				Optional:            true,
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown(), stringplanmodifier.RequiresReplace()},
			},
			"expires_at": schema.StringAttribute{
				MarkdownDescription: "Expiry as an RFC 3339 timestamp. Defaults to 90 days after creation.",
				Optional:            true,
				Computed:            true,
				Validators:          []validator.String{organisationTimestampValidator{}},
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown(), stringplanmodifier.RequiresReplace()},
			},
			"scopes": apiTokenScopeAttribute(),
			"account_id": schema.StringAttribute{
				MarkdownDescription: "Service account id.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"project_id": schema.StringAttribute{
				MarkdownDescription: "Project the token is pinned to, if any.",
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

func (token *serviceTokenResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	apiClient := token.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var plan serviceTokenModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}
	scopes := organisationStringsFromSet(ctx, plan.Scopes, &response.Diagnostics)
	if response.Diagnostics.HasError() {
		return
	}

	created, err := apiClient.CreateServiceToken(ctx, client.OrganisationServiceTokenCreate{
		Name:      plan.Name.ValueString(),
		Label:     plan.Label.ValueString(),
		RoleSlug:  plan.RoleSlug.ValueString(),
		ExpiresAt: stringPointer(plan.ExpiresAt),
		Scopes:    scopes,
	})
	if err != nil {
		response.Diagnostics.AddError("Unable to create service token", err.Error())
		return
	}
	plan.ID = types.StringValue(created.ID)
	plan.Token = types.StringValue(created.Token)
	plan.Label = types.StringValue(created.Label)
	plan.AccountID = types.StringValue(created.AccountID)
	plan.ExpiresAt = organisationTimestampState(plan.ExpiresAt, created.ExpiresAt)
	plan.ProjectID = types.StringNull()

	listed, err := apiClient.GetServiceToken(ctx, created.ID)
	if err != nil {
		response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
		response.Diagnostics.AddError("Unable to read the created service token", err.Error())
		return
	}
	serviceTokenApplyListing(&plan, listed)
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
}

func (token *serviceTokenResource) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	apiClient := token.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state serviceTokenModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}

	listed, err := apiClient.GetServiceToken(ctx, state.ID.ValueString())
	if removeWhenNotFound(ctx, err, apiClient, response) {
		return
	}
	if err != nil {
		response.Diagnostics.AddError("Unable to read service token", err.Error())
		return
	}
	if listed.Revoked {
		response.State.RemoveResource(ctx)
		return
	}
	serviceTokenApplyListing(&state, listed)
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
}

// Update is never reached with a change: every configurable attribute
// forces replacement.
func (token *serviceTokenResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	if token.requireClient(&response.Diagnostics) == nil {
		return
	}
	var plan serviceTokenModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
}

// Delete revokes the token, then deletes it: the platform refuses to delete
// a live token. The service account itself stays.
func (token *serviceTokenResource) Delete(ctx context.Context, request resource.DeleteRequest, response *resource.DeleteResponse) {
	apiClient := token.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state serviceTokenModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	if err := apiClient.RevokeServiceToken(ctx, state.ID.ValueString()); err != nil {
		response.Diagnostics.AddError("Unable to revoke service token", err.Error())
		return
	}
	if err := apiClient.DeleteServiceToken(ctx, state.ID.ValueString()); err != nil {
		response.Diagnostics.AddError("Unable to delete service token", err.Error())
	}
}

// ImportState imports by token id. The token value cannot be recovered, so
// it is null after an import.
func (token *serviceTokenResource) ImportState(ctx context.Context, request resource.ImportStateRequest, response *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), request, response)
}

// serviceTokenApplyListing copies the listing into state. An empty role
// means the account was stood down: it keeps authenticating at the viewer
// floor, and the configured role is kept rather than planned away.
func serviceTokenApplyListing(model *serviceTokenModel, listed *client.OrganisationServiceToken) {
	model.Name = types.StringValue(listed.AccountName)
	if listed.RoleSlug != "" {
		model.RoleSlug = types.StringValue(listed.RoleSlug)
	}
	model.Label = types.StringValue(listed.Label)
	model.AccountID = types.StringValue(listed.AccountID)
	model.ExpiresAt = organisationTimestampState(model.ExpiresAt, listed.ExpiresAt)
	model.Scopes = organisationOptionalSet(model.Scopes, listed.Scopes)
	model.ProjectID = types.StringPointerValue(listed.ProjectID)
}
