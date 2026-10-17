// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"encoding/json"
	"sort"

	"github.com/hashicorp/terraform-plugin-framework/attr"

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

// The platform defaults destroy restores for the settings added after the
// first release of this resource (enginekit/agentcisettings).
const (
	clusterAgentCIDefaultMaxAdmittingRuns  = 2
	clusterAgentCIDefaultReservationSizing = "declared"
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
	CIMaxAdmittingRuns     types.Int64  `tfsdk:"ci_max_admitting_runs"`
	CIReservationSizing    types.String `tfsdk:"ci_reservation_sizing"`
	CIPlacement            types.Object `tfsdk:"ci_placement"`
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
			"Destroying the resource restores the platform default of each setting the configuration has set; a setting the " +
			"configuration never set (only reported) is left as it is. An imported resource resets nothing on destroy until an apply has written a setting.",
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
			"ci_max_admitting_runs": schema.Int64Attribute{
				MarkdownDescription: "How many pipeline runs the cluster admits at once while their run reservations are still being " +
					"scheduled, 1 to 32. It holds runs back only while `ci_run_reservation` is `on` and the agent supports it. " +
					"The default, 2, reads as automatic: the platform raises it with `ci_worker_count`.",
				Optional:      true,
				Computed:      true,
				PlanModifiers: []planmodifier.Int64{int64planmodifier.UseStateForUnknown()},
				Validators:    []validator.Int64{applicationIntegerBetween{minimum: 1, maximum: 32}},
			},
			"ci_reservation_sizing": schema.StringAttribute{
				MarkdownDescription: "What a run reservation is sized from: `declared` (what stages declare, the default) or `measured` " +
					"(what their recent successful attempts used). It matters only while `ci_run_reservation` is `shadow` or `on`.",
				Optional:      true,
				Computed:      true,
				PlanModifiers: keep,
				Validators:    []validator.String{clusterSettingsOneOf("declared", "measured")},
			},
			"ci_placement": clusterAgentCIPlacementAttribute(),
			"apply_state": schema.StringAttribute{
				MarkdownDescription: "How the last write reached the agent: `applied`, `pending_upgrade` or `agent_offline`. The values are stored in every case.",
				Computed:            true,
			},
		},
	}
}

// clusterAgentCISettingsConfiguredKey is the private state key that records
// which settings the configuration has set, so destroy resets only those.
const clusterAgentCISettingsConfiguredKey = "configured_settings"

func (settingsResource *clusterAgentCISettingsResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	var plan, config clusterAgentCISettingsModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	response.Diagnostics.Append(request.Config.Get(ctx, &config)...)
	if response.Diagnostics.HasError() {
		return
	}
	if !settingsResource.write(ctx, &plan, clusterAgentCISettingsUpdateFrom(ctx, config, &response.Diagnostics), &response.Diagnostics) {
		return
	}
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
	response.Diagnostics.Append(response.Private.SetKey(ctx, clusterAgentCISettingsConfiguredKey, clusterAgentCISettingsConfigured(config, nil))...)
}

func (settingsResource *clusterAgentCISettingsResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	var plan, config clusterAgentCISettingsModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	response.Diagnostics.Append(request.Config.Get(ctx, &config)...)
	if response.Diagnostics.HasError() {
		return
	}
	if !settingsResource.write(ctx, &plan, clusterAgentCISettingsUpdateFrom(ctx, config, &response.Diagnostics), &response.Diagnostics) {
		return
	}
	previous, diagnostics := request.Private.GetKey(ctx, clusterAgentCISettingsConfiguredKey)
	response.Diagnostics.Append(diagnostics...)
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
	response.Diagnostics.Append(response.Private.SetKey(ctx, clusterAgentCISettingsConfiguredKey, clusterAgentCISettingsConfigured(config, previous))...)
}

