// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"ankra.io/terraform-provider-ankra/internal/client"
)

var (
	_ datasource.DataSourceWithConfigure = (*kubernetesVersionsDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*playgroundPlansDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*nodeGroupsDataSource)(nil)
)

// ---------------------------------------------------------------------------
// ankra_kubernetes_versions

type kubernetesVersionsDataSource struct {
	configuredDataSourceClient
}

type kubernetesVersionEntryModel struct {
	Version     types.String `tfsdk:"version"`
	Channel     types.String `tfsdk:"channel"`
	PublishedAt types.String `tfsdk:"published_at"`
}

type kubernetesVersionsModel struct {
	Distribution  types.String                  `tfsdk:"distribution"`
	StableVersion types.String                  `tfsdk:"stable_version"`
	Versions      []kubernetesVersionEntryModel `tfsdk:"versions"`
}

// NewKubernetesVersionsDataSource returns ankra_kubernetes_versions, the
// counterpart of `ankra cluster kubeadm-versions` and `ankra cluster
// k3s-versions`.
func NewKubernetesVersionsDataSource() datasource.DataSource {
	return &kubernetesVersionsDataSource{}
}

func (versions *kubernetesVersionsDataSource) Metadata(_ context.Context, request datasource.MetadataRequest, response *datasource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_kubernetes_versions"
}

func (versions *kubernetesVersionsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, response *datasource.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: "The Kubernetes versions the platform supports for a distribution, to pin a cluster's " +
			"`kubernetes_version`. The counterpart of `ankra cluster kubeadm-versions` and `ankra cluster k3s-versions`.",
		Attributes: map[string]schema.Attribute{
			"distribution": schema.StringAttribute{
				MarkdownDescription: "`kubeadm` or `k3s`.",
				Required:            true,
				Validators:          []validator.String{laneOneOf(client.DistributionKubeadm, client.DistributionK3s)},
			},
			"stable_version": schema.StringAttribute{
				MarkdownDescription: "The version a cluster gets when it pins none; null when the catalogue names none.",
				Computed:            true,
			},
			"versions": schema.ListNestedAttribute{
				MarkdownDescription: "Supported versions, newest first as the platform orders them.",
				Computed:            true,
				NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
					"version":      schema.StringAttribute{MarkdownDescription: "Version, e.g. `v1.33.4`.", Computed: true},
					"channel":      schema.StringAttribute{MarkdownDescription: "Minor channel, e.g. `v1.33`.", Computed: true},
					"published_at": schema.StringAttribute{MarkdownDescription: "Publication time, when known.", Computed: true},
				}},
			},
		},
	}
}

func (versions *kubernetesVersionsDataSource) Read(ctx context.Context, request datasource.ReadRequest, response *datasource.ReadResponse) {
	apiClient := versions.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var model kubernetesVersionsModel
	response.Diagnostics.Append(request.Config.Get(ctx, &model)...)
	if response.Diagnostics.HasError() {
		return
	}
	catalogue, err := apiClient.ListKubernetesVersions(ctx, model.Distribution.ValueString())
	if err != nil {
		response.Diagnostics.AddError("Unable to list Kubernetes versions", err.Error())
		return
	}
	model.StableVersion = types.StringPointerValue(catalogue.StableVersion)
	model.Versions = make([]kubernetesVersionEntryModel, 0, len(catalogue.Versions))
	for _, entry := range catalogue.Versions {
		model.Versions = append(model.Versions, kubernetesVersionEntryModel{
			Version:     types.StringValue(entry.Version),
			Channel:     types.StringValue(entry.Channel),
			PublishedAt: types.StringPointerValue(entry.PublishedAt),
		})
	}
	response.Diagnostics.Append(response.State.Set(ctx, &model)...)
}

// ---------------------------------------------------------------------------
// ankra_playground_plans

type playgroundPlansDataSource struct {
	configuredDataSourceClient
}

type playgroundPlanModel struct {
	ID                types.String  `tfsdk:"id"`
	DisplayName       types.String  `tfsdk:"display_name"`
	Summary           types.String  `tfsdk:"summary"`
	Vcpus             types.Int64   `tfsdk:"vcpus"`
	MemoryGB          types.Float64 `tfsdk:"memory_gb"`
	StorageGB         types.Int64   `tfsdk:"storage_gb"`
	MaxPods           types.Int64   `tfsdk:"max_pods"`
	PriceMonthlyCents types.Int64   `tfsdk:"price_monthly_cents"`
	Currency          types.String  `tfsdk:"currency"`
	Available         types.Bool    `tfsdk:"available"`
}

