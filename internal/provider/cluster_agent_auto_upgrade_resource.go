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
	_ resource.Resource                = (*clusterAgentAutoUpgradeResource)(nil)
	_ resource.ResourceWithConfigure   = (*clusterAgentAutoUpgradeResource)(nil)
	_ resource.ResourceWithImportState = (*clusterAgentAutoUpgradeResource)(nil)
)

type clusterAgentAutoUpgradeResource struct {
	configuredClient
}

type clusterAgentAutoUpgradeModel struct {
	ID        types.String `tfsdk:"id"`
	ClusterID types.String `tfsdk:"cluster_id"`
	Enabled   types.Bool   `tfsdk:"enabled"`
}

// NewClusterAgentAutoUpgradeResource returns the
// ankra_cluster_agent_auto_upgrade resource, the counterpart of `ankra
// cluster agent auto-upgrade enable|disable`.
func NewClusterAgentAutoUpgradeResource() resource.Resource {
	return &clusterAgentAutoUpgradeResource{}
}

func (upgradeResource *clusterAgentAutoUpgradeResource) Metadata(_ context.Context, request resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_cluster_agent_auto_upgrade"
}

func (upgradeResource *clusterAgentAutoUpgradeResource) Schema(_ context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: "Whether the platform's fleet rollout upgrades a cluster's Ankra agent automatically. " +
			"The counterpart of `ankra cluster agent auto-upgrade enable` and `disable`. Destroying the resource restores the " +
			"platform default, automatic upgrades on. The cluster must have a registered agent.",
		Attributes: map[string]schema.Attribute{
			"id":         clusterSettingsIDAttribute("The cluster id."),
			"cluster_id": clusterSettingsClusterIDAttribute(),
			"enabled": schema.BoolAttribute{
				MarkdownDescription: "`false` fences the agent off from automatic upgrades.",
				Required:            true,
			},
		},
	}
}

func (upgradeResource *clusterAgentAutoUpgradeResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	var plan clusterAgentAutoUpgradeModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if upgradeResource.set(ctx, &plan, &response.Diagnostics) {
		response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
	}
}

func (upgradeResource *clusterAgentAutoUpgradeResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	var plan clusterAgentAutoUpgradeModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if upgradeResource.set(ctx, &plan, &response.Diagnostics) {
		response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
	}
}

func (upgradeResource *clusterAgentAutoUpgradeResource) set(ctx context.Context, plan *clusterAgentAutoUpgradeModel, diagnostics *diag.Diagnostics) bool {
	apiClient := upgradeResource.requireClient(diagnostics)
	if apiClient == nil || diagnostics.HasError() {
		return false
	}
	if err := apiClient.SetClusterAgentAutoUpgrade(ctx, plan.ClusterID.ValueString(), plan.Enabled.ValueBool()); err != nil {
		diagnostics.AddError("Unable to set cluster agent auto-upgrade", err.Error())
		return false
	}
	plan.ID = plan.ClusterID
	return true
}

func (upgradeResource *clusterAgentAutoUpgradeResource) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	apiClient := upgradeResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state clusterAgentAutoUpgradeModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	agent, err := apiClient.GetClusterAgent(ctx, state.ClusterID.ValueString())
	if clusterSettingsReadNotFound(ctx, err, state.ClusterID.ValueString(), response) {
		return
	}
	if err != nil {
		response.Diagnostics.AddError("Unable to read cluster agent", err.Error())
		return
	}
	state.ID = state.ClusterID
	state.Enabled = types.BoolValue(!agent.DisableAutoUpgrade)
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
}

// Delete restores the platform default: automatic upgrades on.
func (upgradeResource *clusterAgentAutoUpgradeResource) Delete(ctx context.Context, request resource.DeleteRequest, response *resource.DeleteResponse) {
	apiClient := upgradeResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state clusterAgentAutoUpgradeModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	err := apiClient.SetClusterAgentAutoUpgrade(ctx, state.ClusterID.ValueString(), true)
	if deleteError := clusterSettingsDeleteError(err, state.ClusterID.ValueString()); deleteError != nil {
		response.Diagnostics.AddError("Unable to restore cluster agent auto-upgrade", deleteError.Error())
	}
}

// ImportState imports by cluster id.
func (upgradeResource *clusterAgentAutoUpgradeResource) ImportState(ctx context.Context, request resource.ImportStateRequest, response *resource.ImportStateResponse) {
	clusterSettingsImportClusterID(ctx, request, response)
}
