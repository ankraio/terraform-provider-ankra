// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"slices"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"ankra.io/terraform-provider-ankra/internal/client"
)

var (
	clusterCriticalities  = []string{"production", "staging", "development", "disposable"}
	k3sComponents         = []string{"coredns", "servicelb", "traefik", "local-storage", "metrics-server", "runtimes"}
	k3sComponentLanes     = slices.DeleteFunc(slices.Clone(client.ProvisionedClusterLanes), func(lane string) bool { return lane == client.LaneScaleway })
	gatewayAPIFeatureName = "The Kubernetes Gateway API"
)

var (
	_ resource.ResourceWithImportState = (*clusterEnvironmentResource)(nil)
	_ resource.ResourceWithImportState = (*clusterStateSnapshotRetentionResource)(nil)
	_ resource.ResourceWithImportState = (*clusterDNSZoneResource)(nil)
	_ resource.ResourceWithImportState = (*clusterGatewayAPIResource)(nil)
	_ resource.ResourceWithImportState = (*clusterK3sDisabledComponentsResource)(nil)
	_ resource.ResourceWithConfigure   = (*clusterEnvironmentResource)(nil)
	_ resource.ResourceWithConfigure   = (*clusterStateSnapshotRetentionResource)(nil)
	_ resource.ResourceWithConfigure   = (*clusterDNSZoneResource)(nil)
	_ resource.ResourceWithConfigure   = (*clusterGatewayAPIResource)(nil)
	_ resource.ResourceWithConfigure   = (*clusterK3sDisabledComponentsResource)(nil)
)

// clusterKindAttribute is the computed kind a lane-scoped resource resolves.
func clusterKindAttribute() schema.StringAttribute {
	return schema.StringAttribute{
		MarkdownDescription: "Kind of the cluster, resolved from the platform; it selects the API lane.",
		Computed:            true,
		PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
	}
}

type clusterEnvironmentResource struct {
	configuredClient
}

type clusterEnvironmentModel struct {
	ID          types.String `tfsdk:"id"`
	ClusterID   types.String `tfsdk:"cluster_id"`
	Environment types.String `tfsdk:"environment"`
	Criticality types.String `tfsdk:"criticality"`
}

// NewClusterEnvironmentResource returns the ankra_cluster_environment
// resource, the counterpart of `ankra cluster update --environment`.
func NewClusterEnvironmentResource() resource.Resource {
	return &clusterEnvironmentResource{}
}

func (environment *clusterEnvironmentResource) Metadata(_ context.Context, request resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_cluster_environment"
}

func (environment *clusterEnvironmentResource) Schema(_ context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: "What a cluster is called and what it is for (`ankra cluster update --environment`): its environment label " +
			"and its criticality, which decides how loudly the cluster reports and how much Ankra's AI may do on it unasked. " +
			"Both are written in one transaction. A person's own token records the label as that person's choice; a service " +
			"account's token records it as automation. An ImportCluster file's `metadata.environment` applies only when the " +
			"cluster is created, so this is how a cluster moves between environments afterwards. Destroy leaves both as they are.",
		Attributes: map[string]schema.Attribute{
			"id":         clusterSettingsIDAttribute("The cluster id."),
			"cluster_id": clusterSettingsClusterIDAttribute(),
			"environment": schema.StringAttribute{
				MarkdownDescription: "Environment label, 1 to 50 characters, such as `production`.",
				Required:            true,
			},
			"criticality": schema.StringAttribute{
				MarkdownDescription: "What the cluster is for: `production`, `staging`, `development` or `disposable`. Unset keeps the stored " +
					"criticality and reports it; a cluster never classified reads as `production`.",
				Optional:      true,
				Computed:      true,
				Validators:    []validator.String{organisationStringOneOf{values: clusterCriticalities}},
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
		},
	}
}

