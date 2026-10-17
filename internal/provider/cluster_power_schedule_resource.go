// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"ankra.io/terraform-provider-ankra/internal/client"
)

var (
	_ resource.Resource                = (*clusterPowerScheduleResource)(nil)
	_ resource.ResourceWithConfigure   = (*clusterPowerScheduleResource)(nil)
	_ resource.ResourceWithImportState = (*clusterPowerScheduleResource)(nil)
)

type clusterPowerScheduleResource struct {
	configuredClient
}

type clusterPowerScheduleModel struct {
	ID                      types.String `tfsdk:"id"`
	ClusterID               types.String `tfsdk:"cluster_id"`
	Action                  types.String `tfsdk:"action"`
	StopMode                types.String `tfsdk:"stop_mode"`
	PreserveState           types.Bool   `tfsdk:"preserve_state"`
	ScheduleKind            types.String `tfsdk:"schedule_kind"`
	RunAt                   types.String `tfsdk:"run_at"`
	CronExpression          types.String `tfsdk:"cron_expression"`
	Timezone                types.String `tfsdk:"timezone"`
	Enabled                 types.Bool   `tfsdk:"enabled"`
	AcceptNodeLocalDataLoss types.Bool   `tfsdk:"accept_node_local_data_loss"`
	NextRunAt               types.String `tfsdk:"next_run_at"`
	LastRunAt               types.String `tfsdk:"last_run_at"`
	LastRunStatus           types.String `tfsdk:"last_run_status"`
}

// NewClusterPowerScheduleResource returns the ankra_cluster_power_schedule
// resource, the counterpart of `ankra cluster power-schedules
// create|update|delete`.
func NewClusterPowerScheduleResource() resource.Resource {
	return &clusterPowerScheduleResource{}
}

func (scheduleResource *clusterPowerScheduleResource) Metadata(_ context.Context, request resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_cluster_power_schedule"
}

func (scheduleResource *clusterPowerScheduleResource) Schema(_ context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: "A scheduled stop or start of a cluster: once at `run_at`, or repeatedly per `cron_expression` in `timezone`. " +
			"The counterpart of `ankra cluster power-schedules create`, `update` and `delete`. Every attribute but `cluster_id` " +
			"updates in place.",
		Attributes: map[string]schema.Attribute{
			"id":         clusterSettingsIDAttribute("Identifier of the schedule."),
			"cluster_id": clusterSettingsClusterIDAttribute(),
			"action": schema.StringAttribute{
				MarkdownDescription: "`stop` or `start`.",
				Required:            true,
				Validators:          []validator.String{clusterSettingsOneOf("stop", "start")},
			},
			"stop_mode": schema.StringAttribute{
				MarkdownDescription: "How a stop stops the cluster: `delete_resources`, `scale_to_zero` or `pause`. Defaults to `delete_resources`, " +
					"the only value a start schedule accepts.",
				Optional:   true,
				Computed:   true,
				Default:    stringdefault.StaticString("delete_resources"),
				Validators: []validator.String{clusterSettingsOneOf("delete_resources", "scale_to_zero", "pause")},
			},
			"preserve_state": schema.BoolAttribute{
				MarkdownDescription: "Whether a `delete_resources` stop captures the cluster's state for the next start to restore, where the provider supports it. Defaults to `true`.",
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(true),
			},
			"schedule_kind": schema.StringAttribute{
				MarkdownDescription: "`once` (fires at `run_at`) or `cron` (fires per `cron_expression`).",
				Required:            true,
				Validators:          []validator.String{clusterSettingsOneOf("once", "cron")},
			},
			"run_at": schema.StringAttribute{
				MarkdownDescription: "RFC 3339 time a `once` schedule fires; must be in the future when written.",
				Optional:            true,
			},
			"cron_expression": schema.StringAttribute{
				MarkdownDescription: "Five-field cron expression of a `cron` schedule, evaluated in `timezone`.",
				Optional:            true,
			},
			"timezone": schema.StringAttribute{
				MarkdownDescription: "IANA timezone a `cron` schedule is evaluated in. Defaults to `UTC`.",
				Optional:            true,
				Computed:            true,
				Default:             stringdefault.StaticString("UTC"),
			},
			"enabled": schema.BoolAttribute{
				MarkdownDescription: "Whether the schedule fires. Defaults to `true`.",
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(true),
			},
			"accept_node_local_data_loss": schema.BoolAttribute{
				MarkdownDescription: "Acknowledges that an enabled `scale_to_zero` stop empties the volumes kept on worker disks; the platform refuses " +
					"such a stop on a cluster with them without it. Sent with every write, never read back. Defaults to `false`.",
				Optional: true,
				Computed: true,
				Default:  booldefault.StaticBool(false),
			},
			"next_run_at": schema.StringAttribute{
				MarkdownDescription: "When the schedule next fires; null while disabled or spent.",
				Computed:            true,
			},
			"last_run_at": schema.StringAttribute{
				MarkdownDescription: "When the schedule last fired.",
				Computed:            true,
			},
			"last_run_status": schema.StringAttribute{
				MarkdownDescription: "Outcome of the last run: `completed`, `skipped`, `deferred` or `failed`.",
				Computed:            true,
			},
		},
	}
}

