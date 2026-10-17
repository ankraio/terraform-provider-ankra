// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"ankra.io/terraform-provider-ankra/internal/client"
)

const aiBoardIdentityID = "ai_board_identity"

var (
	_ resource.Resource                = (*aiBoardIdentityResource)(nil)
	_ resource.ResourceWithConfigure   = (*aiBoardIdentityResource)(nil)
	_ resource.ResourceWithImportState = (*aiBoardIdentityResource)(nil)
)

type aiBoardIdentityResource struct {
	configuredClient
}

type aiBoardIdentityModel struct {
	ID          types.String `tfsdk:"id"`
	RoleSlug    types.String `tfsdk:"role_slug"`
	Subject     types.String `tfsdk:"subject"`
	AnkraUserID types.String `tfsdk:"ankra_user_id"`
}

// NewAIBoardIdentityResource returns the ankra_ai_board_identity resource,
// the counterpart of `ankra ai board-identity provision|revoke`.
func NewAIBoardIdentityResource() resource.Resource {
	return &aiBoardIdentityResource{}
}

func (identityResource *aiBoardIdentityResource) Metadata(_ context.Context, request resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_ai_board_identity"
}

func (identityResource *aiBoardIdentityResource) Schema(_ context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: "The identity the AI board's platform-created agents act as, a singleton. " +
			"The counterpart of `ankra ai board-identity provision` and `ankra ai board-identity revoke`. " +
			"Without it, designated board workers escalate every ticket to a human.\n\n" +
			"The platform provisions an `operator` identity for most organisations by itself; import it " +
			"(`terraform import ankra_ai_board_identity.board ai_board_identity`) rather than creating one. " +
			"Provisioning never changes the role of an identity that already exists, so changing `role_slug` stands the identity down " +
			"and provisions it again. Destroying it stands the identity down: the principal is kept for attribution and loses every grant above viewer.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Always `" + aiBoardIdentityID + "`.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"role_slug": schema.StringAttribute{
				MarkdownDescription: "Built-in role the identity carries: `operator`, `member` or `viewer`. Defaults to `operator`. Changing it replaces the identity's grant.",
				Optional:            true,
				Computed:            true,
				Default:             stringdefault.StaticString("operator"),
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
				Validators:          []validator.String{aiStringOneOf{allowed: []string{"operator", "member", "viewer"}}},
			},
			"subject": schema.StringAttribute{
				MarkdownDescription: "Subject of the service principal.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"ankra_user_id": schema.StringAttribute{
				MarkdownDescription: "Ankra user id of the service principal.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
		},
	}
}

func (identityResource *aiBoardIdentityResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	apiClient := identityResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var plan aiBoardIdentityModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}
	identity, err := apiClient.ProvisionAIBoardIdentity(ctx, plan.RoleSlug.ValueString())
	if err != nil {
		response.Diagnostics.AddError("Unable to provision AI board identity", err.Error())
		return
	}
	if identity.RoleSlug != plan.RoleSlug.ValueString() {
		response.Diagnostics.AddError(
			"AI board identity already exists with another role",
			fmt.Sprintf("The organisation's board identity already exists with role %q, and provisioning never changes the role of an existing identity. "+
				"Set role_slug = %q, or import the identity and then change role_slug.", identity.RoleSlug, identity.RoleSlug),
		)
		return
	}
	plan.applyAPI(identity)
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
	response.Diagnostics.Append(trackScope(ctx, apiClient, response.Private)...)
}

func (identityResource *aiBoardIdentityResource) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	apiClient := identityResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state aiBoardIdentityModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	identity, err := apiClient.GetAIBoardIdentity(ctx)
	if err != nil {
		response.Diagnostics.AddError("Unable to read AI board identity", err.Error())
		return
	}
	if !identity.Provisioned {
		response.State.RemoveResource(ctx)
		return
	}
	state.applyAPI(identity)
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
	response.Diagnostics.Append(trackScope(ctx, apiClient, response.Private)...)
}

// Update is never called with a change: role_slug forces replacement and the
// other attributes are computed.
func (identityResource *aiBoardIdentityResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	var plan aiBoardIdentityModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
}

func (identityResource *aiBoardIdentityResource) Delete(ctx context.Context, _ resource.DeleteRequest, response *resource.DeleteResponse) {
	apiClient := identityResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	if err := apiClient.RevokeAIBoardIdentity(ctx); err != nil {
		response.Diagnostics.AddError("Unable to stand the AI board identity down", err.Error())
	}
}

// ImportState imports the singleton; any id is accepted and replaced.
func (identityResource *aiBoardIdentityResource) ImportState(ctx context.Context, _ resource.ImportStateRequest, response *resource.ImportStateResponse) {
	response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("id"), aiBoardIdentityID)...)
}

func (model *aiBoardIdentityModel) applyAPI(identity *client.AIBoardIdentity) {
	model.ID = types.StringValue(aiBoardIdentityID)
	model.RoleSlug = types.StringValue(identity.RoleSlug)
	model.Subject = types.StringValue(identity.Subject)
	model.AnkraUserID = types.StringPointerValue(identity.AnkraUserID)
}
