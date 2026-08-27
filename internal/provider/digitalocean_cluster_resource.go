// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework-timeouts/resource/timeouts"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"ankra.io/terraform-provider-ankra/internal/client"
)

var (
	_ resource.Resource                = (*digitalOceanClusterResource)(nil)
	_ resource.ResourceWithConfigure   = (*digitalOceanClusterResource)(nil)
	_ resource.ResourceWithImportState = (*digitalOceanClusterResource)(nil)
)

type digitalOceanClusterResource struct {
	client *client.Client
}

type digitalOceanClusterResourceModel struct {
	ID                    types.String   `tfsdk:"id"`
	ClusterID             types.String   `tfsdk:"cluster_id"`
	State                 types.String   `tfsdk:"state"`
	Kind                  types.String   `tfsdk:"kind"`
	Name                  types.String   `tfsdk:"name"`
	CredentialID          types.String   `tfsdk:"credential_id"`
	Region                types.String   `tfsdk:"region"`
	SSHKeyCredentialID    types.String   `tfsdk:"ssh_key_credential_id"`
	ControlPlaneSize      types.String   `tfsdk:"control_plane_size"`
	WorkerSize            types.String   `tfsdk:"worker_size"`
	BastionSize           types.String   `tfsdk:"bastion_size"`
	EtcdSize              types.String   `tfsdk:"etcd_size"`
	NetworkIPRange        types.String   `tfsdk:"network_ip_range"`
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

// NewDigitalOceanClusterResource returns a new ankra_digitalocean_cluster resource.
func NewDigitalOceanClusterResource() resource.Resource {
	return &digitalOceanClusterResource{}
}

func (digitalOceanResource *digitalOceanClusterResource) Metadata(_ context.Context, request resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_digitalocean_cluster"
}

func (digitalOceanResource *digitalOceanClusterResource) Schema(ctx context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	attributes := commonProvisionedAttributes(ctx)
	attributes["credential_id"] = requiredReplaceString("DigitalOcean API credential id used to provision the cluster.")
	attributes["region"] = requiredReplaceString("DigitalOcean region (e.g. `fra1`, `nyc3`, `ams3`).")
	attributes["ssh_key_credential_id"] = requiredReplaceString("SSH key credential id to attach to the cluster nodes.")
	attributes["control_plane_size"] = defaultedReplaceString("Droplet size for control-plane nodes.", "s-2vcpu-4gb")
	attributes["worker_size"] = defaultedReplaceString("Droplet size for worker nodes.", "s-2vcpu-4gb")
	attributes["bastion_size"] = defaultedReplaceString("Droplet size for the bastion host.", "s-1vcpu-1gb")
	attributes["etcd_size"] = defaultedReplaceString("Droplet size for dedicated etcd nodes.", "s-2vcpu-4gb")
	attributes["network_ip_range"] = optionalReplaceString("Private network IP range.")
	attributes["force_destroy"] = forceDestroyAttribute("DigitalOcean")

	response.Schema = schema.Schema{
		MarkdownDescription: "Provisions a DigitalOcean-backed cluster on the Ankra platform. Provisioning arguments " +
			"are immutable, so changing one forces replacement.",
		Attributes: attributes,
		Blocks:     commonProvisionedBlocks(ctx),
	}
}

func (digitalOceanResource *digitalOceanClusterResource) Configure(_ context.Context, request resource.ConfigureRequest, response *resource.ConfigureResponse) {
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
	digitalOceanResource.client = apiClient
}

func (digitalOceanResource *digitalOceanClusterResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	var plan digitalOceanClusterResourceModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}
	if digitalOceanResource.client == nil || digitalOceanResource.client.Token == "" {
		response.Diagnostics.AddError("Missing API token", missingTokenDetail)
		return
	}

	apiRequest := digitalOceanRequestFromModel(&plan)
	if response.Diagnostics.HasError() {
		return
	}

	created, err := digitalOceanResource.client.CreateDigitalOceanCluster(ctx, apiRequest)
	if err != nil {
		response.Diagnostics.AddError("Unable to create DigitalOcean cluster", err.Error())
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

	cluster := settleProvisioned(ctx, digitalOceanResource.client, plan.ClusterID.ValueString(),
		plan.WaitForReady, plan.Timeouts.Create, &response.Diagnostics)
	if cluster == nil {
		return
	}
	applyClusterIdentity(&plan.State, &plan.Kind, cluster)
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
}

func (digitalOceanResource *digitalOceanClusterResource) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	var state digitalOceanClusterResourceModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	if digitalOceanResource.client == nil || digitalOceanResource.client.Token == "" {
		response.Diagnostics.AddError("Missing API token", missingTokenDetail)
		return
	}

	clusterID := state.ClusterID.ValueString()
	if clusterID == "" {
		clusterID = state.ID.ValueString()
	}

	cluster, err := digitalOceanResource.client.GetClusterByID(ctx, clusterID)
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
func (digitalOceanResource *digitalOceanClusterResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	var plan digitalOceanClusterResourceModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
}

func (digitalOceanResource *digitalOceanClusterResource) Delete(ctx context.Context, request resource.DeleteRequest, response *resource.DeleteResponse) {
	var state digitalOceanClusterResourceModel
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
	if digitalOceanResource.client == nil || digitalOceanResource.client.Token == "" {
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
	if err := digitalOceanResource.client.DeleteProvisionedCluster(deleteContext, client.LaneDigitalOcean, clusterID, force); err != nil {
		response.Diagnostics.AddError("Unable to deprovision DigitalOcean cluster", err.Error())
	}
}

func (digitalOceanResource *digitalOceanClusterResource) ImportState(ctx context.Context, request resource.ImportStateRequest, response *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("cluster_id"), request, response)
}

func digitalOceanRequestFromModel(plan *digitalOceanClusterResourceModel) client.DigitalOceanClusterRequest {
	return client.DigitalOceanClusterRequest{
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
		ControlPlaneSize:      plan.ControlPlaneSize.ValueString(),
		WorkerSize:            plan.WorkerSize.ValueString(),
		BastionSize:           plan.BastionSize.ValueString(),
		EtcdSize:              plan.EtcdSize.ValueString(),
		NetworkIPRange:        optionalStringPointer(plan.NetworkIPRange),
	}
}
