// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"ankra.io/terraform-provider-ankra/internal/client"
)

var (
	_ resource.Resource                = (*serviceTokenResource)(nil)
	_ resource.ResourceWithConfigure   = (*serviceTokenResource)(nil)
	_ resource.ResourceWithImportState = (*serviceTokenResource)(nil)
	_ resource.ResourceWithModifyPlan  = (*serviceTokenResource)(nil)
)

type serviceTokenResource struct {
	configuredClient
}

type serviceTokenModel struct {
	ID             types.String `tfsdk:"id"`
	Name           types.String `tfsdk:"name"`
	RoleSlug       types.String `tfsdk:"role_slug"`
	Label          types.String `tfsdk:"label"`
	ExpiresAt      types.String `tfsdk:"expires_at"`
	Scopes         types.Set    `tfsdk:"scopes"`
	ProjectID      types.String `tfsdk:"project_id"`
	AccountID      types.String `tfsdk:"account_id"`
	OrganisationID types.String `tfsdk:"organisation_id"`
	Revoked        types.Bool   `tfsdk:"revoked"`
	Token          types.String `tfsdk:"token"`
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
			"`role_slug` must be the role it already holds. The provider never changes an account's role: a change of `role_slug`, " +
			"or a role changed in the portal, stops the plan with an error until configuration and portal agree.\n\n" +
			"**Account stand-down.** When an account's last token is deleted, the platform stands the account down and retires its " +
			"role assignments. Changing `name` (the account) therefore replaces the token on a new account and, if it was the old " +
			"account's last token, stands the old account down; destroy does the same. Every other change keeps a token on the " +
			"account throughout." + organisationTokenLifecycleNotes,
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Token id. It changes whenever the token value is re-minted.",
				Computed:            true,
			},
			"name": schema.StringAttribute{
				MarkdownDescription: "Service account name. Changing it replaces the token on another account (destroy before create, so there is a window without a working token).",
				Required:            true,
				Validators:          []validator.String{organisationTokenNameValidator{}},
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"role_slug": schema.StringAttribute{
				MarkdownDescription: "Role the service account holds, such as `member` or a custom role's slug. It may not carry permissions the caller lacks, and the provider never changes it.",
				Required:            true,
			},
			"label": schema.StringAttribute{
				MarkdownDescription: "Name of this token among the account's tokens. Defaults to a timestamp of when it was minted; an empty label is refused, because the platform would substitute a timestamp and every plan would try to change it back.",
				Optional:            true,
				Computed:            true,
				Validators:          []validator.String{organisationTokenNameValidator{}},
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"expires_at": schema.StringAttribute{
				MarkdownDescription: "Expiry as an RFC 3339 timestamp. Defaults to 90 days after creation.",
				Optional:            true,
				Computed:            true,
				Validators:          []validator.String{organisationTimestampValidator{}},
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"scopes":     apiTokenScopeAttribute(),
			"project_id": apiTokenProjectAttribute(),
			"account_id": schema.StringAttribute{
				MarkdownDescription: "Service account id.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"organisation_id": schema.StringAttribute{
				MarkdownDescription: "Organisation the token was minted in.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"revoked": schema.BoolAttribute{
				MarkdownDescription: "Whether the token was revoked outside Terraform; a revoked token is replaced on the next apply.",
				Computed:            true,
			},
			"token": schema.StringAttribute{
				MarkdownDescription: "The token value. Revealed only when minted; null after an import.",
				Computed:            true,
				Sensitive:           true,
			},
		},
	}
}

// ModifyPlan refuses a role change, plans a revoked token for replacement,
// and keeps the computed values while nothing that mints a new token
// changes.
func (token *serviceTokenResource) ModifyPlan(ctx context.Context, request resource.ModifyPlanRequest, response *resource.ModifyPlanResponse) {
	if request.State.Raw.IsNull() || request.Plan.Raw.IsNull() {
		return
	}
	var state, plan serviceTokenModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}
	if !plan.RoleSlug.IsUnknown() && !plan.RoleSlug.Equal(state.RoleSlug) {
		response.Diagnostics.AddAttributeError(path.Root("role_slug"), "Service account role cannot change here",
			fmt.Sprintf("Service account %q holds the %s role, and configuration says %s. Minting never changes an account's role, and "+
				"re-creating the account would retire its assignments. Change the role in the portal (members), then set role_slug to match.",
				state.Name.ValueString(), state.RoleSlug.ValueString(), plan.RoleSlug.ValueString()))
		return
	}
	if state.Revoked.ValueBool() {
		organisationTokenRevokedReplacement(ctx, state.Revoked, plan.Revoked, response)
		return
	}
	if plan.Name.Equal(state.Name) && plan.Label.Equal(state.Label) && plan.ExpiresAt.Equal(state.ExpiresAt) &&
		plan.Scopes.Equal(state.Scopes) && plan.ProjectID.Equal(state.ProjectID) {
		plan.ID, plan.Revoked, plan.Token = state.ID, state.Revoked, state.Token
		response.Diagnostics.Append(response.Plan.Set(ctx, &plan)...)
	}
}

func (token *serviceTokenResource) operations(apiClient *client.Client) organisationTokenOperations {
	return organisationTokenOperations{rotate: apiClient.RotateServiceToken, revoke: apiClient.RevokeServiceToken, delete: apiClient.DeleteServiceToken}
}

