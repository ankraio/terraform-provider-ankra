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
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"ankra.io/terraform-provider-ankra/internal/client"
)

var (
	_ resource.Resource                   = (*securityReportScheduleResource)(nil)
	_ resource.ResourceWithConfigure      = (*securityReportScheduleResource)(nil)
	_ resource.ResourceWithImportState    = (*securityReportScheduleResource)(nil)
	_ resource.ResourceWithValidateConfig = (*securityReportScheduleResource)(nil)
)

type securityReportScheduleResource struct {
	configuredClient
}

type securityReportScheduleModel struct {
	ID         types.String `tfsdk:"id"`
	ClusterID  types.String `tfsdk:"cluster_id"`
	Frequency  types.String `tfsdk:"frequency"`
	Recipients types.List   `tfsdk:"recipients"`
	Enabled    types.Bool   `tfsdk:"enabled"`
	LastSentAt types.String `tfsdk:"last_sent_at"`
	NextDueAt  types.String `tfsdk:"next_due_at"`
}

// NewSecurityReportScheduleResource returns the
// ankra_security_report_schedule resource, the counterpart of
// `ankra security report-schedule set`.
func NewSecurityReportScheduleResource() resource.Resource {
	return &securityReportScheduleResource{}
}

func (scheduleResource *securityReportScheduleResource) Metadata(_ context.Context, request resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_security_report_schedule"
}

func (scheduleResource *securityReportScheduleResource) Schema(_ context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: "The recurring security report one cluster emails. A cluster has at most one schedule, so this is a per-cluster settings resource. " +
			"The platform has no delete: destroying the resource stores the schedule paused with no recipients, so no report is sent. " +
			"The counterpart of `ankra security report-schedule set`.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "The cluster id.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"cluster_id": schema.StringAttribute{
				MarkdownDescription: "Cluster the schedule belongs to. Changing it replaces the resource.",
				Required:            true,
				Validators:          []validator.String{securityUUIDValidator{}},
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"frequency": schema.StringAttribute{
				MarkdownDescription: "`weekly` or `monthly`.",
				Required:            true,
				Validators:          []validator.String{securityOneOf("weekly", "monthly")},
			},
			"recipients": schema.ListAttribute{
				MarkdownDescription: "Recipient email addresses, at most 20; at least one while `enabled`.",
				ElementType:         types.StringType,
				Optional:            true,
				Validators:          []validator.List{securityRecipientsValidator{}},
			},
			"enabled": schema.BoolAttribute{
				MarkdownDescription: "Whether reports are sent; `false` stores the schedule paused. Defaults to `true`.",
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(true),
			},
			"last_sent_at": schema.StringAttribute{
				MarkdownDescription: "When the last report was sent.",
				Computed:            true,
			},
			"next_due_at": schema.StringAttribute{
				MarkdownDescription: "When the next report is due.",
				Computed:            true,
			},
		},
	}
}

// ValidateConfig refuses at plan time an enabled schedule without recipients,
// which the platform rejects. `enabled` defaults to true.
func (scheduleResource *securityReportScheduleResource) ValidateConfig(ctx context.Context, request resource.ValidateConfigRequest, response *resource.ValidateConfigResponse) {
	var enabled types.Bool
	var recipients types.List
	response.Diagnostics.Append(request.Config.GetAttribute(ctx, path.Root("enabled"), &enabled)...)
	response.Diagnostics.Append(request.Config.GetAttribute(ctx, path.Root("recipients"), &recipients)...)
	if response.Diagnostics.HasError() || enabled.IsUnknown() || recipients.IsUnknown() {
		return
	}
	isEnabled := enabled.IsNull() || enabled.ValueBool()
	if isEnabled && len(recipients.Elements()) == 0 {
		response.Diagnostics.AddAttributeError(path.Root("recipients"), "Missing recipients",
			"An enabled report schedule needs at least one recipient; add recipients or set enabled = false.")
	}
}

func (scheduleResource *securityReportScheduleResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	apiClient := scheduleResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var plan securityReportScheduleModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}

	status, err := apiClient.SetSecurityReportSchedule(ctx, plan.ClusterID.ValueString(), client.SecurityReportScheduleWrite{
		Frequency:  plan.Frequency.ValueString(),
		Recipients: stringsFromList(ctx, plan.Recipients, &response.Diagnostics),
		Enabled:    plan.Enabled.ValueBool(),
	})
	if err != nil {
		response.Diagnostics.AddError("Unable to set security report schedule", err.Error())
		return
	}
	securityApplyReportSchedule(&plan, status)
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
	response.Diagnostics.Append(trackScope(ctx, apiClient, response.Private)...)
}

