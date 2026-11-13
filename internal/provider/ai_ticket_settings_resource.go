// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/setplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"ankra.io/terraform-provider-ankra/internal/client"
)

const aiTicketSettingsID = "ai_ticket_settings"

var (
	_ resource.Resource                = (*aiTicketSettingsResource)(nil)
	_ resource.ResourceWithConfigure   = (*aiTicketSettingsResource)(nil)
	_ resource.ResourceWithImportState = (*aiTicketSettingsResource)(nil)
)

type aiTicketSettingsResource struct {
	configuredClient
}

type aiTicketSettingsModel struct {
	ID                        types.String `tfsdk:"id"`
	SlackChannelID            types.String `tfsdk:"slack_channel_id"`
	TeamsConversationID       types.String `tfsdk:"teams_conversation_id"`
	ReviewerAgentTaskID       types.String `tfsdk:"reviewer_agent_task_id"`
	RequireAgentReview        types.Bool   `tfsdk:"require_agent_review"`
	AutoFileInsightSeverities types.Set    `tfsdk:"auto_file_insight_severities"`
	NotifyMode                types.String `tfsdk:"notify_mode"`
	NotifyMinPriority         types.String `tfsdk:"notify_min_priority"`
	NotifySourceKinds         types.Set    `tfsdk:"notify_source_kinds"`
	NotifyEventScope          types.String `tfsdk:"notify_event_scope"`
	NotifyClusterEnvironments types.Set    `tfsdk:"notify_cluster_environments"`
}

// NewAITicketSettingsResource returns the ankra_ai_ticket_settings resource,
// the counterpart of `ankra tickets settings`.
func NewAITicketSettingsResource() resource.Resource {
	return &aiTicketSettingsResource{}
}

func (settings *aiTicketSettingsResource) Metadata(_ context.Context, request resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_ai_ticket_settings"
}

func aiTicketOptionalString(description string) schema.StringAttribute {
	return schema.StringAttribute{
		MarkdownDescription: description,
		Optional:            true,
		Computed:            true,
		PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
	}
}

// settingsComputedString is an optional attribute of a settings singleton
// that reports the stored value when unset. It plans unknown on every
// update, so a change made by others meanwhile is read back, not reverted.
func settingsComputedString(description string) schema.StringAttribute {
	return schema.StringAttribute{MarkdownDescription: description, Optional: true, Computed: true}
}

// settingsComputedSet is settingsComputedString for a set of strings.
func settingsComputedSet(description string) schema.SetAttribute {
	return schema.SetAttribute{MarkdownDescription: description, Optional: true, Computed: true, ElementType: types.StringType}
}

func aiTicketOptionalSet(description string) schema.SetAttribute {
	return schema.SetAttribute{
		MarkdownDescription: description,
		Optional:            true,
		Computed:            true,
		ElementType:         types.StringType,
		PlanModifiers:       []planmodifier.Set{setplanmodifier.UseStateForUnknown()},
	}
}

func (settings *aiTicketSettingsResource) Schema(_ context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: "The organisation's AI ticket desk settings (`ankra tickets settings`): the Slack channel and Microsoft Teams " +
			"conversation tickets are announced in, the reviewing agent, which insight severities are filed automatically, and the " +
			"channel notification policy. There is one per organisation. Only the attributes set in configuration are written; an " +
			"unset attribute keeps the stored value (or the default) and reports it. Changing the settings needs an organisation " +
			"admin. Destroy leaves the settings as they are.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Always `ai_ticket_settings`.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"slack_channel_id":       settingsComputedString("Slack channel tickets are announced in."),
			"teams_conversation_id":  settingsComputedString("Microsoft Teams conversation tickets are announced in."),
			"reviewer_agent_task_id": settingsComputedString("Agent task that reviews tickets."),
			"require_agent_review": schema.BoolAttribute{
				MarkdownDescription: "Whether a ticket needs the reviewing agent's review.",
				Optional:            true,
				Computed:            true,
			},
			"auto_file_insight_severities": settingsComputedSet("Insight severities filed as tickets automatically."),
			"notify_mode":                  settingsComputedString("When the channels are notified about tickets."),
			"notify_min_priority":          settingsComputedString("Lowest ticket priority the channels are notified about."),
			"notify_source_kinds":          settingsComputedSet("Ticket sources the channels are notified about."),
			"notify_event_scope":           settingsComputedString("Which ticket events the channels are notified about."),
			"notify_cluster_environments":  settingsComputedSet("Cluster environments whose tickets the channels are notified about."),
		},
	}
}

func (settings *aiTicketSettingsResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	var plan aiTicketSettingsModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if settings.write(ctx, &plan, request.Config, &response.Diagnostics) {
		response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
		response.Diagnostics.Append(trackScope(ctx, settings.client, response.Private)...)
	}
}

