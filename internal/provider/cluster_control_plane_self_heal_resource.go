// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ resource.Resource                = (*clusterControlPlaneSelfHealResource)(nil)
	_ resource.ResourceWithConfigure   = (*clusterControlPlaneSelfHealResource)(nil)
	_ resource.ResourceWithImportState = (*clusterControlPlaneSelfHealResource)(nil)
)

type clusterControlPlaneSelfHealResource struct {
	configuredClient
}

type clusterControlPlaneSelfHealModel struct {
	ID        types.String `tfsdk:"id"`
	ClusterID types.String `tfsdk:"cluster_id"`
	Enabled   types.Bool   `tfsdk:"enabled"`
}

// NewClusterControlPlaneSelfHealResource returns the
// ankra_cluster_control_plane_self_heal resource.
func NewClusterControlPlaneSelfHealResource() resource.Resource {
	return &clusterControlPlaneSelfHealResource{}
}

func (selfHealResource *clusterControlPlaneSelfHealResource) Metadata(_ context.Context, request resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_cluster_control_plane_self_heal"
}

func (selfHealResource *clusterControlPlaneSelfHealResource) Schema(_ context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: "Whether Ankra may restart a NotReady control plane node of a cluster it provisioned, through the " +
			"cloud provider. It is on by default. Destroying the resource turns it back on.",
		Attributes: map[string]schema.Attribute{
			"id":         clusterSettingsIDAttribute("The cluster id."),
			"cluster_id": clusterSettingsClusterIDAttribute(),
			"enabled": schema.BoolAttribute{
				MarkdownDescription: "Whether control-plane self-heal is on.",
				Required:            true,
			},
		},
	}
}

func (selfHealResource *clusterControlPlaneSelfHealResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	var plan clusterControlPlaneSelfHealModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if selfHealResource.set(ctx, &plan, &response.Diagnostics) {
		response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
		response.Diagnostics.Append(trackScope(ctx, selfHealResource.client, response.Private)...)
	}
}

func (selfHealResource *clusterControlPlaneSelfHealResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	var plan clusterControlPlaneSelfHealModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if selfHealResource.set(ctx, &plan, &response.Diagnostics) {
		response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
	}
}

func (selfHealResource *clusterControlPlaneSelfHealResource) set(ctx context.Context, plan *clusterControlPlaneSelfHealModel, diagnostics *diag.Diagnostics) bool {
	apiClient := selfHealResource.requireClient(diagnostics)
	if apiClient == nil || diagnostics.HasError() {
		return false
	}
	view, err := apiClient.SetClusterControlPlaneSelfHeal(ctx, plan.ClusterID.ValueString(), plan.Enabled.ValueBool())
	if err != nil {
		diagnostics.AddError("Unable to set control-plane self-heal", clusterSettingsErrorText(err, plan.ClusterID.ValueString()))
		return false
	}
	plan.ID = plan.ClusterID
	plan.Enabled = types.BoolValue(view.Enabled)
	return true
}

func (selfHealResource *clusterControlPlaneSelfHealResource) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	apiClient := selfHealResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state clusterControlPlaneSelfHealModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	view, err := apiClient.GetClusterControlPlaneSelfHeal(ctx, state.ClusterID.ValueString())
	if clusterSettingsReadNotFound(ctx, err, apiClient, state.ClusterID.ValueString(), response) {
		return
	}
	if err != nil {
		response.Diagnostics.AddError("Unable to read control-plane self-heal", err.Error())
		return
	}
	state.ID = state.ClusterID
	state.Enabled = types.BoolValue(view.Enabled)
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
	response.Diagnostics.Append(trackScope(ctx, apiClient, response.Private)...)
}

// Delete restores the platform default: self-heal on.
func (selfHealResource *clusterControlPlaneSelfHealResource) Delete(ctx context.Context, request resource.DeleteRequest, response *resource.DeleteResponse) {
	apiClient := selfHealResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state clusterControlPlaneSelfHealModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	_, err := apiClient.SetClusterControlPlaneSelfHeal(ctx, state.ClusterID.ValueString(), true)
	if deleteError := clusterSettingsDeleteError(err, state.ClusterID.ValueString()); deleteError != nil {
		response.Diagnostics.AddError("Unable to turn control-plane self-heal back on", deleteError.Error())
	}
}

// ImportState imports by cluster id.
func (selfHealResource *clusterControlPlaneSelfHealResource) ImportState(ctx context.Context, request resource.ImportStateRequest, response *resource.ImportStateResponse) {
	clusterSettingsImportClusterID(ctx, request, response)
}
