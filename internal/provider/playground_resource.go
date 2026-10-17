// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"slices"
	"time"

	"github.com/hashicorp/terraform-plugin-framework-timeouts/resource/timeouts"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"ankra.io/terraform-provider-ankra/internal/client"
)

var (
	_ resource.Resource                = (*playgroundResource)(nil)
	_ resource.ResourceWithConfigure   = (*playgroundResource)(nil)
	_ resource.ResourceWithImportState = (*playgroundResource)(nil)
)

// defaultPlaygroundTimeout bounds a playground wait: a tenant is a virtual
// cluster on a shared host, ready in minutes.
const defaultPlaygroundTimeout = 20 * time.Minute

// playgroundPollInterval paces the playground waits; tests shorten it.
var playgroundPollInterval = 10 * time.Second

type playgroundResource struct {
	configuredClient
}

type playgroundModel struct {
	ID           types.String   `tfsdk:"id"`
	ClusterID    types.String   `tfsdk:"cluster_id"`
	Plan         types.String   `tfsdk:"plan"`
	Phase        types.String   `tfsdk:"phase"`
	ExpiresAt    types.String   `tfsdk:"expires_at"`
	StarterURL   types.String   `tfsdk:"starter_url"`
	OrderedAt    types.String   `tfsdk:"ordered_at"`
	WaitForReady types.Bool     `tfsdk:"wait_for_ready"`
	Timeouts     timeouts.Value `tfsdk:"timeouts"`
}

// NewPlaygroundResource returns the ankra_playground resource, the
// counterpart of `ankra cluster playground create`, `resize` and `destroy`.
func NewPlaygroundResource() resource.Resource {
	return &playgroundResource{}
}

func (playground *playgroundResource) Metadata(_ context.Context, request resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_playground"
}

func (playground *playgroundResource) Schema(ctx context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	computedString := func(description string) schema.StringAttribute {
		return schema.StringAttribute{MarkdownDescription: description, Computed: true}
	}
	response.Schema = schema.Schema{
		MarkdownDescription: "An Ankra playground: a ready-to-use Kubernetes environment on Ankra's shared hosts. The " +
			"counterpart of `ankra cluster playground create`, `resize` and `destroy`. `data.ankra_playground_plans` " +
			"lists the sizes. A paid playground resizes in place to another paid plan; the free trial cannot be " +
			"resized, so moving to or from `trial` replaces the playground.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Identifier of the playground's cluster (mirrors `cluster_id`).",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"cluster_id": schema.StringAttribute{
				MarkdownDescription: "Identifier of the playground's cluster.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"plan": schema.StringAttribute{
				MarkdownDescription: "Plan id of the size to order. Defaults to `trial`, the free plan, which the platform " +
					"allows one of per organisation. Paid plans need a billing plan and a payment card.",
				Optional: true,
				Computed: true,
				Default:  stringdefault.StaticString(client.PlaygroundTrialPlanID),
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplaceIf(
					func(_ context.Context, request planmodifier.StringRequest, response *stringplanmodifier.RequiresReplaceIfFuncResponse) {
						response.RequiresReplace = request.StateValue.ValueString() == client.PlaygroundTrialPlanID ||
							request.PlanValue.ValueString() == client.PlaygroundTrialPlanID
					},
					"Moving to or from the free trial replaces the playground.",
					"Moving to or from the free `trial` replaces the playground.",
				)},
			},
			"phase":       computedString("Provisioning phase the platform reports, refreshed on every read (`ready` once it serves)."),
			"expires_at":  computedString("When the platform tears the playground down unless it is used."),
			"starter_url": computedString("URL of the seeded starter app of a ready free playground; null otherwise."),
			"ordered_at":  computedString("When a paid size was ordered; null for the free trial."),
			"wait_for_ready": schema.BoolAttribute{
				MarkdownDescription: "Wait for the playground to reach `ready` after a create or resize. Defaults to `true`.",
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(true),
			},
		},
		Blocks: map[string]schema.Block{
			"timeouts": timeouts.Block(ctx, timeouts.Opts{Create: true, Update: true}),
		},
	}
}

func (playground *playgroundResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	apiClient := playground.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var plan playgroundModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}
	clusterID, err := apiClient.CreatePlayground(ctx, plan.Plan.ValueString())
	if err != nil {
		response.Diagnostics.AddError("Unable to create playground", err.Error())
		return
	}
	plan.ID = types.StringValue(clusterID)
	plan.ClusterID = types.StringValue(clusterID)
	plan.Phase = types.StringUnknown()
	plan.ExpiresAt = types.StringUnknown()
	plan.StarterURL = types.StringUnknown()
	plan.OrderedAt = types.StringUnknown()
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}
	playground.settle(ctx, apiClient, &plan, plan.Timeouts.Create, &response.State, &response.Diagnostics)
}

