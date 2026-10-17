// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"
	"strings"

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
	_ resource.Resource                = (*organisationMemberResource)(nil)
	_ resource.ResourceWithConfigure   = (*organisationMemberResource)(nil)
	_ resource.ResourceWithImportState = (*organisationMemberResource)(nil)
	_ resource.ResourceWithModifyPlan  = (*organisationMemberResource)(nil)
)

// organisationMemberRoles are the roles the invite endpoint accepts.
var organisationMemberRoles = []string{"member", "admin", "read-only"}

type organisationMemberResource struct {
	configuredClient
}

type organisationMemberModel struct {
	ID             types.String `tfsdk:"id"`
	OrganisationID types.String `tfsdk:"organisation_id"`
	Email          types.String `tfsdk:"email"`
	Role           types.String `tfsdk:"role"`
	Status         types.String `tfsdk:"status"`
	InviteID       types.String `tfsdk:"invite_id"`
}

// NewOrganisationMemberResource returns the ankra_organisation_member
// resource, the counterpart of `ankra org invite` and `ankra org remove`.
func NewOrganisationMemberResource() resource.Resource {
	return &organisationMemberResource{}
}

func (member *organisationMemberResource) Metadata(_ context.Context, request resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_organisation_member"
}

func (member *organisationMemberResource) Schema(_ context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: "A member of an organisation, added by invitation. The counterpart of `ankra org invite` and `ankra org remove`.\n\n" +
			"API tokens have no role change, and removing and re-inviting a member would retire their role assignments, delete " +
			"their GitHub credentials and leave them pending, so a change of `role` (in configuration, or made in the portal) " +
			"stops the plan with an error: change the role in the portal and set `role` to match. A plan that would remove or " +
			"replace the provider's own membership is refused too. Destroy removes the member, which retires their role " +
			"assignments.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "The member's user id.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"organisation_id": schema.StringAttribute{
				MarkdownDescription: "Organisation to invite into. Defaults to the provider's `organisation_id`, or the token's current organisation.",
				Optional:            true,
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown(), stringplanmodifier.RequiresReplace()},
			},
			"email": schema.StringAttribute{
				MarkdownDescription: "Email address invited. The member's account may carry a different address (a relay address, for example), so it is never read back, and a change of letter case is not a change. Changing it invites someone else.",
				Required:            true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplaceIf(organisationMemberEmailChanged,
					"Inviting a different address replaces the member.", "Inviting a different address replaces the member.")},
			},
			"role": schema.StringAttribute{
				MarkdownDescription: "Role at invitation: `member`, `admin` or `read-only`. It cannot be changed here; change it in the portal and set this to match.",
				Required:            true,
				Validators:          []validator.String{organisationStringOneOf{values: organisationMemberRoles}},
			},
			"status": schema.StringAttribute{
				MarkdownDescription: "Membership status, `pending` until the invite is accepted, then `active`.",
				Computed:            true,
			},
			"invite_id": schema.StringAttribute{
				MarkdownDescription: "Identifier of the membership row the invite created.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
		},
	}
}

func (member *organisationMemberResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	apiClient := member.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var plan organisationMemberModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}

	organisationID := plan.OrganisationID.ValueString()
	if plan.OrganisationID.IsNull() || plan.OrganisationID.IsUnknown() {
		currentID, err := apiClient.CurrentOrganisationID(ctx)
		if err != nil {
			response.Diagnostics.AddError("Unable to resolve the organisation", err.Error())
			return
		}
		organisationID = currentID
	}

	invited, err := apiClient.InviteOrganisationMember(ctx, client.OrganisationInvite{
		OrganisationID: organisationID,
		InviteeEmail:   plan.Email.ValueString(),
		Role:           plan.Role.ValueString(),
	})
	if err != nil {
		response.Diagnostics.AddError("Unable to invite organisation member", err.Error())
		return
	}
	row := invited.PendingUser
	if row == nil || row.ID == nil {
		detail, err := apiClient.GetOrganisation(ctx, organisationID)
		if err != nil {
			response.Diagnostics.AddError("Unable to read the invited member", err.Error())
			return
		}
		row = organisationMemberByEmail(detail.Users, plan.Email.ValueString())
		if row == nil || row.ID == nil {
			response.Diagnostics.AddError("Invited member not found",
				fmt.Sprintf("The platform accepted the invite of %s but its roster does not list them yet.", plan.Email.ValueString()))
			return
		}
	}

	plan.ID = types.StringPointerValue(row.ID)
	plan.OrganisationID = types.StringValue(organisationID)
	plan.Status = types.StringValue(organisationMemberStatus(row))
	plan.InviteID = types.StringPointerValue(row.InviteID)
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
	response.Diagnostics.Append(trackScope(ctx, apiClient, response.Private)...)
}

