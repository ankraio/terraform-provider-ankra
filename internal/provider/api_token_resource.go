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
	_ resource.Resource                = (*apiTokenResource)(nil)
	_ resource.ResourceWithConfigure   = (*apiTokenResource)(nil)
	_ resource.ResourceWithImportState = (*apiTokenResource)(nil)
	_ resource.ResourceWithModifyPlan  = (*apiTokenResource)(nil)
)

type apiTokenResource struct {
	configuredClient
}

type apiTokenModel struct {
	ID             types.String `tfsdk:"id"`
	Name           types.String `tfsdk:"name"`
	ExpiresAt      types.String `tfsdk:"expires_at"`
	Scopes         types.Set    `tfsdk:"scopes"`
	ProjectID      types.String `tfsdk:"project_id"`
	OwnerUserID    types.String `tfsdk:"owner_user_id"`
	OrganisationID types.String `tfsdk:"organisation_id"`
	CreatedAt      types.String `tfsdk:"created_at"`
	Revoked        types.Bool   `tfsdk:"revoked"`
	Token          types.String `tfsdk:"token"`
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
			"Unset keeps the token at the full authority of its holder; an empty set is refused, because the platform reads it as unset.",
		ElementType: types.StringType,
		Optional:    true,
		Validators:  []validator.Set{organisationSetNotEmpty{}},
	}
}

// apiTokenProjectAttribute is the project pin shared by both token resources.
func apiTokenProjectAttribute() schema.StringAttribute {
	return schema.StringAttribute{
		MarkdownDescription: "Project the token is pinned to. Unset makes it organisation-wide; the provider always sends the choice, " +
			"because the platform would otherwise pin an MCP token to whatever project the user last selected in the portal.",
		Optional: true,
	}
}

