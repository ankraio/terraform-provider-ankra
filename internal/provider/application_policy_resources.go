// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"encoding/json"

	"github.com/hashicorp/terraform-plugin-framework-jsontypes/jsontypes"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var applicationProvenanceModes = []string{"off", "warn", "enforce"}

var (
	_ resource.ResourceWithImportState = (*applicationDatabaseBackupResource)(nil)
	_ resource.ResourceWithImportState = (*applicationProvenancePolicyResource)(nil)
	_ resource.ResourceWithImportState = (*pipelineWorkspaceProfilesResource)(nil)
	_ resource.ResourceWithConfigure   = (*applicationDatabaseBackupResource)(nil)
	_ resource.ResourceWithConfigure   = (*applicationProvenancePolicyResource)(nil)
	_ resource.ResourceWithConfigure   = (*pipelineWorkspaceProfilesResource)(nil)
)

// applicationSettingIDAttributes are the id and application_id of a
// resource that manages one setting of an application.
func applicationSettingIDAttributes() (schema.StringAttribute, schema.StringAttribute) {
	return schema.StringAttribute{
		MarkdownDescription: "The application id.",
		Computed:            true,
		PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
	}, schema.StringAttribute{
		MarkdownDescription: "Identifier of the application. Changing it replaces the resource.",
		Required:            true,
		PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
	}
}

// importApplicationSetting imports a per-application setting by its
// application id.
func importApplicationSetting(ctx context.Context, request resource.ImportStateRequest, response *resource.ImportStateResponse) {
	response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("application_id"), request.ID)...)
	response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("id"), request.ID)...)
}

type applicationDatabaseBackupResource struct {
	configuredClient
}

type applicationDatabaseBackupModel struct {
	ID             types.String `tfsdk:"id"`
	ApplicationID  types.String `tfsdk:"application_id"`
	Enabled        types.Bool   `tfsdk:"enabled"`
	DatabaseStatus types.String `tfsdk:"database_status"`
}

// NewApplicationDatabaseBackupResource returns the
// ankra_application_database_backup resource, the counterpart of
// `ankra application database-backup set`.
func NewApplicationDatabaseBackupResource() resource.Resource {
	return &applicationDatabaseBackupResource{}
}

func (backup *applicationDatabaseBackupResource) Metadata(_ context.Context, request resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_application_database_backup"
}

func (backup *applicationDatabaseBackupResource) Schema(_ context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	idAttribute, applicationIDAttribute := applicationSettingIDAttributes()
	response.Schema = schema.Schema{
		MarkdownDescription: "An application's database backup default (`ankra application database-backup set`): whether the database " +
			"the application ships is protected by default where it is deployed. Setting it needs `backups.manage`. Destroy leaves " +
			"the default as it is.",
		Attributes: map[string]schema.Attribute{
			"id":             idAttribute,
			"application_id": applicationIDAttribute,
			"enabled": schema.BoolAttribute{
				MarkdownDescription: "Whether the application's database is backed up by default.",
				Required:            true,
			},
			"database_status": schema.StringAttribute{
				MarkdownDescription: "Whether the application ships a database, as the platform detects it.",
				Computed:            true,
			},
		},
	}
}

func (backup *applicationDatabaseBackupResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	var plan applicationDatabaseBackupModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if backup.write(ctx, &plan, &response.Diagnostics) {
		response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
		response.Diagnostics.Append(trackScope(ctx, backup.client, response.Private)...)
	}
}

func (backup *applicationDatabaseBackupResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	var plan applicationDatabaseBackupModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if backup.write(ctx, &plan, &response.Diagnostics) {
		response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
	}
}

func (backup *applicationDatabaseBackupResource) write(ctx context.Context, plan *applicationDatabaseBackupModel, diagnostics *diag.Diagnostics) bool {
	apiClient := backup.requireClient(diagnostics)
	if apiClient == nil || diagnostics.HasError() {
		return false
	}
	stored, setError := apiClient.SetApplicationDatabaseBackup(ctx, plan.ApplicationID.ValueString(), plan.Enabled.ValueBool())
	if setError != nil {
		diagnostics.AddError("Unable to set the database backup default", setError.Error())
		return false
	}
	plan.ID = plan.ApplicationID
	plan.Enabled = types.BoolValue(stored.Enabled)
	plan.DatabaseStatus = types.StringValue(stored.DatabaseStatus)
	return true
}

func (backup *applicationDatabaseBackupResource) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	apiClient := backup.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state applicationDatabaseBackupModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	stored, readError := apiClient.GetApplicationDatabaseBackup(ctx, state.ApplicationID.ValueString())
	if removeWhenNotFound(ctx, readError, apiClient, response) {
		return
	}
	if readError != nil {
		response.Diagnostics.AddError("Unable to read the database backup default", readError.Error())
		return
	}
	state.ID = state.ApplicationID
	state.Enabled = types.BoolValue(stored.Enabled)
	state.DatabaseStatus = types.StringValue(stored.DatabaseStatus)
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
	response.Diagnostics.Append(trackScope(ctx, apiClient, response.Private)...)
}