func (member *organisationMemberResource) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	apiClient := member.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state organisationMemberModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}

	detail, err := apiClient.GetOrganisation(ctx, state.OrganisationID.ValueString())
	if removeWhenNotFound(ctx, err, apiClient, response) {
		return
	}
	if err != nil {
		response.Diagnostics.AddError("Unable to read organisation members", err.Error())
		return
	}
	var row *client.OrganisationMember
	for index := range detail.Users {
		if detail.Users[index].ID != nil && *detail.Users[index].ID == state.ID.ValueString() {
			row = &detail.Users[index]
			break
		}
	}
	if row == nil {
		response.State.RemoveResource(ctx)
		return
	}

	if row.Role != nil {
		state.Role = types.StringValue(*row.Role)
	}
	state.Status = types.StringValue(organisationMemberStatus(row))
	state.InviteID = types.StringPointerValue(row.InviteID)
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
	response.Diagnostics.Append(trackScope(ctx, apiClient, response.Private)...)
}

// ModifyPlan refuses what the API cannot do safely: a role change, which
// would mean removing and re-inviting the member, and removing or replacing
// the provider's own membership.
func (member *organisationMemberResource) ModifyPlan(ctx context.Context, request resource.ModifyPlanRequest, response *resource.ModifyPlanResponse) {
	if request.State.Raw.IsNull() {
		return
	}
	var state organisationMemberModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	isRemoval := request.Plan.Raw.IsNull()
	if !isRemoval {
		var plan organisationMemberModel
		response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
		if response.Diagnostics.HasError() {
			return
		}
		if !plan.Role.IsUnknown() && !plan.Role.Equal(state.Role) {
			response.Diagnostics.AddAttributeError(path.Root("role"), "Member role cannot change here",
				fmt.Sprintf("Member %s holds the %s role, and configuration says %s. API tokens cannot change a role, and removing and "+
					"re-inviting the member would retire their role assignments and delete their GitHub credentials. Change the role in "+
					"the portal, then set role to match.", state.ID.ValueString(), state.Role.ValueString(), plan.Role.ValueString()))
			return
		}
		isRemoval = organisationMemberEmailReplaced(state.Email, plan.Email)
	}
	if !isRemoval {
		return
	}
	apiClient := member.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	caller, err := apiClient.CurrentCaller(ctx)
	if err != nil {
		response.Diagnostics.AddError("Unable to resolve the provider's identity", err.Error())
		return
	}
	if caller.UserID == state.ID.ValueString() {
		response.Diagnostics.AddError("Refusing to remove the provider's own membership",
			fmt.Sprintf("This plan would remove user %s from organisation %s, and that is the user the provider authenticates as: "+
				"every later request would fail. Remove it from Terraform with `terraform state rm` instead.",
				state.ID.ValueString(), state.OrganisationID.ValueString()))
	}
}

// Update stores the plan: role changes are refused at plan time, so only an
// email spelling change, or an imported member's first email, reaches it.
func (member *organisationMemberResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	if member.requireClient(&response.Diagnostics) == nil {
		return
	}
	var plan organisationMemberModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
}

func (member *organisationMemberResource) Delete(ctx context.Context, request resource.DeleteRequest, response *resource.DeleteResponse) {
	apiClient := member.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state organisationMemberModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	err := apiClient.RemoveOrganisationMember(ctx, state.OrganisationID.ValueString(), state.ID.ValueString())
	if err == nil || deletedWhenNotFound(ctx, err, apiClient, request.Private, &response.Diagnostics) || response.Diagnostics.HasError() {
		return
	}
	response.Diagnostics.AddError("Unable to remove organisation member", err.Error())
}

// ImportState imports by "<organisation_id>/<user_id>". email is not read
// from the roster, which may hold a relay address: the first apply records
// the configured one without replacing the member.
func (member *organisationMemberResource) ImportState(ctx context.Context, request resource.ImportStateRequest, response *resource.ImportStateResponse) {
	parts, err := organisationSplitImportID(request.ID, 2, "<organisation_id>/<user_id>")
	if err != nil {
		response.Diagnostics.AddError("Invalid import id", err.Error())
		return
	}
	response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("organisation_id"), parts[0])...)
	response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("id"), parts[1])...)
}

func organisationMemberByEmail(members []client.OrganisationMember, email string) *client.OrganisationMember {
	for index := range members {
		if members[index].Email != nil && strings.EqualFold(*members[index].Email, email) {
			return &members[index]
		}
	}
	return nil
}

// organisationMemberEmailReplaced reports whether a planned email names a
// different address: an imported member has no recorded email, and a change
// of letter case is the same address.
func organisationMemberEmailReplaced(state, plan types.String) bool {
	if state.IsNull() || state.IsUnknown() || plan.IsUnknown() {
		return false
	}
	return !strings.EqualFold(state.ValueString(), plan.ValueString())
}

func organisationMemberEmailChanged(_ context.Context, request planmodifier.StringRequest, response *stringplanmodifier.RequiresReplaceIfFuncResponse) {
	response.RequiresReplace = organisationMemberEmailReplaced(request.StateValue, request.PlanValue)
}

func organisationMemberStatus(row *client.OrganisationMember) string {
	if row.Status == nil {
		return ""
	}
	return *row.Status
}
