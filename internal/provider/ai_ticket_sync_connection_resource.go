// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/mapplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"ankra.io/terraform-provider-ankra/internal/client"
)

var aiTicketSyncProviders = []string{"jira", "linear", "webhook"}

var (
	_ resource.Resource                   = (*aiTicketSyncConnectionResource)(nil)
	_ resource.ResourceWithConfigure      = (*aiTicketSyncConnectionResource)(nil)
	_ resource.ResourceWithImportState    = (*aiTicketSyncConnectionResource)(nil)
	_ resource.ResourceWithValidateConfig = (*aiTicketSyncConnectionResource)(nil)
)

type aiTicketSyncConnectionResource struct {
	configuredClient
}

type aiTicketSyncConnectionModel struct {
	ID                   types.String `tfsdk:"id"`
	Name                 types.String `tfsdk:"name"`
	Provider             types.String `tfsdk:"provider_name"`
	Config               types.Map    `tfsdk:"config"`
	CredentialWO         types.Map    `tfsdk:"credential_wo"`
	CredentialWOVersion  types.Int64  `tfsdk:"credential_wo_version"`
	Enabled              types.Bool   `tfsdk:"enabled"`
	SyncKinds            types.Set    `tfsdk:"sync_kinds"`
	SyncMinPriority      types.String `tfsdk:"sync_min_priority"`
	SyncEventScope       types.String `tfsdk:"sync_event_scope"`
	CredentialConfigured types.Bool   `tfsdk:"credential_configured"`
	LastError            types.String `tfsdk:"last_error"`
}

// NewAITicketSyncConnectionResource returns the
// ankra_ai_ticket_sync_connection resource, the counterpart of
// `ankra tickets sync connections`.
func NewAITicketSyncConnectionResource() resource.Resource {
	return &aiTicketSyncConnectionResource{}
}

func (connection *aiTicketSyncConnectionResource) Metadata(_ context.Context, request resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_ai_ticket_sync_connection"
}

func (connection *aiTicketSyncConnectionResource) Schema(_ context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: "An outbound AI ticket sync connection (`ankra tickets sync connections`) that mirrors the organisation's " +
			"tickets to Jira, Linear or a generic webhook. The tracker credential is write-only (Terraform 1.11 or later): it is " +
			"sent to the platform, stored in its secret store and never returned, so it never reaches plan or state; bump " +
			"`credential_wo_version` to send a new one. Destroy deletes the connection and its stored credential. Needs an " +
			"organisation admin.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Identifier of the connection.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				MarkdownDescription: "Connection name, unique in the organisation.",
				Required:            true,
			},
			"provider_name": schema.StringAttribute{
				MarkdownDescription: "Tracker: `jira`, `linear` or `webhook`. Changing it replaces the connection.",
				Required:            true,
				Validators:          []validator.String{organisationStringOneOf{values: aiTicketSyncProviders}},
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"config": schema.MapAttribute{
				MarkdownDescription: "The tracker's non-secret settings, such as the Jira site and project or the Linear team. The platform keeps only the keys the tracker knows.",
				Optional:            true,
				Computed:            true,
				ElementType:         types.StringType,
				PlanModifiers:       []planmodifier.Map{mapplanmodifier.UseStateForUnknown()},
			},
			"credential_wo": schema.MapAttribute{
				MarkdownDescription: "The tracker credential, such as an API token, as a write-only attribute. Required by trackers that need one.",
				Optional:            true,
				Sensitive:           true,
				WriteOnly:           true,
				ElementType:         types.StringType,
			},
			"credential_wo_version": schema.Int64Attribute{
				MarkdownDescription: "Version of `credential_wo`. Changing it sends `credential_wo` again, which replaces the stored credential. Required with `credential_wo`.",
				Optional:            true,
			},
			"enabled": schema.BoolAttribute{
				MarkdownDescription: "Whether tickets are mirrored.",
				Optional:            true,
				Computed:            true,
				PlanModifiers:       []planmodifier.Bool{boolplanmodifier.UseStateForUnknown()},
			},
			"sync_kinds":        aiTicketOptionalSet("Ticket kinds mirrored."),
			"sync_min_priority": aiTicketOptionalString("Lowest priority mirrored."),
			"sync_event_scope":  aiTicketOptionalString("Which ticket events are mirrored."),
			"credential_configured": schema.BoolAttribute{
				MarkdownDescription: "Whether the platform holds a credential for the connection.",
				Computed:            true,
			},
			"last_error": schema.StringAttribute{
				MarkdownDescription: "The last sync error; null when the last sync succeeded.",
				Computed:            true,
			},
		},
	}
}

func (connection *aiTicketSyncConnectionResource) ValidateConfig(ctx context.Context, request resource.ValidateConfigRequest, response *resource.ValidateConfigResponse) {
	var config aiTicketSyncConnectionModel
	response.Diagnostics.Append(request.Config.Get(ctx, &config)...)
	if response.Diagnostics.HasError() || config.CredentialWO.IsUnknown() {
		return
	}
	if !config.CredentialWO.IsNull() && config.CredentialWOVersion.IsNull() {
		response.Diagnostics.AddAttributeError(path.Root("credential_wo_version"), "Missing credential_wo_version",
			"credential_wo is never stored, so Terraform needs credential_wo_version to know when to send it again.")
	}
}

func (connection *aiTicketSyncConnectionResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	apiClient := connection.requireOrganisationClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var plan aiTicketSyncConnectionModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	write := aiTicketSyncConnectionWrite(ctx, plan, request.Config, true, &response.Diagnostics)
	if response.Diagnostics.HasError() {
		return
	}
	write.Provider = plan.Provider.ValueString()
	created, createError := apiClient.CreateAITicketSyncConnection(ctx, write)
	if createError != nil {
		response.Diagnostics.AddError("Unable to create the ticket sync connection", createError.Error())
		return
	}
	aiTicketSyncConnectionApply(&plan, created)
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
	response.Diagnostics.Append(trackScope(ctx, apiClient, response.Private)...)
}

func (connection *aiTicketSyncConnectionResource) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	apiClient := connection.requireOrganisationClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state aiTicketSyncConnectionModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	found, readError := apiClient.GetAITicketSyncConnection(ctx, state.ID.ValueString())
	if removeWhenNotFound(ctx, readError, apiClient, response) {
		return
	}
	if readError != nil {
		response.Diagnostics.AddError("Unable to read the ticket sync connection", readError.Error())
		return
	}
	state.Config = stringMapFromAny(state.Config, found.Config)
	aiTicketSyncConnectionApply(&state, found)
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
	response.Diagnostics.Append(trackScope(ctx, apiClient, response.Private)...)
}

func (connection *aiTicketSyncConnectionResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	apiClient := connection.requireOrganisationClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var plan, state aiTicketSyncConnectionModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	isCredentialSent := !plan.CredentialWOVersion.Equal(state.CredentialWOVersion)
	write := aiTicketSyncConnectionWrite(ctx, plan, request.Config, isCredentialSent, &response.Diagnostics)
	if response.Diagnostics.HasError() {
		return
	}
	updated, updateError := apiClient.UpdateAITicketSyncConnection(ctx, state.ID.ValueString(), write)
	if updateError != nil {
		response.Diagnostics.AddError("Unable to update the ticket sync connection", updateError.Error())
		return
	}
	aiTicketSyncConnectionApply(&plan, updated)
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
}

func (connection *aiTicketSyncConnectionResource) Delete(ctx context.Context, request resource.DeleteRequest, response *resource.DeleteResponse) {
	apiClient := connection.requireOrganisationClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state aiTicketSyncConnectionModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	deleteError := apiClient.DeleteAITicketSyncConnection(ctx, state.ID.ValueString())
	if deleteError == nil || deletedWhenNotFound(ctx, deleteError, apiClient, request.Private, &response.Diagnostics) || response.Diagnostics.HasError() {
		return
	}
	response.Diagnostics.AddError("Unable to delete the ticket sync connection", deleteError.Error())
}

// ImportState imports by connection id. The credential is never read back;
// set credential_wo_version to send one.
func (connection *aiTicketSyncConnectionResource) ImportState(ctx context.Context, request resource.ImportStateRequest, response *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), request, response)
}

func aiTicketSyncConnectionWrite(ctx context.Context, plan aiTicketSyncConnectionModel, config tfsdk.Config, isCredentialSent bool, diagnostics *diag.Diagnostics) client.AITicketSyncConnectionWrite {
	write := client.AITicketSyncConnectionWrite{
		Name:            stringPointer(plan.Name),
		Config:          applicationStringMap(ctx, plan.Config, diagnostics),
		Enabled:         boolPointer(plan.Enabled),
		SyncMinPriority: stringPointer(plan.SyncMinPriority),
		SyncEventScope:  stringPointer(plan.SyncEventScope),
	}
	if !plan.SyncKinds.IsNull() && !plan.SyncKinds.IsUnknown() {
		write.SyncKinds = organisationNonNilStrings(organisationStringsFromSet(ctx, plan.SyncKinds, diagnostics))
	}
	if isCredentialSent {
		write.Credential = writeOnlyCredentialMap(ctx, config, diagnostics)
	}
	return write
}

// aiTicketSyncConnectionApply records what the platform answered. config
// keeps the configured value after a write; a read refreshes it first.
func aiTicketSyncConnectionApply(model *aiTicketSyncConnectionModel, found *client.AITicketSyncConnection) {
	model.ID = types.StringValue(found.ID)
	model.Name = types.StringValue(found.Name)
	model.Provider = types.StringValue(found.Provider)
	if model.Config.IsUnknown() {
		model.Config = stringMapFromAny(types.MapNull(types.StringType), found.Config)
	}
	model.Enabled = types.BoolValue(found.Enabled)
	model.SyncKinds = organisationSetFromStrings(organisationNonNilStrings(found.SyncKinds))
	model.SyncMinPriority = types.StringValue(found.SyncMinPriority)
	model.SyncEventScope = types.StringValue(found.SyncEventScope)
	model.CredentialConfigured = types.BoolValue(found.CredentialConfigured)
	model.LastError = optionalStringFromPointer(found.LastError)
}
