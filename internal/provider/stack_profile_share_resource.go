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
	"github.com/hashicorp/terraform-plugin-framework/types"

	"ankra.io/terraform-provider-ankra/internal/client"
)

var (
	_ resource.Resource                = (*stackProfileShareResource)(nil)
	_ resource.ResourceWithConfigure   = (*stackProfileShareResource)(nil)
	_ resource.ResourceWithImportState = (*stackProfileShareResource)(nil)
)

type stackProfileShareResource struct {
	configuredClient
}

type stackProfileShareModel struct {
	ID               types.String `tfsdk:"id"`
	ProfileID        types.String `tfsdk:"profile_id"`
	OrganisationSlug types.String `tfsdk:"organisation_slug"`
	OrganisationID   types.String `tfsdk:"organisation_id"`
	OrganisationName types.String `tfsdk:"organisation_name"`
}

// NewStackProfileShareResource returns the ankra_stack_profile_share
// resource, the counterpart of `ankra stack-profiles share add|remove`.
func NewStackProfileShareResource() resource.Resource {
	return &stackProfileShareResource{}
}

func (shareResource *stackProfileShareResource) Metadata(_ context.Context, request resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_stack_profile_share"
}

func (shareResource *stackProfileShareResource) Schema(_ context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	replaceString := []planmodifier.String{stringplanmodifier.RequiresReplace()}
	computedString := []planmodifier.String{stringplanmodifier.UseStateForUnknown()}
	response.Schema = schema.Schema{
		MarkdownDescription: "Shares a stack profile with another organisation, which can then see and deploy every version of " +
			"it. The counterpart of `ankra stack-profiles share add` and `ankra stack-profiles share remove`. The platform " +
			"re-scans every version for secrets before sharing, and a profile takes a limited number of grants. A grant whose " +
			"organisation was deleted is treated as gone.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Identifier of the share grant.",
				Computed:            true,
				PlanModifiers:       computedString,
			},
			"profile_id": schema.StringAttribute{
				MarkdownDescription: "Profile to share.",
				Required:            true,
				PlanModifiers:       replaceString,
			},
			"organisation_slug": schema.StringAttribute{
				MarkdownDescription: "Slug of the organisation to share with. The platform matches it case-insensitively; " +
					"state keeps the configured spelling.",
				Required:      true,
				PlanModifiers: replaceString,
			},
			"organisation_id": schema.StringAttribute{
				MarkdownDescription: "Identifier of that organisation.",
				Computed:            true,
				PlanModifiers:       computedString,
			},
			"organisation_name": schema.StringAttribute{
				MarkdownDescription: "Name of that organisation.",
				Computed:            true,
			},
		},
	}
}

func (shareResource *stackProfileShareResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	apiClient := shareResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var plan stackProfileShareModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}

	share, err := apiClient.CreateStackProfileShare(ctx, plan.ProfileID.ValueString(), plan.OrganisationSlug.ValueString())
	if err != nil {
		response.Diagnostics.AddError("Unable to share stack profile", err.Error())
		return
	}
	stackProfileShareToState(share, &plan)
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
}

func (shareResource *stackProfileShareResource) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	apiClient := shareResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state stackProfileShareModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}

	shares, err := apiClient.ListStackProfileShares(ctx, state.ProfileID.ValueString())
	if removeWhenNotFound(ctx, err, response) {
		return
	}
	if err != nil {
		response.Diagnostics.AddError("Unable to read stack profile shares", err.Error())
		return
	}
	share := stackProfileShareFind(shares, state)
	if share == nil {
		response.State.RemoveResource(ctx)
		return
	}
	stackProfileShareToState(share, &state)
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
}

// Update is never called with a change: every configurable attribute
// replaces the grant.
func (shareResource *stackProfileShareResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	var plan stackProfileShareModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
}

func (shareResource *stackProfileShareResource) Delete(ctx context.Context, request resource.DeleteRequest, response *resource.DeleteResponse) {
	apiClient := shareResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state stackProfileShareModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	if err := apiClient.DeleteStackProfileShare(ctx, state.ProfileID.ValueString(), state.ID.ValueString()); err != nil {
		response.Diagnostics.AddError("Unable to revoke stack profile share", err.Error())
	}
}

// ImportState imports by "<profile_id>/<organisation_slug>".
func (shareResource *stackProfileShareResource) ImportState(ctx context.Context, request resource.ImportStateRequest, response *resource.ImportStateResponse) {
	profileID, organisationSlug, found := strings.Cut(request.ID, "/")
	if !found || profileID == "" || organisationSlug == "" || strings.Contains(organisationSlug, "/") {
		response.Diagnostics.AddError("Invalid import ID",
			fmt.Sprintf("Expected \"<profile_id>/<organisation_slug>\", got %q.", request.ID))
		return
	}
	response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("profile_id"), profileID)...)
	response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("organisation_slug"), organisationSlug)...)
}

// stackProfileShareFind finds the grant in state by its ID, or by slug right
// after an import, when the ID is not known yet. A grant whose organisation
// was deleted (no slug any more) counts as gone.
func stackProfileShareFind(shares []client.StackProfileShare, state stackProfileShareModel) *client.StackProfileShare {
	for index := range shares {
		share := &shares[index]
		if share.OrganisationSlug == nil {
			continue
		}
		if state.ID.IsNull() || state.ID.IsUnknown() {
			if strings.EqualFold(*share.OrganisationSlug, state.OrganisationSlug.ValueString()) {
				return share
			}
			continue
		}
		if share.ID == state.ID.ValueString() {
			return share
		}
	}
	return nil
}

func stackProfileShareToState(share *client.StackProfileShare, state *stackProfileShareModel) {
	state.ID = types.StringValue(share.ID)
	if share.OrganisationSlug != nil && !strings.EqualFold(*share.OrganisationSlug, state.OrganisationSlug.ValueString()) {
		state.OrganisationSlug = types.StringValue(*share.OrganisationSlug)
	}
	state.OrganisationID = types.StringValue(share.OrganisationID)
	state.OrganisationName = types.StringPointerValue(share.OrganisationName)
}