func (scheduleResource *securityReportScheduleResource) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	apiClient := scheduleResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state securityReportScheduleModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}

	status, err := apiClient.GetSecurityReportSchedule(ctx, state.ClusterID.ValueString())
	if scopeError := securityClusterScopeError(err, state.ClusterID.ValueString()); scopeError != nil {
		response.Diagnostics.AddError("Cluster not in scope", scopeError.Error())
		return
	}
	if err != nil {
		response.Diagnostics.AddError("Unable to read security report schedule", err.Error())
		return
	}
	if !status.Configured || status.Schedule == nil {
		response.State.RemoveResource(ctx)
		return
	}
	state.Frequency = types.StringValue(status.Schedule.Frequency)
	state.Enabled = types.BoolValue(status.Schedule.Enabled)
	state.Recipients = securityRecipientsList(state.Recipients, status.Schedule.Recipients)
	securityApplyReportSchedule(&state, status)
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
	response.Diagnostics.Append(trackScope(ctx, apiClient, response.Private)...)
}

func (scheduleResource *securityReportScheduleResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	apiClient := scheduleResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var plan securityReportScheduleModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}

	status, err := apiClient.SetSecurityReportSchedule(ctx, plan.ClusterID.ValueString(), client.SecurityReportScheduleWrite{
		Frequency:  plan.Frequency.ValueString(),
		Recipients: stringsFromList(ctx, plan.Recipients, &response.Diagnostics),
		Enabled:    plan.Enabled.ValueBool(),
	})
	if err != nil {
		response.Diagnostics.AddError("Unable to set security report schedule", err.Error())
		return
	}
	securityApplyReportSchedule(&plan, status)
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
	response.Diagnostics.Append(trackScope(ctx, apiClient, response.Private)...)
}

// Delete pauses the schedule and clears its recipients: the platform offers
// no way to remove a schedule.
func (scheduleResource *securityReportScheduleResource) Delete(ctx context.Context, request resource.DeleteRequest, response *resource.DeleteResponse) {
	apiClient := scheduleResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state securityReportScheduleModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}

	_, err := apiClient.SetSecurityReportSchedule(ctx, state.ClusterID.ValueString(), client.SecurityReportScheduleWrite{
		Frequency:  state.Frequency.ValueString(),
		Recipients: []string{},
		Enabled:    false,
	})
	if scopeError := securityClusterScopeError(err, state.ClusterID.ValueString()); scopeError != nil {
		response.Diagnostics.AddError("Cluster not in scope", scopeError.Error())
		return
	}
	if err != nil {
		response.Diagnostics.AddError("Unable to pause security report schedule", err.Error())
	}
}

// securityClusterScopeError reports a 404 from the report schedule routes.
// They answer "Cluster not found" whenever the cluster is outside the
// provider's organisation and project scope, which says nothing about the
// schedule: it keeps sending reports. So a 404 is never read as "gone": it
// is an error, and a cluster that was really deleted is removed with
// `terraform state rm`. It returns nil for any other error.
func securityClusterScopeError(err error, clusterID string) error {
	detail, isNotFound := clusterSettingsNotFoundDetail(err)
	if !isNotFound {
		return nil
	}
	if strings.EqualFold(detail, "cluster not found") {
		return clusterSettingsScopeError(err, clusterID)
	}
	return fmt.Errorf("the platform answered 404 for cluster %s's report schedule, which does not mean the schedule is gone; "+
		"if the cluster was deleted, remove this resource with `terraform state rm`. (%w)", clusterID, err)
}

// ImportState imports by cluster id.
func (scheduleResource *securityReportScheduleResource) ImportState(ctx context.Context, request resource.ImportStateRequest, response *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("cluster_id"), request, response)
}

func securityApplyReportSchedule(model *securityReportScheduleModel, status *client.SecurityReportScheduleStatus) {
	model.ID = model.ClusterID
	model.LastSentAt = types.StringNull()
	model.NextDueAt = types.StringNull()
	if status.Schedule != nil {
		model.LastSentAt = types.StringPointerValue(status.Schedule.LastSentAt)
		model.NextDueAt = types.StringPointerValue(status.Schedule.NextDueAt)
	}
}

// securityRecipientsList keeps an unset recipients attribute null while the
// platform holds none, so a paused schedule without recipients plans no diff.
func securityRecipientsList(current types.List, recipients []string) types.List {
	if len(recipients) == 0 && current.IsNull() {
		return types.ListNull(types.StringType)
	}
	if recipients == nil {
		recipients = []string{}
	}
	return listFromStrings(recipients)
}