func (environment *clusterEnvironmentResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	var plan clusterEnvironmentModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if environment.write(ctx, &plan, &response.Diagnostics) {
		response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
		response.Diagnostics.Append(trackScope(ctx, environment.client, response.Private)...)
	}
}

func (environment *clusterEnvironmentResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	var plan clusterEnvironmentModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if environment.write(ctx, &plan, &response.Diagnostics) {
		response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
	}
}

func (environment *clusterEnvironmentResource) write(ctx context.Context, plan *clusterEnvironmentModel, diagnostics *diag.Diagnostics) bool {
	apiClient := environment.requireClient(diagnostics)
	if apiClient == nil || diagnostics.HasError() {
		return false
	}
	clusterID := plan.ClusterID.ValueString()
	stored, setError := apiClient.SetClusterEnvironment(ctx, clusterID, client.ClusterEnvironment{
		Environment: plan.Environment.ValueString(), Criticality: stringPointer(plan.Criticality),
	})
	if setError != nil {
		diagnostics.AddError("Unable to set the cluster's environment", clusterSettingsErrorText(setError, clusterID))
		return false
	}
	plan.ID = plan.ClusterID
	plan.Environment = types.StringValue(stored.Environment)
	if stored.Criticality != nil {
		plan.Criticality = types.StringValue(*stored.Criticality)
		return true
	}
	criticality, readError := apiClient.GetClusterCriticality(ctx, clusterID)
	if readError != nil {
		diagnostics.AddError("Unable to read the cluster's criticality", clusterSettingsErrorText(readError, clusterID))
		return false
	}
	plan.Criticality = types.StringValue(criticality)
	return true
}

func (environment *clusterEnvironmentResource) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	apiClient := environment.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state clusterEnvironmentModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	clusterID := state.ClusterID.ValueString()
	cluster, clusterError := apiClient.GetClusterByID(ctx, clusterID)
	if clusterError != nil {
		response.Diagnostics.AddError("Unable to read cluster", clusterError.Error())
		return
	}
	if cluster == nil {
		removeMissingCluster(ctx, apiClient, response)
		return
	}
	criticality, readError := apiClient.GetClusterCriticality(ctx, clusterID)
	if clusterSettingsReadNotFound(ctx, readError, apiClient, clusterID, response) {
		return
	}
	if readError != nil {
		response.Diagnostics.AddError("Unable to read the cluster's criticality", readError.Error())
		return
	}
	state.ID = state.ClusterID
	state.Environment = types.StringPointerValue(cluster.Environment)
	state.Criticality = types.StringValue(criticality)
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
	response.Diagnostics.Append(trackScope(ctx, apiClient, response.Private)...)
}

// Delete leaves the label and criticality as they are: there is no
// unlabelled state to return to.
func (environment *clusterEnvironmentResource) Delete(_ context.Context, _ resource.DeleteRequest, _ *resource.DeleteResponse) {
}

// ImportState imports by cluster id.
func (environment *clusterEnvironmentResource) ImportState(ctx context.Context, request resource.ImportStateRequest, response *resource.ImportStateResponse) {
	clusterSettingsImportClusterID(ctx, request, response)
}

type clusterStateSnapshotRetentionResource struct {
	configuredClient
}

type clusterStateSnapshotRetentionModel struct {
	ID            types.String `tfsdk:"id"`
	ClusterID     types.String `tfsdk:"cluster_id"`
	RetentionDays types.Int64  `tfsdk:"retention_days"`
	IsDefault     types.Bool   `tfsdk:"is_default"`
	KeepLatest    types.Int64  `tfsdk:"keep_latest"`
}

// NewClusterStateSnapshotRetentionResource returns the
// ankra_cluster_state_snapshot_retention resource, the counterpart of
// `ankra cluster state-snapshots retention --days`.
func NewClusterStateSnapshotRetentionResource() resource.Resource {
	return &clusterStateSnapshotRetentionResource{}
}