// Delete leaves the default as it is: turning it off would stop protecting
// a database.
func (backup *applicationDatabaseBackupResource) Delete(_ context.Context, _ resource.DeleteRequest, _ *resource.DeleteResponse) {
}

// ImportState imports by application id.
func (backup *applicationDatabaseBackupResource) ImportState(ctx context.Context, request resource.ImportStateRequest, response *resource.ImportStateResponse) {
	importApplicationSetting(ctx, request, response)
}

type applicationProvenancePolicyResource struct {
	configuredClient
}

type applicationProvenancePolicyModel struct {
	ID            types.String `tfsdk:"id"`
	ApplicationID types.String `tfsdk:"application_id"`
	Mode          types.String `tfsdk:"mode"`
	EffectiveMode types.String `tfsdk:"effective_mode"`
}

// NewApplicationProvenancePolicyResource returns the
// ankra_application_provenance_policy resource, the counterpart of
// `ankra application provenance set`.
func NewApplicationProvenancePolicyResource() resource.Resource {
	return &applicationProvenancePolicyResource{}
}

func (policy *applicationProvenancePolicyResource) Metadata(_ context.Context, request resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_application_provenance_policy"
}

func (policy *applicationProvenancePolicyResource) Schema(_ context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	idAttribute, applicationIDAttribute := applicationSettingIDAttributes()
	response.Schema = schema.Schema{
		MarkdownDescription: "An application's own deploy provenance mode (`ankra application provenance set`), which wins over the " +
			"organisation's default: `off`, `warn` (deploy and file a finding for an image without verified provenance) or " +
			"`enforce` (refuse such a deploy and pin verified images to their digest). Setting it needs `security.manage`. " +
			"Verifying a deploy's provenance stays imperative (`ankra application provenance verify`). Destroy leaves the mode as it is.",
		Attributes: map[string]schema.Attribute{
			"id":             idAttribute,
			"application_id": applicationIDAttribute,
			"mode": schema.StringAttribute{
				MarkdownDescription: "`off`, `warn` or `enforce`.",
				Required:            true,
				Validators:          []validator.String{organisationStringOneOf{values: applicationProvenanceModes}},
			},
			"effective_mode": schema.StringAttribute{
				MarkdownDescription: "The mode the application's deploys use now.",
				Computed:            true,
			},
		},
	}
}

func (policy *applicationProvenancePolicyResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	var plan applicationProvenancePolicyModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if policy.write(ctx, &plan, &response.Diagnostics) {
		response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
		response.Diagnostics.Append(trackScope(ctx, policy.client, response.Private)...)
	}
}

func (policy *applicationProvenancePolicyResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	var plan applicationProvenancePolicyModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if policy.write(ctx, &plan, &response.Diagnostics) {
		response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
	}
}

func (policy *applicationProvenancePolicyResource) write(ctx context.Context, plan *applicationProvenancePolicyModel, diagnostics *diag.Diagnostics) bool {
	apiClient := policy.requireClient(diagnostics)
	if apiClient == nil || diagnostics.HasError() {
		return false
	}
	stored, setError := apiClient.SetApplicationProvenancePolicy(ctx, plan.ApplicationID.ValueString(), stringPointer(plan.Mode))
	if setError != nil {
		diagnostics.AddError("Unable to set the application's provenance mode", setError.Error())
		return false
	}
	plan.ID = plan.ApplicationID
	plan.Mode = types.StringPointerValue(stored.ApplicationMode)
	plan.EffectiveMode = types.StringValue(stored.Mode)
	return true
}

func (policy *applicationProvenancePolicyResource) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	apiClient := policy.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state applicationProvenancePolicyModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	stored, readError := apiClient.GetApplicationProvenancePolicy(ctx, state.ApplicationID.ValueString())
	if removeWhenNotFound(ctx, readError, apiClient, response) {
		return
	}
	if readError != nil {
		response.Diagnostics.AddError("Unable to read the application's provenance mode", readError.Error())
		return
	}
	if stored.ApplicationMode == nil {
		response.State.RemoveResource(ctx)
		return
	}
	state.ID = state.ApplicationID
	state.Mode = types.StringValue(*stored.ApplicationMode)
	state.EffectiveMode = types.StringValue(stored.Mode)
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
	response.Diagnostics.Append(trackScope(ctx, apiClient, response.Private)...)
}

// Delete leaves the mode as it is: clearing it would hand the application
// to the organisation's default, which can be looser.
func (policy *applicationProvenancePolicyResource) Delete(_ context.Context, _ resource.DeleteRequest, _ *resource.DeleteResponse) {
}

