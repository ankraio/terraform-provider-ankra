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
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"ankra.io/terraform-provider-ankra/internal/client"
)

var setupAutoMergeChoices = []string{"on", "off", "organisation"}

var (
	_ resource.Resource                = (*applicationAutonomyResource)(nil)
	_ resource.ResourceWithConfigure   = (*applicationAutonomyResource)(nil)
	_ resource.ResourceWithImportState = (*applicationAutonomyResource)(nil)
)

type applicationAutonomyResource struct {
	configuredClient
}

type applicationAutonomyModel struct {
	ID                          types.String `tfsdk:"id"`
	ApplicationID               types.String `tfsdk:"application_id"`
	Level                       types.String `tfsdk:"level"`
	Areas                       types.Map    `tfsdk:"areas"`
	Limits                      types.Object `tfsdk:"limits"`
	AutoFixBuilds               types.Bool   `tfsdk:"auto_fix_builds"`
	SetupPullRequestAutoMerge   types.String `tfsdk:"setup_pull_request_auto_merge"`
	SetupPullRequestAutoMergeOn types.Bool   `tfsdk:"setup_pull_request_auto_merge_effective"`
}

// NewApplicationAutonomyResource returns the ankra_application_autonomy
// resource, the counterpart of `ankra application autonomy set`,
// `ankra application auto-fix-builds set` and
// `ankra application setup-auto-merge set`.
func NewApplicationAutonomyResource() resource.Resource {
	return &applicationAutonomyResource{}
}

func (autonomy *applicationAutonomyResource) Metadata(_ context.Context, request resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_application_autonomy"
}

func (autonomy *applicationAutonomyResource) Schema(_ context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	attributes := autonomyLayerAttributes("the application")
	attributes["id"] = schema.StringAttribute{
		MarkdownDescription: "The application id.",
		Computed:            true,
		PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
	}
	attributes["application_id"] = schema.StringAttribute{
		MarkdownDescription: "Identifier of the application. Changing it replaces the resource.",
		Required:            true,
		PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
	}
	attributes["auto_fix_builds"] = schema.BoolAttribute{
		MarkdownDescription: "Whether Ankra fixes a failed build of the application's tracked branch on its own, opening a pull request. On by default.",
		Optional:            true,
		Computed:            true,
		PlanModifiers:       []planmodifier.Bool{boolplanmodifier.UseStateForUnknown()},
	}
	attributes["setup_pull_request_auto_merge"] = schema.StringAttribute{
		MarkdownDescription: "Whether Ankra may merge the application's setup pull requests on its own once every check is green: `on`, " +
			"`off`, or `organisation` to follow the organisation's setting. Changing it needs an organisation administrator.",
		Optional:      true,
		Computed:      true,
		Validators:    []validator.String{organisationStringOneOf{values: setupAutoMergeChoices}},
		PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
	}
	attributes["setup_pull_request_auto_merge_effective"] = schema.BoolAttribute{
		MarkdownDescription: "Whether setup pull requests are merged on their own now, after following the organisation where asked.",
		Computed:            true,
	}
	response.Schema = schema.Schema{
		MarkdownDescription: "Who runs an application (`ankra application autonomy set`): the application's own \"who runs it\" layer, a " +
			"level for every area, per-area levels and limits on what Ankra may do unasked, plus automatic build fixing and setup " +
			"pull request auto-merge. `level`, `areas` and `limits` always report the stored layer. A change replaces the layer as " +
			"a whole and sends the layer the last refresh saw as the expected one, so a layer changed between plan and apply is " +
			"refused rather than overwritten. Creating the resource over a stored layer that holds parts the configuration does " +
			"not set is refused: import it first, so the plan shows what would change. Asking for more than the organisation " +
			"allows needs an organisation admin. Each setting is written only when it changes. Pausing Ankra for an application " +
			"stays imperative (`ankra application autonomy pause`). Destroy leaves every setting as it is.",
		Attributes: attributes,
	}
}

func (autonomy *applicationAutonomyResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	apiClient := autonomy.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var plan applicationAutonomyModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}
	applicationID := plan.ApplicationID.ValueString()
	importCommand := "terraform import <address> " + applicationID
	if !autonomyLayerCreate(ctx, applicationAutonomyStore(apiClient, applicationID), applicationAutonomyParts(&plan), importCommand,
		response.Private, &response.Diagnostics) {
		return
	}
	if autonomy.writeSettings(ctx, apiClient, &plan, nil, &response.Diagnostics) {
		response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
		response.Diagnostics.Append(trackScope(ctx, apiClient, response.Private)...)
	}
}

func (autonomy *applicationAutonomyResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	apiClient := autonomy.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var plan, state applicationAutonomyModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	store := applicationAutonomyStore(apiClient, plan.ApplicationID.ValueString())
	if !autonomyLayerUpdate(ctx, store, applicationAutonomyParts(&plan), applicationAutonomyParts(&state), request.Private, response.Private, &response.Diagnostics) {
		return
	}
	if autonomy.writeSettings(ctx, apiClient, &plan, &state, &response.Diagnostics) {
		response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
	}
}