func (scheduleResource *clusterPowerScheduleResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	apiClient := scheduleResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var plan clusterPowerScheduleModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}
	schedule, err := apiClient.CreateClusterPowerSchedule(ctx, plan.ClusterID.ValueString(), clusterPowerScheduleRequest(plan))
	if err != nil {
		response.Diagnostics.AddError("Unable to create cluster power schedule", err.Error())
		return
	}
	applyClusterPowerSchedule(&plan, schedule)
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
}

func (scheduleResource *clusterPowerScheduleResource) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	apiClient := scheduleResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state clusterPowerScheduleModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	schedule, err := apiClient.GetClusterPowerSchedule(ctx, state.ClusterID.ValueString(), state.ID.ValueString())
	if removeWhenNotFound(ctx, err, response) {
		return
	}
	if err != nil {
		response.Diagnostics.AddError("Unable to read cluster power schedule", err.Error())
		return
	}
	if state.AcceptNodeLocalDataLoss.IsNull() {
		state.AcceptNodeLocalDataLoss = types.BoolValue(false)
	}
	applyClusterPowerSchedule(&state, schedule)
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
}

func (scheduleResource *clusterPowerScheduleResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	apiClient := scheduleResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var plan clusterPowerScheduleModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}
	schedule, err := apiClient.UpdateClusterPowerSchedule(ctx, plan.ClusterID.ValueString(), plan.ID.ValueString(), clusterPowerScheduleRequest(plan))
	if err != nil {
		response.Diagnostics.AddError("Unable to update cluster power schedule", err.Error())
		return
	}
	applyClusterPowerSchedule(&plan, schedule)
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
}

func (scheduleResource *clusterPowerScheduleResource) Delete(ctx context.Context, request resource.DeleteRequest, response *resource.DeleteResponse) {
	apiClient := scheduleResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state clusterPowerScheduleModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	if err := apiClient.DeleteClusterPowerSchedule(ctx, state.ClusterID.ValueString(), state.ID.ValueString()); err != nil {
		response.Diagnostics.AddError("Unable to delete cluster power schedule", err.Error())
	}
}

// ImportState imports by "<cluster_id>/<schedule_id>".
func (scheduleResource *clusterPowerScheduleResource) ImportState(ctx context.Context, request resource.ImportStateRequest, response *resource.ImportStateResponse) {
	clusterSettingsImportComposite(ctx, request, response, "id")
}

func clusterPowerScheduleRequest(plan clusterPowerScheduleModel) client.ClusterPowerScheduleRequest {
	return client.ClusterPowerScheduleRequest{
		Action:                  plan.Action.ValueString(),
		StopMode:                plan.StopMode.ValueString(),
		PreserveState:           plan.PreserveState.ValueBool(),
		ScheduleKind:            plan.ScheduleKind.ValueString(),
		RunAt:                   stringPointer(plan.RunAt),
		CronExpression:          stringPointer(plan.CronExpression),
		Timezone:                plan.Timezone.ValueString(),
		Enabled:                 plan.Enabled.ValueBool(),
		AcceptNodeLocalDataLoss: plan.AcceptNodeLocalDataLoss.ValueBool(),
	}
}

func applyClusterPowerSchedule(model *clusterPowerScheduleModel, schedule *client.ClusterPowerSchedule) {
	model.ID = types.StringValue(schedule.ID)
	model.Action = types.StringValue(schedule.Action)
	model.StopMode = types.StringValue(schedule.StopMode)
	model.PreserveState = types.BoolValue(schedule.PreserveState)
	model.ScheduleKind = types.StringValue(schedule.ScheduleKind)
	model.RunAt = clusterSettingsTimestamp(model.RunAt, schedule.RunAt)
	model.CronExpression = types.StringPointerValue(schedule.CronExpression)
	model.Timezone = types.StringValue(schedule.Timezone)
	model.Enabled = types.BoolValue(schedule.Enabled)
	model.NextRunAt = types.StringPointerValue(schedule.NextRunAt)
	model.LastRunAt = types.StringPointerValue(schedule.LastRunAt)
	model.LastRunStatus = types.StringPointerValue(schedule.LastRunStatus)
}
