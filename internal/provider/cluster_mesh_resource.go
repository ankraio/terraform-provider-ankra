// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"ankra.io/terraform-provider-ankra/internal/client"
)

var (
	_ resource.Resource                = (*clusterMeshResource)(nil)
	_ resource.ResourceWithConfigure   = (*clusterMeshResource)(nil)
	_ resource.ResourceWithImportState = (*clusterMeshResource)(nil)
	_ resource.Resource                = (*clusterMeshMemberResource)(nil)
	_ resource.ResourceWithConfigure   = (*clusterMeshMemberResource)(nil)
	_ resource.ResourceWithImportState = (*clusterMeshMemberResource)(nil)
)

type clusterMeshResource struct {
	configuredClient
}

type clusterMeshModel struct {
	ID     types.String `tfsdk:"id"`
	Name   types.String `tfsdk:"name"`
	Slug   types.String `tfsdk:"slug"`
	Status types.String `tfsdk:"status"`
}

// NewClusterMeshResource returns the ankra_cluster_mesh resource, the
// counterpart of `ankra cluster mesh create|delete`.
func NewClusterMeshResource() resource.Resource {
	return &clusterMeshResource{}
}

func (meshResource *clusterMeshResource) Metadata(_ context.Context, request resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_cluster_mesh"
}

func (meshResource *clusterMeshResource) Schema(_ context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: "An organisation's Cilium ClusterMesh. Clusters join it with `ankra_cluster_mesh_member`. " +
			"The counterpart of `ankra cluster mesh create` and `delete`. The platform deletes only an empty mesh, so remove its members first " +
			"(Terraform does when the members reference the mesh).",
		Attributes: map[string]schema.Attribute{
			"id": clusterSettingsIDAttribute("Identifier of the mesh."),
			"name": schema.StringAttribute{
				MarkdownDescription: "Mesh name. Changing it replaces the mesh.",
				Required:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"slug": schema.StringAttribute{
				MarkdownDescription: "Slug the platform derived from the name.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"status": schema.StringAttribute{
				MarkdownDescription: "Mesh status.",
				Computed:            true,
			},
		},
	}
}

func (meshResource *clusterMeshResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	apiClient := meshResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var plan clusterMeshModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}
	mesh, err := apiClient.CreateClusterMesh(ctx, plan.Name.ValueString())
	if err != nil {
		response.Diagnostics.AddError("Unable to create cluster mesh", err.Error())
		return
	}
	applyClusterMesh(&plan, mesh)
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
}

func (meshResource *clusterMeshResource) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	apiClient := meshResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state clusterMeshModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	mesh, err := apiClient.GetClusterMesh(ctx, state.ID.ValueString())
	if removeWhenNotFound(ctx, err, response) {
		return
	}
	if err != nil {
		response.Diagnostics.AddError("Unable to read cluster mesh", err.Error())
		return
	}
	applyClusterMesh(&state, mesh)
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
}

// Update is never called: name is the only configurable attribute and it
// forces replacement.
func (meshResource *clusterMeshResource) Update(_ context.Context, _ resource.UpdateRequest, response *resource.UpdateResponse) {
	response.Diagnostics.AddError("Cluster mesh cannot be updated", "Every change to a cluster mesh replaces it. This is a bug in the provider.")
}

func (meshResource *clusterMeshResource) Delete(ctx context.Context, request resource.DeleteRequest, response *resource.DeleteResponse) {
	apiClient := meshResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state clusterMeshModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	if err := apiClient.DeleteClusterMesh(ctx, state.ID.ValueString()); err != nil {
		response.Diagnostics.AddError("Unable to delete cluster mesh", err.Error())
	}
}

// ImportState imports by mesh id.
func (meshResource *clusterMeshResource) ImportState(ctx context.Context, request resource.ImportStateRequest, response *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), request, response)
}

func applyClusterMesh(model *clusterMeshModel, mesh *client.ClusterMesh) {
	model.ID = types.StringValue(mesh.ID)
	model.Name = types.StringValue(mesh.Name)
	model.Slug = types.StringValue(mesh.Slug)
	model.Status = types.StringValue(mesh.Status)
}

type clusterMeshMemberResource struct {
	configuredClient
}

type clusterMeshMemberModel struct {
	ID                types.String `tfsdk:"id"`
	ClusterID         types.String `tfsdk:"cluster_id"`
	MeshID            types.String `tfsdk:"mesh_id"`
	CiliumClusterID   types.Int64  `tfsdk:"cilium_cluster_id"`
	CiliumClusterName types.String `tfsdk:"cilium_cluster_name"`
	Status            types.String `tfsdk:"status"`
}

