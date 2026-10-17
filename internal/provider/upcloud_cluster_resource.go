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
	_ resource.Resource                = (*upCloudClusterResource)(nil)
	_ resource.ResourceWithConfigure   = (*upCloudClusterResource)(nil)
	_ resource.ResourceWithImportState = (*upCloudClusterResource)(nil)
)

type upCloudClusterResource struct {
	client *client.Client
}

type upCloudClusterResourceModel struct {
	ID                    types.String   `tfsdk:"id"`
	ClusterID             types.String   `tfsdk:"cluster_id"`
	State                 types.String   `tfsdk:"state"`
	Kind                  types.String   `tfsdk:"kind"`
	ProjectID             types.String   `tfsdk:"project_id"`
	Name                  types.String   `tfsdk:"name"`
	CredentialID          types.String   `tfsdk:"credential_id"`
	Zone                  types.String   `tfsdk:"zone"`
	SSHKeyCredentialID    types.String   `tfsdk:"ssh_key_credential_id"`
	ControlPlanePlan      types.String   `tfsdk:"control_plane_plan"`
	WorkerPlan            types.String   `tfsdk:"worker_plan"`
	BastionPlan           types.String   `tfsdk:"bastion_plan"`
	EtcdPlan              types.String   `tfsdk:"etcd_plan"`
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
	AcceptVolumeDataLoss  types.Bool     `tfsdk:"accept_volume_data_loss"`
	ForceDestroy          types.Bool     `tfsdk:"force_destroy"`
	WaitForReady          types.Bool     `tfsdk:"wait_for_ready"`
	Timeouts              timeouts.Value `tfsdk:"timeouts"`
}

// NewUpCloudClusterResource returns a new ankra_upcloud_cluster resource.
func NewUpCloudClusterResource() resource.Resource {
	return &upCloudClusterResource{}
}

func (upCloudResource *upCloudClusterResource) Metadata(_ context.Context, request resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_upcloud_cluster"
}

func (upCloudResource *upCloudClusterResource) Schema(ctx context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	attributes := commonProvisionedAttributes(ctx)
	attributes["credential_id"] = requiredReplaceString("UpCloud API credential id used to provision the cluster.")
	attributes["zone"] = requiredReplaceString("UpCloud zone (e.g. `de-fra1`, `fi-hel1`).")
	attributes["ssh_key_credential_id"] = requiredReplaceString("SSH key credential id to attach to the cluster nodes.")
	attributes["control_plane_plan"] = defaultedReplaceString("Server plan for control-plane nodes.", "2xCPU-4GB")
	attributes["worker_plan"] = defaultedReplaceString("Server plan for worker nodes.", "2xCPU-4GB")
	attributes["bastion_plan"] = defaultedReplaceString("Server plan for the bastion host.", "1xCPU-1GB")
	attributes["etcd_plan"] = defaultedReplaceString("Server plan for dedicated etcd nodes.", "2xCPU-4GB")
	attributes["network_ip_range"] = optionalReplaceString("Private network IP range.")
	attributes["force_destroy"] = forceDestroyAttribute("UpCloud")
	attributes["accept_volume_data_loss"] = originalLaneAcceptVolumeDataLossAttribute()

	response.Schema = schema.Schema{
		MarkdownDescription: "Provisions a UpCloud-backed cluster on the Ankra platform. Provisioning arguments " +
			"are immutable, so changing one forces replacement.",
		Attributes: attributes,
		Blocks:     commonProvisionedBlocks(ctx),
	}
}

func (upCloudResource *upCloudClusterResource) Configure(_ context.Context, request resource.ConfigureRequest, response *resource.ConfigureResponse) {
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
	upCloudResource.client = apiClient
}

