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
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"ankra.io/terraform-provider-ankra/internal/client"
)

var (
	_ resource.Resource                = (*aiLaneModelResource)(nil)
	_ resource.ResourceWithConfigure   = (*aiLaneModelResource)(nil)
	_ resource.ResourceWithImportState = (*aiLaneModelResource)(nil)
)

type aiLaneModelResource struct {
	configuredClient
}

type aiLaneModelModel struct {
	ID                 types.String `tfsdk:"id"`
	Lane               types.String `tfsdk:"lane"`
	ModelKey           types.String `tfsdk:"model_key"`
	DefaultTier        types.String `tfsdk:"default_tier"`
	EffectiveModelID   types.String `tfsdk:"effective_model_id"`
	EffectiveModelName types.String `tfsdk:"effective_model_name"`
	IsSelectionStale   types.Bool   `tfsdk:"is_selection_stale"`
}

// NewAILaneModelResource returns the ankra_ai_lane_model resource, the
// counterpart of `ankra ai lanes set|clear`.
func NewAILaneModelResource() resource.Resource {
	return &aiLaneModelResource{}
}

func (laneResource *aiLaneModelResource) Metadata(_ context.Context, request resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_ai_lane_model"
}

func (laneResource *aiLaneModelResource) Schema(_ context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: "The model one AI lane (a platform function such as stack README generation or AI code review) runs on. " +
			"The counterpart of `ankra ai lanes set` and `ankra ai lanes clear`; `ankra ai lanes list` names the lanes. " +
			"Destroying it returns the lane to its default tier. A lane cleared outside Terraform is planned for re-creation.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "The lane name.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"lane": schema.StringAttribute{
				MarkdownDescription: "Lane name. Changing it replaces the resource.",
				Required:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
				Validators:          []validator.String{aiNoSlash{}},
			},
			"model_key": schema.StringAttribute{
				MarkdownDescription: "Catalogue key (`ankra_ai_model.key`) or an exact OpenRouter model id the lane runs on.",
				Required:            true,
			},
			"default_tier": schema.StringAttribute{
				MarkdownDescription: "Tier the lane falls back to without a selection.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"effective_model_id": schema.StringAttribute{
				MarkdownDescription: "Model id the lane runs on now.",
				Computed:            true,
			},
			"effective_model_name": schema.StringAttribute{
				MarkdownDescription: "Display name of the model the lane runs on now.",
				Computed:            true,
			},
			"is_selection_stale": schema.BoolAttribute{
				MarkdownDescription: "Whether the selection no longer resolves (for example its catalogue entry was deleted), so the lane runs on its default.",
				Computed:            true,
			},
		},
	}
}

func (laneResource *aiLaneModelResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	var plan aiLaneModelModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}
	laneResource.set(ctx, &plan, &response.State, &response.Diagnostics)
}

func (laneResource *aiLaneModelResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	var plan aiLaneModelModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}
	laneResource.set(ctx, &plan, &response.State, &response.Diagnostics)
}

func (laneResource *aiLaneModelResource) set(ctx context.Context, plan *aiLaneModelModel, state *tfsdk.State, diagnostics *diag.Diagnostics) {
	apiClient := laneResource.requireClient(diagnostics)
	if apiClient == nil {
		return
	}
	lanes, err := apiClient.SetAILaneModel(ctx, plan.Lane.ValueString(), stringPointer(plan.ModelKey))
	if err != nil {
		diagnostics.AddError("Unable to set AI lane model", err.Error())
		return
	}
	for index := range lanes {
		if lanes[index].Lane == plan.Lane.ValueString() {
			plan.applyAPI(&lanes[index])
			diagnostics.Append(state.Set(ctx, plan)...)
			return
		}
	}
	diagnostics.AddError("Unable to set AI lane model", "The platform accepted the selection but did not list lane "+plan.Lane.ValueString()+" afterwards.")
}

func (laneResource *aiLaneModelResource) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	apiClient := laneResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state aiLaneModelModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	lane, err := apiClient.GetAILaneModel(ctx, state.Lane.ValueString())
	if removeWhenNotFound(ctx, err, response) {
		return
	}
	if err != nil {
		response.Diagnostics.AddError("Unable to read AI lane model", err.Error())
		return
	}
	if lane.SelectedModelKey == "" {
		response.State.RemoveResource(ctx)
		return
	}
	state.applyAPI(lane)
	state.ModelKey = types.StringValue(lane.SelectedModelKey)
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
}

// Delete returns the lane to its default tier.
func (laneResource *aiLaneModelResource) Delete(ctx context.Context, request resource.DeleteRequest, response *resource.DeleteResponse) {
	apiClient := laneResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state aiLaneModelModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	_, err := apiClient.SetAILaneModel(ctx, state.Lane.ValueString(), nil)
	if err != nil && !client.IsNotFound(err) {
		response.Diagnostics.AddError("Unable to clear AI lane model", err.Error())
	}
}

// ImportState imports by lane name.
func (laneResource *aiLaneModelResource) ImportState(ctx context.Context, request resource.ImportStateRequest, response *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("lane"), request, response)
}

func (model *aiLaneModelModel) applyAPI(lane *client.AILaneModel) {
	model.ID = types.StringValue(lane.Lane)
	model.Lane = types.StringValue(lane.Lane)
	model.DefaultTier = types.StringValue(lane.DefaultTier)
	model.EffectiveModelID = types.StringValue(lane.EffectiveModelID)
	model.EffectiveModelName = types.StringValue(lane.EffectiveModelName)
	model.IsSelectionStale = types.BoolValue(lane.IsSelectionStale)
}
