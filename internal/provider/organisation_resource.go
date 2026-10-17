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
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"ankra.io/terraform-provider-ankra/internal/client"
)

var (
	_ resource.Resource                = (*organisationResource)(nil)
	_ resource.ResourceWithConfigure   = (*organisationResource)(nil)
	_ resource.ResourceWithImportState = (*organisationResource)(nil)
	_ resource.ResourceWithModifyPlan  = (*organisationResource)(nil)
)

type organisationResource struct {
	configuredClient
}

type organisationResourceModel struct {
	ID      types.String `tfsdk:"id"`
	Name    types.String `tfsdk:"name"`
	Country types.String `tfsdk:"country"`
	Slug    types.String `tfsdk:"slug"`
}

// NewOrganisationResource returns the ankra_organisation resource, the
// counterpart of `ankra org create`.
func NewOrganisationResource() resource.Resource {
	return &organisationResource{}
}

func (organisation *organisationResource) Metadata(_ context.Context, request resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_organisation"
}

func (organisation *organisationResource) Schema(_ context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: "An Ankra organisation, created with the token's user as its admin. The counterpart of `ankra org create`.\n\n" +
			"~> **Destroy does not delete the organisation.** The API offers no organisation delete or rename to API tokens, so " +
			"`terraform destroy` only removes the organisation from state and warns; delete it in the portal. For the same reason " +
			"changing `name` or `country` is refused at plan time rather than planned as a replacement, which would create a " +
			"second organisation and leave the first in place.\n\n" +
			"The platform also makes a newly created organisation its creator's current organisation in the portal.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Organisation id.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				MarkdownDescription: "Organisation name. It cannot be changed through the API.",
				Required:            true,
			},
			"country": schema.StringAttribute{
				MarkdownDescription: "Country code recorded at creation. The platform never returns it, so it is not read back, and it cannot be changed through the API.",
				Optional:            true,
			},
			"slug": schema.StringAttribute{
				MarkdownDescription: "URL slug the platform derived from the name.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
		},
	}
}

// ModifyPlan refuses a change to name or country on an existing
// organisation: the API cannot apply it, and replacing would duplicate the
// organisation.
func (organisation *organisationResource) ModifyPlan(ctx context.Context, request resource.ModifyPlanRequest, response *resource.ModifyPlanResponse) {
	if request.State.Raw.IsNull() || request.Plan.Raw.IsNull() {
		return
	}
	var state, plan organisationResourceModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}
	if !plan.Name.IsUnknown() && !plan.Name.Equal(state.Name) {
		response.Diagnostics.AddAttributeError(path.Root("name"), "Organisation cannot be renamed",
			fmt.Sprintf("The API cannot rename organisation %s from %q to %q. Rename it in the portal, then set name to match.",
				state.ID.ValueString(), state.Name.ValueString(), plan.Name.ValueString()))
	}
	if !state.Country.IsNull() && !plan.Country.IsUnknown() && !plan.Country.Equal(state.Country) {
		response.Diagnostics.AddAttributeError(path.Root("country"), "Organisation country cannot be changed",
			"The API records the country only when the organisation is created.")
	}
}

func (organisation *organisationResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	apiClient := organisation.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var plan organisationResourceModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}

	created, err := apiClient.CreateOrganisation(ctx, client.OrganisationCreate{
		Name:    plan.Name.ValueString(),
		Country: stringPointer(plan.Country),
	})
	if err != nil {
		response.Diagnostics.AddError("Unable to create organisation", err.Error())
		return
	}

	plan.ID = types.StringValue(created.OrganisationID)
	plan.Slug = types.StringValue(created.Slug)
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
	response.Diagnostics.Append(trackScope(ctx, apiClient, response.Private)...)
}

func (organisation *organisationResource) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	apiClient := organisation.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state organisationResourceModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}

	detail, err := apiClient.GetOrganisation(ctx, state.ID.ValueString())
	if removeWhenNotFound(ctx, err, apiClient, response) {
		return
	}
	if err != nil {
		response.Diagnostics.AddError("Unable to read organisation", err.Error())
		return
	}

	state.Name = types.StringPointerValue(detail.Name)
	state.Slug = types.StringPointerValue(detail.Slug)
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
	response.Diagnostics.Append(trackScope(ctx, apiClient, response.Private)...)
}

// Update is never reached with a change: ModifyPlan refuses every change to
// a configurable attribute. It stores the plan so a no-op apply converges.
func (organisation *organisationResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	if organisation.requireClient(&response.Diagnostics) == nil {
		return
	}
	var plan organisationResourceModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
}

// Delete forgets the organisation: the API offers API tokens no
// organisation delete.
func (organisation *organisationResource) Delete(ctx context.Context, request resource.DeleteRequest, response *resource.DeleteResponse) {
	if organisation.requireClient(&response.Diagnostics) == nil {
		return
	}
	var state organisationResourceModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	response.Diagnostics.AddWarning("Organisation not deleted",
		fmt.Sprintf("Organisation %s (%s) was removed from Terraform state but still exists: the API cannot delete an organisation. Delete it in the portal if it is no longer needed.",
			state.Name.ValueString(), state.ID.ValueString()))
}

// ImportState imports by organisation id. country cannot be read back, so it
// stays unset after an import.
func (organisation *organisationResource) ImportState(ctx context.Context, request resource.ImportStateRequest, response *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), request, response)
}
