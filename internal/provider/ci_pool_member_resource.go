// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64default"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"ankra.io/terraform-provider-ankra/internal/client"
)

var (
	_ resource.Resource                = (*ciPoolMemberResource)(nil)
	_ resource.ResourceWithConfigure   = (*ciPoolMemberResource)(nil)
	_ resource.ResourceWithImportState = (*ciPoolMemberResource)(nil)
)

type ciPoolMemberResource struct {
	configuredClient
}

type ciPoolMemberModel struct {
	ID          types.String `tfsdk:"id"`
	ClusterID   types.String `tfsdk:"cluster_id"`
	Weight      types.Int64  `tfsdk:"weight"`
	ClusterName types.String `tfsdk:"cluster_name"`
	IsPrimary   types.Bool   `tfsdk:"is_primary"`
}

// NewCIPoolMemberResource returns the ankra_ci_pool_member resource, the
// counterpart of `ankra org ci-settings pool add` and `pool remove`.
func NewCIPoolMemberResource() resource.Resource {
	return &ciPoolMemberResource{}
}

func (member *ciPoolMemberResource) Metadata(_ context.Context, request resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_ci_pool_member"
}

func (member *ciPoolMemberResource) Schema(_ context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: "A cluster listed in the organisation's CI cluster pool, sharing pipeline runs by weight. " +
			"The counterpart of `ankra org ci-settings pool add` and `pool remove`. Changes need organisation admin.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "The cluster id.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"cluster_id": schema.StringAttribute{
				MarkdownDescription: "Cluster to list. It must belong to the organisation and be able to run pipeline steps.",
				Required:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"weight": schema.Int64Attribute{
				MarkdownDescription: "Relative share of runs, 1 to 1000. Defaults to 100.",
				Optional:            true,
				Computed:            true,
				Default:             int64default.StaticInt64(100),
			},
			"cluster_name": schema.StringAttribute{
				MarkdownDescription: "Cluster name.",
				Computed:            true,
			},
			"is_primary": schema.BoolAttribute{
				MarkdownDescription: "Whether the cluster is also the organisation's pipeline cluster (`ci_cluster_id`).",
				Computed:            true,
			},
		},
	}
}

func (member *ciPoolMemberResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	apiClient := member.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var plan ciPoolMemberModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}
	pool, err := apiClient.PutOrganisationCIPoolMember(ctx, plan.ClusterID.ValueString(), plan.Weight.ValueInt64())
	if err != nil {
		response.Diagnostics.AddError("Unable to list cluster in the CI pool", err.Error())
		return
	}
	ciPoolMemberApply(&plan, pool)
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
	response.Diagnostics.Append(trackScope(ctx, apiClient, response.Private)...)
}

func (member *ciPoolMemberResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	apiClient := member.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var plan ciPoolMemberModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}
	pool, err := apiClient.PutOrganisationCIPoolMember(ctx, plan.ClusterID.ValueString(), plan.Weight.ValueInt64())
	if err != nil {
		response.Diagnostics.AddError("Unable to re-weight CI pool member", err.Error())
		return
	}
	ciPoolMemberApply(&plan, pool)
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
}

func (member *ciPoolMemberResource) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	apiClient := member.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state ciPoolMemberModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	pool, err := apiClient.GetOrganisationCIPool(ctx)
	if err != nil {
		response.Diagnostics.AddError("Unable to read the CI pool", err.Error())
		return
	}
	if ciPoolListedMember(pool, state.ClusterID.ValueString()) == nil {
		response.State.RemoveResource(ctx)
		return
	}
	ciPoolMemberApply(&state, pool)
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
	response.Diagnostics.Append(trackScope(ctx, apiClient, response.Private)...)
}

func (member *ciPoolMemberResource) Delete(ctx context.Context, request resource.DeleteRequest, response *resource.DeleteResponse) {
	apiClient := member.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state ciPoolMemberModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	err := apiClient.RemoveOrganisationCIPoolMember(ctx, state.ClusterID.ValueString())
	if err == nil || deletedWhenNotFound(ctx, err, apiClient, request.Private, &response.Diagnostics) || response.Diagnostics.HasError() {
		return
	}
	response.Diagnostics.AddError("Unable to remove cluster from the CI pool", err.Error())
}

// ImportState imports by cluster id.
func (member *ciPoolMemberResource) ImportState(ctx context.Context, request resource.ImportStateRequest, response *resource.ImportStateResponse) {
	response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("id"), request.ID)...)
	response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("cluster_id"), request.ID)...)
}

func ciPoolListedMember(pool *client.OrganisationCIPool, clusterID string) *client.OrganisationCIPoolMember {
	for index := range pool.Members {
		if pool.Members[index].ClusterID == clusterID && pool.Members[index].IsListed {
			return &pool.Members[index]
		}
	}
	return nil
}

func ciPoolMemberApply(model *ciPoolMemberModel, pool *client.OrganisationCIPool) {
	model.ID = model.ClusterID
	listed := ciPoolListedMember(pool, model.ClusterID.ValueString())
	if listed == nil {
		model.ClusterName = types.StringNull()
		model.IsPrimary = types.BoolValue(false)
		return
	}
	model.Weight = types.Int64Value(listed.Weight)
	model.ClusterName = types.StringValue(listed.ClusterName)
	model.IsPrimary = types.BoolValue(listed.IsPrimary)
}