func (settingsResource *clusterAgentCISettingsResource) write(ctx context.Context, plan *clusterAgentCISettingsModel, update client.ClusterAgentCISettingsUpdate, diagnostics *diag.Diagnostics) bool {
	apiClient := settingsResource.requireClient(diagnostics)
	if apiClient == nil || diagnostics.HasError() {
		return false
	}
	var settings *client.ClusterAgentCISettings
	var err error
	if update.IsEmpty() {
		// The platform refuses a write naming nothing; with nothing
		// configured the resource only reports.
		settings, err = apiClient.GetClusterAgentCISettings(ctx, plan.ClusterID.ValueString())
	} else {
		settings, err = apiClient.UpdateClusterAgentCISettings(ctx, plan.ClusterID.ValueString(), update)
	}
	if err != nil {
		diagnostics.AddError("Unable to set cluster agent CI settings", clusterSettingsErrorText(err, plan.ClusterID.ValueString()))
		return false
	}
	applyClusterAgentCISettings(ctx, plan, settings, diagnostics)
	return !diagnostics.HasError()
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
	if clusterSettingsReadNotFound(ctx, err, apiClient, state.ClusterID.ValueString(), response) {
		return
	}
	if err != nil {
		response.Diagnostics.AddError("Unable to read cluster agent CI settings", err.Error())
		return
	}
	applyClusterAgentCISettings(ctx, &state, settings, &response.Diagnostics)
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
}

// Delete restores the platform default of each setting the configuration
// has set; a setting only reported (set in the portal or the CLI) is left
// as it is.
func (settingsResource *clusterAgentCISettingsResource) Delete(ctx context.Context, request resource.DeleteRequest, response *resource.DeleteResponse) {
	apiClient := settingsResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state clusterAgentCISettingsModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	recorded, diagnostics := request.Private.GetKey(ctx, clusterAgentCISettingsConfiguredKey)
	response.Diagnostics.Append(diagnostics...)
	if response.Diagnostics.HasError() {
		return
	}
	configured := map[string]bool{}
	if len(recorded) > 0 {
		var names []string
		if err := json.Unmarshal(recorded, &names); err != nil {
			response.Diagnostics.AddError("Unable to read private state", err.Error())
			return
		}
		for _, name := range names {
			configured[name] = true
		}
	}
	defaults := clusterAgentCISettingsDefaults(configured)
	if defaults.IsEmpty() {
		return
	}
	_, err := apiClient.UpdateClusterAgentCISettings(ctx, state.ClusterID.ValueString(), defaults)
	if deleteError := clusterSettingsDeleteError(err, state.ClusterID.ValueString()); deleteError != nil {
		response.Diagnostics.AddError("Unable to restore cluster agent CI settings", deleteError.Error())
	}
}

// clusterAgentCISettingsDefaults is the write that restores the platform
// default of every named setting.
func clusterAgentCISettingsDefaults(configured map[string]bool) client.ClusterAgentCISettingsUpdate {
	zero := int64(0)
	empty := ""
	off := "off"
	admitting := int64(clusterAgentCIDefaultMaxAdmittingRuns)
	sizing := clusterAgentCIDefaultReservationSizing
	defaults := client.ClusterAgentCISettingsUpdate{}
	if configured["ci_worker_count"] {
		defaults.CIWorkerCount = &zero
	}
	if configured["ci_storage_class"] {
		defaults.CIStorageClass = &empty
	}
	if configured["ci_run_reservation"] {
		defaults.CIRunReservation = &off
	}
	if configured["ci_image_prepull"] {
		defaults.CIImagePrepull = &off
	}
	if configured["ci_build_apparmor_profile"] {
		defaults.CIBuildAppArmorProfile = &empty
	}
	if configured["ci_max_admitting_runs"] {
		defaults.CIMaxAdmittingRuns = &admitting
	}
	if configured["ci_reservation_sizing"] {
		defaults.CIReservationSizing = &sizing
	}
	if configured["ci_placement"] {
		defaults.ClearPlacement = true
	}
	return defaults
}