// ImportState imports by application id.
func (policy *applicationProvenancePolicyResource) ImportState(ctx context.Context, request resource.ImportStateRequest, response *resource.ImportStateResponse) {
	importApplicationSetting(ctx, request, response)
}

type pipelineWorkspaceProfilesResource struct {
	configuredClient
}

type pipelineWorkspaceProfilesModel struct {
	ID           types.String         `tfsdk:"id"`
	RepositoryID types.String         `tfsdk:"repository_id"`
	ProfilesJSON jsontypes.Normalized `tfsdk:"profiles_json"`
}

// NewPipelineWorkspaceProfilesResource returns the
// ankra_pipeline_workspace_profiles resource, the counterpart of
// `ankra pipeline workspace-profiles put`.
func NewPipelineWorkspaceProfilesResource() resource.Resource {
	return &pipelineWorkspaceProfilesResource{}
}

func (profiles *pipelineWorkspaceProfilesResource) Metadata(_ context.Context, request resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_pipeline_workspace_profiles"
}

func (profiles *pipelineWorkspaceProfilesResource) Schema(_ context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: "The workspace profiles of a pipeline repository (`ankra pipeline workspace-profiles put`): for each " +
			"workspace kind, the image, size, setup steps, tools and service sidecars an AI workspace of that kind runs. The " +
			"resource owns the repository's whole set and replaces it on every change; the platform validates each profile as a " +
			"workspace would be rendered from it. A running workspace picks a change up at its next bring-up. Managing profiles " +
			"needs `workspaces.manage`. Destroy clears the profiles, so workspaces take their image from the repository's pipeline again.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "The repository id.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"repository_id": schema.StringAttribute{
				MarkdownDescription: "Identifier of the pipeline repository. Changing it replaces the resource.",
				Required:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"profiles_json": schema.StringAttribute{
				MarkdownDescription: "The profiles as a JSON object keyed by workspace kind, each with `image`, `resources` (`cpu`, " +
					"`memory`), `volume_size`, `setup`, `tools` and `services`. Write it with `jsonencode`.",
				Required:   true,
				CustomType: jsontypes.NormalizedType{},
			},
		},
	}
}

func (profiles *pipelineWorkspaceProfilesResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	var plan pipelineWorkspaceProfilesModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if profiles.write(ctx, &plan, &response.Diagnostics) {
		response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
		response.Diagnostics.Append(trackScope(ctx, profiles.client, response.Private)...)
	}
}

func (profiles *pipelineWorkspaceProfilesResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	var plan pipelineWorkspaceProfilesModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if profiles.write(ctx, &plan, &response.Diagnostics) {
		response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
	}
}

func (profiles *pipelineWorkspaceProfilesResource) write(ctx context.Context, plan *pipelineWorkspaceProfilesModel, diagnostics *diag.Diagnostics) bool {
	apiClient := profiles.requireClient(diagnostics)
	if apiClient == nil || diagnostics.HasError() {
		return false
	}
	body := json.RawMessage(plan.ProfilesJSON.ValueString())
	if _, setError := apiClient.SetWorkspaceProfiles(ctx, plan.RepositoryID.ValueString(), body); setError != nil {
		diagnostics.AddError("Unable to set the workspace profiles", setError.Error())
		return false
	}
	plan.ID = plan.RepositoryID
	return true
}

func (profiles *pipelineWorkspaceProfilesResource) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	apiClient := profiles.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state pipelineWorkspaceProfilesModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	stored, readError := apiClient.GetWorkspaceProfiles(ctx, state.RepositoryID.ValueString())
	if removeWhenNotFound(ctx, readError, apiClient, response) {
		return
	}
	if readError != nil {
		response.Diagnostics.AddError("Unable to read the workspace profiles", readError.Error())
		return
	}
	state.ID = state.RepositoryID
	state.ProfilesJSON = jsontypes.NewNormalizedValue(string(stored))
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
	response.Diagnostics.Append(trackScope(ctx, apiClient, response.Private)...)
}

func (profiles *pipelineWorkspaceProfilesResource) Delete(ctx context.Context, request resource.DeleteRequest, response *resource.DeleteResponse) {
	apiClient := profiles.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state pipelineWorkspaceProfilesModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	_, clearError := apiClient.SetWorkspaceProfiles(ctx, state.RepositoryID.ValueString(), json.RawMessage(`{}`))
	if clearError == nil || deletedWhenNotFound(ctx, clearError, apiClient, request.Private, &response.Diagnostics) || response.Diagnostics.HasError() {
		return
	}
	response.Diagnostics.AddError("Unable to clear the workspace profiles", clearError.Error())
}

// ImportState imports by repository id.
func (profiles *pipelineWorkspaceProfilesResource) ImportState(ctx context.Context, request resource.ImportStateRequest, response *resource.ImportStateResponse) {
	response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("repository_id"), request.ID)...)
	response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("id"), request.ID)...)
}
