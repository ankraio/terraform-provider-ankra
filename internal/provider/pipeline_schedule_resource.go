// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"ankra.io/terraform-provider-ankra/internal/client"
)

var (
	_ resource.Resource                   = (*pipelineScheduleResource)(nil)
	_ resource.ResourceWithConfigure      = (*pipelineScheduleResource)(nil)
	_ resource.ResourceWithImportState    = (*pipelineScheduleResource)(nil)
	_ resource.ResourceWithValidateConfig = (*pipelineScheduleResource)(nil)
)

type pipelineScheduleResource struct {
	configuredClient
}

type pipelineScheduleModel struct {
	ID            types.String `tfsdk:"id"`
	ApplicationID types.String `tfsdk:"application_id"`
	RepositoryID  types.String `tfsdk:"repository_id"`
	Cron          types.String `tfsdk:"cron"`
	Timezone      types.String `tfsdk:"timezone"`
	Ref           types.String `tfsdk:"ref"`
	Inputs        types.Map    `tfsdk:"inputs"`
	Enabled       types.Bool   `tfsdk:"enabled"`
	NextFireAt    types.String `tfsdk:"next_fire_at"`
}

// NewPipelineScheduleResource returns the ankra_pipeline_schedule resource,
// the counterpart of `ankra pipeline schedules create|update|delete` and
// `ankra application pipeline schedules create|update|delete`.
func NewPipelineScheduleResource() resource.Resource {
	return &pipelineScheduleResource{}
}

func (scheduleResource *pipelineScheduleResource) Metadata(_ context.Context, request resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_pipeline_schedule"
}

func (scheduleResource *pipelineScheduleResource) Schema(_ context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	attributes := pipelineSelectorAttributes("the schedule runs")
	attributes["id"] = schema.StringAttribute{
		MarkdownDescription: "Identifier of the schedule.",
		Computed:            true,
		PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
	}
	attributes["cron"] = schema.StringAttribute{
		MarkdownDescription: "Five-field cron expression, or a macro such as `@daily`. The platform stores it lower-cased with macros expanded; " +
			"an expression that stores the same is not a change.",
		Required:   true,
		Validators: []validator.String{pipelineCronValidator{}},
	}
	attributes["timezone"] = schema.StringAttribute{
		MarkdownDescription: "IANA timezone the expression is read in. Defaults to `UTC`.",
		Optional:            true,
		Computed:            true,
		Default:             stringdefault.StaticString("UTC"),
		Validators:          []validator.String{pipelineTimezoneValidator{}},
	}
	attributes["ref"] = schema.StringAttribute{
		MarkdownDescription: "Branch or tag the scheduled run checks out. Unset runs the default branch.",
		Optional:            true,
	}
	attributes["inputs"] = schema.MapAttribute{
		MarkdownDescription: "Pipeline inputs the scheduled run starts with.",
		Optional:            true,
		ElementType:         types.StringType,
	}
	attributes["enabled"] = schema.BoolAttribute{
		MarkdownDescription: "Whether the schedule fires. Defaults to `true`.",
		Optional:            true,
		Computed:            true,
		Default:             booldefault.StaticBool(true),
	}
	attributes["next_fire_at"] = schema.StringAttribute{
		MarkdownDescription: "When the schedule next fires.",
		Computed:            true,
	}
	response.Schema = schema.Schema{
		MarkdownDescription: "A cron schedule that starts a repository's Ankra pipeline. The counterpart of `ankra pipeline schedules create`, " +
			"`update` and `delete`, and of the same commands under `ankra application pipeline`.",
		Attributes: attributes,
	}
}

func (scheduleResource *pipelineScheduleResource) ValidateConfig(ctx context.Context, request resource.ValidateConfigRequest, response *resource.ValidateConfigResponse) {
	var model pipelineScheduleModel
	response.Diagnostics.Append(request.Config.Get(ctx, &model)...)
	if response.Diagnostics.HasError() {
		return
	}
	pipelineValidateSelector(model.ApplicationID, model.RepositoryID, &response.Diagnostics)
}

// pipelineCronEquivalent reports whether two cron expressions store the same,
// after the platform lower-cases, expands macros and single-spaces them.
func pipelineCronEquivalent(left string, right string) bool {
	return pipelineCanonicalCron(left) == pipelineCanonicalCron(right)
}