func (upCloudResource *upCloudClusterResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	var plan upCloudClusterResourceModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}
	if upCloudResource.client == nil || upCloudResource.client.Token == "" {
		response.Diagnostics.AddError("Missing API token", missingTokenDetail)
		return
	}

	apiRequest := upCloudRequestFromModel(&plan)
	if response.Diagnostics.HasError() {
		return
	}

	created, err := upCloudResource.client.CreateUpCloudCluster(ctx, apiRequest)
	if err != nil {
		response.Diagnostics.AddError("Unable to create UpCloud cluster", err.Error())
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
	plan.ProjectID = optionalString(upCloudResource.client.ScopeProjectID())

	// Persist the id before waiting. A wait that times out must not lose the
	// cluster the platform already started building.
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
	response.Diagnostics.Append(trackScope(ctx, upCloudResource.client, response.Private)...)
	if response.Diagnostics.HasError() {
		return
	}

	cluster := settleProvisioned(ctx, upCloudResource.client, plan.ClusterID.ValueString(),
		plan.WaitForReady, plan.Timeouts.Create, &response.Diagnostics)
	if cluster == nil {
		return
	}
	applyClusterIdentity(&plan.State, &plan.Kind, &plan.ProjectID, upCloudResource.client, cluster)
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
}

func (upCloudResource *upCloudClusterResource) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	var state upCloudClusterResourceModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	if upCloudResource.client == nil || upCloudResource.client.Token == "" {
		response.Diagnostics.AddError("Missing API token", missingTokenDetail)
		return
	}

	clusterID := state.ClusterID.ValueString()
	if clusterID == "" {
		clusterID = state.ID.ValueString()
	}

	cluster, err := upCloudResource.client.GetClusterByID(ctx, clusterID)
	if err != nil {
		response.Diagnostics.AddError("Unable to read cluster", err.Error())
		return
	}
	if cluster == nil {
		removeMissingCluster(ctx, upCloudResource.client, response)
		return
	}

	state.ID = types.StringValue(cluster.ID)
	state.ClusterID = types.StringValue(cluster.ID)
	if cluster.Name != "" {
		state.Name = types.StringValue(cluster.Name)
	}
	applyClusterIdentity(&state.State, &state.Kind, &state.ProjectID, upCloudResource.client, cluster)
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
	response.Diagnostics.Append(trackScope(ctx, upCloudResource.client, response.Private)...)
}

// Update carries through the arguments that can change in place: force_destroy
// and wait_for_ready affect future operations rather than the running cluster.
// Every provisioning argument carries RequiresReplace, so it forces a replace.
func (upCloudResource *upCloudClusterResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	var plan upCloudClusterResourceModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
	response.Diagnostics.Append(clearClusterImported(ctx, response.Private)...)
}

func (upCloudResource *upCloudClusterResource) Delete(ctx context.Context, request resource.DeleteRequest, response *resource.DeleteResponse) {
	var state upCloudClusterResourceModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	clusterID := originalLaneClusterID(state.ClusterID, state.ID)
	if clusterID == "" {
		return
	}
	if upCloudResource.client == nil || upCloudResource.client.Token == "" {
		response.Diagnostics.AddError("Missing API token", missingTokenDetail)
		return
	}
	deprovisionLaneClusterAndWait(ctx, upCloudResource.client, laneDeprovision{
		lane:        client.LaneUpCloud,
		displayName: "UpCloud",
		clusterID:   clusterID,
		options: client.LaneDeprovisionOptions{
			Force:                originalLaneForceDestroy(state.ForceDestroy),
			AcceptVolumeDataLoss: state.AcceptVolumeDataLoss.ValueBool(),
		},
		deleteTimeout: state.Timeouts.Delete,
	}, request.Private, &response.Diagnostics)
}

func (upCloudResource *upCloudClusterResource) ImportState(ctx context.Context, request resource.ImportStateRequest, response *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("cluster_id"), request, response)
	response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("id"), request.ID)...)
	response.Diagnostics.Append(markClusterImported(ctx, response.Private)...)
}

func upCloudRequestFromModel(plan *upCloudClusterResourceModel) client.UpCloudClusterRequest {
	return client.UpCloudClusterRequest{
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
		Zone:                  plan.Zone.ValueString(),
		SSHKeyCredentialID:    plan.SSHKeyCredentialID.ValueString(),
		ControlPlanePlan:      plan.ControlPlanePlan.ValueString(),
		WorkerPlan:            plan.WorkerPlan.ValueString(),
		BastionPlan:           plan.BastionPlan.ValueString(),
		EtcdPlan:              plan.EtcdPlan.ValueString(),
		NetworkIPRange:        optionalStringPointer(plan.NetworkIPRange),
	}
}