func (token *serviceTokenResource) mint(ctx context.Context, apiClient *client.Client, plan *serviceTokenModel) (string, error) {
	var scopeDiagnostics diag.Diagnostics
	scopes := organisationStringsFromSet(ctx, plan.Scopes, &scopeDiagnostics)
	if scopeDiagnostics.HasError() {
		return "", fmt.Errorf("reading scopes: %v", scopeDiagnostics)
	}
	label := ""
	if !plan.Label.IsUnknown() {
		label = plan.Label.ValueString()
	}
	created, err := apiClient.CreateServiceToken(ctx, client.OrganisationServiceTokenCreate{
		Name:      plan.Name.ValueString(),
		Label:     label,
		RoleSlug:  plan.RoleSlug.ValueString(),
		ExpiresAt: stringPointer(plan.ExpiresAt),
		Scopes:    scopes,
		ProjectID: stringPointer(plan.ProjectID),
	})
	if err != nil {
		return "", err
	}
	plan.ID = types.StringValue(created.ID)
	plan.Token = types.StringValue(created.Token)
	plan.Label = types.StringValue(created.Label)
	plan.AccountID = types.StringValue(created.AccountID)
	plan.ExpiresAt = organisationTimestampState(plan.ExpiresAt, created.ExpiresAt)
	plan.Revoked = types.BoolValue(false)
	return created.ID, nil
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
	organisationID, err := apiClient.CurrentOrganisationID(ctx)
	if err != nil {
		response.Diagnostics.AddError("Unable to resolve the organisation", err.Error())
		return
	}
	if _, err := token.mint(ctx, apiClient, &plan); err != nil {
		response.Diagnostics.AddError("Unable to create service token", err.Error())
		return
	}
	plan.OrganisationID = types.StringValue(organisationID)
	token.finish(ctx, apiClient, &plan, &response.State, response.Private, &response.Diagnostics)
}

// Update re-mints the token on the same account without letting the account
// run out of tokens, so it is never stood down.
func (token *serviceTokenResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	apiClient := token.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state, plan serviceTokenModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}
	if plan.Label.IsUnknown() {
		plan.Label = state.Label
	}
	replaced := organisationReplaceToken(ctx, token.operations(apiClient), state.ID.ValueString(), plan.Label.Equal(state.Label),
		func() (string, error) { return token.mint(ctx, apiClient, &plan) }, &response.Diagnostics)
	if !replaced {
		return
	}
	plan.OrganisationID = state.OrganisationID
	token.finish(ctx, apiClient, &plan, &response.State, response.Private, &response.Diagnostics)
}

func (token *serviceTokenResource) finish(ctx context.Context, apiClient *client.Client, plan *serviceTokenModel, state *tfsdk.State, private privateStateWriter, diagnostics *diag.Diagnostics) {
	if plan.ProjectID.IsUnknown() {
		plan.ProjectID = types.StringNull()
	}
	listed, err := apiClient.GetServiceToken(ctx, plan.ID.ValueString())
	if err == nil {
		serviceTokenApplyListing(plan, listed)
	}
	diagnostics.Append(state.Set(ctx, plan)...)
	diagnostics.Append(trackScope(ctx, apiClient, private)...)
	if err != nil {
		diagnostics.AddWarning("Unable to read the minted service token back", err.Error())
	}
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
	organisationID, err := apiClient.CurrentOrganisationID(ctx)
	if err != nil {
		response.Diagnostics.AddError("Unable to resolve the organisation", err.Error())
		return
	}
	if !state.OrganisationID.IsNull() && state.OrganisationID.ValueString() != organisationID {
		response.Diagnostics.AddError("Service token belongs to another organisation",
			fmt.Sprintf("Token %s was minted in organisation %s, but the provider now acts in %s. Point the provider back at that "+
				"organisation, or `terraform state rm` the resource.", state.ID.ValueString(), state.OrganisationID.ValueString(), organisationID))
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
	if organisationTokenIsRenamed(listed.Label) {
		summary, detail := organisationTokenRenewedError("ankra_service_token", listed.ID, listed.Label)
		response.Diagnostics.AddError(summary, detail)
		return
	}
	state.OrganisationID = types.StringValue(organisationID)
	serviceTokenApplyListing(&state, listed)
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
	response.Diagnostics.Append(trackScope(ctx, apiClient, response.Private)...)
}

// Delete revokes the token, then deletes it. Deleting the account's last
// token stands the account down. A token already revoked is deleted without
// a second revoke.
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
	err := apiClient.RevokeServiceToken(ctx, state.ID.ValueString())
	if err == nil {
		err = apiClient.DeleteServiceToken(ctx, state.ID.ValueString())
	}
	if err == nil || deletedWhenNotFound(ctx, err, apiClient, request.Private, &response.Diagnostics) || response.Diagnostics.HasError() {
		return
	}
	response.Diagnostics.AddError("Unable to delete service token", err.Error())
}

// ImportState imports by token id. The token value cannot be recovered, so
// it is null after an import.
func (token *serviceTokenResource) ImportState(ctx context.Context, request resource.ImportStateRequest, response *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), request, response)
}

// serviceTokenApplyListing copies the listing into state. An empty role
// means the account was stood down; the recorded role is kept, since no
// configuration value could match an absent role.
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
	model.Revoked = types.BoolValue(listed.Revoked)
}