func (settings *aiTicketSettingsResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	var plan aiTicketSettingsModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if settings.write(ctx, &plan, request.Config, &response.Diagnostics) {
		response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
	}
}

func (settings *aiTicketSettingsResource) write(ctx context.Context, plan *aiTicketSettingsModel, config tfsdk.Config, diagnostics *diag.Diagnostics) bool {
	apiClient := settings.requireOrganisationClient(diagnostics)
	if apiClient == nil || diagnostics.HasError() {
		return false
	}
	var configured aiTicketSettingsModel
	diagnostics.Append(config.Get(ctx, &configured)...)
	update := client.AITicketDeskSettingsUpdate{}
	aiTicketPutString(update, "slack_channel_id", configured.SlackChannelID)
	aiTicketPutString(update, "teams_conversation_id", configured.TeamsConversationID)
	aiTicketPutString(update, "reviewer_agent_task_id", configured.ReviewerAgentTaskID)
	if flag := boolPointer(configured.RequireAgentReview); flag != nil {
		update["require_agent_review"] = *flag
	}
	aiTicketPutSet(ctx, update, "auto_file_insight_severities", configured.AutoFileInsightSeverities, diagnostics)
	aiTicketPutString(update, "notify_mode", configured.NotifyMode)
	aiTicketPutString(update, "notify_min_priority", configured.NotifyMinPriority)
	aiTicketPutSet(ctx, update, "notify_source_kinds", configured.NotifySourceKinds, diagnostics)
	aiTicketPutString(update, "notify_event_scope", configured.NotifyEventScope)
	aiTicketPutSet(ctx, update, "notify_cluster_environments", configured.NotifyClusterEnvironments, diagnostics)
	if diagnostics.HasError() {
		return false
	}
	stored, updateError := apiClient.UpdateAITicketDeskSettings(ctx, update)
	if updateError != nil {
		diagnostics.AddError("Unable to update the AI ticket settings", updateError.Error())
		return false
	}
	aiTicketSettingsApply(plan, stored)
	return true
}

func (settings *aiTicketSettingsResource) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	apiClient := settings.requireOrganisationClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state aiTicketSettingsModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	stored, readError := apiClient.GetAITicketDeskSettings(ctx)
	if readError != nil {
		response.Diagnostics.AddError("Unable to read the AI ticket settings", readError.Error())
		return
	}
	aiTicketSettingsApply(&state, stored)
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
	response.Diagnostics.Append(trackScope(ctx, apiClient, response.Private)...)
}

// Delete leaves the settings as they are: the platform has no route that
// removes them, and writing defaults back would silence the channels the
// organisation relies on.
func (settings *aiTicketSettingsResource) Delete(_ context.Context, _ resource.DeleteRequest, _ *resource.DeleteResponse) {
}

// ImportState accepts any id: there is one settings object per organisation.
func (settings *aiTicketSettingsResource) ImportState(ctx context.Context, _ resource.ImportStateRequest, response *resource.ImportStateResponse) {
	response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("id"), aiTicketSettingsID)...)
}

func aiTicketPutString(update map[string]any, key string, value types.String) {
	if text := stringPointer(value); text != nil {
		update[key] = *text
	}
}

func aiTicketPutSet(ctx context.Context, update map[string]any, key string, value types.Set, diagnostics *diag.Diagnostics) {
	if value.IsNull() || value.IsUnknown() {
		return
	}
	update[key] = organisationNonNilStrings(organisationStringsFromSet(ctx, value, diagnostics))
}

func aiTicketSettingsApply(model *aiTicketSettingsModel, stored *client.AITicketDeskSettings) {
	model.ID = types.StringValue(aiTicketSettingsID)
	model.SlackChannelID = types.StringPointerValue(stored.SlackChannelID)
	model.TeamsConversationID = types.StringPointerValue(stored.TeamsConversationID)
	model.ReviewerAgentTaskID = types.StringPointerValue(stored.ReviewerAgentTaskID)
	model.RequireAgentReview = types.BoolValue(stored.RequireAgentReview)
	model.AutoFileInsightSeverities = organisationSetFromStrings(organisationNonNilStrings(stored.AutoFileInsightSeverities))
	model.NotifyMode = types.StringValue(stored.NotifyMode)
	model.NotifyMinPriority = types.StringValue(stored.NotifyMinPriority)
	model.NotifySourceKinds = organisationSetFromStrings(organisationNonNilStrings(stored.NotifySourceKinds))
	model.NotifyEventScope = types.StringValue(stored.NotifyEventScope)
	model.NotifyClusterEnvironments = organisationSetFromStrings(organisationNonNilStrings(stored.NotifyClusterEnvironments))
}
