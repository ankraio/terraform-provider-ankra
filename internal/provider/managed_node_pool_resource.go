// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"
	"strings"

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
	_ resource.Resource                = (*managedNodePoolResource)(nil)
	_ resource.ResourceWithConfigure   = (*managedNodePoolResource)(nil)
	_ resource.ResourceWithImportState = (*managedNodePoolResource)(nil)
)

type managedNodePoolResource struct {
	configuredClient
}

type managedNodePoolModel struct {
	ID        types.String `tfsdk:"id"`
	Provider  types.String `tfsdk:"provider_name"`
	ClusterID types.String `tfsdk:"cluster_id"`
	Name      types.String `tfsdk:"name"`
	managedNodePoolFields
}

// NewManagedNodePoolResource returns the ankra_managed_node_pool resource,
// the counterpart of `ankra cluster managed node-pool add|update|delete`.
func NewManagedNodePoolResource() resource.Resource {
	return &managedNodePoolResource{}
}

func (poolResource *managedNodePoolResource) Metadata(_ context.Context, request resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_managed_node_pool"
}

// managedProviderAttribute names the managed Kubernetes provider. The name
// provider_name avoids Terraform's reserved meta-argument "provider".
func managedProviderAttribute(description string) schema.StringAttribute {
	return schema.StringAttribute{
		MarkdownDescription: description + " One of `" + strings.Join(client.ManagedProviders, "`, `") + "`. Changing it forces replacement.",
		Required:            true,
		PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
		Validators:          []validator.String{laneOneOf(client.ManagedProviders...)},
	}
}

func (poolResource *managedNodePoolResource) Schema(_ context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	attributes := managedNodePoolAttributes(true)
	attributes["id"] = schema.StringAttribute{
		MarkdownDescription: "`<provider_name>/<cluster_id>/<name>`.",
		Computed:            true,
		PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
	}
	attributes["provider_name"] = managedProviderAttribute("Managed Kubernetes provider of the cluster.")
	attributes["cluster_id"] = schema.StringAttribute{
		MarkdownDescription: "Managed cluster the pool belongs to. Changing it forces replacement.",
		Required:            true,
		PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
	}
	attributes["name"] = schema.StringAttribute{
		MarkdownDescription: "Pool name, unique within the cluster. Changing it forces replacement.",
		Required:            true,
		PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
		Validators:          []validator.String{laneNameValidator{}},
	}
	response.Schema = schema.Schema{
		MarkdownDescription: "A node pool of a managed Kubernetes cluster (DOKS, UKS, GKE, AKS, EKS, OVHcloud MKS, " +
			"Kapsule or Ankra Cloud Kubernetes). The counterpart of `ankra cluster managed node-pool add|update|delete`. " +
			"`node_count` and `autoscaling` change in place; every other argument replaces the pool. Pools declared in " +
			"`ankra_managed_cluster.node_pools` are managed there; use this resource for the others.",
		Attributes: attributes,
	}
}

func (poolResource *managedNodePoolResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	apiClient := poolResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var plan managedNodePoolModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}
	name := plan.Name.ValueString()
	createRequest := managedNodePoolCreateFromFields(ctx, name, plan.managedNodePoolFields, &response.Diagnostics)
	if response.Diagnostics.HasError() {
		return
	}
	provider := plan.Provider.ValueString()
	clusterID := plan.ClusterID.ValueString()
	if err := apiClient.AddManagedNodePool(ctx, provider, clusterID, createRequest); err != nil {
		response.Diagnostics.AddError("Unable to add managed node pool", err.Error())
		return
	}
	plan.ID = types.StringValue(provider + "/" + clusterID + "/" + name)
	if pool, err := apiClient.GetManagedNodePool(ctx, provider, clusterID, name); err == nil {
		refreshManagedNodePoolFields(&plan.managedNodePoolFields, *pool, false)
	}
	settleUnknownPoolFields(&plan.managedNodePoolFields, createRequest.Count)
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
	response.Diagnostics.Append(trackScope(ctx, apiClient, response.Private)...)
}

func (poolResource *managedNodePoolResource) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	apiClient := poolResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state managedNodePoolModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	pool, err := apiClient.GetManagedNodePool(ctx, state.Provider.ValueString(), state.ClusterID.ValueString(), state.Name.ValueString())
	if removeWhenNotFound(ctx, err, apiClient, response) {
		return
	}
	if err != nil {
		response.Diagnostics.AddError("Unable to read managed node pool", err.Error())
		return
	}
	marker, _ := request.Private.GetKey(ctx, clusterImportedKey)
	refreshManagedNodePoolFields(&state.managedNodePoolFields, *pool, len(marker) > 0)
	state.ID = types.StringValue(state.Provider.ValueString() + "/" + state.ClusterID.ValueString() + "/" + pool.Name)
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
	response.Diagnostics.Append(trackScope(ctx, apiClient, response.Private)...)
}

// Update changes node_count and autoscaling in place; every other argument
// carries RequiresReplace.
func (poolResource *managedNodePoolResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	apiClient := poolResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var plan, state managedNodePoolModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	if update, changed := managedNodePoolSizingUpdate(plan.managedNodePoolFields, state.managedNodePoolFields); changed {
		if err := apiClient.UpdateManagedNodePool(ctx, state.Provider.ValueString(), state.ClusterID.ValueString(), state.Name.ValueString(), update); err != nil {
			response.Diagnostics.AddError("Unable to update managed node pool", err.Error())
			return
		}
	}
	plan.ID = state.ID
	settleUnknownPoolFields(&plan.managedNodePoolFields, state.Count.ValueInt64())
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
	response.Diagnostics.Append(clearClusterImported(ctx, response.Private)...)
}

func (poolResource *managedNodePoolResource) Delete(ctx context.Context, request resource.DeleteRequest, response *resource.DeleteResponse) {
	apiClient := poolResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state managedNodePoolModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	err := apiClient.DeleteManagedNodePool(ctx, state.Provider.ValueString(), state.ClusterID.ValueString(), state.Name.ValueString())
	if err == nil || deletedWhenNotFound(ctx, err, apiClient, request.Private, &response.Diagnostics) || response.Diagnostics.HasError() {
		return
	}
	response.Diagnostics.AddError("Unable to delete managed node pool", err.Error())
}

// ImportState imports by `<provider_name>/<cluster_id>/<name>`.
func (poolResource *managedNodePoolResource) ImportState(ctx context.Context, request resource.ImportStateRequest, response *resource.ImportStateResponse) {
	parts := strings.Split(request.ID, "/")
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		response.Diagnostics.AddError("Invalid import id",
			fmt.Sprintf("Expected <provider_name>/<cluster_id>/<name>, got %q.", request.ID))
		return
	}
	response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("id"), request.ID)...)
	response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("provider_name"), parts[0])...)
	response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("cluster_id"), parts[1])...)
	response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("name"), parts[2])...)
	response.Diagnostics.Append(markClusterImported(ctx, response.Private)...)
}
