// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-timeouts/resource/timeouts"
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
	ID              types.String   `tfsdk:"id"`
	Provider        types.String   `tfsdk:"provider_name"`
	ClusterID       types.String   `tfsdk:"cluster_id"`
	Name            types.String   `tfsdk:"name"`
	CurrentPoolName types.String   `tfsdk:"current_pool_name"`
	Timeouts        timeouts.Value `tfsdk:"timeouts"`
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

func (poolResource *managedNodePoolResource) Schema(ctx context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
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
		MarkdownDescription: "Name the pool is created with, unique within the cluster. It stays the resource's name when a " +
			"size change replaces the pool; `current_pool_name` is the pool's name now. Changing it forces replacement.",
		Required:      true,
		PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
		Validators:    []validator.String{laneNameValidator{}},
	}
	attributes["current_pool_name"] = schema.StringAttribute{
		MarkdownDescription: "Name of the pool at the provider now: `name` until a size change, then the name of the pool " +
			"that replaced it, named from up to 8 letters and digits of `name` and a 4-character hash of `name` and the " +
			"size (for example `batch3f2a`), short enough for every provider. Known at plan time.",
		Computed:      true,
		PlanModifiers: []planmodifier.String{managedPoolCurrentNamePlanModifier{}},
	}
	response.Schema = schema.Schema{
		MarkdownDescription: "A node pool of a managed Kubernetes cluster (DOKS, UKS, GKE, AKS, EKS, OVHcloud MKS, " +
			"Kapsule or Ankra Cloud Kubernetes). The counterpart of `ankra cluster managed node-pool add|update|delete`. " +
			"`node_count` and `autoscaling` change in place. A `size` change replaces the pool blue/green at the platform " +
			"and keeps the resource: a new pool of the new size (with the same count, labels, taints, zone and autoscaling) " +
			"is created, becomes ready, and the old pool is deleted; `current_pool_name` then names the new pool. Apply waits " +
			"for this up to the `update` timeout (default 90 minutes). Every other argument replaces the resource. Pools " +
			"declared in `ankra_managed_cluster.node_pools` are managed there; use this resource for the others.",
		Attributes: attributes,
		Blocks: map[string]schema.Block{
			"timeouts": timeouts.Block(ctx, timeouts.Opts{Update: true}),
		},
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
	plan.CurrentPoolName = types.StringValue(name)
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
	provider := state.Provider.ValueString()
	clusterID := state.ClusterID.ValueString()
	pools, err := apiClient.ListManagedNodePools(ctx, provider, clusterID)
	if removeWhenNotFound(ctx, err, apiClient, response) {
		return
	}
	if err != nil {
		response.Diagnostics.AddError("Unable to read managed node pool", err.Error())
		return
	}
	current := managedPoolCurrentName(state)
	pool := findTrackedManagedPool(pools, state.Name.ValueString(), current)
	if pool == nil {
		removeWhenNotFound(ctx, &client.APIError{
			Method: http.MethodGet, Path: "/api/v1/clusters/managed/" + provider + "/" + clusterID + "/node-pools",
			StatusCode: http.StatusNotFound, Body: "node pool " + current + " not found",
		}, apiClient, response)
		return
	}
	marker, _ := request.Private.GetKey(ctx, clusterImportedKey)
	refreshManagedNodePoolFields(&state.managedNodePoolFields, *pool, len(marker) > 0)
	state.CurrentPoolName = types.StringValue(pool.Name)
	state.ID = types.StringValue(provider + "/" + clusterID + "/" + state.Name.ValueString())
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
	response.Diagnostics.Append(trackScope(ctx, apiClient, response.Private)...)
}

// Update changes node_count and autoscaling in place, then a size change by a
// blue/green replacement of the pool, which it waits for; every other
// argument carries RequiresReplace. A replacement that fails or outlasts the
// timeout leaves the old pool recorded with its old size, so the next apply
// asks for the same replacement again and the platform resumes it, and the
// next refresh finds the new pool once the platform has finished.
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
	provider := state.Provider.ValueString()
	clusterID := state.ClusterID.ValueString()
	current := managedPoolCurrentName(state)
	if update, changed := managedNodePoolSizingUpdate(plan.managedNodePoolFields, state.managedNodePoolFields); changed {
		if err := apiClient.UpdateManagedNodePool(ctx, provider, clusterID, current, update); err != nil {
			response.Diagnostics.AddError("Unable to update managed node pool", err.Error())
			return
		}
		state.Count = plan.Count
		state.Autoscaling = plan.Autoscaling
		settleUnknownPoolFields(&state.managedNodePoolFields, state.Count.ValueInt64())
	}
	plan.ID = state.ID
	plan.CurrentPoolName = types.StringValue(current)
	if !strings.EqualFold(plan.Size.ValueString(), state.Size.ValueString()) {
		wait := resolveTimeout(ctx, plan.Timeouts.Update, defaultManagedPoolReplaceTimeout, &response.Diagnostics)
		if response.Diagnostics.HasError() {
			return
		}
		waitContext, cancel := context.WithTimeout(ctx, wait)
		defer cancel()
		replacementName := managedReplacementPoolName(plan.Name.ValueString(), plan.Size.ValueString())
		if err := replaceManagedPool(waitContext, apiClient, provider, clusterID, current, replacementName, plan.Size.ValueString()); err != nil {
			state.CurrentPoolName = types.StringValue(current)
			state.Timeouts = plan.Timeouts
			response.Diagnostics.Append(response.State.Set(ctx, &state)...)
			response.Diagnostics.AddError("Unable to change the managed node pool's size",
				fmt.Sprintf("Replacing pool %s with pool %s of size %s: %s. Terraform still records pool %s with its old size. "+
					"Apply again to retry: it asks for the same replacement, which the platform resumes, and a replacement "+
					"that finishes meanwhile is picked up by the next refresh. See `ankra cluster operations list`.",
					current, replacementName, plan.Size.ValueString(), err.Error(), current))
			return
		}
		plan.CurrentPoolName = types.StringValue(replacementName)
	}
	settleUnknownPoolFields(&plan.managedNodePoolFields, state.Count.ValueInt64())
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
	response.Diagnostics.Append(clearClusterImported(ctx, response.Private)...)
	response.Diagnostics.Append(trackScope(ctx, apiClient, response.Private)...)
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
	err := apiClient.DeleteManagedNodePool(ctx, state.Provider.ValueString(), state.ClusterID.ValueString(), managedPoolCurrentName(state))
	if err == nil || deletedWhenNotFound(ctx, err, apiClient, request.Private, &response.Diagnostics) || response.Diagnostics.HasError() {
		return
	}
	response.Diagnostics.AddError("Unable to delete managed node pool", err.Error())
}

// ImportState imports by `<provider_name>/<cluster_id>/<name>`. The name is
// the pool's name now, or the name a pool this resource resized was created
// with: the first refresh finds the pool that replaced it.
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