type playgroundPlansModel struct {
	DefaultPlanID              types.String          `tfsdk:"default_plan_id"`
	Currency                   types.String          `tfsdk:"currency"`
	OrganisationHasPaidPlan    types.Bool            `tfsdk:"organisation_has_paid_plan"`
	OrganisationHasPaymentCard types.Bool            `tfsdk:"organisation_has_payment_card"`
	QuotaLimitMiB              types.Int64           `tfsdk:"quota_limit_mib"`
	QuotaUsedMiB               types.Int64           `tfsdk:"quota_used_mib"`
	Plans                      []playgroundPlanModel `tfsdk:"plans"`
}

// NewPlaygroundPlansDataSource returns ankra_playground_plans, the
// counterpart of `ankra cluster playground plans`.
func NewPlaygroundPlansDataSource() datasource.DataSource {
	return &playgroundPlansDataSource{}
}

func (plans *playgroundPlansDataSource) Metadata(_ context.Context, request datasource.MetadataRequest, response *datasource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_playground_plans"
}

func (plans *playgroundPlansDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, response *datasource.SchemaResponse) {
	computedString := func(description string) schema.StringAttribute {
		return schema.StringAttribute{MarkdownDescription: description, Computed: true}
	}
	computedInt64 := func(description string) schema.Int64Attribute {
		return schema.Int64Attribute{MarkdownDescription: description, Computed: true}
	}
	computedBool := func(description string) schema.BoolAttribute {
		return schema.BoolAttribute{MarkdownDescription: description, Computed: true}
	}
	response.Schema = schema.Schema{
		MarkdownDescription: "The playground sizes the organisation can order, for `ankra_playground.plan`. The " +
			"counterpart of `ankra cluster playground plans`.",
		Attributes: map[string]schema.Attribute{
			"default_plan_id":               computedString("Plan a playground gets when none is ordered (the free trial)."),
			"currency":                      computedString("Currency of the prices."),
			"organisation_has_paid_plan":    computedBool("Whether the organisation can be invoiced for a paid size."),
			"organisation_has_payment_card": computedBool("Whether the organisation has a payment card on file."),
			"quota_limit_mib":               computedInt64("The organisation's playground memory budget, in MiB."),
			"quota_used_mib":                computedInt64("Memory its playgrounds already use, in MiB."),
			"plans": schema.ListNestedAttribute{
				MarkdownDescription: "Orderable sizes.",
				Computed:            true,
				NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
					"id":                  computedString("Plan id, the value of `ankra_playground.plan`."),
					"display_name":        computedString("Display name."),
					"summary":             computedString("One-line description."),
					"vcpus":               computedInt64("vCPUs."),
					"memory_gb":           schema.Float64Attribute{MarkdownDescription: "Memory, in GB.", Computed: true},
					"storage_gb":          computedInt64("Storage, in GB."),
					"max_pods":            computedInt64("Pod limit."),
					"price_monthly_cents": computedInt64("Monthly price, in cents."),
					"currency":            computedString("Currency of the price."),
					"available":           computedBool("Whether a host has capacity for this size right now."),
				}},
			},
		},
	}
}

func (plans *playgroundPlansDataSource) Read(ctx context.Context, _ datasource.ReadRequest, response *datasource.ReadResponse) {
	apiClient := plans.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	catalog, err := apiClient.ListPlaygroundPlans(ctx)
	if err != nil {
		response.Diagnostics.AddError("Unable to list playground plans", err.Error())
		return
	}
	model := playgroundPlansModel{
		DefaultPlanID:              types.StringValue(catalog.DefaultPlanID),
		Currency:                   types.StringValue(catalog.Currency),
		OrganisationHasPaidPlan:    types.BoolValue(catalog.OrganisationHasPaidPlan),
		OrganisationHasPaymentCard: types.BoolValue(catalog.OrganisationHasPaymentCard),
		QuotaLimitMiB:              types.Int64Value(catalog.Quota.LimitMiB),
		QuotaUsedMiB:               types.Int64Value(catalog.Quota.UsedMiB),
		Plans:                      make([]playgroundPlanModel, 0, len(catalog.Plans)),
	}
	for _, plan := range catalog.Plans {
		model.Plans = append(model.Plans, playgroundPlanModel{
			ID:                types.StringValue(plan.ID),
			DisplayName:       types.StringValue(plan.DisplayName),
			Summary:           types.StringValue(plan.Summary),
			Vcpus:             types.Int64Value(plan.Vcpus),
			MemoryGB:          types.Float64Value(plan.MemoryGB),
			StorageGB:         types.Int64Value(plan.StorageGB),
			MaxPods:           types.Int64Value(plan.MaxPods),
			PriceMonthlyCents: types.Int64Value(plan.PriceMonthlyCents),
			Currency:          types.StringValue(plan.Currency),
			Available:         types.BoolValue(plan.Available),
		})
	}
	response.Diagnostics.Append(response.State.Set(ctx, &model)...)
}

