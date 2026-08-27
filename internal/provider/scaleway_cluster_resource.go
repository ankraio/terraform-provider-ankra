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
	_ resource.Resource                = (*scalewayClusterResource)(nil)
	_ resource.ResourceWithConfigure   = (*scalewayClusterResource)(nil)
	_ resource.ResourceWithImportState = (*scalewayClusterResource)(nil)
)

type scalewayClusterResource struct {
	client *client.Client
}

type scalewayClusterResourceModel struct {
	ID                    types.String   `tfsdk:"id"`
	ClusterID             types.String   `tfsdk:"cluster_id"`
	State                 types.String   `tfsdk:"state"`
	Kind                  types.String   `tfsdk:"kind"`
	Name                  types.String   `tfsdk:"name"`
	CredentialID          types.String   `tfsdk:"credential_id"`
	Region                types.String   `tfsdk:"region"`
	Zone                  types.String   `tfsdk:"zone"`
	SSHKeyCredentialID    types.String   `tfsdk:"ssh_key_credential_id"`
	ControlPlaneType      types.String   `tfsdk:"control_plane_type"`
	WorkerType            types.String   `tfsdk:"worker_type"`
	EtcdType              types.String   `tfsdk:"etcd_type"`
	GatewayType           types.String   `tfsdk:"gateway_type"`
	GatewayAllowedIPs     types.List     `tfsdk:"gateway_allowed_ips"`
	BastionPort           types.Int64    `tfsdk:"bastion_port"`
	IncludeDNS            types.Bool     `tfsdk:"include_dns"`
	RetentionPolicy       types.String   `tfsdk:"retention_policy"`
	NetworkIPRange        types.String   `tfsdk:"network_ip_range"`
	PrivateNetworkID      types.String   `tfsdk:"private_network_id"`
	RuntimeCredentialID   types.String   `tfsdk:"runtime_credential_id"`
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

// NewScalewayClusterResource returns a new ankra_scaleway_cluster resource.
func NewScalewayClusterResource() resource.Resource {
	return &scalewayClusterResource{}
}

func (scalewayResource *scalewayClusterResource) Metadata(_ context.Context, request resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_scaleway_cluster"
}

func (scalewayResource *scalewayClusterResource) Schema(ctx context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	attributes := commonProvisionedAttributes(ctx)
	attributes["credential_id"] = requiredReplaceString("Scaleway API credential id used to provision the cluster.")
	attributes["region"] = requiredReplaceString("Scaleway region (e.g. `fr-par`, `nl-ams`).")
	attributes["zone"] = requiredReplaceString("Scaleway zone (e.g. `fr-par-1`).")
	attributes["ssh_key_credential_id"] = requiredReplaceString("SSH key credential id to attach to the cluster nodes.")
	attributes["control_plane_type"] = defaultedReplaceString("Instance type for control-plane nodes.", "DEV1-M")
	attributes["worker_type"] = defaultedReplaceString("Instance type for worker nodes.", "DEV1-M")
	attributes["etcd_type"] = defaultedReplaceString("Instance type for dedicated etcd nodes.", "DEV1-M")
	attributes["gateway_type"] = defaultedReplaceString("Public gateway type.", "VPC-GW-S")
	attributes["gateway_allowed_ips"] = optionalReplaceStringList("CIDRs allowed to reach the public gateway.")
	attributes["bastion_port"] = defaultedReplaceInt64("SSH port exposed by the gateway's bastion.", 61000)
	attributes["include_dns"] = defaultedReplaceBool("Provision a managed DNS zone for the cluster.", true)
	attributes["retention_policy"] = defaultedReplaceString(
		"What happens to the cluster's Scaleway resources on deprovision: `retain` or `delete`.", "retain")
	attributes["network_ip_range"] = optionalReplaceString("Private network IP range.")
	attributes["private_network_id"] = optionalReplaceString("Existing Scaleway private network to attach to.")
	attributes["runtime_credential_id"] = optionalReplaceString(
		"Separate credential the cluster uses at runtime, when it differs from the provisioning credential.")
	attributes["force_destroy"] = forceDestroyAttribute("Scaleway")

	response.Schema = schema.Schema{
		MarkdownDescription: "Provisions a Scaleway-backed cluster on the Ankra platform. Provisioning arguments " +
			"are immutable, so changing one forces replacement.",
		Attributes: attributes,
		Blocks:     commonProvisionedBlocks(ctx),
	}
}

func (scalewayResource *scalewayClusterResource) Configure(_ context.Context, request resource.ConfigureRequest, response *resource.ConfigureResponse) {
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
	scalewayResource.client = apiClient
}

func (scalewayResource *scalewayClusterResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	var plan scalewayClusterResourceModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}
	if scalewayResource.client == nil || scalewayResource.client.Token == "" {
		response.Diagnostics.AddError("Missing API token", missingTokenDetail)
		return
	}

	apiRequest := scalewayRequestFromModel(ctx, &plan, &response.Diagnostics)
	if response.Diagnostics.HasError() {
		return
	}

	created, err := scalewayResource.client.CreateScalewayCluster(ctx, apiRequest)
	if err != nil {
		response.Diagnostics.AddError("Unable to create Scaleway cluster", err.Error())
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

	cluster := settleProvisioned(ctx, scalewayResource.client, plan.ClusterID.ValueString(),
		plan.WaitForReady, plan.Timeouts.Create, &response.Diagnostics)
	if cluster == nil {
		return
	}
	applyClusterIdentity(&plan.State, &plan.Kind, cluster)
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
}

func (scalewayResource *scalewayClusterResource) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	var state scalewayClusterResourceModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	if scalewayResource.client == nil || scalewayResource.client.Token == "" {
		response.Diagnostics.AddError("Missing API token", missingTokenDetail)
		return
	}

	clusterID := state.ClusterID.ValueString()
	if clusterID == "" {
		clusterID = state.ID.ValueString()
	}

	cluster, err := scalewayResource.client.GetClusterByID(ctx, clusterID)
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
func (scalewayResource *scalewayClusterResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	var plan scalewayClusterResourceModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
}

func (scalewayResource *scalewayClusterResource) Delete(ctx context.Context, request resource.DeleteRequest, response *resource.DeleteResponse) {
	var state scalewayClusterResourceModel
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
	if scalewayResource.client == nil || scalewayResource.client.Token == "" {
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
	if err := scalewayResource.client.DeleteProvisionedCluster(deleteContext, client.LaneScaleway, clusterID, force); err != nil {
		response.Diagnostics.AddError("Unable to deprovision Scaleway cluster", err.Error())
	}
}

func (scalewayResource *scalewayClusterResource) ImportState(ctx context.Context, request resource.ImportStateRequest, response *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("cluster_id"), request, response)
}

func scalewayRequestFromModel(ctx context.Context, plan *scalewayClusterResourceModel, diagnostics *diag.Diagnostics) client.ScalewayClusterRequest {
	return client.ScalewayClusterRequest{
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
		GitopsCredentialName:  plan.GitopsCredentialName.ValueString(),
		GitopsRepository:      plan.GitopsRepository.ValueString(),
		Region:                plan.Region.ValueString(),
		Zone:                  plan.Zone.ValueString(),
		SSHKeyCredentialID:    plan.SSHKeyCredentialID.ValueString(),
		ControlPlaneType:      plan.ControlPlaneType.ValueString(),
		WorkerType:            plan.WorkerType.ValueString(),
		EtcdType:              plan.EtcdType.ValueString(),
		GatewayType:           plan.GatewayType.ValueString(),
		GatewayAllowedIPs:     stringListToAPI(ctx, plan.GatewayAllowedIPs, diagnostics),
		BastionPort:           plan.BastionPort.ValueInt64(),
		IncludeDNS:            plan.IncludeDNS.ValueBool(),
		RetentionPolicy:       plan.RetentionPolicy.ValueString(),
		NetworkIPRange:        optionalStringPointer(plan.NetworkIPRange),
		PrivateNetworkID:      optionalStringPointer(plan.PrivateNetworkID),
		RuntimeCredentialID:   optionalStringPointer(plan.RuntimeCredentialID),
	}
}