func (retention *clusterStateSnapshotRetentionResource) Metadata(_ context.Context, request resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_cluster_state_snapshot_retention"
}

func (retention *clusterStateSnapshotRetentionResource) Schema(_ context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: "The retention window of a provisioned cluster's scheduled etcd backups (`ankra cluster state-snapshots " +
			"retention`). Ankra backs up the etcd state every six hours to the organisation's backup vault and keeps the newest " +
			"backups plus the newest one of each UTC day within the window. A shorter window deletes the daily backups that fall " +
			"outside it, and a longer one cannot bring back backups already deleted. Every change is audited. Destroy leaves the " +
			"window as it is.",
		Attributes: map[string]schema.Attribute{
			"id":         clusterSettingsIDAttribute("The cluster id."),
			"cluster_id": clusterSettingsClusterIDAttribute(),
			"retention_days": schema.Int64Attribute{
				MarkdownDescription: "Days the newest backup of each UTC day is kept, 1 to 90.",
				Required:            true,
				Validators:          []validator.Int64{alertDNSRangeValidator{minimum: 1, maximum: 90}},
			},
			"is_default": schema.BoolAttribute{
				MarkdownDescription: "Whether the window is the platform default.",
				Computed:            true,
			},
			"keep_latest": schema.Int64Attribute{
				MarkdownDescription: "How many newest backups are kept whatever the window.",
				Computed:            true,
			},
		},
	}
}

func (retention *clusterStateSnapshotRetentionResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	var plan clusterStateSnapshotRetentionModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if retention.write(ctx, &plan, &response.Diagnostics) {
		response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
		response.Diagnostics.Append(trackScope(ctx, retention.client, response.Private)...)
	}
}

func (retention *clusterStateSnapshotRetentionResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	var plan clusterStateSnapshotRetentionModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if retention.write(ctx, &plan, &response.Diagnostics) {
		response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
	}
}

func (retention *clusterStateSnapshotRetentionResource) write(ctx context.Context, plan *clusterStateSnapshotRetentionModel, diagnostics *diag.Diagnostics) bool {
	apiClient := retention.requireClient(diagnostics)
	if apiClient == nil || diagnostics.HasError() {
		return false
	}
	clusterID := plan.ClusterID.ValueString()
	stored, setError := apiClient.SetStateSnapshotRetention(ctx, clusterID, plan.RetentionDays.ValueInt64())
	if setError != nil {
		diagnostics.AddError("Unable to set the state snapshot retention", clusterSettingsErrorText(setError, clusterID))
		return false
	}
	clusterStateSnapshotRetentionApply(plan, stored)
	return true
}

func (retention *clusterStateSnapshotRetentionResource) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	apiClient := retention.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state clusterStateSnapshotRetentionModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	stored, readError := apiClient.GetStateSnapshotRetention(ctx, state.ClusterID.ValueString())
	if clusterSettingsReadNotFound(ctx, readError, apiClient, state.ClusterID.ValueString(), response) {
		return
	}
	if readError != nil {
		response.Diagnostics.AddError("Unable to read the state snapshot retention", readError.Error())
		return
	}
	clusterStateSnapshotRetentionApply(&state, stored)
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
	response.Diagnostics.Append(trackScope(ctx, apiClient, response.Private)...)
}

// Delete leaves the window as it is: returning to the default could delete
// daily backups the owners kept on purpose.
func (retention *clusterStateSnapshotRetentionResource) Delete(_ context.Context, _ resource.DeleteRequest, _ *resource.DeleteResponse) {
}

// ImportState imports by cluster id.
func (retention *clusterStateSnapshotRetentionResource) ImportState(ctx context.Context, request resource.ImportStateRequest, response *resource.ImportStateResponse) {
	clusterSettingsImportClusterID(ctx, request, response)
}

