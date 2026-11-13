// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"ankra.io/terraform-provider-ankra/internal/client"
)

var (
	_ resource.Resource                     = (*aiTicketProjectChannelsResource)(nil)
	_ resource.ResourceWithConfigure        = (*aiTicketProjectChannelsResource)(nil)
	_ resource.ResourceWithImportState      = (*aiTicketProjectChannelsResource)(nil)
	_ resource.ResourceWithConfigValidators = (*aiTicketProjectChannelsResource)(nil)
)

type aiTicketProjectChannelsResource struct {
	configuredClient
}

type aiTicketProjectChannelsModel struct {
	ID                  types.String `tfsdk:"id"`
	ProjectID           types.String `tfsdk:"project_id"`
	SlackChannelID      types.String `tfsdk:"slack_channel_id"`
	TeamsConversationID types.String `tfsdk:"teams_conversation_id"`
}

// NewAITicketProjectChannelsResource returns the
// ankra_ai_ticket_project_channels resource, the counterpart of
// `ankra tickets settings projects set` and `remove`.
func NewAITicketProjectChannelsResource() resource.Resource {
	return &aiTicketProjectChannelsResource{}
}

func (channels *aiTicketProjectChannelsResource) Metadata(_ context.Context, request resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_ai_ticket_project_channels"
}

func (channels *aiTicketProjectChannelsResource) Schema(_ context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: "The Slack channel and Microsoft Teams conversation one project's AI board threads go to instead of the " +
			"organisation's (`ankra tickets settings projects set`). Set at least one. Destroy sends the project's threads back to " +
			"the organisation's channels. Needs an organisation admin.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "The project id.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"project_id": schema.StringAttribute{
				MarkdownDescription: "Identifier of the project. Changing it replaces the resource.",
				Required:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"slack_channel_id": schema.StringAttribute{
				MarkdownDescription: "Slack channel the project's board threads go to.",
				Optional:            true,
			},
			"teams_conversation_id": schema.StringAttribute{
				MarkdownDescription: "Microsoft Teams conversation the project's board threads go to.",
				Optional:            true,
			},
		},
	}
}

func (channels *aiTicketProjectChannelsResource) ConfigValidators(_ context.Context) []resource.ConfigValidator {
	return []resource.ConfigValidator{atLeastOneOfConfigValidator{attributes: []string{"slack_channel_id", "teams_conversation_id"}}}
}

func (channels *aiTicketProjectChannelsResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	var plan aiTicketProjectChannelsModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if channels.write(ctx, &plan, &response.Diagnostics) {
		response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
		response.Diagnostics.Append(trackScope(ctx, channels.client, response.Private)...)
	}
}

func (channels *aiTicketProjectChannelsResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	var plan aiTicketProjectChannelsModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if channels.write(ctx, &plan, &response.Diagnostics) {
		response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
	}
}

func (channels *aiTicketProjectChannelsResource) write(ctx context.Context, plan *aiTicketProjectChannelsModel, diagnostics *diag.Diagnostics) bool {
	apiClient := channels.requireOrganisationClient(diagnostics)
	if apiClient == nil || diagnostics.HasError() {
		return false
	}
	update := client.AITicketProjectChannelsUpdate{
		SlackChannelID:      stringPointer(plan.SlackChannelID),
		TeamsConversationID: stringPointer(plan.TeamsConversationID),
	}
	if setError := apiClient.SetAITicketProjectChannels(ctx, plan.ProjectID.ValueString(), update); setError != nil {
		diagnostics.AddError("Unable to set the project's ticket channels", setError.Error())
		return false
	}
	plan.ID = plan.ProjectID
	return true
}

func (channels *aiTicketProjectChannelsResource) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	apiClient := channels.requireOrganisationClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state aiTicketProjectChannelsModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	projects, listError := apiClient.ListAITicketProjectChannels(ctx)
	if listError != nil {
		response.Diagnostics.AddError("Unable to read the project ticket channels", listError.Error())
		return
	}
	for _, project := range projects {
		if project.ProjectID != state.ProjectID.ValueString() {
			continue
		}
		state.ID = state.ProjectID
		state.SlackChannelID = types.StringPointerValue(project.SlackChannelID)
		state.TeamsConversationID = types.StringPointerValue(project.TeamsConversationID)
		response.Diagnostics.Append(response.State.Set(ctx, &state)...)
		response.Diagnostics.Append(trackScope(ctx, apiClient, response.Private)...)
		return
	}
	removeWhenTargetChanged(ctx, apiClient, response)
}

func (channels *aiTicketProjectChannelsResource) Delete(ctx context.Context, request resource.DeleteRequest, response *resource.DeleteResponse) {
	apiClient := channels.requireOrganisationClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state aiTicketProjectChannelsModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	removeError := apiClient.RemoveAITicketProjectChannels(ctx, state.ProjectID.ValueString())
	if removeError == nil || deletedWhenNotFound(ctx, removeError, apiClient, request.Private, &response.Diagnostics) || response.Diagnostics.HasError() {
		return
	}
	response.Diagnostics.AddError("Unable to remove the project's ticket channels", removeError.Error())
}

// ImportState imports by project id.
func (channels *aiTicketProjectChannelsResource) ImportState(ctx context.Context, request resource.ImportStateRequest, response *resource.ImportStateResponse) {
	response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("project_id"), request.ID)...)
	response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("id"), request.ID)...)
}

// atLeastOneOfConfigValidator requires at least one of the named top-level
// attributes in configuration.
type atLeastOneOfConfigValidator struct {
	attributes []string
}

func (atLeastOne atLeastOneOfConfigValidator) Description(_ context.Context) string {
	return "at least one of the attributes must be set"
}

func (atLeastOne atLeastOneOfConfigValidator) MarkdownDescription(ctx context.Context) string {
	return atLeastOne.Description(ctx)
}

func (atLeastOne atLeastOneOfConfigValidator) ValidateResource(ctx context.Context, request resource.ValidateConfigRequest, response *resource.ValidateConfigResponse) {
	for _, attribute := range atLeastOne.attributes {
		var value types.String
		response.Diagnostics.Append(request.Config.GetAttribute(ctx, path.Root(attribute), &value)...)
		if response.Diagnostics.HasError() || !value.IsNull() {
			return
		}
	}
	response.Diagnostics.AddError("Missing attribute", "Set at least one of: `"+strings.Join(atLeastOne.attributes, "`, `")+"`.")
}