// ---------------------------------------------------------------------------
// ankra_node_groups

type nodeGroupsDataSource struct {
	configuredDataSourceClient
}

type nodeGroupSummaryModel struct {
	Name               types.String `tfsdk:"name"`
	InstanceType       types.String `tfsdk:"instance_type"`
	Count              types.Int64  `tfsdk:"count"`
	AutoscalingEnabled types.Bool   `tfsdk:"autoscaling_enabled"`
	MinCount           types.Int64  `tfsdk:"min_count"`
	MaxCount           types.Int64  `tfsdk:"max_count"`
	Labels             types.Map    `tfsdk:"labels"`
}

type nodeGroupsModel struct {
	ClusterID   types.String            `tfsdk:"cluster_id"`
	ClusterKind types.String            `tfsdk:"cluster_kind"`
	NodeGroups  []nodeGroupSummaryModel `tfsdk:"node_groups"`
}

// NewNodeGroupsDataSource returns ankra_node_groups, the counterpart of
// `ankra cluster node-group list`.
func NewNodeGroupsDataSource() datasource.DataSource {
	return &nodeGroupsDataSource{}
}

func (groups *nodeGroupsDataSource) Metadata(_ context.Context, request datasource.MetadataRequest, response *datasource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_node_groups"
}

func (groups *nodeGroupsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, response *datasource.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: "The node groups of a cluster Ankra provisioned, including the default worker group " +
			"the cluster was created with. The counterpart of `ankra cluster node-group list`.",
		Attributes: map[string]schema.Attribute{
			"cluster_id":   schema.StringAttribute{MarkdownDescription: "Cluster to list.", Required: true},
			"cluster_kind": schema.StringAttribute{MarkdownDescription: "Kind of the cluster.", Computed: true},
			"node_groups": schema.ListNestedAttribute{
				MarkdownDescription: "The cluster's node groups.",
				Computed:            true,
				NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
					"name":                schema.StringAttribute{MarkdownDescription: "Group name.", Computed: true},
					"instance_type":       schema.StringAttribute{MarkdownDescription: "Instance type.", Computed: true},
					"count":               schema.Int64Attribute{MarkdownDescription: "Node count.", Computed: true},
					"autoscaling_enabled": schema.BoolAttribute{MarkdownDescription: "Whether autoscaling is on.", Computed: true},
					"min_count":           schema.Int64Attribute{MarkdownDescription: "Autoscaling lower bound.", Computed: true},
					"max_count":           schema.Int64Attribute{MarkdownDescription: "Autoscaling upper bound.", Computed: true},
					"labels":              schema.MapAttribute{MarkdownDescription: "Node labels.", Computed: true, ElementType: types.StringType},
				}},
			},
		},
	}
}

func (groups *nodeGroupsDataSource) Read(ctx context.Context, request datasource.ReadRequest, response *datasource.ReadResponse) {
	apiClient := groups.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var model nodeGroupsModel
	response.Diagnostics.Append(request.Config.Get(ctx, &model)...)
	if response.Diagnostics.HasError() {
		return
	}
	clusterID := model.ClusterID.ValueString()
	lane, ok := resolveNodeGroupLane(ctx, apiClient, clusterID, &response.Diagnostics)
	if !ok {
		return
	}
	listed, err := apiClient.ListNodeGroups(ctx, lane, clusterID)
	if err != nil {
		response.Diagnostics.AddError("Unable to list node groups", err.Error())
		return
	}
	model.ClusterKind = types.StringValue(lane)
	model.NodeGroups = make([]nodeGroupSummaryModel, 0, len(listed))
	for _, group := range listed {
		model.NodeGroups = append(model.NodeGroups, nodeGroupSummaryModel{
			Name:               types.StringValue(group.Name),
			InstanceType:       types.StringValue(group.InstanceType),
			Count:              types.Int64Value(group.Count),
			AutoscalingEnabled: types.BoolValue(group.AutoscalingEnabled),
			MinCount:           types.Int64Value(group.Min),
			MaxCount:           types.Int64Value(group.Max),
			Labels:             nodeGroupLabelsFromAPI(group.Labels, types.MapValueMust(types.StringType, nil)),
		})
	}
	response.Diagnostics.Append(response.State.Set(ctx, &model)...)
}
