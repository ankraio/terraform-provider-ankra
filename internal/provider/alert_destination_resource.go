// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"ankra.io/terraform-provider-ankra/internal/client"
)

var (
	_ resource.Resource                   = (*alertDestinationResource)(nil)
	_ resource.ResourceWithConfigure      = (*alertDestinationResource)(nil)
	_ resource.ResourceWithImportState    = (*alertDestinationResource)(nil)
	_ resource.ResourceWithValidateConfig = (*alertDestinationResource)(nil)
)

type alertDestinationResource struct {
	configuredClient
}

type alertDestinationModel struct {
	ID              types.String `tfsdk:"id"`
	Name            types.String `tfsdk:"name"`
	IntegrationType types.String `tfsdk:"integration_type"`
	URL             types.String `tfsdk:"url"`
	ChannelID       types.String `tfsdk:"channel_id"`
	ChannelName     types.String `tfsdk:"channel_name"`
	TeamsTenantID   types.String `tfsdk:"teams_tenant_id"`
	Description     types.String `tfsdk:"description"`
	Template        types.String `tfsdk:"template"`
	Enabled         types.Bool   `tfsdk:"enabled"`
}

// NewAlertDestinationResource returns the ankra_alert_destination resource,
// the counterpart of `ankra alerts destinations create|update|delete`.
func NewAlertDestinationResource() resource.Resource {
	return &alertDestinationResource{}
}

func (destinationResource *alertDestinationResource) Metadata(_ context.Context, request resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_alert_destination"
}

func (destinationResource *alertDestinationResource) Schema(_ context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: "An alert destination: a webhook URL (Slack, Teams, Discord, PagerDuty or a custom endpoint), or a Slack or Teams channel the Ankra bot posts to. " +
			"Alerts and notification routes (`ankra_alert_route`) deliver to it. The counterpart of `ankra alerts destinations create|update|delete`. " +
			"Set exactly one of `url` and `channel_id`.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Identifier of the destination; what `ankra_alert_route.destination_id` references.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				MarkdownDescription: "Destination name, unique in the organisation.",
				Required:            true,
			},
			"integration_type": schema.StringAttribute{
				MarkdownDescription: "Receiver kind: `slack`, `teams`, `discord`, `pagerduty` or `custom`. Defaults to `slack`. Changing it replaces the destination.",
				Optional:            true,
				Computed:            true,
				Default:             stringdefault.StaticString("slack"),
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"url": schema.StringAttribute{
				MarkdownDescription: "Webhook URL, which usually embeds a secret token. A change made outside Terraform is detected while the platform still returns the URL; a destination whose URL moved to Vault returns none, and then drift is not detected.",
				Optional:            true,
				Sensitive:           true,
			},
			"channel_id": schema.StringAttribute{
				MarkdownDescription: "Slack or Teams channel the Ankra bot posts to, instead of a webhook URL. List the candidates with `ankra alerts destinations channels`.",
				Optional:            true,
			},
			"channel_name": schema.StringAttribute{
				MarkdownDescription: "Display name of `channel_id`. Only stored alongside a channel.",
				Optional:            true,
			},
			"teams_tenant_id": schema.StringAttribute{
				MarkdownDescription: "Microsoft Teams tenant of a Teams channel destination; required when `integration_type` is `teams` and `channel_id` is set.",
				Optional:            true,
			},
			"description": schema.StringAttribute{
				MarkdownDescription: "Free-text description.",
				Optional:            true,
			},
			"template": schema.StringAttribute{
				MarkdownDescription: "Payload template; unset uses the platform's default payload for the receiver.",
				Optional:            true,
			},
			"enabled": schema.BoolAttribute{
				MarkdownDescription: "Whether alerts are delivered to the destination. Defaults to `true`.",
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(true),
			},
		},
	}
}

func (destinationResource *alertDestinationResource) ValidateConfig(ctx context.Context, request resource.ValidateConfigRequest, response *resource.ValidateConfigResponse) {
	var configuration alertDestinationModel
	response.Diagnostics.Append(request.Config.Get(ctx, &configuration)...)
	if response.Diagnostics.HasError() || configuration.URL.IsUnknown() || configuration.ChannelID.IsUnknown() {
		return
	}
	hasURL := !configuration.URL.IsNull()
	hasChannel := !configuration.ChannelID.IsNull()
	if hasURL == hasChannel {
		response.Diagnostics.AddAttributeError(path.Root("url"), "Invalid alert destination",
			"Set exactly one of url (a webhook destination) and channel_id (a channel the Ankra bot posts to).")
	}
	if hasChannel && configuration.IntegrationType.ValueString() == "teams" && configuration.TeamsTenantID.IsNull() {
		response.Diagnostics.AddAttributeError(path.Root("teams_tenant_id"), "Invalid alert destination",
			"A Teams channel destination needs teams_tenant_id.")
	}
	if !hasChannel && (!configuration.ChannelName.IsNull() || !configuration.TeamsTenantID.IsNull()) {
		response.Diagnostics.AddAttributeError(path.Root("channel_name"), "Invalid alert destination",
			"channel_name and teams_tenant_id only apply to a channel destination (channel_id).")
	}
}