func (playground *playgroundResource) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	apiClient := playground.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state playgroundModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	status, err := apiClient.GetPlaygroundStatus(ctx, state.ClusterID.ValueString())
	if removeWhenNotFound(ctx, err, response) {
		return
	}
	if err != nil {
		response.Diagnostics.AddError("Unable to read playground", err.Error())
		return
	}
	if slices.Contains(client.PlaygroundGonePhases, status.Phase) {
		response.State.RemoveResource(ctx)
		return
	}
	applyPlaygroundStatus(&state, status)
	if state.WaitForReady.IsNull() {
		state.WaitForReady = types.BoolValue(true)
	}
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
}

// Update resizes a paid playground to another paid plan. Plans involving the
// free trial carry RequiresReplace, so they never reach here.
func (playground *playgroundResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	apiClient := playground.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var plan, state playgroundModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	plan.ID = state.ID
	plan.ClusterID = state.ClusterID
	if plan.Plan.Equal(state.Plan) {
		plan.Phase, plan.ExpiresAt, plan.StarterURL, plan.OrderedAt = state.Phase, state.ExpiresAt, state.StarterURL, state.OrderedAt
		response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
		return
	}
	if err := apiClient.ResizePlayground(ctx, state.ClusterID.ValueString(), plan.Plan.ValueString()); err != nil {
		response.Diagnostics.AddError("Unable to resize playground", err.Error())
		return
	}
	plan.Phase, plan.ExpiresAt, plan.StarterURL, plan.OrderedAt = state.Phase, state.ExpiresAt, state.StarterURL, state.OrderedAt
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
	playground.settle(ctx, apiClient, &plan, plan.Timeouts.Update, &response.State, &response.Diagnostics)
}

func (playground *playgroundResource) Delete(ctx context.Context, request resource.DeleteRequest, response *resource.DeleteResponse) {
	apiClient := playground.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state playgroundModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	if err := apiClient.DestroyPlayground(ctx, state.ClusterID.ValueString()); err != nil {
		response.Diagnostics.AddError("Unable to destroy playground", err.Error())
	}
}

// ImportState imports by cluster id.
func (playground *playgroundResource) ImportState(ctx context.Context, request resource.ImportStateRequest, response *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("cluster_id"), request, response)
	response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("id"), request.ID)...)
	response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("wait_for_ready"), true)...)
}

// settle records the playground's status after a create or resize: it waits
// for `ready` when asked to and otherwise reads the status once. The id is
// already in state, so a wait that fails does not orphan the playground.
func (playground *playgroundResource) settle(ctx context.Context, apiClient *client.Client, model *playgroundModel,
	configured timeoutFunc, state *tfsdk.State, diagnostics *diag.Diagnostics) {
	clusterID := model.ClusterID.ValueString()
	var status *client.PlaygroundStatus
	var err error
	if model.WaitForReady.ValueBool() {
		wait := resolveTimeout(ctx, configured, defaultPlaygroundTimeout, diagnostics)
		if diagnostics.HasError() {
			return
		}
		waitContext, cancel := context.WithTimeout(ctx, wait)
		defer cancel()
		status, err = apiClient.WaitForPlaygroundReady(waitContext, clusterID, playgroundPollInterval)
	} else {
		status, err = apiClient.GetPlaygroundStatus(ctx, clusterID)
	}
	if status != nil {
		applyPlaygroundStatus(model, status)
		diagnostics.Append(state.Set(ctx, model)...)
	}
	if err != nil {
		diagnostics.AddError("Playground did not become ready", err.Error())
	}
}

// applyPlaygroundStatus copies the platform's status onto the model.
func applyPlaygroundStatus(model *playgroundModel, status *client.PlaygroundStatus) {
	if status.ClusterID != "" {
		model.ID = types.StringValue(status.ClusterID)
		model.ClusterID = types.StringValue(status.ClusterID)
	}
	if status.Plan.ID != "" {
		model.Plan = types.StringValue(status.Plan.ID)
	}
	model.Phase = types.StringValue(status.Phase)
	model.ExpiresAt = optionalString(status.ExpiresAt)
	model.StarterURL = types.StringPointerValue(status.StarterURL)
	model.OrderedAt = types.StringPointerValue(status.Plan.OrderedAt)
}
