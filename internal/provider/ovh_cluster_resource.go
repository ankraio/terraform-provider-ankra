// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework-timeouts/resource/timeouts"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"ankra.io/terraform-provider-ankra/internal/client"
)

var (
	_ resource.Resource                = (*ovhClusterResource)(nil)
	_ resource.ResourceWithConfigure   = (*ovhClusterResource)(nil)
	_ resource.ResourceWithImportState = (*ovhClusterResource)(nil)
)

type ovhClusterResource struct {
	client *client.Client
}

type ovhClusterResourceModel struct {
	ID                    types.String   `tfsdk:"id"`
	ClusterID             types.String   `tfsdk:"cluster_id"`
	State                 types.String   `tfsdk:"state"`
	Kind                  types.String   `tfsdk:"kind"`
	Name                  types.String   `tfsdk:"name"`
	CredentialID          types.String   `tfsdk:"credential_id"`
	Region                types.String   `tfsdk:"region"`
	SSHKeyCredentialID    types.String   `tfsdk:"ssh_key_credential_id"`
	ControlPlaneFlavorID  types.String   `tfsdk:"control_plane_flavor_id"`
	WorkerFlavorID        types.String   `tfsdk:"worker_flavor_id"`
	EtcdFlavorID          types.String   `tfsdk:"etcd_flavor_id"`
	GatewayFlavorID       types.String   `tfsdk:"gateway_flavor_id"`
	NetworkVLANID         types.Int64    `tfsdk:"network_vlan_id"`
	SubnetCIDR            types.String   `tfsdk:"subnet_cidr"`
	DHCPStart             types.String   `tfsdk:"dhcp_start"`
	DHCPEnd               types.String   `tfsdk:"dhcp_end"`
	AvailabilityZones     types.List     `tfsdk:"availability_zones"`
	Distribution          types.String   `tfsdk:"distribution"`
	CNI                   types.String   `tfsdk:"cni"`
	KubernetesVersion     types.String   `tfsdk:"kubernetes_version"`
	ControlPlaneCount     types.Int64    `tfsdk:"control_plane_count"`
	WorkerCount           types.Int64    `tfsdk:"worker_count"`
	ExternalCloudProvider types.Bool     `tfsdk:"external_cloud_provider"`
	IncludeNetworking     types.Bool     `tfsdk:"include_networking"`
	EtcdTopology          types.String   `tfsdk:"etcd_topology"`
	EtcdNodeCount         types.Int64    `tfsdk:"etcd_node_count"`
	GitopsBranch          types.String   `tfsdk:"gitops_branch"`
	GitopsCredentialName  types.String   `tfsdk:"gitops_credential_name"`
	GitopsRepository      types.String   `tfsdk:"gitops_repository"`
	Description           types.String   `tfsdk:"description"`
	ForceDestroy          types.Bool     `tfsdk:"force_destroy"`
	WaitForReady          types.Bool     `tfsdk:"wait_for_ready"`
	Timeouts              timeouts.Value `tfsdk:"timeouts"`
}

// NewOVHClusterResource returns a new ankra_ovh_cluster resource.
func NewOVHClusterResource() resource.Resource {
	return &ovhClusterResource{}
}

func (ovhResource *ovhClusterResource) Metadata(_ context.Context, request resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_ovh_cluster"
}

