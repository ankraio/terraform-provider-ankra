// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"ankra.io/terraform-provider-ankra/internal/client"
)

var (
	_ resource.Resource                = (*clusterAgentCISettingsResource)(nil)
	_ resource.ResourceWithConfigure   = (*clusterAgentCISettingsResource)(nil)
	_ resource.ResourceWithImportState = (*clusterAgentCISettingsResource)(nil)
)

type clusterAgentCISettingsResource struct {
	configuredClient
}

type clusterAgentCISettingsModel struct {
	ID                     types.String `tfsdk:"id"`
	ClusterID              types.String `tfsdk:"cluster_id"`
	CIWorkerCount          types.Int64  `tfsdk:"ci_worker_count"`
	CIStorageClass         types.String `tfsdk:"ci_storage_class"`
	CIRunReservation       types.String `tfsdk:"ci_run_reservation"`
	CIImagePrepull         types.String `tfsdk:"ci_image_prepull"`
	CIBuildAppArmorProfile types.String `tfsdk:"ci_build_apparmor_profile"`
	ApplyState             types.String `tfsdk:"apply_state"`
}

// NewClusterAgentCISettingsResource returns the
// ankra_cluster_agent_ci_settings resource, the counterpart of `ankra cluster
// agent ci set`.
func NewClusterAgentCISettingsResource() resource.Resource {
	return &clusterAgentCISettingsResource{}
}

func (settingsResource *clusterAgentCISettingsResource) Metadata(_ context.Context, request resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_cluster_agent_ci_settings"
}

func (settingsResource *clusterAgentCISettingsResource) Schema(_ context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	keep := []planmodifier.String{stringplanmodifier.UseStateForUnknown()}
	response.Schema = schema.Schema{
		MarkdownDescription: "How a cluster's Ankra agent runs pipeline steps. The counterpart of `ankra cluster agent ci set`. " +
			"Only the attributes set in configuration are written; an unset attribute keeps the platform's value and reports it. " +
			"Destroying the resource restores the platform defaults of the attributes this resource manages. " +
			"The CI placement (`--node-group`, `--node-selector`, `--toleration`) is not managed here.",
		Attributes: map[string]schema.Attribute{
			"id":         clusterSettingsIDAttribute("The cluster id."),
			"cluster_id": clusterSettingsClusterIDAttribute(),
			"ci_worker_count": schema.Int64Attribute{
				MarkdownDescription: "How many pipeline steps the agent runs at once, 0 to 128. 0, the default, runs no pipeline-step scheduler.",
				Optional:            true,
				Computed:            true,
				PlanModifiers:       []planmodifier.Int64{int64planmodifier.UseStateForUnknown()},
			},
			"ci_storage_class": schema.StringAttribute{
				MarkdownDescription: "StorageClass step workspaces are cut from; empty is the cluster default.",
				Optional:            true,
				Computed:            true,
				PlanModifiers:       keep,
			},
			"ci_run_reservation": schema.StringAttribute{
				MarkdownDescription: "Run reservation mode: `off` (the default), `shadow` or `on`.",
				Optional:            true,
				Computed:            true,
				PlanModifiers:       keep,
				Validators:          []validator.String{clusterSettingsOneOf("off", "shadow", "on")},
			},
			"ci_image_prepull": schema.StringAttribute{
				MarkdownDescription: "Whether the agent keeps the platform's step images pulled on its nodes: `off` (the default) or `on`.",
				Optional:            true,
				Computed:            true,
				PlanModifiers:       keep,
				Validators:          []validator.String{clusterSettingsOneOf("off", "on")},
			},
			"ci_build_apparmor_profile": schema.StringAttribute{
				MarkdownDescription: "Node-installed AppArmor profile the rootless image builder runs under; empty for none.",
				Optional:            true,
				Computed:            true,
				PlanModifiers:       keep,
			},
			"apply_state": schema.StringAttribute{
				MarkdownDescription: "How the last write reached the agent: `applied`, `pending_upgrade` or `agent_offline`. The values are stored in every case.",
				Computed:            true,
			},
		},
	}
}

