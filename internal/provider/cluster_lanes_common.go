// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework-timeouts/resource/timeouts"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"ankra.io/terraform-provider-ankra/internal/client"
)

// laneClusterCommonModel holds the attributes every lane in this file family
// shares. Each lane's model embeds it and adds its own placement and sizing
// vocabulary.
type laneClusterCommonModel struct {
	ID                types.String   `tfsdk:"id"`
	ClusterID         types.String   `tfsdk:"cluster_id"`
	State             types.String   `tfsdk:"state"`
	Kind              types.String   `tfsdk:"kind"`
	Name              types.String   `tfsdk:"name"`
	Description       types.String   `tfsdk:"description"`
	Distribution      types.String   `tfsdk:"distribution"`
	CNI               types.String   `tfsdk:"cni"`
	KubernetesVersion types.String   `tfsdk:"kubernetes_version"`
	ControlPlaneCount types.Int64    `tfsdk:"control_plane_count"`
	WorkerCount       types.Int64    `tfsdk:"worker_count"`
	EtcdTopology      types.String   `tfsdk:"etcd_topology"`
	EtcdNodeCount     types.Int64    `tfsdk:"etcd_node_count"`
	IncludeNetworking types.Bool     `tfsdk:"include_networking"`
	IncludeDNS        types.Bool     `tfsdk:"include_dns"`
	ForceDestroy      types.Bool     `tfsdk:"force_destroy"`
	WaitForReady      types.Bool     `tfsdk:"wait_for_ready"`
	Timeouts          timeouts.Value `tfsdk:"timeouts"`
}

// laneClusterAttributes are the schema attributes behind
// laneClusterCommonModel. Every provisioning argument carries
// RequiresReplace: the platform has no update for a cluster's build
// parameters, and day-2 sizing goes through ankra_node_group instead.
func laneClusterAttributes(laneDisplayName string) map[string]schema.Attribute {
	attributes := identityAttributes()
	attributes["name"] = requiredReplaceString("Name of the cluster.")
	attributes["description"] = optionalReplaceString("Description of the cluster.")
	attributes["distribution"] = defaultedReplaceString("Kubernetes distribution: `kubeadm` (default, vanilla upstream Kubernetes) or `k3s`.", "kubeadm")
	attributes["cni"] = defaultedReplaceString("Container network interface. Defaults to `cilium`, the only CNI kubeadm clusters run; set `flannel` or `calico` for k3s clusters.", "cilium")
	attributes["kubernetes_version"] = optionalReplaceString(
		"Kubernetes version. Defaults to the platform's stable version when unset; `data.ankra_kubernetes_versions` lists the supported ones.")
	attributes["control_plane_count"] = defaultedReplaceInt64("Number of control-plane nodes.", 1)
	attributes["worker_count"] = defaultedReplaceInt64(
		"Number of worker nodes in the cluster's default worker group. Further groups, and in-place resizing, are `ankra_node_group` resources.", 1)
	attributes["etcd_topology"] = defaultedReplaceString("etcd topology for kubeadm clusters: `stacked` or `external`.", "stacked")
	attributes["etcd_node_count"] = defaultedReplaceInt64("Number of dedicated etcd nodes when `etcd_topology` is `external`.", 3)
	attributes["include_networking"] = defaultedReplaceBool("Install the Ankra networking stack (Traefik and cert-manager) for ingress.", true)
	attributes["include_dns"] = defaultedReplaceBool("Delegate a DNS subzone to the cluster and wire external-dns into the networking stack.", true)
	attributes["name"] = withStringValidators(attributes["name"], laneNameValidator{})
	attributes["distribution"] = withStringValidators(attributes["distribution"], laneOneOf(laneDistributions...))
	attributes["cni"] = withStringValidators(attributes["cni"], laneOneOf(laneCNIs...))
	attributes["etcd_topology"] = withStringValidators(attributes["etcd_topology"], laneOneOf(laneEtcdTopologies...))
	attributes["force_destroy"] = forceDestroyAttribute(laneDisplayName)
	attributes["wait_for_ready"] = waitForReadyAttribute()
	return attributes
}

// laneClusterDefinition describes one lane to the shared lifecycle.
type laneClusterDefinition[Model any] struct {
	typeSuffix  string
	lane        string
	displayName string
	description string
	attributes  func() map[string]schema.Attribute
	common      func(model *Model) *laneClusterCommonModel
	request     func(ctx context.Context, model *Model, diagnostics *diag.Diagnostics) any
	// acceptVolumeDataLoss reads the volume-loss consent for a lane whose
	// deprovision asks for it; nil for lanes that never do.
	acceptVolumeDataLoss func(model *Model) bool
}

// laneClusterResource is the shared lifecycle of a provisioned cluster lane.
// It reuses the provisioned-cluster machinery of the original five clouds:
// the id is persisted before the readiness wait, state and kind are refreshed
// from the platform, and a cluster gone from the listing is removed from
// state so the next plan recreates it.
type laneClusterResource[Model any] struct {
	configuredClient
	definition laneClusterDefinition[Model]
}

func (laneResource *laneClusterResource[Model]) Metadata(_ context.Context, request resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + laneResource.definition.typeSuffix
}

func (laneResource *laneClusterResource[Model]) Schema(ctx context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: laneResource.definition.description,
		Attributes:          laneResource.definition.attributes(),
		Blocks:              commonProvisionedBlocks(ctx),
	}
}