func (ovhResource *ovhClusterResource) Schema(ctx context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	attributes := commonProvisionedAttributes(ctx)
	attributes["credential_id"] = requiredReplaceString("OVH API credential id used to provision the cluster.")
	attributes["region"] = requiredReplaceString("OVH region (e.g. `GRA11`, `SBG5`).")
	attributes["ssh_key_credential_id"] = requiredReplaceString("SSH key credential id to attach to the cluster nodes.")
	attributes["control_plane_flavor_id"] = defaultedReplaceString("OVH flavor for control-plane nodes.", "b3-16")
	attributes["worker_flavor_id"] = defaultedReplaceString("OVH flavor for worker nodes.", "b3-16")
	attributes["etcd_flavor_id"] = defaultedReplaceString("OVH flavor for dedicated etcd nodes.", "b3-16")
	attributes["gateway_flavor_id"] = defaultedReplaceString("OVH flavor for the network gateway.", "c3-4")
	attributes["network_vlan_id"] = defaultedReplaceInt64("VLAN id for the private network.", 0)
	attributes["subnet_cidr"] = defaultedReplaceString("Private subnet CIDR.", "10.0.1.0/24")
	attributes["dhcp_start"] = defaultedReplaceString("First address of the private subnet's DHCP range.", "10.0.1.100")
	attributes["dhcp_end"] = defaultedReplaceString("Last address of the private subnet's DHCP range.", "10.0.1.200")
	attributes["availability_zones"] = optionalReplaceStringList(
		"3-AZ regions only: availability zones to spread the cluster across (e.g. `eu-west-par-b`).")
	attributes["force_destroy"] = forceDestroyAttribute("OVH")

	response.Schema = schema.Schema{
		MarkdownDescription: "Provisions a OVH-backed cluster on the Ankra platform. Provisioning arguments " +
			"are immutable, so changing one forces replacement.",
		Attributes: attributes,
		Blocks:     commonProvisionedBlocks(ctx),
	}
}

func (ovhResource *ovhClusterResource) Configure(_ context.Context, request resource.ConfigureRequest, response *resource.ConfigureResponse) {
	if request.ProviderData == nil {
		return
	}
	apiClient, ok := request.ProviderData.(*client.Client)
	if !ok {
		response.Diagnostics.AddError(
			"Unexpected provider data type",
			fmt.Sprintf("Expected *client.Client, got %T. This is a bug in the provider.", request.ProviderData),
		)
		return
	}
	ovhResource.client = apiClient
}

func (ovhResource *ovhClusterResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	var plan ovhClusterResourceModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}
	if ovhResource.client == nil || ovhResource.client.Token == "" {
		response.Diagnostics.AddError("Missing API token", missingTokenDetail)
		return
	}

	apiRequest := ovhRequestFromModel(ctx, &plan, &response.Diagnostics)
	if response.Diagnostics.HasError() {
		return
	}

	created, err := ovhResource.client.CreateOVHCluster(ctx, apiRequest)
	if err != nil {
		response.Diagnostics.AddError("Unable to create OVH cluster", err.Error())
		return
	}
	if created.ClusterID == "" {
		response.Diagnostics.AddError("Invalid create response", "The Ankra platform did not return a cluster_id.")
		return
	}

	plan.ID = types.StringValue(created.ClusterID)
	plan.ClusterID = types.StringValue(created.ClusterID)
	plan.State = types.StringUnknown()
	plan.Kind = types.StringUnknown()

	// Persist the id before waiting. A wait that times out must not lose the
	// cluster the platform already started building.
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}

	cluster := settleProvisioned(ctx, ovhResource.client, plan.ClusterID.ValueString(),
		plan.WaitForReady, plan.Timeouts.Create, &response.Diagnostics)
	if cluster == nil {
		return
	}
	applyClusterIdentity(&plan.State, &plan.Kind, cluster)
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
}

func (ovhResource *ovhClusterResource) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	var state ovhClusterResourceModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	if ovhResource.client == nil || ovhResource.client.Token == "" {
		response.Diagnostics.AddError("Missing API token", missingTokenDetail)
		return
	}

	clusterID := state.ClusterID.ValueString()
	if clusterID == "" {
		clusterID = state.ID.ValueString()
	}

	cluster, err := ovhResource.client.GetClusterByID(ctx, clusterID)
	if err != nil {
		response.Diagnostics.AddError("Unable to read cluster", err.Error())
		return
	}
	if cluster == nil {
		response.State.RemoveResource(ctx)
		return
	}

	state.ID = types.StringValue(cluster.ID)
	state.ClusterID = types.StringValue(cluster.ID)
	if cluster.Name != "" {
		state.Name = types.StringValue(cluster.Name)
	}
	applyClusterIdentity(&state.State, &state.Kind, cluster)
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
}