func pipelineScheduleToModel(schedule *client.PipelineSchedule, model *pipelineScheduleModel) {
	model.ID = types.StringValue(schedule.ID)
	if model.Cron.IsNull() || model.Cron.IsUnknown() || !pipelineCronEquivalent(model.Cron.ValueString(), schedule.Cron) {
		model.Cron = types.StringValue(schedule.Cron)
	}
	model.Timezone = types.StringValue(schedule.Timezone)
	model.Ref = optionalString(schedule.Ref)
	model.Inputs = applicationMapValue(schedule.Inputs)
	model.Enabled = types.BoolValue(schedule.Enabled)
	model.NextFireAt = types.StringPointerValue(schedule.NextFireAt)
}

func pipelineScheduleInputs(ctx context.Context, model *pipelineScheduleModel, diagnostics *diag.Diagnostics) map[string]string {
	inputs := applicationStringMap(ctx, model.Inputs, diagnostics)
	if inputs == nil {
		inputs = map[string]string{}
	}
	return inputs
}

func (scheduleResource *pipelineScheduleResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	apiClient := scheduleResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var plan pipelineScheduleModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}
	inputs := pipelineScheduleInputs(ctx, &plan, &response.Diagnostics)
	if response.Diagnostics.HasError() {
		return
	}
	schedule, err := apiClient.CreatePipelineSchedule(ctx, pipelineSelectorFrom(plan.ApplicationID, plan.RepositoryID), client.PipelineScheduleCreate{
		Cron:     plan.Cron.ValueString(),
		Timezone: plan.Timezone.ValueString(),
		Ref:      plan.Ref.ValueString(),
		Inputs:   inputs,
		Enabled:  plan.Enabled.ValueBool(),
	})
	if err != nil {
		response.Diagnostics.AddError("Unable to create pipeline schedule", err.Error())
		return
	}
	pipelineScheduleToModel(schedule, &plan)
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
	response.Diagnostics.Append(trackScope(ctx, scheduleResource.client, response.Private)...)
}

func (scheduleResource *pipelineScheduleResource) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	apiClient := scheduleResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state pipelineScheduleModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	schedule, err := apiClient.GetPipelineSchedule(ctx, pipelineSelectorFrom(state.ApplicationID, state.RepositoryID), state.ID.ValueString())
	if removeWhenNotFound(ctx, err, scheduleResource.client, response) {
		return
	}
	if err != nil {
		response.Diagnostics.AddError("Unable to read pipeline schedule", err.Error())
		return
	}
	pipelineScheduleToModel(schedule, &state)
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
	response.Diagnostics.Append(trackScope(ctx, scheduleResource.client, response.Private)...)
}

func (scheduleResource *pipelineScheduleResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	apiClient := scheduleResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var plan, state pipelineScheduleModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	inputs := pipelineScheduleInputs(ctx, &plan, &response.Diagnostics)
	if response.Diagnostics.HasError() {
		return
	}
	schedule, err := apiClient.UpdatePipelineSchedule(ctx, pipelineSelectorFrom(state.ApplicationID, state.RepositoryID), state.ID.ValueString(), client.PipelineScheduleUpdate{
		Cron:     plan.Cron.ValueString(),
		Timezone: plan.Timezone.ValueString(),
		Ref:      plan.Ref.ValueString(),
		Inputs:   inputs,
		Enabled:  plan.Enabled.ValueBool(),
	})
	if err != nil {
		response.Diagnostics.AddError("Unable to update pipeline schedule", err.Error())
		return
	}
	pipelineScheduleToModel(schedule, &plan)
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
}

func (scheduleResource *pipelineScheduleResource) Delete(ctx context.Context, request resource.DeleteRequest, response *resource.DeleteResponse) {
	apiClient := scheduleResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state pipelineScheduleModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	err := apiClient.DeletePipelineSchedule(ctx, pipelineSelectorFrom(state.ApplicationID, state.RepositoryID), state.ID.ValueString())
	if err == nil || deletedWhenNotFound(ctx, err, apiClient, request.Private, &response.Diagnostics) || response.Diagnostics.HasError() {
		return
	}
	response.Diagnostics.AddError("Unable to delete pipeline schedule", err.Error())
}

// ImportState imports by `application/<application_id>/<schedule_id>` or
// `repository/<repository_id>/<schedule_id>`.
func (scheduleResource *pipelineScheduleResource) ImportState(ctx context.Context, request resource.ImportStateRequest, response *resource.ImportStateResponse) {
	parts, err := applicationSplitImportID(request.ID, "application|repository", "id", "schedule_id")
	if err != nil {
		response.Diagnostics.AddError("Invalid import id", err.Error())
		return
	}
	pipelineSelectorImport(ctx, parts[0], parts[1], response)
	response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("id"), parts[2])...)
}
