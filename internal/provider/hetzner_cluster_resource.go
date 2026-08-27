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
	_ resource.Resource                = (*hetznerClusterResource)(nil)
	_ resource.ResourceWithConfigure   = (*hetznerClusterResource)(nil)
	_ resource.ResourceWithImportState = (*hetznerClusterResource)(nil)
)

type hetznerClusterResource struct {
	client *client.Client
}

type hetznerClusterResourceModel struct {
	ID                     types.String   `tfsdk:"id"`
	ClusterID              types.String   `tfsdk:"cluster_id"`
	State                  types.String   `tfsdk:"state"`
	Kind                   types.String   `tfsdk:"kind"`
	Name                   types.String   `tfsdk:"name"`
	CredentialID           types.String   `tfsdk:"credential_id"`
	Location               types.String   `tfsdk:"location"`
	Distribution           types.String   `tfsdk:"distribution"`
	CNI                    types.String   `tfsdk:"cni"`
	KubernetesVersion      types.String   `tfsdk:"kubernetes_version"`
	ControlPlaneCount      types.Int64    `tfsdk:"control_plane_count"`
	ControlPlaneServerType types.String   `tfsdk:"control_plane_server_type"`
	WorkerCount            types.Int64    `tfsdk:"worker_count"`
	WorkerServerType       types.String   `tfsdk:"worker_server_type"`
	BastionServerType      types.String   `tfsdk:"bastion_server_type"`
	NetworkIPRange         types.String   `tfsdk:"network_ip_range"`
	SubnetRange            types.String   `tfsdk:"subnet_range"`
	ExternalCloudProvider  types.Bool     `tfsdk:"external_cloud_provider"`
	IncludeNetworking      types.Bool     `tfsdk:"include_networking"`
	EtcdTopology           types.String   `tfsdk:"etcd_topology"`
	EtcdNodeCount          types.Int64    `tfsdk:"etcd_node_count"`
	EtcdServerType         types.String   `tfsdk:"etcd_server_type"`
	GitopsBranch           types.String   `tfsdk:"gitops_branch"`
	GitopsCredentialName   types.String   `tfsdk:"gitops_credential_name"`
	GitopsRepository       types.String   `tfsdk:"gitops_repository"`
	SSHKeyCredentialID     types.String   `tfsdk:"ssh_key_credential_id"`
	Description            types.String   `tfsdk:"description"`
	ForceDestroy           types.Bool     `tfsdk:"force_destroy"`
	WaitForReady           types.Bool     `tfsdk:"wait_for_ready"`
	Timeouts               timeouts.Value `tfsdk:"timeouts"`
}

// NewHetznerClusterResource returns a new ankra_hetzner_cluster resource.
func NewHetznerClusterResource() resource.Resource {
	return &hetznerClusterResource{}
}

func (hetznerResource *hetznerClusterResource) Metadata(_ context.Context, request resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_hetzner_cluster"
}

func (hetznerResource *hetznerClusterResource) Schema(ctx context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	attributes := commonProvisionedAttributes(ctx)
	attributes["credential_id"] = requiredReplaceString("Hetzner API credential id used to provision the cluster.")
	attributes["location"] = requiredReplaceString("Hetzner location (e.g. `fsn1`, `nbg1`, `hel1`).")
	attributes["kubernetes_version"] = optionalReplaceString(
		"Kubernetes version (see `ankra cluster k3s-versions`). Defaults to the platform's stable version when unset.")
	attributes["control_plane_server_type"] = defaultedReplaceString("Hetzner server type for control-plane nodes.", "cx33")
	attributes["worker_server_type"] = defaultedReplaceString("Hetzner server type for worker nodes.", "cx23")
	attributes["bastion_server_type"] = defaultedReplaceString("Hetzner server type for the bastion host.", "cx23")
	attributes["network_ip_range"] = defaultedReplaceString("Private network IP range.", "10.0.0.0/16")
	attributes["subnet_range"] = defaultedReplaceString("Private subnet range.", "10.0.1.0/24")
	attributes["etcd_server_type"] = defaultedReplaceString("Hetzner server type for dedicated etcd nodes.", "cx33")
	attributes["ssh_key_credential_id"] = optionalReplaceString("SSH key credential id to attach to the cluster nodes.")
	attributes["force_destroy"] = forceDestroyAttribute("Hetzner")

	response.Schema = schema.Schema{
		MarkdownDescription: "Provisions a Hetzner-backed cluster on the Ankra platform. Provisioning arguments " +
			"are immutable, so changing one forces replacement.",
		Attributes: attributes,
		Blocks:     commonProvisionedBlocks(ctx),
	}
}