func clusterStateSnapshotRetentionApply(model *clusterStateSnapshotRetentionModel, stored *client.StateSnapshotRetention) {
	model.ID = model.ClusterID
	model.RetentionDays = types.Int64Value(stored.RetentionDays)
	model.IsDefault = types.BoolValue(stored.IsDefault)
	model.KeepLatest = types.Int64Value(stored.KeepLatest)
}

type clusterDNSZoneResource struct {
	configuredClient
}

type clusterDNSZoneModel struct {
	ID        types.String `tfsdk:"id"`
	ClusterID types.String `tfsdk:"cluster_id"`
	FQDN      types.String `tfsdk:"fqdn"`
	State     types.String `tfsdk:"state"`
}

// NewClusterDNSZoneResource returns the ankra_cluster_dns_zone resource, the
// counterpart of `ankra cluster domain`.
func NewClusterDNSZoneResource() resource.Resource {
	return &clusterDNSZoneResource{}
}

func (zone *clusterDNSZoneResource) Metadata(_ context.Context, request resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_cluster_dns_zone"
}

func (zone *clusterDNSZoneResource) Schema(_ context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: "A cluster's generated public DNS zone under the organisation's Ankra root domain (`ankra cluster domain`). " +
			"The zone is provisioned asynchronously, after which Ankra installs external-dns on the cluster to publish hostnames in " +
			"it; an existing zone keeps its FQDN and a failed one is retried. Rotating the zone's DNS write token stays imperative " +
			"(`ankra cluster dns-token rotate`). Destroy hands the zone back for teardown; the cluster itself is not changed.",
		Attributes: map[string]schema.Attribute{
			"id":         clusterSettingsIDAttribute("The cluster id."),
			"cluster_id": clusterSettingsClusterIDAttribute(),
			"fqdn": schema.StringAttribute{
				MarkdownDescription: "The zone's domain.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"state": schema.StringAttribute{
				MarkdownDescription: "Provisioning state of the zone.",
				Computed:            true,
			},
		},
	}
}

func (zone *clusterDNSZoneResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	apiClient := zone.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var plan clusterDNSZoneModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}
	clusterID := plan.ClusterID.ValueString()
	enabled, enableError := apiClient.EnableClusterGeneratedDNSZone(ctx, clusterID)
	if enableError != nil {
		response.Diagnostics.AddError("Unable to enable the cluster's DNS zone", clusterSettingsErrorText(enableError, clusterID))
		return
	}
	plan.ID = plan.ClusterID
	plan.FQDN = types.StringValue(enabled.FQDN)
	plan.State = types.StringValue(enabled.State)
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
	response.Diagnostics.Append(trackScope(ctx, apiClient, response.Private)...)
}

func (zone *clusterDNSZoneResource) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	apiClient := zone.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state clusterDNSZoneModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	found, readError := apiClient.GetClusterGeneratedDNSZone(ctx, state.ClusterID.ValueString())
	if clusterSettingsReadNotFound(ctx, readError, apiClient, state.ClusterID.ValueString(), response) {
		return
	}
	if readError != nil {
		response.Diagnostics.AddError("Unable to read the cluster's DNS zone", readError.Error())
		return
	}
	if found.State == "none" || found.FQDN == "" {
		response.State.RemoveResource(ctx)
		return
	}
	state.ID = state.ClusterID
	state.FQDN = types.StringValue(found.FQDN)
	state.State = types.StringValue(found.State)
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
	response.Diagnostics.Append(trackScope(ctx, apiClient, response.Private)...)
}

// Update is never planned: the only argument replaces the resource.
func (zone *clusterDNSZoneResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	var plan clusterDNSZoneModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
}

func (zone *clusterDNSZoneResource) Delete(ctx context.Context, request resource.DeleteRequest, response *resource.DeleteResponse) {
	apiClient := zone.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state clusterDNSZoneModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	clusterID := state.ClusterID.ValueString()
	if deleteError := clusterSettingsDeleteError(apiClient.DisableClusterGeneratedDNSZone(ctx, clusterID), clusterID); deleteError != nil {
		response.Diagnostics.AddError("Unable to disable the cluster's DNS zone", deleteError.Error())
	}
}