func (settingsResource *clusterAgentCISettingsResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	var plan clusterAgentCISettingsModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if settingsResource.write(ctx, &plan, clusterAgentCISettingsUpdateFrom(plan), &response.Diagnostics) {
		response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
	}
}

func (settingsResource *clusterAgentCISettingsResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	var plan clusterAgentCISettingsModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if settingsResource.write(ctx, &plan, clusterAgentCISettingsUpdateFrom(plan), &response.Diagnostics) {
		response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
	}
}

func (settingsResource *clusterAgentCISettingsResource) write(ctx context.Context, plan *clusterAgentCISettingsModel, update client.ClusterAgentCISettingsUpdate, diagnostics *diag.Diagnostics) bool {
	apiClient := settingsResource.requireClient(diagnostics)
	if apiClient == nil || diagnostics.HasError() {
		return false
	}
	settings, err := apiClient.UpdateClusterAgentCISettings(ctx, plan.ClusterID.ValueString(), update)
	if err != nil {
		diagnostics.AddError("Unable to set cluster agent CI settings", err.Error())
		return false
	}
	applyClusterAgentCISettings(plan, settings)
	return true
}

func (settingsResource *clusterAgentCISettingsResource) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	apiClient := settingsResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state clusterAgentCISettingsModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	settings, err := apiClient.GetClusterAgentCISettings(ctx, state.ClusterID.ValueString())
	if removeWhenNotFound(ctx, err, response) {
		return
	}
	if err != nil {
		response.Diagnostics.AddError("Unable to read cluster agent CI settings", err.Error())
		return
	}
	applyClusterAgentCISettings(&state, settings)
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
}

// Delete restores the platform defaults of the managed settings.
func (settingsResource *clusterAgentCISettingsResource) Delete(ctx context.Context, request resource.DeleteRequest, response *resource.DeleteResponse) {
	apiClient := settingsResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state clusterAgentCISettingsModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	zero := int64(0)
	empty := ""
	off := "off"
	defaults := client.ClusterAgentCISettingsUpdate{
		CIWorkerCount: &zero, CIStorageClass: &empty, CIRunReservation: &off, CIImagePrepull: &off, CIBuildAppArmorProfile: &empty,
	}
	if _, err := apiClient.UpdateClusterAgentCISettings(ctx, state.ClusterID.ValueString(), defaults); err != nil && !client.IsNotFound(err) {
		response.Diagnostics.AddError("Unable to restore cluster agent CI settings", err.Error())
	}
}

// ImportState imports by cluster id.
func (settingsResource *clusterAgentCISettingsResource) ImportState(ctx context.Context, request resource.ImportStateRequest, response *resource.ImportStateResponse) {
	clusterSettingsImportClusterID(ctx, request, response)
}

func clusterAgentCISettingsUpdateFrom(plan clusterAgentCISettingsModel) client.ClusterAgentCISettingsUpdate {
	return client.ClusterAgentCISettingsUpdate{
		CIWorkerCount:          int64Pointer(plan.CIWorkerCount),
		CIStorageClass:         stringPointer(plan.CIStorageClass),
		CIRunReservation:       stringPointer(plan.CIRunReservation),
		CIImagePrepull:         stringPointer(plan.CIImagePrepull),
		CIBuildAppArmorProfile: stringPointer(plan.CIBuildAppArmorProfile),
	}
}

func applyClusterAgentCISettings(model *clusterAgentCISettingsModel, settings *client.ClusterAgentCISettings) {
	model.ID = model.ClusterID
	model.CIWorkerCount = types.Int64Value(settings.CIWorkerCount)
	model.CIStorageClass = types.StringValue(settings.CIStorageClass)
	model.CIRunReservation = types.StringValue(clusterSettingsValueOr(settings.CIRunReservation, "off"))
	model.CIImagePrepull = types.StringValue(clusterSettingsValueOr(settings.CIImagePrepull, "off"))
	model.CIBuildAppArmorProfile = types.StringValue(clusterSettingsValueOr(settings.CIBuildAppArmorProfile, ""))
	model.ApplyState = types.StringValue(settings.ApplyState)
}

func clusterSettingsValueOr(value *string, fallback string) string {
	if value == nil {
		return fallback
	}
	return *value
}