func (token *apiTokenResource) Schema(_ context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: "A personal API token of the provider token's user, in the current organisation. The counterpart of " +
			"`ankra tokens create`; destroy revokes and then deletes it, like `ankra tokens revoke` followed by `ankra tokens delete`.\n\n" +
			"The token value is revealed once, at creation, and kept in state as a sensitive value. A personal token belongs to " +
			"the user who minted it: if the provider later authenticates as someone else (a CI service token, say) or in another " +
			"organisation, every plan stops with an error instead of losing track of the token and minting a second one." +
			organisationTokenLifecycleNotes,
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Token id. It changes whenever the token value is re-minted.",
				Computed:            true,
			},
			"name": schema.StringAttribute{
				MarkdownDescription: "Token name: letters, digits, spaces, `-`, `_` and `.`, at most 100 characters, unique among the user's tokens.",
				Required:            true,
				Validators:          []validator.String{organisationTokenNameValidator{}},
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
			"owner_user_id": schema.StringAttribute{
				MarkdownDescription: "User the token belongs to: the provider token's user when it was minted.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"organisation_id": schema.StringAttribute{
				MarkdownDescription: "Organisation the token was minted in.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"created_at": schema.StringAttribute{
				MarkdownDescription: "Creation time of the current token value.",
				Computed:            true,
			},
			"revoked": schema.BoolAttribute{
				MarkdownDescription: "Whether the token needs re-minting: it was revoked outside Terraform, or an earlier re-mint was interrupted. The next apply re-mints it in place.",
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

// ModifyPlan keeps the computed values while nothing that mints a new token
// changes, plans an in-place re-mint for a token marked revoked, and plans an
// update while an earlier apply left old tokens to retire.
func (token *apiTokenResource) ModifyPlan(ctx context.Context, request resource.ModifyPlanRequest, response *resource.ModifyPlanResponse) {
	if request.State.Raw.IsNull() || request.Plan.Raw.IsNull() {
		return
	}
	var state, plan apiTokenModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}
	if organisationTokenPlanRemint(ctx, state.Revoked, request, response) {
		response.Diagnostics.Append(response.Plan.SetAttribute(ctx, path.Root("created_at"), types.StringUnknown())...)
		return
	}
	if apiTokenNeedsMint(state, plan) {
		return
	}
	plan.ID, plan.CreatedAt, plan.Revoked, plan.Token = state.ID, state.CreatedAt, state.Revoked, state.Token
	response.Diagnostics.Append(response.Plan.Set(ctx, &plan)...)
	organisationTokenPlanPendingRetire(ctx, request, response)
}

// apiTokenNeedsMint reports whether the plan needs a new token value.
func apiTokenNeedsMint(state, plan apiTokenModel) bool {
	return state.Revoked.ValueBool() || !plan.Name.Equal(state.Name) || !plan.ExpiresAt.Equal(state.ExpiresAt) ||
		!plan.Scopes.Equal(state.Scopes) || !plan.ProjectID.Equal(state.ProjectID)
}

// apiTokenCondition classifies the token in state for a re-mint.
func apiTokenCondition(ctx context.Context, apiClient *client.Client, tokenID string) (organisationTokenCondition, error) {
	listed, err := apiClient.GetAPIToken(ctx, tokenID)
	switch {
	case client.IsNotFound(err):
		return organisationTokenGone, nil
	case err != nil:
		return organisationTokenLive, err
	case organisationTokenIsRenamed(listed.Name):
		return organisationTokenRenamed, nil
	case listed.Revoked:
		return organisationTokenRevoked, nil
	}
	return organisationTokenLive, nil
}

func (token *apiTokenResource) operations(apiClient *client.Client) organisationTokenOperations {
	return organisationTokenOperations{rotate: apiClient.RotateAPIToken, revoke: apiClient.RevokeAPIToken, delete: apiClient.DeleteAPIToken}
}

// mint creates a token from the plan and fills the state fields of the new
// value.
func (token *apiTokenResource) mint(ctx context.Context, apiClient *client.Client, plan *apiTokenModel) (string, error) {
	var scopeDiagnostics diag.Diagnostics
	scopes := organisationStringsFromSet(ctx, plan.Scopes, &scopeDiagnostics)
	if scopeDiagnostics.HasError() {
		return "", fmt.Errorf("reading scopes: %v", scopeDiagnostics)
	}
	created, err := apiClient.CreateAPIToken(ctx, client.OrganisationAPITokenCreate{
		Name:      plan.Name.ValueString(),
		ExpiresAt: stringPointer(plan.ExpiresAt),
		Scopes:    scopes,
		ProjectID: stringPointer(plan.ProjectID),
	})
	if err != nil {
		return "", err
	}
	plan.ID = types.StringValue(created.ID)
	plan.Token = types.StringValue(created.Token)
	plan.ExpiresAt = organisationTimestampState(plan.ExpiresAt, created.ExpiresAt)
	plan.Revoked = types.BoolValue(false)
	plan.CreatedAt = types.StringNull()
	return created.ID, nil
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
	caller, err := apiClient.CurrentCaller(ctx)
	if err != nil {
		response.Diagnostics.AddError("Unable to resolve the token's owner", err.Error())
		return
	}
	if _, err := token.mint(ctx, apiClient, &plan); err != nil {
		response.Diagnostics.AddError("Unable to create API token", err.Error())
		return
	}
	plan.OwnerUserID = types.StringValue(caller.UserID)
	plan.OrganisationID = types.StringValue(caller.OrganisationID)
	token.finish(ctx, apiClient, &plan, &response.State, response.Private, &response.Diagnostics)
}

func (token *apiTokenResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	apiClient := token.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state, plan apiTokenModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}
	operations := token.operations(apiClient)
	lifecycle := organisationReadTokenLifecycle(ctx, request.Private)
	plan.OwnerUserID, plan.OrganisationID = state.OwnerUserID, state.OrganisationID

	if !apiTokenNeedsMint(state, plan) {
		organisationRetirePending(ctx, operations, &lifecycle, &response.Diagnostics)
		plan.ID, plan.CreatedAt, plan.Revoked, plan.Token = state.ID, state.CreatedAt, state.Revoked, state.Token
		response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
		response.Diagnostics.Append(organisationWriteTokenLifecycle(ctx, lifecycle, response.Private)...)
		return
	}

	condition, err := apiTokenCondition(ctx, apiClient, state.ID.ValueString())
	if err != nil {
		response.Diagnostics.AddError("Unable to read the API token before re-minting it", err.Error())
		response.Diagnostics.Append(response.State.Set(ctx, &state)...)
		return
	}
	replaced := organisationReplaceToken(ctx, operations, &lifecycle, state.ID.ValueString(), condition, plan.Name.Equal(state.Name),
		func() (string, error) { return token.mint(ctx, apiClient, &plan) }, &response.Diagnostics)
	if !replaced {
		response.Diagnostics.Append(response.State.Set(ctx, &state)...)
		response.Diagnostics.Append(organisationWriteTokenLifecycle(ctx, lifecycle, response.Private)...)
		return
	}
	organisationRetirePending(ctx, operations, &lifecycle, &response.Diagnostics)
	token.finish(ctx, apiClient, &plan, &response.State, response.Private, &response.Diagnostics)
	response.Diagnostics.Append(organisationWriteTokenLifecycle(ctx, lifecycle, response.Private)...)
}

