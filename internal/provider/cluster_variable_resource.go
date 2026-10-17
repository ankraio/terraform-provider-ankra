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
	_ resource.Resource                = (*clusterVariableResource)(nil)
	_ resource.ResourceWithConfigure   = (*clusterVariableResource)(nil)
	_ resource.ResourceWithImportState = (*clusterVariableResource)(nil)
)

type clusterVariableResource struct {
	configuredClient
}

type clusterVariableModel struct {
	ID          types.String `tfsdk:"id"`
	ClusterID   types.String `tfsdk:"cluster_id"`
	Name        types.String `tfsdk:"name"`
	Value       types.String `tfsdk:"value"`
	Description types.String `tfsdk:"description"`
}

// NewClusterVariableResource returns the ankra_cluster_variable resource,
// the counterpart of `ankra cluster variables set|delete`.
func NewClusterVariableResource() resource.Resource {
	return &clusterVariableResource{}
}

func (variableResource *clusterVariableResource) Metadata(_ context.Context, request resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_cluster_variable"
}

func (variableResource *clusterVariableResource) Schema(_ context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: "A cluster-scoped variable, referenced from the cluster's stack manifests and add-on values as `${{ ankra.<name> }}`. " +
			"It shadows an organisation variable of the same name and is shadowed by a stack variable. " +
			"The counterpart of `ankra cluster variables set` and `ankra cluster variables delete`.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Identifier of the variable.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"cluster_id": schema.StringAttribute{
				MarkdownDescription: "Identifier of the cluster. Changing it replaces the variable.",
				Required:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"name": schema.StringAttribute{
				MarkdownDescription: "Variable name. Changing it replaces the variable.",
				Required:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
				Validators:          []validator.String{configurationPathNameValidator{}},
			},
			"value": schema.StringAttribute{
				MarkdownDescription: "Variable value. Cluster variables have no secret mode: the platform stores and returns the value " +
					"in plain text, so keep secrets in a secret organisation variable instead. Marked sensitive so plans do not print it.",
				Required:  true,
				Sensitive: true,
			},
			"description": schema.StringAttribute{
				MarkdownDescription: "Free-text description.",
				Optional:            true,
			},
		},
	}
}

func (variableResource *clusterVariableResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	apiClient := variableResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var plan clusterVariableModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}

	created, err := apiClient.CreateClusterVariable(ctx, plan.ClusterID.ValueString(), client.ClusterVariableCreate{
		Name:        plan.Name.ValueString(),
		Value:       plan.Value.ValueString(),
		Description: stringPointer(plan.Description),
	})
	if err != nil {
		response.Diagnostics.AddError("Unable to create cluster variable", err.Error())
		return
	}

	plan.ID = types.StringValue(created.ID)
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
}

func (variableResource *clusterVariableResource) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	apiClient := variableResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state clusterVariableModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}

	variable, err := apiClient.GetClusterVariable(ctx, state.ClusterID.ValueString(), state.Name.ValueString())
	if removeWhenNotFound(ctx, err, response) {
		return
	}
	if err != nil {
		response.Diagnostics.AddError("Unable to read cluster variable", err.Error())
		return
	}
	if readTargetChanged(state.ID, variable.ID) {
		response.State.RemoveResource(ctx)
		return
	}

	state.ID = types.StringValue(variable.ID)
	state.Value = types.StringValue(variable.Value)
	state.Description = types.StringPointerValue(variable.Description)
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
}

func (variableResource *clusterVariableResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	apiClient := variableResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var plan clusterVariableModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}

	updated, err := apiClient.UpdateClusterVariable(ctx, plan.ClusterID.ValueString(), plan.Name.ValueString(), client.ClusterVariableUpdate{
		Value:       plan.Value.ValueString(),
		Description: stringPointer(plan.Description),
	})
	if err != nil {
		response.Diagnostics.AddError("Unable to update cluster variable", err.Error())
		return
	}

	plan.ID = types.StringValue(updated.ID)
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
}

func (variableResource *clusterVariableResource) Delete(ctx context.Context, request resource.DeleteRequest, response *resource.DeleteResponse) {
	apiClient := variableResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state clusterVariableModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}

	if err := apiClient.DeleteClusterVariable(ctx, state.ClusterID.ValueString(), state.Name.ValueString()); err != nil {
		response.Diagnostics.AddError("Unable to delete cluster variable", err.Error())
	}
}

// ImportState imports by `<cluster_id>/<name>`.
func (variableResource *clusterVariableResource) ImportState(ctx context.Context, request resource.ImportStateRequest, response *resource.ImportStateResponse) {
	parts, err := configurationSplitImportID(request.ID, "cluster_id", "name")
	if err != nil {
		response.Diagnostics.AddError("Invalid import ID", err.Error())
		return
	}
	response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("cluster_id"), parts[0])...)
	response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("name"), parts[1])...)
}