// clusterAgentCISettingsConfigured lists the settings the configuration
// sets, plus those an earlier apply recorded, as the JSON kept in private
// state. Keeping the earlier ones means a setting the configuration stops
// naming is still reset on destroy, since Terraform last wrote it.
func clusterAgentCISettingsConfigured(config clusterAgentCISettingsModel, previous []byte) []byte {
	set := map[string]bool{}
	var earlier []string
	if len(previous) > 0 && json.Unmarshal(previous, &earlier) == nil {
		for _, name := range earlier {
			set[name] = true
		}
	}
	for name, value := range map[string]attr.Value{
		"ci_worker_count":           config.CIWorkerCount,
		"ci_storage_class":          config.CIStorageClass,
		"ci_run_reservation":        config.CIRunReservation,
		"ci_image_prepull":          config.CIImagePrepull,
		"ci_build_apparmor_profile": config.CIBuildAppArmorProfile,
		"ci_max_admitting_runs":     config.CIMaxAdmittingRuns,
		"ci_reservation_sizing":     config.CIReservationSizing,
		"ci_placement":              config.CIPlacement,
	} {
		if !value.IsNull() {
			set[name] = true
		}
	}
	names := make([]string, 0, len(set))
	for name := range set {
		names = append(names, name)
	}
	sort.Strings(names)
	encoded, _ := json.Marshal(names)
	return encoded
}

// ImportState imports by cluster id.
func (settingsResource *clusterAgentCISettingsResource) ImportState(ctx context.Context, request resource.ImportStateRequest, response *resource.ImportStateResponse) {
	clusterSettingsImportClusterID(ctx, request, response)
}

func clusterAgentCISettingsUpdateFrom(ctx context.Context, config clusterAgentCISettingsModel, diagnostics *diag.Diagnostics) client.ClusterAgentCISettingsUpdate {
	return client.ClusterAgentCISettingsUpdate{
		CIWorkerCount:          int64Pointer(config.CIWorkerCount),
		CIStorageClass:         stringPointer(config.CIStorageClass),
		CIRunReservation:       stringPointer(config.CIRunReservation),
		CIImagePrepull:         stringPointer(config.CIImagePrepull),
		CIBuildAppArmorProfile: stringPointer(config.CIBuildAppArmorProfile),
		CIMaxAdmittingRuns:     int64Pointer(config.CIMaxAdmittingRuns),
		CIReservationSizing:    stringPointer(config.CIReservationSizing),
		CIPlacement:            clusterAgentCIPlacementFromObject(ctx, config.CIPlacement, diagnostics),
	}
}

func applyClusterAgentCISettings(ctx context.Context, model *clusterAgentCISettingsModel, settings *client.ClusterAgentCISettings, diagnostics *diag.Diagnostics) {
	model.ID = model.ClusterID
	model.CIWorkerCount = types.Int64Value(settings.CIWorkerCount)
	model.CIStorageClass = types.StringValue(settings.CIStorageClass)
	model.CIRunReservation = types.StringValue(clusterSettingsValueOr(settings.CIRunReservation, "off"))
	model.CIImagePrepull = types.StringValue(clusterSettingsValueOr(settings.CIImagePrepull, "off"))
	model.CIBuildAppArmorProfile = types.StringValue(clusterSettingsValueOr(settings.CIBuildAppArmorProfile, ""))
	admitting := int64(clusterAgentCIDefaultMaxAdmittingRuns)
	if settings.CIMaxAdmittingRuns != nil {
		admitting = *settings.CIMaxAdmittingRuns
	}
	model.CIMaxAdmittingRuns = types.Int64Value(admitting)
	model.CIReservationSizing = types.StringValue(clusterSettingsValueOr(settings.CIReservationSizing, clusterAgentCIDefaultReservationSizing))
	model.CIPlacement = clusterAgentCIPlacementState(ctx, model.CIPlacement, settings.CIPlacement, diagnostics)
	model.ApplyState = types.StringValue(settings.ApplyState)
}

func clusterSettingsValueOr(value *string, fallback string) string {
	if value == nil {
		return fallback
	}
	return *value
}