func (laneResource *laneClusterResource[Model]) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	apiClient := laneResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var plan Model
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}
	common := laneResource.definition.common(&plan)

	createRequest := laneResource.definition.request(ctx, &plan, &response.Diagnostics)
	if response.Diagnostics.HasError() {
		return
	}
	created, err := apiClient.CreateLaneCluster(ctx, laneResource.definition.lane, createRequest)
	if err != nil {
		response.Diagnostics.AddError("Unable to create "+laneResource.definition.displayName+" cluster", err.Error())
		return
	}
	if created.ClusterID == "" {
		response.Diagnostics.AddError("Invalid create response", "The Ankra platform did not return a cluster_id.")
		return
	}

	common.ID = types.StringValue(created.ClusterID)
	common.ClusterID = types.StringValue(created.ClusterID)
	common.State = types.StringUnknown()
	common.Kind = types.StringUnknown()

	// Persist the id before waiting. A wait that times out must not lose the
	// cluster the platform already started building.
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}

	cluster := settleProvisioned(ctx, apiClient, created.ClusterID, common.WaitForReady, common.Timeouts.Create, &response.Diagnostics)
	if cluster == nil {
		return
	}
	applyClusterIdentity(&common.State, &common.Kind, cluster)
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
}

func (laneResource *laneClusterResource[Model]) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	apiClient := laneResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state Model
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	common := laneResource.definition.common(&state)

	clusterID := common.ClusterID.ValueString()
	if clusterID == "" {
		clusterID = common.ID.ValueString()
	}
	cluster, err := apiClient.GetClusterByID(ctx, clusterID)
	if err != nil {
		response.Diagnostics.AddError("Unable to read cluster", err.Error())
		return
	}
	if cluster == nil {
		response.State.RemoveResource(ctx)
		return
	}

	common.ID = types.StringValue(cluster.ID)
	common.ClusterID = types.StringValue(cluster.ID)
	if cluster.Name != "" {
		common.Name = types.StringValue(cluster.Name)
	}
	applyClusterIdentity(&common.State, &common.Kind, cluster)
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
}

// Update carries through the arguments that can change in place:
// force_destroy, wait_for_ready and accept_volume_data_loss affect future
// operations rather than the running cluster. Every provisioning argument
// carries RequiresReplace, so it never reaches Update.
func (laneResource *laneClusterResource[Model]) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	if laneResource.requireClient(&response.Diagnostics) == nil {
		return
	}
	var plan Model
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
}

func (laneResource *laneClusterResource[Model]) Delete(ctx context.Context, request resource.DeleteRequest, response *resource.DeleteResponse) {
	apiClient := laneResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state Model
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	common := laneResource.definition.common(&state)

	clusterID := common.ClusterID.ValueString()
	if clusterID == "" {
		clusterID = common.ID.ValueString()
	}
	if clusterID == "" {
		return
	}

	wait := resolveTimeout(ctx, common.Timeouts.Delete, defaultDeleteTimeout, &response.Diagnostics)
	if response.Diagnostics.HasError() {
		return
	}
	deleteContext, cancel := context.WithTimeout(ctx, wait)
	defer cancel()

	options := client.LaneDeprovisionOptions{Force: true}
	if !common.ForceDestroy.IsNull() && !common.ForceDestroy.IsUnknown() {
		options.Force = common.ForceDestroy.ValueBool()
	}
	if laneResource.definition.acceptVolumeDataLoss != nil {
		options.AcceptVolumeDataLoss = laneResource.definition.acceptVolumeDataLoss(&state)
	}
	if err := apiClient.DeprovisionLaneCluster(deleteContext, laneResource.definition.lane, clusterID, options); err != nil {
		response.Diagnostics.AddError("Unable to deprovision "+laneResource.definition.displayName+" cluster", err.Error())
	}
}

// ImportState imports by cluster id. The lifecycle flags take their defaults
// so an import does not plan an update for them; the provisioning arguments
// are not returned by the platform and must match the configuration.
func (laneResource *laneClusterResource[Model]) ImportState(ctx context.Context, request resource.ImportStateRequest, response *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("cluster_id"), request, response)
	response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("id"), request.ID)...)
	response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("force_destroy"), true)...)
	response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("wait_for_ready"), true)...)
}

// acceptVolumeDataLossAttribute is the deprovision consent of the lanes
// whose teardown deletes captured CSI volumes.
func acceptVolumeDataLossAttribute() schema.BoolAttribute {
	return schema.BoolAttribute{
		MarkdownDescription: "Send `accept_volume_data_loss=true` when deprovisioning, consenting to the deletion of the " +
			"cluster's persistent volumes. Without it the platform refuses to destroy a cluster that still holds " +
			"volumes it would delete, and `terraform destroy` fails with that refusal. Defaults to `false`. Changing " +
			"this only affects a future destroy, so it never replaces the cluster.",
		Optional: true,
		Computed: true,
		Default:  booldefault.StaticBool(false),
	}
}

// withStringValidators adds validators to a string attribute built by one of
// the shared constructors.
func withStringValidators(attribute schema.Attribute, validators ...validator.String) schema.Attribute {
	stringAttribute, ok := attribute.(schema.StringAttribute)
	if !ok {
		return attribute
	}
	stringAttribute.Validators = append(stringAttribute.Validators, validators...)
	return stringAttribute
}