func (destinationResource *alertDestinationResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	apiClient := destinationResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var plan alertDestinationModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}

	destinationID, err := apiClient.CreateAlertDestination(ctx, client.AlertDestinationCreate{
		Name:            plan.Name.ValueString(),
		URL:             stringPointer(plan.URL),
		ChannelID:       stringPointer(plan.ChannelID),
		ChannelName:     stringPointer(plan.ChannelName),
		TeamsTenantID:   stringPointer(plan.TeamsTenantID),
		IntegrationType: stringPointer(plan.IntegrationType),
		Description:     stringPointer(plan.Description),
		Template:        stringPointer(plan.Template),
		Enabled:         boolPointer(plan.Enabled),
	})
	if err != nil {
		response.Diagnostics.AddError("Unable to create alert destination", err.Error())
		return
	}
	plan.ID = types.StringValue(destinationID)
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
	response.Diagnostics.Append(trackScope(ctx, apiClient, response.Private)...)
}

func (destinationResource *alertDestinationResource) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	apiClient := destinationResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state alertDestinationModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}

	destination, err := apiClient.GetAlertDestination(ctx, state.ID.ValueString())
	if removeWhenNotFound(ctx, err, apiClient, response) {
		return
	}
	if err != nil {
		response.Diagnostics.AddError("Unable to read alert destination", err.Error())
		return
	}

	state.Name = types.StringValue(destination.Name)
	state.IntegrationType = types.StringValue(destination.IntegrationType)
	state.ChannelID = optionalString(alertDNSStringValue(destination.ChannelID))
	state.ChannelName = optionalString(alertDNSStringValue(destination.ChannelName))
	state.TeamsTenantID = optionalString(alertDNSStringValue(destination.TeamsTenantID))
	state.Description = optionalString(alertDNSStringValue(destination.Description))
	state.Template = optionalString(alertDNSStringValue(destination.Template))
	state.Enabled = types.BoolValue(destination.Enabled)
	if storedURL := alertDNSStringValue(destination.URL); storedURL != "" && storedURL != client.AlertDestinationMaskedURL {
		state.URL = types.StringValue(storedURL)
	} else if !state.ChannelID.IsNull() {
		state.URL = types.StringNull()
	}
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
	response.Diagnostics.Append(trackScope(ctx, apiClient, response.Private)...)
}

func (destinationResource *alertDestinationResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	apiClient := destinationResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var plan, state alertDestinationModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}

	if err := apiClient.UpdateAlertDestination(ctx, plan.ID.ValueString(), alertDestinationUpdateFromPlan(plan, state)); err != nil {
		response.Diagnostics.AddError("Unable to update alert destination", err.Error())
		return
	}
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
	response.Diagnostics.Append(trackScope(ctx, apiClient, response.Private)...)
}

// alertDestinationUpdateFromPlan states every member, because the platform
// keeps an absent or null one: a removed optional value is sent as an empty
// string, which clears it, and an empty channel_id moves a channel
// destination back to its URL. The URL is the exception: it is sent only when
// it changed, because the platform treats any URL it receives as new, drops
// the Vault path a migrated destination keeps its URL under, and stores the
// URL in plain text again.
func alertDestinationUpdateFromPlan(plan, state alertDestinationModel) client.AlertDestinationUpdate {
	empty := ""
	clearedWhenNull := func(value types.String) *string {
		if pointer := stringPointer(value); pointer != nil {
			return pointer
		}
		return &empty
	}
	var changedURL *string
	if !plan.URL.Equal(state.URL) {
		changedURL = stringPointer(plan.URL)
	}
	return client.AlertDestinationUpdate{
		Name:          stringPointer(plan.Name),
		URL:           changedURL,
		ChannelID:     clearedWhenNull(plan.ChannelID),
		ChannelName:   stringPointer(plan.ChannelName),
		TeamsTenantID: clearedWhenNull(plan.TeamsTenantID),
		Description:   clearedWhenNull(plan.Description),
		Template:      clearedWhenNull(plan.Template),
		Enabled:       boolPointer(plan.Enabled),
	}
}

func (destinationResource *alertDestinationResource) Delete(ctx context.Context, request resource.DeleteRequest, response *resource.DeleteResponse) {
	apiClient := destinationResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state alertDestinationModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}

	if err := apiClient.DeleteAlertDestination(ctx, state.ID.ValueString()); err != nil {
		response.Diagnostics.AddError("Unable to delete alert destination", err.Error())
	}
}

// ImportState imports by destination id.
func (destinationResource *alertDestinationResource) ImportState(ctx context.Context, request resource.ImportStateRequest, response *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), request, response)
}

// alertDNSStringValue dereferences an optional API string.
func alertDNSStringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