// NewClusterMeshMemberResource returns the ankra_cluster_mesh_member
// resource, the counterpart of `ankra cluster mesh join|leave`.
func NewClusterMeshMemberResource() resource.Resource {
	return &clusterMeshMemberResource{}
}

func (memberResource *clusterMeshMemberResource) Metadata(_ context.Context, request resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_cluster_mesh_member"
}

func (memberResource *clusterMeshMemberResource) Schema(_ context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: "A cluster's membership of a Cilium ClusterMesh. The counterpart of `ankra cluster mesh join` and `leave`. " +
			"Run `ankra cluster mesh make-ready` (an operation, not managed here) before a cluster joins.",
		Attributes: map[string]schema.Attribute{
			"id":         clusterSettingsIDAttribute("`<cluster_id>/<mesh_id>`."),
			"cluster_id": clusterSettingsClusterIDAttribute(),
			"mesh_id": schema.StringAttribute{
				MarkdownDescription: "Identifier of the mesh. Changing it replaces the membership.",
				Required:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"cilium_cluster_id": schema.Int64Attribute{
				MarkdownDescription: "Cilium cluster identity the cluster carries in the mesh.",
				Computed:            true,
			},
			"cilium_cluster_name": schema.StringAttribute{
				MarkdownDescription: "Cilium cluster name the cluster carries in the mesh.",
				Computed:            true,
			},
			"status": schema.StringAttribute{
				MarkdownDescription: "Membership status.",
				Computed:            true,
			},
		},
	}
}

func (memberResource *clusterMeshMemberResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	apiClient := memberResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var plan clusterMeshMemberModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}
	if err := apiClient.JoinClusterMesh(ctx, plan.MeshID.ValueString(), plan.ClusterID.ValueString()); err != nil {
		response.Diagnostics.AddError("Unable to join cluster mesh", err.Error())
		return
	}
	plan.ID = types.StringValue(plan.ClusterID.ValueString() + "/" + plan.MeshID.ValueString())
	mesh, err := apiClient.GetClusterMesh(ctx, plan.MeshID.ValueString())
	if err != nil {
		response.Diagnostics.AddError("Unable to read cluster mesh after joining", err.Error())
		return
	}
	member := clusterMeshMemberOf(mesh, plan.ClusterID.ValueString())
	if member == nil {
		response.Diagnostics.AddError("Unable to join cluster mesh", "The platform accepted the join but the mesh does not list the cluster.")
		return
	}
	applyClusterMeshMember(&plan, member)
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
}

func (memberResource *clusterMeshMemberResource) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	apiClient := memberResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state clusterMeshMemberModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	mesh, err := apiClient.GetClusterMesh(ctx, state.MeshID.ValueString())
	if removeWhenNotFound(ctx, err, response) {
		return
	}
	if err != nil {
		response.Diagnostics.AddError("Unable to read cluster mesh", err.Error())
		return
	}
	member := clusterMeshMemberOf(mesh, state.ClusterID.ValueString())
	if member == nil {
		response.State.RemoveResource(ctx)
		return
	}
	state.ID = types.StringValue(state.ClusterID.ValueString() + "/" + state.MeshID.ValueString())
	applyClusterMeshMember(&state, member)
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
}

// Update is never called: both configurable attributes force replacement.
func (memberResource *clusterMeshMemberResource) Update(_ context.Context, _ resource.UpdateRequest, response *resource.UpdateResponse) {
	response.Diagnostics.AddError("Cluster mesh membership cannot be updated", "Every change to a membership replaces it. This is a bug in the provider.")
}

func (memberResource *clusterMeshMemberResource) Delete(ctx context.Context, request resource.DeleteRequest, response *resource.DeleteResponse) {
	apiClient := memberResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state clusterMeshMemberModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	if err := apiClient.LeaveClusterMesh(ctx, state.MeshID.ValueString(), state.ClusterID.ValueString()); err != nil {
		response.Diagnostics.AddError("Unable to leave cluster mesh", err.Error())
	}
}

// ImportState imports by "<cluster_id>/<mesh_id>".
func (memberResource *clusterMeshMemberResource) ImportState(ctx context.Context, request resource.ImportStateRequest, response *resource.ImportStateResponse) {
	clusterSettingsImportComposite(ctx, request, response, "mesh_id")
}

func clusterMeshMemberOf(mesh *client.ClusterMesh, clusterID string) *client.ClusterMeshMember {
	for index := range mesh.Members {
		if mesh.Members[index].ClusterID == clusterID {
			return &mesh.Members[index]
		}
	}
	return nil
}

func applyClusterMeshMember(model *clusterMeshMemberModel, member *client.ClusterMeshMember) {
	model.CiliumClusterID = types.Int64Value(member.CiliumClusterID)
	model.CiliumClusterName = types.StringValue(member.CiliumClusterName)
	model.Status = types.StringValue(member.Status)
}
