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
	ID                          types.String         `tfsdk:"id"`
	ApplicationID               types.String         `tfsdk:"application_id"`
	Level                       types.String         `tfsdk:"level"`
	Areas                       types.Map            `tfsdk:"areas"`
	Limits                      *autonomyLimitsModel `tfsdk:"limits"`
	AutoFixBuilds               types.Bool           `tfsdk:"auto_fix_builds"`
	SetupPullRequestAutoMerge   types.String         `tfsdk:"setup_pull_request_auto_merge"`
	SetupPullRequestAutoMergeOn types.Bool           `tfsdk:"setup_pull_request_auto_merge_effective"`
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
			"pull request auto-merge. The layer is managed only when `level`, `areas` or `limits` is set, and is then replaced as " +
			"a whole, starting from the stored layer so a change made meanwhile is refused rather than overwritten. Asking for more " +
			"than the organisation allows needs an organisation admin. Each setting is written only when it changes. Pausing Ankra " +
			"for an application stays imperative (`ankra application autonomy pause`). Destroy leaves every setting as it is.",
		Attributes: attributes,
	}
}

func (autonomy *applicationAutonomyResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	var plan applicationAutonomyModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if autonomy.write(ctx, &plan, nil, &response.Diagnostics) {
		response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
		response.Diagnostics.Append(trackScope(ctx, autonomy.client, response.Private)...)
	}
}

func (autonomy *applicationAutonomyResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	var plan, state applicationAutonomyModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if autonomy.write(ctx, &plan, &state, &response.Diagnostics) {
		response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
	}
}

// write applies each setting that changed from state (every configured one
// on create) and reads the result back.
func (autonomy *applicationAutonomyResource) write(ctx context.Context, plan, state *applicationAutonomyModel, diagnostics *diag.Diagnostics) bool {
	apiClient := autonomy.requireClient(diagnostics)
	if apiClient == nil || diagnostics.HasError() {
		return false
	}
	applicationID := plan.ApplicationID.ValueString()
	isLayerChanged := autonomyLayerIsManaged(plan.Level, plan.Areas, plan.Limits)
	if state != nil {
		isLayerChanged = autonomyLayerChanged(plan.Level, state.Level, plan.Areas, state.Areas, plan.Limits, state.Limits)
	}
	if isLayerChanged {
		layers, readError := apiClient.GetApplicationAutonomyLayers(ctx, applicationID)
		if readError != nil {
			diagnostics.AddError("Unable to read the application's autonomy", readError.Error())
			return false
		}
		if layers == nil {
			diagnostics.AddError("Unable to read the application's autonomy", "The platform could not read the stored layers, so no write can be built from them. Try again.")
			return false
		}
		write := autonomyLayerWrite(ctx, plan.Level, plan.Areas, plan.Limits, layers.Application, diagnostics)
		if diagnostics.HasError() {
			return false
		}
		if setError := apiClient.SetApplicationAutonomyLayer(ctx, applicationID, write); setError != nil {
			diagnostics.AddError("Unable to set who runs the application", setError.Error())
			return false
		}
	}
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
	return autonomy.readSettings(ctx, apiClient, plan, false, diagnostics)
}

// readSettings reads every setting into model. The layer is read only when
// it is managed, or when refreshLayer asks for it.
func (autonomy *applicationAutonomyResource) readSettings(ctx context.Context, apiClient *client.Client, model *applicationAutonomyModel, refreshLayer bool,
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
	if refreshLayer && autonomyLayerIsManaged(model.Level, model.Areas, model.Limits) {
		layers, readError := apiClient.GetApplicationAutonomyLayers(ctx, applicationID)
		if readError != nil {
			diagnostics.AddError("Unable to read the application's autonomy", readError.Error())
			return false
		}
		if layers != nil {
			model.Level, model.Areas, model.Limits = autonomyLayerState(layers.Application, model.Areas, model.Limits)
		}
	}
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
	if !autonomy.readSettings(ctx, apiClient, &state, true, &response.Diagnostics) {
		return
	}
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
	response.Diagnostics.Append(trackScope(ctx, apiClient, response.Private)...)
}

// Delete leaves every setting as it is: resetting the layer or the flags
// could widen what Ankra does unasked.
func (autonomy *applicationAutonomyResource) Delete(_ context.Context, _ resource.DeleteRequest, _ *resource.DeleteResponse) {
}

// ImportState imports by application id. The layer is adopted on the first
// apply that configures it.
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
