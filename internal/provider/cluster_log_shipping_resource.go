// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"ankra.io/terraform-provider-ankra/internal/client"
)

var (
	_ resource.Resource                = (*clusterLogShippingResource)(nil)
	_ resource.ResourceWithConfigure   = (*clusterLogShippingResource)(nil)
	_ resource.ResourceWithImportState = (*clusterLogShippingResource)(nil)
)

type clusterLogShippingResource struct {
	configuredClient
}

type clusterLogShippingModel struct {
	ID                  types.String `tfsdk:"id"`
	ClusterID           types.String `tfsdk:"cluster_id"`
	Enabled             types.Bool   `tfsdk:"enabled"`
	Available           types.Bool   `tfsdk:"available"`
	AgentSupportsSwitch types.Bool   `tfsdk:"agent_supports_switch"`
}

// NewClusterLogShippingResource returns the ankra_cluster_log_shipping
// resource, the counterpart of `ankra cluster logs-ship enable|disable`.
func NewClusterLogShippingResource() resource.Resource {
	return &clusterLogShippingResource{}
}

func (shippingResource *clusterLogShippingResource) Metadata(_ context.Context, request resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_cluster_log_shipping"
}

func (shippingResource *clusterLogShippingResource) Schema(_ context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: "Whether a cluster's container logs are shipped to Ankra's hosted log store. Shipping is opt-in and off by default. " +
			"The counterpart of `ankra cluster logs-ship enable` and `disable`. Destroying the resource turns shipping off again.",
		Attributes: map[string]schema.Attribute{
			"id":         clusterSettingsIDAttribute("The cluster id."),
			"cluster_id": clusterSettingsClusterIDAttribute(),
			"enabled": schema.BoolAttribute{
				MarkdownDescription: "Whether the cluster ships its logs.",
				Required:            true,
			},
			"available": schema.BoolAttribute{
				MarkdownDescription: "Whether the hosted log store is live on the platform; while it is not, the switch is stored but nothing ships.",
				Computed:            true,
			},
			"agent_supports_switch": schema.BoolAttribute{
				MarkdownDescription: "Whether the cluster's agent advertised that it follows the switch at its last check-in.",
				Computed:            true,
			},
		},
	}
}

func (shippingResource *clusterLogShippingResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	var plan clusterLogShippingModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if shippingResource.set(ctx, &plan, &response.Diagnostics) {
		response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
	}
}

func (shippingResource *clusterLogShippingResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	var plan clusterLogShippingModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if shippingResource.set(ctx, &plan, &response.Diagnostics) {
		response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
	}
}

func (shippingResource *clusterLogShippingResource) set(ctx context.Context, plan *clusterLogShippingModel, diagnostics *diag.Diagnostics) bool {
	apiClient := shippingResource.requireClient(diagnostics)
	if apiClient == nil || diagnostics.HasError() {
		return false
	}
	state, err := apiClient.SetClusterLogShipping(ctx, plan.ClusterID.ValueString(), plan.Enabled.ValueBool())
	if err != nil {
		diagnostics.AddError("Unable to set cluster log shipping", err.Error())
		return false
	}
	applyClusterLogShipping(plan, state)
	return true
}

func (shippingResource *clusterLogShippingResource) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	apiClient := shippingResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state clusterLogShippingModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	shipping, err := apiClient.GetClusterLogShipping(ctx, state.ClusterID.ValueString())
	if clusterSettingsReadNotFound(ctx, err, state.ClusterID.ValueString(), response) {
		return
	}
	if err != nil {
		response.Diagnostics.AddError("Unable to read cluster log shipping", err.Error())
		return
	}
	applyClusterLogShipping(&state, shipping)
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
}

// Delete restores the platform default: shipping off.
func (shippingResource *clusterLogShippingResource) Delete(ctx context.Context, request resource.DeleteRequest, response *resource.DeleteResponse) {
	apiClient := shippingResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state clusterLogShippingModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	_, err := apiClient.SetClusterLogShipping(ctx, state.ClusterID.ValueString(), false)
	if deleteError := clusterSettingsDeleteError(err, state.ClusterID.ValueString()); deleteError != nil {
		response.Diagnostics.AddError("Unable to turn cluster log shipping off", deleteError.Error())
	}
}

// ImportState imports by cluster id.
func (shippingResource *clusterLogShippingResource) ImportState(ctx context.Context, request resource.ImportStateRequest, response *resource.ImportStateResponse) {
	clusterSettingsImportClusterID(ctx, request, response)
}

func applyClusterLogShipping(model *clusterLogShippingModel, shipping *client.ClusterLogShipping) {
	model.ID = model.ClusterID
	model.Enabled = types.BoolValue(shipping.ShippingEnabled)
	model.Available = types.BoolValue(shipping.Available)
	model.AgentSupportsSwitch = types.BoolValue(shipping.AgentSupportsSwitch)
}