// ImportState imports by cluster id.
func (zone *clusterDNSZoneResource) ImportState(ctx context.Context, request resource.ImportStateRequest, response *resource.ImportStateResponse) {
	clusterSettingsImportClusterID(ctx, request, response)
}

type clusterGatewayAPIResource struct {
	configuredClient
}

type clusterGatewayAPIModel struct {
	ID          types.String `tfsdk:"id"`
	ClusterID   types.String `tfsdk:"cluster_id"`
	ClusterKind types.String `tfsdk:"cluster_kind"`
	Phase       types.String `tfsdk:"phase"`
}

// NewClusterGatewayAPIResource returns the ankra_cluster_gateway_api
// resource, the counterpart of `ankra cluster gateway-api enable`.
func NewClusterGatewayAPIResource() resource.Resource {
	return &clusterGatewayAPIResource{}
}

func (gateway *clusterGatewayAPIResource) Metadata(_ context.Context, request resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_cluster_gateway_api"
}

func (gateway *clusterGatewayAPIResource) Schema(_ context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: "Turns the Kubernetes Gateway API on for a cluster Ankra provisions (`ankra cluster gateway-api enable`). " +
			"Ankra records the option, adds the Gateway API CRDs (or uses a bundle the cluster already carries) and, once they are " +
			"established, writes the Gateway API keys into the Traefik and cert-manager values through the ordinary stack write. " +
			"The cluster needs Ankra-managed networking. The platform can only turn the Gateway API on, so destroy leaves it on.",
		Attributes: map[string]schema.Attribute{
			"id":           clusterSettingsIDAttribute("The cluster id."),
			"cluster_id":   clusterSettingsClusterIDAttribute(),
			"cluster_kind": clusterKindAttribute(),
			"phase": schema.StringAttribute{
				MarkdownDescription: "Where the enable stands, such as `crds_pending`, `values_pending`, `applying` or `enabled`.",
				Computed:            true,
			},
		},
	}
}

func (gateway *clusterGatewayAPIResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	apiClient := gateway.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var plan clusterGatewayAPIModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}
	clusterID := plan.ClusterID.ValueString()
	lane, ok := resolveProvisionedLane(ctx, apiClient, clusterID, client.ProvisionedClusterLanes, gatewayAPIFeatureName, &response.Diagnostics)
	if !ok {
		return
	}
	status, enableError := apiClient.EnableGatewayAPI(ctx, lane, clusterID)
	if enableError != nil {
		response.Diagnostics.AddError("Unable to enable the Gateway API", clusterSettingsErrorText(enableError, clusterID))
		return
	}
	if !status.Enabled {
		response.Diagnostics.AddError("Gateway API not available", "The platform did not record the Gateway API: "+status.UnavailableReason)
		return
	}
	plan.ID = plan.ClusterID
	plan.ClusterKind = types.StringValue(lane)
	plan.Phase = types.StringValue(status.Phase)
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
	response.Diagnostics.Append(trackScope(ctx, apiClient, response.Private)...)
}

func (gateway *clusterGatewayAPIResource) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	apiClient := gateway.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state clusterGatewayAPIModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	clusterID := state.ClusterID.ValueString()
	lane := state.ClusterKind.ValueString()
	if lane == "" {
		resolved, ok := resolveProvisionedLane(ctx, apiClient, clusterID, client.ProvisionedClusterLanes, gatewayAPIFeatureName, &response.Diagnostics)
		if !ok {
			return
		}
		lane = resolved
	}
	status, readError := apiClient.GetGatewayAPIStatus(ctx, lane, clusterID)
	if clusterSettingsReadNotFound(ctx, readError, apiClient, clusterID, response) {
		return
	}
	if readError != nil {
		response.Diagnostics.AddError("Unable to read the Gateway API status", readError.Error())
		return
	}
	if !status.Enabled {
		response.State.RemoveResource(ctx)
		return
	}
	state.ID = state.ClusterID
	state.ClusterKind = types.StringValue(lane)
	state.Phase = types.StringValue(status.Phase)
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
	response.Diagnostics.Append(trackScope(ctx, apiClient, response.Private)...)
}