// Update carries through the arguments that can change in place: force_destroy
// and wait_for_ready affect future operations rather than the running cluster.
// Every provisioning argument carries RequiresReplace, so it forces a replace.
func (ovhResource *ovhClusterResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	var plan ovhClusterResourceModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
}

func (ovhResource *ovhClusterResource) Delete(ctx context.Context, request resource.DeleteRequest, response *resource.DeleteResponse) {
	var state ovhClusterResourceModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}

	clusterID := state.ClusterID.ValueString()
	if clusterID == "" {
		clusterID = state.ID.ValueString()
	}
	if clusterID == "" {
		return
	}
	if ovhResource.client == nil || ovhResource.client.Token == "" {
		response.Diagnostics.AddError("Missing API token", missingTokenDetail)
		return
	}

	wait := resolveTimeout(ctx, state.Timeouts.Delete, defaultDeleteTimeout, &response.Diagnostics)
	if response.Diagnostics.HasError() {
		return
	}
	deleteContext, cancel := context.WithTimeout(ctx, wait)
	defer cancel()

	force := true
	if !state.ForceDestroy.IsNull() && !state.ForceDestroy.IsUnknown() {
		force = state.ForceDestroy.ValueBool()
	}
	if err := ovhResource.client.DeleteProvisionedCluster(deleteContext, client.LaneOVH, clusterID, force); err != nil {
		response.Diagnostics.AddError("Unable to deprovision OVH cluster", err.Error())
	}
}

func (ovhResource *ovhClusterResource) ImportState(ctx context.Context, request resource.ImportStateRequest, response *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("cluster_id"), request, response)
}

func ovhRequestFromModel(ctx context.Context, plan *ovhClusterResourceModel, diagnostics *diag.Diagnostics) client.OVHClusterRequest {
	return client.OVHClusterRequest{
		Name:                  plan.Name.ValueString(),
		CredentialID:          plan.CredentialID.ValueString(),
		Distribution:          plan.Distribution.ValueString(),
		CNI:                   plan.CNI.ValueString(),
		ControlPlaneCount:     plan.ControlPlaneCount.ValueInt64(),
		WorkerCount:           plan.WorkerCount.ValueInt64(),
		ExternalCloudProvider: plan.ExternalCloudProvider.ValueBool(),
		IncludeNetworking:     plan.IncludeNetworking.ValueBool(),
		EtcdTopology:          plan.EtcdTopology.ValueString(),
		EtcdNodeCount:         plan.EtcdNodeCount.ValueInt64(),
		GitopsBranch:          plan.GitopsBranch.ValueString(),
		KubernetesVersion:     optionalStringPointer(plan.KubernetesVersion),
		Description:           optionalStringPointer(plan.Description),
		GitopsCredentialName:  optionalStringPointer(plan.GitopsCredentialName),
		GitopsRepository:      optionalStringPointer(plan.GitopsRepository),
		Region:                plan.Region.ValueString(),
		SSHKeyCredentialID:    plan.SSHKeyCredentialID.ValueString(),
		ControlPlaneFlavorID:  plan.ControlPlaneFlavorID.ValueString(),
		WorkerFlavorID:        plan.WorkerFlavorID.ValueString(),
		EtcdFlavorID:          plan.EtcdFlavorID.ValueString(),
		GatewayFlavorID:       plan.GatewayFlavorID.ValueString(),
		NetworkVLANID:         plan.NetworkVLANID.ValueInt64(),
		SubnetCIDR:            plan.SubnetCIDR.ValueString(),
		DHCPStart:             plan.DHCPStart.ValueString(),
		DHCPEnd:               plan.DHCPEnd.ValueString(),
		AvailabilityZones:     stringListToAPI(ctx, plan.AvailabilityZones, diagnostics),
	}
}
