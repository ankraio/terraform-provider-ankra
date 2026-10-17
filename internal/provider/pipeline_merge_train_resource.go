// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"ankra.io/terraform-provider-ankra/internal/client"
)

var (
	_ resource.Resource                   = (*pipelineMergeTrainResource)(nil)
	_ resource.ResourceWithConfigure      = (*pipelineMergeTrainResource)(nil)
	_ resource.ResourceWithImportState    = (*pipelineMergeTrainResource)(nil)
	_ resource.ResourceWithValidateConfig = (*pipelineMergeTrainResource)(nil)
	_ resource.ResourceWithModifyPlan     = (*pipelineMergeTrainResource)(nil)
)

// pipelineMergeTrainMaxCars mirrors the platform's cap (enginekit/mergetrain
// MaxCarsLimit).
const pipelineMergeTrainMaxCars = 8

// pipelineMergeTrainProvider is the only source-code provider the platform
// runs a merge train on.
const pipelineMergeTrainProvider = "github"

type pipelineMergeTrainResource struct {
	configuredClient
}

type pipelineMergeTrainModel struct {
	ApplicationID types.String `tfsdk:"application_id"`
	RepositoryID  types.String `tfsdk:"repository_id"`
	Enabled       types.Bool   `tfsdk:"enabled"`
	MaxCars       types.Int64  `tfsdk:"max_cars"`
}

// NewPipelineMergeTrainResource returns the ankra_pipeline_merge_train
// resource, the counterpart of `ankra pipeline train enable|disable`.
func NewPipelineMergeTrainResource() resource.Resource {
	return &pipelineMergeTrainResource{}
}

func (trainResource *pipelineMergeTrainResource) Metadata(_ context.Context, request resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_pipeline_merge_train"
}

func (trainResource *pipelineMergeTrainResource) Schema(_ context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	attributes := pipelineSelectorAttributes("the merge train belongs to")
	attributes["enabled"] = schema.BoolAttribute{
		MarkdownDescription: "Whether pull requests merge through the train.",
		Required:            true,
	}
	attributes["max_cars"] = schema.Int64Attribute{
		MarkdownDescription: "How many pull requests the train tests at once, 1 to 8. Unset keeps the platform's value.",
		Optional:            true,
		Computed:            true,
		PlanModifiers:       []planmodifier.Int64{int64planmodifier.UseStateForUnknown()},
		Validators:          []validator.Int64{applicationIntegerBetween{minimum: 1, maximum: pipelineMergeTrainMaxCars}},
	}
	response.Schema = schema.Schema{
		MarkdownDescription: "A repository's merge train setting: whether approved pull requests are tested together on top of each other " +
			"and merged in order. The counterpart of `ankra pipeline train enable` and `disable`; it is available on GitHub repositories only, which the plan checks for a `repository_id` that already exists; queueing a pull request " +
			"(`ankra pipeline train add|remove`) is an operation, not configuration. Destroying it turns the train off.",
		Attributes: attributes,
	}
}

func (trainResource *pipelineMergeTrainResource) ValidateConfig(ctx context.Context, request resource.ValidateConfigRequest, response *resource.ValidateConfigResponse) {
	var model pipelineMergeTrainModel
	response.Diagnostics.Append(request.Config.Get(ctx, &model)...)
	if response.Diagnostics.HasError() {
		return
	}
	pipelineValidateSelector(model.ApplicationID, model.RepositoryID, &response.Diagnostics)
}

// ModifyPlan refuses at plan time to turn the train on for a repository the
// platform cannot run one on: the merge train is GitHub-only. It can only
// check a repository addressed by repository_id that already exists; the
// platform refuses the rest at apply.
func (trainResource *pipelineMergeTrainResource) ModifyPlan(ctx context.Context, request resource.ModifyPlanRequest, response *resource.ModifyPlanResponse) {
	if request.Plan.Raw.IsNull() || trainResource.client == nil || trainResource.client.Token == "" {
		return
	}
	var plan pipelineMergeTrainModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() || !plan.Enabled.ValueBool() || plan.RepositoryID.IsNull() || plan.RepositoryID.IsUnknown() {
		return
	}
	repository, err := trainResource.client.GetPipelineRepository(ctx, plan.RepositoryID.ValueString())
	if err != nil {
		return
	}
	if repository.Provider != pipelineMergeTrainProvider {
		response.Diagnostics.AddAttributeError(path.Root("enabled"), "Merge train not supported",
			fmt.Sprintf("The merge train supports GitHub repositories only; repository %s is on %s.", repository.ID, repository.Provider))
	}
}

func (trainResource *pipelineMergeTrainResource) write(ctx context.Context, plan *pipelineMergeTrainModel) error {
	settings, err := trainResource.client.SetPipelineMergeTrainSettings(ctx, pipelineSelectorFrom(plan.ApplicationID, plan.RepositoryID),
		client.PipelineMergeTrainSettingsUpdate{Enabled: plan.Enabled.ValueBool(), MaxCars: int64Pointer(plan.MaxCars)})
	if err != nil {
		return err
	}
	plan.Enabled = types.BoolValue(settings.Enabled)
	plan.MaxCars = types.Int64Value(settings.MaxCars)
	return nil
}

func (trainResource *pipelineMergeTrainResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	if trainResource.requireClient(&response.Diagnostics) == nil {
		return
	}
	var plan pipelineMergeTrainModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}
	if err := trainResource.write(ctx, &plan); err != nil {
		response.Diagnostics.AddError("Unable to set merge train", err.Error())
		return
	}
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
}

func (trainResource *pipelineMergeTrainResource) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	apiClient := trainResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state pipelineMergeTrainModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	settings, err := apiClient.GetPipelineMergeTrainSettings(ctx, pipelineSelectorFrom(state.ApplicationID, state.RepositoryID))
	if removeWhenNotFound(ctx, err, response) {
		return
	}
	if err != nil {
		response.Diagnostics.AddError("Unable to read merge train", err.Error())
		return
	}
	state.Enabled = types.BoolValue(settings.Enabled)
	state.MaxCars = types.Int64Value(settings.MaxCars)
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
}

func (trainResource *pipelineMergeTrainResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	if trainResource.requireClient(&response.Diagnostics) == nil {
		return
	}
	var plan pipelineMergeTrainModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}
	if err := trainResource.write(ctx, &plan); err != nil {
		response.Diagnostics.AddError("Unable to update merge train", err.Error())
		return
	}
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
}

func (trainResource *pipelineMergeTrainResource) Delete(ctx context.Context, request resource.DeleteRequest, response *resource.DeleteResponse) {
	apiClient := trainResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state pipelineMergeTrainModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	_, err := apiClient.SetPipelineMergeTrainSettings(ctx, pipelineSelectorFrom(state.ApplicationID, state.RepositoryID),
		client.PipelineMergeTrainSettingsUpdate{Enabled: false})
	if err != nil && !client.IsNotFound(err) {
		response.Diagnostics.AddError("Unable to turn merge train off", err.Error())
	}
}

// ImportState imports by `application/<application_id>` or
// `repository/<repository_id>`.
func (trainResource *pipelineMergeTrainResource) ImportState(ctx context.Context, request resource.ImportStateRequest, response *resource.ImportStateResponse) {
	parts, err := applicationSplitImportID(request.ID, "application|repository", "id")
	if err != nil {
		response.Diagnostics.AddError("Invalid import id", err.Error())
		return
	}
	pipelineSelectorImport(ctx, parts[0], parts[1], response)
}