// Update is never planned: the only argument replaces the resource.
func (gateway *clusterGatewayAPIResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	var plan clusterGatewayAPIModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
}

// Delete leaves the Gateway API on: the platform has no route that turns it
// off.
func (gateway *clusterGatewayAPIResource) Delete(_ context.Context, _ resource.DeleteRequest, response *resource.DeleteResponse) {
	response.Diagnostics.AddWarning("Gateway API left on",
		"The platform can only turn the Gateway API on, so it stays on for this cluster; Terraform stops managing it.")
}

// ImportState imports by cluster id; the read resolves the cluster's kind.
func (gateway *clusterGatewayAPIResource) ImportState(ctx context.Context, request resource.ImportStateRequest, response *resource.ImportStateResponse) {
	clusterSettingsImportClusterID(ctx, request, response)
}

type clusterK3sDisabledComponentsResource struct {
	configuredClient
}

type clusterK3sDisabledComponentsModel struct {
	ID          types.String `tfsdk:"id"`
	ClusterID   types.String `tfsdk:"cluster_id"`
	ClusterKind types.String `tfsdk:"cluster_kind"`
	Components  types.Set    `tfsdk:"components"`
}

// NewClusterK3sDisabledComponentsResource returns the
// ankra_cluster_k3s_disabled_components resource, the counterpart of
// `ankra cluster k3s-disabled-components set`.
func NewClusterK3sDisabledComponentsResource() resource.Resource {
	return &clusterK3sDisabledComponentsResource{}
}

func (components *clusterK3sDisabledComponentsResource) Metadata(_ context.Context, request resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_cluster_k3s_disabled_components"
}

func (components *clusterK3sDisabledComponentsResource) Schema(_ context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: "The k3s packaged components the platform keeps disabled on a running k3s cluster Ankra provisions " +
			"(`ankra cluster k3s-disabled-components set`), on top of the provider shape's own defaults, for example `traefik` on a " +
			"cluster that brings its own ingress. A change is reconciled within minutes by restarting k3s on one control plane at a " +
			"time; control planes already carrying every entry are not touched. kubeadm and Scaleway clusters are refused. Destroy " +
			"leaves the list as it is, so a component kept off stays off.",
		Attributes: map[string]schema.Attribute{
			"id":           clusterSettingsIDAttribute("The cluster id."),
			"cluster_id":   clusterSettingsClusterIDAttribute(),
			"cluster_kind": clusterKindAttribute(),
			"components": schema.SetAttribute{
				MarkdownDescription: "Components kept disabled: `coredns`, `servicelb`, `traefik`, `local-storage`, `metrics-server` or `runtimes`.",
				Required:            true,
				ElementType:         types.StringType,
				Validators:          []validator.Set{stringSetOneOfValidator{allowed: k3sComponents}},
			},
		},
	}
}

func (components *clusterK3sDisabledComponentsResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	apiClient := components.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var plan clusterK3sDisabledComponentsModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}
	lane, ok := resolveProvisionedLane(ctx, apiClient, plan.ClusterID.ValueString(), k3sComponentLanes, "Disabling k3s components", &response.Diagnostics)
	if !ok {
		return
	}
	plan.ClusterKind = types.StringValue(lane)
	if components.write(ctx, apiClient, &plan, &response.Diagnostics) {
		response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
		response.Diagnostics.Append(trackScope(ctx, apiClient, response.Private)...)
	}
}