func (hetznerResource *hetznerClusterResource) Configure(_ context.Context, request resource.ConfigureRequest, response *resource.ConfigureResponse) {
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
	hetznerResource.client = apiClient
}

func (hetznerResource *hetznerClusterResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	var plan hetznerClusterResourceModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}

	if clientError := hetznerResource.requireClient(); clientError != nil {
		response.Diagnostics.AddError("Missing API token", missingTokenDetail)
		return
	}

	created, err := hetznerResource.client.CreateHetznerCluster(ctx, hetznerRequestFromModel(&plan))
	if err != nil {
		response.Diagnostics.AddError("Unable to create Hetzner cluster", err.Error())
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

	cluster := settleProvisioned(ctx, hetznerResource.client, plan.ClusterID.ValueString(),
		plan.WaitForReady, plan.Timeouts.Create, &response.Diagnostics)
	if cluster == nil {
		return
	}
	applyClusterIdentity(&plan.State, &plan.Kind, cluster)
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
}

func (hetznerResource *hetznerClusterResource) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	var state hetznerClusterResourceModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	if clientError := hetznerResource.requireClient(); clientError != nil {
		response.Diagnostics.AddError("Missing API token", missingTokenDetail)
		return
	}

	clusterID := state.ClusterID.ValueString()
	if clusterID == "" {
		clusterID = state.ID.ValueString()
	}

	cluster, err := hetznerResource.client.GetClusterByID(ctx, clusterID)
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
func (hetznerResource *hetznerClusterResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	var plan hetznerClusterResourceModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
}

func (hetznerResource *hetznerClusterResource) Delete(ctx context.Context, request resource.DeleteRequest, response *resource.DeleteResponse) {
	var state hetznerClusterResourceModel
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
	if clientError := hetznerResource.requireClient(); clientError != nil {
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
	if err := hetznerResource.client.DeleteHetznerCluster(deleteContext, clusterID, force); err != nil {
		response.Diagnostics.AddError("Unable to deprovision Hetzner cluster", err.Error())
	}
}

func (hetznerResource *hetznerClusterResource) ImportState(ctx context.Context, request resource.ImportStateRequest, response *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("cluster_id"), request, response)
}

// requireClient reports whether the resource has a usable API client, rather
// than dereferencing one the provider never configured.
func (hetznerResource *hetznerClusterResource) requireClient() error {
	if hetznerResource.client == nil || hetznerResource.client.Token == "" {
		return errMissingToken
	}
	return nil
}

func hetznerRequestFromModel(plan *hetznerClusterResourceModel) client.HetznerClusterRequest {
	return client.HetznerClusterRequest{
		Name:                   plan.Name.ValueString(),
		CredentialID:           plan.CredentialID.ValueString(),
		Location:               plan.Location.ValueString(),
		Distribution:           plan.Distribution.ValueString(),
		CNI:                    plan.CNI.ValueString(),
		ControlPlaneCount:      plan.ControlPlaneCount.ValueInt64(),
		ControlPlaneServerType: plan.ControlPlaneServerType.ValueString(),
		WorkerCount:            plan.WorkerCount.ValueInt64(),
		WorkerServerType:       plan.WorkerServerType.ValueString(),
		BastionServerType:      plan.BastionServerType.ValueString(),
		NetworkIPRange:         plan.NetworkIPRange.ValueString(),
		SubnetRange:            plan.SubnetRange.ValueString(),
		ExternalCloudProvider:  plan.ExternalCloudProvider.ValueBool(),
		IncludeNetworking:      plan.IncludeNetworking.ValueBool(),
		EtcdTopology:           plan.EtcdTopology.ValueString(),
		EtcdNodeCount:          plan.EtcdNodeCount.ValueInt64(),
		EtcdServerType:         plan.EtcdServerType.ValueString(),
		GitopsBranch:           plan.GitopsBranch.ValueString(),
		KubernetesVersion:      optionalStringPointer(plan.KubernetesVersion),
		Description:            optionalStringPointer(plan.Description),
		SSHKeyCredentialID:     optionalStringPointer(plan.SSHKeyCredentialID),
		GitopsCredentialName:   optionalStringPointer(plan.GitopsCredentialName),
		GitopsRepository:       optionalStringPointer(plan.GitopsRepository),
	}
}