// finish stores a freshly minted token, completed from the listing.
func (token *apiTokenResource) finish(ctx context.Context, apiClient *client.Client, plan *apiTokenModel, state *tfsdk.State, private privateStateWriter, diagnostics *diag.Diagnostics) {
	listed, err := apiClient.GetAPIToken(ctx, plan.ID.ValueString())
	if err == nil {
		apiTokenApplyListing(plan, listed)
	}
	diagnostics.Append(state.Set(ctx, plan)...)
	diagnostics.Append(trackScope(ctx, apiClient, private)...)
	if err != nil {
		diagnostics.AddWarning("Unable to read the minted API token back", err.Error())
	}
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
	caller, err := apiClient.CurrentCaller(ctx)
	if err != nil {
		response.Diagnostics.AddError("Unable to resolve the provider's identity", err.Error())
		return
	}
	if !state.OwnerUserID.IsNull() && state.OwnerUserID.ValueString() != caller.UserID ||
		!state.OrganisationID.IsNull() && state.OrganisationID.ValueString() != caller.OrganisationID {
		response.Diagnostics.AddError("API token belongs to another identity",
			fmt.Sprintf("Token %s was minted by user %s in organisation %s, but the provider now authenticates as user %s in organisation %s. "+
				"A personal token is only visible to its owner, so Terraform cannot tell whether it still exists. Run this configuration "+
				"with the owner's credentials, or `terraform state rm` the resource.",
				state.ID.ValueString(), state.OwnerUserID.ValueString(), state.OrganisationID.ValueString(), caller.UserID, caller.OrganisationID))
		return
	}

	lifecycle := organisationReadTokenLifecycle(ctx, request.Private)
	listed, err := apiClient.GetAPIToken(ctx, state.ID.ValueString())
	if client.IsNotFound(err) && len(lifecycle.PendingRetire) > 0 {
		// An interrupted re-mint cleared the old token and left others to
		// retire: re-mint in place rather than forget them.
		state.Revoked = types.BoolValue(true)
		response.Diagnostics.Append(response.State.Set(ctx, &state)...)
		return
	}
	if removeWhenNotFound(ctx, err, apiClient, response) {
		return
	}
	if err != nil {
		response.Diagnostics.AddError("Unable to read API token", err.Error())
		return
	}
	if organisationTokenIsRenamed(listed.Name) {
		if lifecycle.Rotating == state.ID.ValueString() {
			// Terraform renewed it to free the name and the mint after that
			// failed: the next apply finishes the re-mint.
			state.Revoked = types.BoolValue(true)
			response.Diagnostics.Append(response.State.Set(ctx, &state)...)
			return
		}
		summary, detail := organisationTokenRenewedError("ankra_api_token", listed.ID, listed.Name)
		response.Diagnostics.AddError(summary, detail)
		return
	}
	state.OwnerUserID = types.StringValue(caller.UserID)
	state.OrganisationID = types.StringValue(caller.OrganisationID)
	apiTokenApplyListing(&state, listed)
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
	response.Diagnostics.Append(trackScope(ctx, apiClient, response.Private)...)
}

// Delete revokes the token, then deletes it: the platform refuses to delete
// a live token. A token already revoked, or revoked by an earlier attempt
// whose delete failed, is deleted without a second revoke.
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
	lifecycle := organisationReadTokenLifecycle(ctx, request.Private)
	organisationRetirePending(ctx, token.operations(apiClient), &lifecycle, &response.Diagnostics)
	if err := apiClient.RevokeAPIToken(ctx, state.ID.ValueString()); err != nil {
		response.Diagnostics.AddError("Unable to revoke API token", err.Error())
		return
	}
	err := apiClient.DeleteAPIToken(ctx, state.ID.ValueString())
	if err == nil || deletedWhenNotFound(ctx, err, apiClient, request.Private, &response.Diagnostics) || response.Diagnostics.HasError() {
		return
	}
	response.Diagnostics.AddError("Unable to delete API token", err.Error())
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
	model.Revoked = types.BoolValue(listed.Revoked)
}