func (components *clusterK3sDisabledComponentsResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	apiClient := components.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var plan, state clusterK3sDisabledComponentsModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	plan.ClusterKind = state.ClusterKind
	if components.write(ctx, apiClient, &plan, &response.Diagnostics) {
		response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
	}
}

func (components *clusterK3sDisabledComponentsResource) write(ctx context.Context, apiClient *client.Client, plan *clusterK3sDisabledComponentsModel, diagnostics *diag.Diagnostics) bool {
	clusterID := plan.ClusterID.ValueString()
	wanted := organisationNonNilStrings(organisationStringsFromSet(ctx, plan.Components, diagnostics))
	if diagnostics.HasError() {
		return false
	}
	stored, setError := apiClient.SetK3sDisabledComponents(ctx, plan.ClusterKind.ValueString(), clusterID, wanted)
	if setError != nil {
		diagnostics.AddError("Unable to set the disabled k3s components", clusterSettingsErrorText(setError, clusterID))
		return false
	}
	plan.ID = plan.ClusterID
	plan.Components = organisationSetFromStrings(organisationNonNilStrings(stored))
	return true
}

func (components *clusterK3sDisabledComponentsResource) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	apiClient := components.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state clusterK3sDisabledComponentsModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	clusterID := state.ClusterID.ValueString()
	lane := state.ClusterKind.ValueString()
	if lane == "" {
		resolved, ok := resolveProvisionedLane(ctx, apiClient, clusterID, k3sComponentLanes, "Disabling k3s components", &response.Diagnostics)
		if !ok {
			return
		}
		lane = resolved
	}
	stored, readError := apiClient.GetK3sDisabledComponents(ctx, lane, clusterID)
	if clusterSettingsReadNotFound(ctx, readError, apiClient, clusterID, response) {
		return
	}
	if readError != nil {
		response.Diagnostics.AddError("Unable to read the disabled k3s components", readError.Error())
		return
	}
	state.ID = state.ClusterID
	state.ClusterKind = types.StringValue(lane)
	state.Components = organisationSetFromStrings(organisationNonNilStrings(stored))
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
	response.Diagnostics.Append(trackScope(ctx, apiClient, response.Private)...)
}

// Delete leaves the list as it is: re-enabling a component could install an
// ingress or storage class next to the cluster's own.
func (components *clusterK3sDisabledComponentsResource) Delete(_ context.Context, _ resource.DeleteRequest, _ *resource.DeleteResponse) {
}

// ImportState imports by cluster id; the read resolves the cluster's kind.
func (components *clusterK3sDisabledComponentsResource) ImportState(ctx context.Context, request resource.ImportStateRequest, response *resource.ImportStateResponse) {
	clusterSettingsImportClusterID(ctx, request, response)
}

// stringSetOneOfValidator refuses, at plan time, a set element outside the
// vocabulary the platform accepts.
type stringSetOneOfValidator struct {
	allowed []string
}

func (oneOf stringSetOneOfValidator) Description(ctx context.Context) string {
	return clusterSettingsOneOf(oneOf.allowed...).Description(ctx)
}

func (oneOf stringSetOneOfValidator) MarkdownDescription(ctx context.Context) string {
	return oneOf.Description(ctx)
}

func (oneOf stringSetOneOfValidator) ValidateSet(ctx context.Context, request validator.SetRequest, response *validator.SetResponse) {
	if request.ConfigValue.IsNull() || request.ConfigValue.IsUnknown() {
		return
	}
	for _, element := range request.ConfigValue.Elements() {
		value, isString := element.(types.String)
		if isString && !value.IsNull() && !value.IsUnknown() && !slices.Contains(oneOf.allowed, value.ValueString()) {
			response.Diagnostics.AddAttributeError(request.Path, "Invalid value",
				value.String()+" is not accepted: "+oneOf.Description(ctx)+".")
		}
	}
}
