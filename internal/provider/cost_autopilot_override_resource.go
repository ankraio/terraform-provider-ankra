// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"ankra.io/terraform-provider-ankra/internal/client"
)

var (
	_ resource.Resource                = (*costAutopilotOverrideResource)(nil)
	_ resource.ResourceWithConfigure   = (*costAutopilotOverrideResource)(nil)
	_ resource.ResourceWithImportState = (*costAutopilotOverrideResource)(nil)
)

type costAutopilotOverrideResource struct {
	configuredClient
}

type costAutopilotOverrideModel struct {
	ID        types.String `tfsdk:"id"`
	ClusterID types.String `tfsdk:"cluster_id"`
	Tier      types.String `tfsdk:"tier"`
	Reason    types.String `tfsdk:"reason"`
	ExpiresAt types.String `tfsdk:"expires_at"`
}

// NewCostAutopilotOverrideResource returns the ankra_cost_autopilot_override
// resource, the counterpart of `ankra cost autopilot override` and
// `ankra cost autopilot clear`.
func NewCostAutopilotOverrideResource() resource.Resource {
	return &costAutopilotOverrideResource{}
}

func (overrideResource *costAutopilotOverrideResource) Metadata(_ context.Context, request resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_cost_autopilot_override"
}

func (overrideResource *costAutopilotOverrideResource) Schema(_ context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: "Puts one cluster in a cost autopilot tier whatever its environment kind. " +
			"Destroying it returns the cluster to its environment's default tier. " +
			"An override that reaches `expires_at` stops applying; Terraform then keeps it in state unchanged, with no planned change, " +
			"until `expires_at` is moved forward (which sets it again) or the resource is removed. " +
			"The counterpart of `ankra cost autopilot override` and `ankra cost autopilot clear`; it needs `billing.manage` and `clusters.write` on the cluster.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "The cluster id.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"cluster_id": schema.StringAttribute{
				MarkdownDescription: "Cluster the override applies to. Changing it replaces the override.",
				Required:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"tier": schema.StringAttribute{
				MarkdownDescription: "Tier: `hands-off`, `scheduled`, `managed` or `ephemeral`.",
				Required:            true,
				Validators:          []validator.String{costOneOfValidator{values: costAutopilotTiers}},
			},
			"reason": schema.StringAttribute{
				MarkdownDescription: "Why the cluster is overridden, 3 to 500 characters; shown on the cluster's card.",
				Required:            true,
				Validators:          []validator.String{costStringLengthValidator{minimum: 3, maximum: 500}, platformServicesTrimmed{}},
			},
			"expires_at": schema.StringAttribute{
				MarkdownDescription: "RFC 3339 instant the override stops applying, in the future. Unset means it never expires.",
				Optional:            true,
				Validators:          []validator.String{costTimeFormatValidator{layout: time.RFC3339, example: "2027-01-05T00:00:00Z"}},
			},
		},
	}
}

func (overrideResource *costAutopilotOverrideResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	apiClient := overrideResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var plan costAutopilotOverrideModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}
	if !costPutAutopilotOverride(ctx, apiClient, &plan, &response.Diagnostics) {
		return
	}
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
}

func (overrideResource *costAutopilotOverrideResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	apiClient := overrideResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var plan costAutopilotOverrideModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}
	if !costPutAutopilotOverride(ctx, apiClient, &plan, &response.Diagnostics) {
		return
	}
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
}

// costPutAutopilotOverride replaces the cluster's override with the planned
// one; the platform's PUT is a full replace, so an expiry removed from
// configuration clears.
func costPutAutopilotOverride(ctx context.Context, apiClient *client.Client, plan *costAutopilotOverrideModel, diagnostics *diag.Diagnostics) bool {
	if _, err := apiClient.SetCostAutopilotOverride(ctx, plan.ClusterID.ValueString(), client.CostAutopilotOverrideRequest{
		Tier:      plan.Tier.ValueString(),
		Reason:    plan.Reason.ValueString(),
		ExpiresAt: stringPointer(plan.ExpiresAt),
	}); err != nil {
		diagnostics.AddError("Unable to set cost autopilot override", err.Error())
		return false
	}
	plan.ID = plan.ClusterID
	return true
}

func (overrideResource *costAutopilotOverrideResource) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	apiClient := overrideResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state costAutopilotOverrideModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}

	cluster, err := apiClient.GetCostAutopilotCluster(ctx, state.ClusterID.ValueString())
	if removeWhenNotFound(ctx, err, response) {
		return
	}
	if err != nil {
		response.Diagnostics.AddError("Unable to read cost autopilot override", err.Error())
		return
	}
	if cluster.Override == nil {
		if !state.ExpiresAt.IsNull() && clusterSettingsIsPast(state.ExpiresAt.ValueString()) {
			// The override ran out as configured: re-creating it would be
			// refused for its past expiry and block every apply, so the
			// expired override is kept as it is, with no planned change.
			response.Diagnostics.Append(response.State.Set(ctx, &state)...)
			return
		}
		response.State.RemoveResource(ctx)
		return
	}

	state.ID = types.StringValue(cluster.ClusterID)
	state.Tier = types.StringValue(cluster.Override.Tier)
	state.Reason = types.StringValue(cluster.Override.Reason)
	state.ExpiresAt = costKeepSameInstant(state.ExpiresAt, cluster.Override.ExpiresAt)
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
}

func (overrideResource *costAutopilotOverrideResource) Delete(ctx context.Context, request resource.DeleteRequest, response *resource.DeleteResponse) {
	apiClient := overrideResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state costAutopilotOverrideModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}

	if err := apiClient.ClearCostAutopilotOverride(ctx, state.ClusterID.ValueString()); err != nil {
		response.Diagnostics.AddError("Unable to clear cost autopilot override", err.Error())
	}
}

// ImportState imports by cluster id.
func (overrideResource *costAutopilotOverrideResource) ImportState(ctx context.Context, request resource.ImportStateRequest, response *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("cluster_id"), request, response)
	response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("id"), request.ID)...)
}

// costKeepSameInstant keeps the configured spelling of a timestamp the
// platform answers in another offset or precision, so the same instant does
// not plan a diff.
func costKeepSameInstant(current types.String, remote *string) types.String {
	if remote == nil || *remote == "" {
		return types.StringNull()
	}
	if !current.IsNull() && !current.IsUnknown() {
		configured, configuredError := time.Parse(time.RFC3339Nano, current.ValueString())
		returned, returnedError := time.Parse(time.RFC3339Nano, *remote)
		if configuredError == nil && returnedError == nil && configured.Equal(returned) {
			return current
		}
	}
	return types.StringValue(*remote)
}