// writeSettings applies the build-fixing and setup auto-merge settings that
// changed from state (every configured one on create) and reads them back.
func (autonomy *applicationAutonomyResource) writeSettings(ctx context.Context, apiClient *client.Client, plan, state *applicationAutonomyModel,
	diagnostics *diag.Diagnostics) bool {
	applicationID := plan.ApplicationID.ValueString()
	if !plan.AutoFixBuilds.IsUnknown() && (state == nil || !plan.AutoFixBuilds.Equal(state.AutoFixBuilds)) {
		if setError := apiClient.SetApplicationAutoFixBuilds(ctx, applicationID, plan.AutoFixBuilds.ValueBool()); setError != nil {
			diagnostics.AddError("Unable to set automatic build fixing", setError.Error())
			return false
		}
	}
	if !plan.SetupPullRequestAutoMerge.IsUnknown() && (state == nil || !plan.SetupPullRequestAutoMerge.Equal(state.SetupPullRequestAutoMerge)) {
		if _, setError := apiClient.SetApplicationSetupAutoMerge(ctx, applicationID, setupAutoMergeChoice(plan.SetupPullRequestAutoMerge.ValueString())); setError != nil {
			diagnostics.AddError("Unable to set setup pull request auto-merge", setError.Error())
			return false
		}
	}
	plan.ID = plan.ApplicationID
	return autonomy.readSettings(ctx, apiClient, plan, diagnostics)
}

// readSettings reads the build-fixing and setup auto-merge settings into
// model.
func (autonomy *applicationAutonomyResource) readSettings(ctx context.Context, apiClient *client.Client, model *applicationAutonomyModel,
	diagnostics *diag.Diagnostics) bool {
	applicationID := model.ApplicationID.ValueString()
	autoFix, autoFixError := apiClient.GetApplicationAutoFixBuilds(ctx, applicationID)
	if autoFixError != nil {
		diagnostics.AddError("Unable to read automatic build fixing", autoFixError.Error())
		return false
	}
	model.AutoFixBuilds = types.BoolValue(autoFix)
	autoMerge, autoMergeError := apiClient.GetApplicationSetupAutoMerge(ctx, applicationID)
	if autoMergeError != nil {
		diagnostics.AddError("Unable to read setup pull request auto-merge", autoMergeError.Error())
		return false
	}
	model.SetupPullRequestAutoMerge = types.StringValue(setupAutoMergeName(autoMerge.Enabled))
	model.SetupPullRequestAutoMergeOn = types.BoolValue(autoMerge.Effective)
	return true
}

func (autonomy *applicationAutonomyResource) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	apiClient := autonomy.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state applicationAutonomyModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	_, probeError := apiClient.GetApplicationAutoFixBuilds(ctx, state.ApplicationID.ValueString())
	if removeWhenNotFound(ctx, probeError, apiClient, response) {
		return
	}
	state.ID = state.ApplicationID
	store := applicationAutonomyStore(apiClient, state.ApplicationID.ValueString())
	if !autonomyLayerRefresh(ctx, store, applicationAutonomyParts(&state), response.Private, &response.Diagnostics) {
		return
	}
	if !autonomy.readSettings(ctx, apiClient, &state, &response.Diagnostics) {
		return
	}
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
	response.Diagnostics.Append(trackScope(ctx, apiClient, response.Private)...)
}

func applicationAutonomyParts(model *applicationAutonomyModel) autonomyLayerParts {
	return autonomyLayerParts{Level: &model.Level, Areas: &model.Areas, Limits: &model.Limits}
}

func applicationAutonomyStore(apiClient *client.Client, applicationID string) autonomyLayerStore {
	return autonomyLayerStore{
		read: func(ctx context.Context) (*client.AutonomyLayer, error) {
			return apiClient.GetApplicationAutonomyLayer(ctx, applicationID)
		},
		write: func(ctx context.Context, write client.AutonomyLayerWrite) (*client.AutonomyLayer, error) {
			return apiClient.SetApplicationAutonomyLayer(ctx, applicationID, write)
		},
	}
}

// Delete leaves every setting as it is: resetting the layer or the flags
// could widen what Ankra does unasked.
func (autonomy *applicationAutonomyResource) Delete(_ context.Context, _ resource.DeleteRequest, _ *resource.DeleteResponse) {
}

// ImportState imports by application id; the read records the stored layer.
func (autonomy *applicationAutonomyResource) ImportState(ctx context.Context, request resource.ImportStateRequest, response *resource.ImportStateResponse) {
	response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("application_id"), request.ID)...)
	response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("id"), request.ID)...)
}

func setupAutoMergeChoice(choice string) *bool {
	switch choice {
	case "on":
		enabled := true
		return &enabled
	case "off":
		enabled := false
		return &enabled
	}
	return nil
}

func setupAutoMergeName(enabled *bool) string {
	switch {
	case enabled == nil:
		return "organisation"
	case *enabled:
		return "on"
	}
	return "off"
}
