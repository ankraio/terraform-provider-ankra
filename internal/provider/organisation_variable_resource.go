// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"ankra.io/terraform-provider-ankra/internal/client"
)

var (
	_ resource.Resource                = (*organisationVariableResource)(nil)
	_ resource.ResourceWithConfigure   = (*organisationVariableResource)(nil)
	_ resource.ResourceWithImportState = (*organisationVariableResource)(nil)
)

type organisationVariableResource struct {
	configuredClient
}

type organisationVariableModel struct {
	ID          types.String `tfsdk:"id"`
	Name        types.String `tfsdk:"name"`
	Value       types.String `tfsdk:"value"`
	Description types.String `tfsdk:"description"`
	IsSecret    types.Bool   `tfsdk:"is_secret"`
}

// NewOrganisationVariableResource returns the ankra_organisation_variable
// resource, the counterpart of `ankra org variables set|delete`.
func NewOrganisationVariableResource() resource.Resource {
	return &organisationVariableResource{}
}

func (variableResource *organisationVariableResource) Metadata(_ context.Context, request resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_organisation_variable"
}

func (variableResource *organisationVariableResource) Schema(_ context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: "An organisation variable, referenced from stack manifests and add-on values as `${{ ankra.<name> }}`. " +
			"The counterpart of `ankra org variables set` and `ankra org variables delete`.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Identifier of the variable.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				MarkdownDescription: "Variable name. Changing it replaces the variable.",
				Required:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"value": schema.StringAttribute{
				MarkdownDescription: "Variable value. For a secret variable the platform never returns the value, so a change made outside Terraform is not detected.",
				Required:            true,
				Sensitive:           true,
			},
			"description": schema.StringAttribute{
				MarkdownDescription: "Free-text description.",
				Optional:            true,
			},
			"is_secret": schema.BoolAttribute{
				MarkdownDescription: "Whether the value is a secret: hidden from listings and only revealed to members with write access. Defaults to `false`.",
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(false),
			},
		},
	}
}

func (variableResource *organisationVariableResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	apiClient := variableResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var plan organisationVariableModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}

	created, err := apiClient.CreateOrganisationVariable(ctx, client.OrganisationVariableCreate{
		Name:        plan.Name.ValueString(),
		Value:       plan.Value.ValueString(),
		Description: stringPointer(plan.Description),
		IsSecret:    plan.IsSecret.ValueBool(),
	})
	if err != nil {
		response.Diagnostics.AddError("Unable to create organisation variable", err.Error())
		return
	}

	plan.ID = types.StringValue(created.ID)
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
}

func (variableResource *organisationVariableResource) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	apiClient := variableResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state organisationVariableModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}

	variable, err := apiClient.GetOrganisationVariable(ctx, state.Name.ValueString())
	if removeWhenNotFound(ctx, err, response) {
		return
	}
	if err != nil {
		response.Diagnostics.AddError("Unable to read organisation variable", err.Error())
		return
	}

	state.ID = types.StringValue(variable.ID)
	state.IsSecret = types.BoolValue(variable.IsSecret)
	state.Description = types.StringPointerValue(variable.Description)
	if variable.Value != nil {
		state.Value = types.StringValue(*variable.Value)
	}
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
}

func (variableResource *organisationVariableResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	apiClient := variableResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var plan organisationVariableModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}

	updated, err := apiClient.UpdateOrganisationVariable(ctx, plan.Name.ValueString(), client.OrganisationVariableUpdate{
		Value:       plan.Value.ValueString(),
		Description: stringPointer(plan.Description),
		IsSecret:    plan.IsSecret.ValueBool(),
	})
	if err != nil {
		response.Diagnostics.AddError("Unable to update organisation variable", err.Error())
		return
	}

	plan.ID = types.StringValue(updated.ID)
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
}

func (variableResource *organisationVariableResource) Delete(ctx context.Context, request resource.DeleteRequest, response *resource.DeleteResponse) {
	apiClient := variableResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state organisationVariableModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}

	if err := apiClient.DeleteOrganisationVariable(ctx, state.Name.ValueString()); err != nil {
		response.Diagnostics.AddError("Unable to delete organisation variable", err.Error())
	}
}

// ImportState imports by variable name. A secret's value cannot be read back,
// so the first plan after importing one shows the configured value as a change.
func (variableResource *organisationVariableResource) ImportState(ctx context.Context, request resource.ImportStateRequest, response *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("name"), request, response)
}
