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
			"The API offers API tokens no role change, so changing `role` removes the member and invites them again; " +
			"an active member is then pending until they accept.",
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
				MarkdownDescription: "Email address invited. The member's account may carry a different address (a relay address, for example), so this is not read back.",
				Required:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"role": schema.StringAttribute{
				MarkdownDescription: "Role: `member`, `admin` or `read-only`. Changing it re-invites the member.",
				Required:            true,
				Validators:          []validator.String{organisationStringOneOf{values: organisationMemberRoles}},
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
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
	if removeWhenNotFound(ctx, err, response) {
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
	if state.Email.IsNull() && row.Email != nil {
		state.Email = types.StringValue(*row.Email)
	}
	state.Status = types.StringValue(organisationMemberStatus(row))
	state.InviteID = types.StringPointerValue(row.InviteID)
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
}

// Update is never reached with a change: every configurable attribute
// forces replacement.
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
	if err := apiClient.RemoveOrganisationMember(ctx, state.OrganisationID.ValueString(), state.ID.ValueString()); err != nil {
		response.Diagnostics.AddError("Unable to remove organisation member", err.Error())
	}
}

// ImportState imports by "<organisation_id>/<user_id>". email is filled
// from the roster.
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

func organisationMemberStatus(row *client.OrganisationMember) string {
	if row.Status == nil {
		return ""
	}
	return *row.Status
}
