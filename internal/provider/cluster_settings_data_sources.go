// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"ankra.io/terraform-provider-ankra/internal/client"
)

var (
	_ datasource.DataSource              = (*clusterDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*clusterDataSource)(nil)
	_ datasource.DataSource              = (*clusterAccessGrantsDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*clusterAccessGrantsDataSource)(nil)
	_ datasource.DataSource              = (*clusterCustomDNSZonesDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*clusterCustomDNSZonesDataSource)(nil)
	_ datasource.DataSource              = (*clusterPowerSchedulesDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*clusterPowerSchedulesDataSource)(nil)
	_ datasource.DataSource              = (*clusterMeshesDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*clusterMeshesDataSource)(nil)
)

func clusterSettingsComputedString(description string) schema.StringAttribute {
	return schema.StringAttribute{MarkdownDescription: description, Computed: true}
}

func clusterSettingsComputedBool(description string) schema.BoolAttribute {
	return schema.BoolAttribute{MarkdownDescription: description, Computed: true}
}

func clusterSettingsDataSourceClusterID() schema.StringAttribute {
	return schema.StringAttribute{MarkdownDescription: "Identifier of the cluster.", Required: true}
}

// --- ankra_cluster ---

type clusterDataSource struct {
	configuredDataSourceClient
}

type clusterDataSourceModel struct {
	ID    types.String `tfsdk:"id"`
	Name  types.String `tfsdk:"name"`
	Kind  types.String `tfsdk:"kind"`
	State types.String `tfsdk:"state"`
}

// NewClusterDataSource returns the ankra_cluster data source: one cluster by
// id or by name.
func NewClusterDataSource() datasource.DataSource {
	return &clusterDataSource{}
}

func (clusterSource *clusterDataSource) Metadata(_ context.Context, request datasource.MetadataRequest, response *datasource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_cluster"
}

func (clusterSource *clusterDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, response *datasource.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: "One cluster of the organisation, looked up by `id` or by exact `name`, with its kind and state. " +
			"The counterpart of `ankra cluster info`. Feeds the `cluster_id` of the cluster settings resources.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Identifier of the cluster. Set exactly one of `id` and `name`.",
				Optional:            true,
				Computed:            true,
			},
			"name": schema.StringAttribute{
				MarkdownDescription: "Exact name of the cluster. Set exactly one of `id` and `name`.",
				Optional:            true,
				Computed:            true,
			},
			"kind":  clusterSettingsComputedString("Cluster kind: `imported`, `hetzner`, `ovh`, `upcloud`, ..."),
			"state": clusterSettingsComputedString("Cluster state."),
		},
	}
}

func (clusterSource *clusterDataSource) Read(ctx context.Context, request datasource.ReadRequest, response *datasource.ReadResponse) {
	apiClient := clusterSource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var config clusterDataSourceModel
	response.Diagnostics.Append(request.Config.Get(ctx, &config)...)
	if response.Diagnostics.HasError() {
		return
	}
	hasID := !config.ID.IsNull() && config.ID.ValueString() != ""
	hasName := !config.Name.IsNull() && config.Name.ValueString() != ""
	if hasID == hasName {
		response.Diagnostics.AddError("Invalid cluster lookup", "Set exactly one of id and name.")
		return
	}
	var cluster *client.Cluster
	var err error
	if hasID {
		cluster, err = apiClient.GetClusterByID(ctx, config.ID.ValueString())
	} else {
		cluster, err = apiClient.GetClusterByName(ctx, config.Name.ValueString())
	}
	if err != nil {
		response.Diagnostics.AddError("Unable to read cluster", err.Error())
		return
	}
	if cluster == nil {
		response.Diagnostics.AddError("Cluster not found", "No cluster in the organisation matches the lookup.")
		return
	}
	state := clusterDataSourceModel{
		ID: types.StringValue(cluster.ID), Name: types.StringValue(cluster.Name),
		Kind: types.StringValue(cluster.Kind), State: types.StringValue(cluster.State),
	}
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
}

// --- ankra_cluster_access_grants ---

type clusterAccessGrantsDataSource struct {
	configuredDataSourceClient
}

type clusterAccessGrantItemModel struct {
	ID              types.String `tfsdk:"id"`
	UserEmail       types.String `tfsdk:"user_email"`
	AnkraUserID     types.String `tfsdk:"ankra_user_id"`
	Scope           types.String `tfsdk:"scope"`
	Namespace       types.String `tfsdk:"namespace"`
	Role            types.String `tfsdk:"role"`
	ExpiresAt       types.String `tfsdk:"expires_at"`
	Reason          types.String `tfsdk:"reason"`
	ReconcileStatus types.String `tfsdk:"reconcile_status"`
	Source          types.String `tfsdk:"source"`
}

type clusterAccessGrantsModel struct {
	ID        types.String                  `tfsdk:"id"`
	ClusterID types.String                  `tfsdk:"cluster_id"`
	Grants    []clusterAccessGrantItemModel `tfsdk:"grants"`
}

// NewClusterAccessGrantsDataSource returns the ankra_cluster_access_grants
// data source, the counterpart of `ankra cluster access list`.
func NewClusterAccessGrantsDataSource() datasource.DataSource {
	return &clusterAccessGrantsDataSource{}
}

func (grantsSource *clusterAccessGrantsDataSource) Metadata(_ context.Context, request datasource.MetadataRequest, response *datasource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_cluster_access_grants"
}

func (grantsSource *clusterAccessGrantsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, response *datasource.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: "Lists the kubectl access grants on a cluster, those from roles and the creator seed included. " +
			"The counterpart of `ankra cluster access list`. Only organisation admins may read it.",
		Attributes: map[string]schema.Attribute{
			"id":         clusterSettingsComputedString("The cluster id."),
			"cluster_id": clusterSettingsDataSourceClusterID(),
			"grants": schema.ListNestedAttribute{
				MarkdownDescription: "Grants that are not revoked, newest first. A grant past its expiry stays listed until the platform retires it.",
				Computed:            true,
				NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
					"id":               clusterSettingsComputedString("Identifier of the grant."),
					"user_email":       clusterSettingsComputedString("Email of the member."),
					"ankra_user_id":    clusterSettingsComputedString("Identifier of the member."),
					"scope":            clusterSettingsComputedString("`cluster` or `namespace`."),
					"namespace":        clusterSettingsComputedString("Namespace of a namespace grant."),
					"role":             clusterSettingsComputedString("`view`, `edit`, `admin` or `cluster-admin`."),
					"expires_at":       clusterSettingsComputedString("When the grant ends; null for a standing grant."),
					"reason":           clusterSettingsComputedString("Recorded justification."),
					"reconcile_status": clusterSettingsComputedString("Whether the binding is applied on the cluster."),
					"source":           clusterSettingsComputedString("`manual`, `creator`, `role` or `elevated`."),
				}},
			},
		},
	}
}

func (grantsSource *clusterAccessGrantsDataSource) Read(ctx context.Context, request datasource.ReadRequest, response *datasource.ReadResponse) {
	apiClient := grantsSource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state clusterAccessGrantsModel
	response.Diagnostics.Append(request.Config.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	grants, err := apiClient.ListClusterAccessGrants(ctx, state.ClusterID.ValueString())
	if err != nil {
		response.Diagnostics.AddError("Unable to list cluster access grants", err.Error())
		return
	}
	state.ID = state.ClusterID
	state.Grants = make([]clusterAccessGrantItemModel, 0, len(grants))
	for _, grant := range grants {
		state.Grants = append(state.Grants, clusterAccessGrantItemModel{
			ID: types.StringValue(grant.ID), UserEmail: types.StringPointerValue(grant.UserEmail),
			AnkraUserID: types.StringValue(grant.AnkraUserID), Scope: types.StringValue(grant.Scope),
			Namespace: types.StringPointerValue(grant.Namespace), Role: types.StringValue(grant.Role),
			ExpiresAt: types.StringPointerValue(grant.ExpiresAt), Reason: types.StringPointerValue(grant.Reason),
			ReconcileStatus: types.StringValue(grant.ReconcileStatus), Source: types.StringPointerValue(grant.Source),
		})
	}
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
}

// --- ankra_cluster_custom_dns_zones ---

type clusterCustomDNSZonesDataSource struct {
	configuredDataSourceClient
}

type clusterCustomDNSZoneItemModel struct {
	Zone           types.String `tfsdk:"zone"`
	CredentialName types.String `tfsdk:"credential_name"`
	Source         types.String `tfsdk:"source"`
	Provider       types.String `tfsdk:"credential_provider"`
	Policy         types.String `tfsdk:"policy"`
}

type clusterCustomDNSZonesModel struct {
	ID        types.String                    `tfsdk:"id"`
	ClusterID types.String                    `tfsdk:"cluster_id"`
	Zones     []clusterCustomDNSZoneItemModel `tfsdk:"zones"`
}

// NewClusterCustomDNSZonesDataSource returns the
// ankra_cluster_custom_dns_zones data source, the counterpart of `ankra
// cluster custom-dns-zones list`.
func NewClusterCustomDNSZonesDataSource() datasource.DataSource {
	return &clusterCustomDNSZonesDataSource{}
}

func (zonesSource *clusterCustomDNSZonesDataSource) Metadata(_ context.Context, request datasource.MetadataRequest, response *datasource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_cluster_custom_dns_zones"
}

func (zonesSource *clusterCustomDNSZonesDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, response *datasource.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: "Lists the zones a cluster serves with the organisation's own DNS credentials: its own declarations and the " +
			"organisation-wide ones it inherits. The counterpart of `ankra cluster custom-dns-zones list`.",
		Attributes: map[string]schema.Attribute{
			"id":         clusterSettingsComputedString("The cluster id."),
			"cluster_id": clusterSettingsDataSourceClusterID(),
			"zones": schema.ListNestedAttribute{
				MarkdownDescription: "Zones the cluster serves.",
				Computed:            true,
				NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
					"zone":                clusterSettingsComputedString("Zone name."),
					"credential_name":     clusterSettingsComputedString("Credential that publishes into the zone."),
					"source":              clusterSettingsComputedString("`cluster` or `organisation`."),
					"credential_provider": clusterSettingsComputedString("`cloudflare`, `webhook` or `avura`; null when the credential is gone."),
					"policy":              clusterSettingsComputedString("`upsert-only` or `sync`."),
				}},
			},
		},
	}
}

func (zonesSource *clusterCustomDNSZonesDataSource) Read(ctx context.Context, request datasource.ReadRequest, response *datasource.ReadResponse) {
	apiClient := zonesSource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state clusterCustomDNSZonesModel
	response.Diagnostics.Append(request.Config.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	zones, err := apiClient.ListClusterCustomDNSZones(ctx, state.ClusterID.ValueString())
	if err != nil {
		response.Diagnostics.AddError("Unable to list cluster custom DNS zones", err.Error())
		return
	}
	state.ID = state.ClusterID
	state.Zones = make([]clusterCustomDNSZoneItemModel, 0, len(zones))
	for _, zone := range zones {
		source := zone.Source
		if source == "" {
			source = "cluster"
		}
		state.Zones = append(state.Zones, clusterCustomDNSZoneItemModel{
			Zone: types.StringValue(zone.Zone), CredentialName: types.StringValue(zone.CredentialName),
			Source: types.StringValue(source), Provider: types.StringPointerValue(zone.Provider), Policy: types.StringPointerValue(zone.Policy),
		})
	}
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
}

// --- ankra_cluster_power_schedules ---

type clusterPowerSchedulesDataSource struct {
	configuredDataSourceClient
}

type clusterPowerScheduleItemModel struct {
	ID             types.String `tfsdk:"id"`
	Action         types.String `tfsdk:"action"`
	StopMode       types.String `tfsdk:"stop_mode"`
	PreserveState  types.Bool   `tfsdk:"preserve_state"`
	ScheduleKind   types.String `tfsdk:"schedule_kind"`
	RunAt          types.String `tfsdk:"run_at"`
	CronExpression types.String `tfsdk:"cron_expression"`
	Timezone       types.String `tfsdk:"timezone"`
	Enabled        types.Bool   `tfsdk:"enabled"`
	NextRunAt      types.String `tfsdk:"next_run_at"`
}

type clusterPowerSchedulesModel struct {
	ID        types.String                    `tfsdk:"id"`
	ClusterID types.String                    `tfsdk:"cluster_id"`
	Schedules []clusterPowerScheduleItemModel `tfsdk:"schedules"`
}

// NewClusterPowerSchedulesDataSource returns the
// ankra_cluster_power_schedules data source, the counterpart of `ankra
// cluster power-schedules list`.
func NewClusterPowerSchedulesDataSource() datasource.DataSource {
	return &clusterPowerSchedulesDataSource{}
}

func (schedulesSource *clusterPowerSchedulesDataSource) Metadata(_ context.Context, request datasource.MetadataRequest, response *datasource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_cluster_power_schedules"
}

func (schedulesSource *clusterPowerSchedulesDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, response *datasource.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: "Lists a cluster's scheduled stops and starts. The counterpart of `ankra cluster power-schedules list`.",
		Attributes: map[string]schema.Attribute{
			"id":         clusterSettingsComputedString("The cluster id."),
			"cluster_id": clusterSettingsDataSourceClusterID(),
			"schedules": schema.ListNestedAttribute{
				MarkdownDescription: "Schedules, newest first.",
				Computed:            true,
				NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
					"id":              clusterSettingsComputedString("Identifier of the schedule."),
					"action":          clusterSettingsComputedString("`stop` or `start`."),
					"stop_mode":       clusterSettingsComputedString("How a stop stops the cluster."),
					"preserve_state":  clusterSettingsComputedBool("Whether a stop captures the cluster's state."),
					"schedule_kind":   clusterSettingsComputedString("`once` or `cron`."),
					"run_at":          clusterSettingsComputedString("When a `once` schedule fires."),
					"cron_expression": clusterSettingsComputedString("Cron expression of a `cron` schedule."),
					"timezone":        clusterSettingsComputedString("Timezone of a `cron` schedule."),
					"enabled":         clusterSettingsComputedBool("Whether the schedule fires."),
					"next_run_at":     clusterSettingsComputedString("When the schedule next fires."),
				}},
			},
		},
	}
}

func (schedulesSource *clusterPowerSchedulesDataSource) Read(ctx context.Context, request datasource.ReadRequest, response *datasource.ReadResponse) {
	apiClient := schedulesSource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state clusterPowerSchedulesModel
	response.Diagnostics.Append(request.Config.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	schedules, err := apiClient.ListClusterPowerSchedules(ctx, state.ClusterID.ValueString())
	if err != nil {
		response.Diagnostics.AddError("Unable to list cluster power schedules", err.Error())
		return
	}
	state.ID = state.ClusterID
	state.Schedules = make([]clusterPowerScheduleItemModel, 0, len(schedules))
	for _, schedule := range schedules {
		state.Schedules = append(state.Schedules, clusterPowerScheduleItemModel{
			ID: types.StringValue(schedule.ID), Action: types.StringValue(schedule.Action),
			StopMode: types.StringValue(schedule.StopMode), PreserveState: types.BoolValue(schedule.PreserveState),
			ScheduleKind: types.StringValue(schedule.ScheduleKind), RunAt: types.StringPointerValue(schedule.RunAt),
			CronExpression: types.StringPointerValue(schedule.CronExpression), Timezone: types.StringValue(schedule.Timezone),
			Enabled: types.BoolValue(schedule.Enabled), NextRunAt: types.StringPointerValue(schedule.NextRunAt),
		})
	}
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
}

// --- ankra_cluster_meshes ---

type clusterMeshesDataSource struct {
	configuredDataSourceClient
}

type clusterMeshMemberItemModel struct {
	ClusterID         types.String `tfsdk:"cluster_id"`
	CiliumClusterID   types.Int64  `tfsdk:"cilium_cluster_id"`
	CiliumClusterName types.String `tfsdk:"cilium_cluster_name"`
	Status            types.String `tfsdk:"status"`
}

type clusterMeshItemModel struct {
	ID      types.String                 `tfsdk:"id"`
	Name    types.String                 `tfsdk:"name"`
	Slug    types.String                 `tfsdk:"slug"`
	Status  types.String                 `tfsdk:"status"`
	Members []clusterMeshMemberItemModel `tfsdk:"members"`
}

type clusterMeshesModel struct {
	ID     types.String           `tfsdk:"id"`
	Meshes []clusterMeshItemModel `tfsdk:"meshes"`
}

// NewClusterMeshesDataSource returns the ankra_cluster_meshes data source,
// the counterpart of `ankra cluster mesh list`.
func NewClusterMeshesDataSource() datasource.DataSource {
	return &clusterMeshesDataSource{}
}

func (meshesSource *clusterMeshesDataSource) Metadata(_ context.Context, request datasource.MetadataRequest, response *datasource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_cluster_meshes"
}

func (meshesSource *clusterMeshesDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, response *datasource.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: "Lists the organisation's Cilium ClusterMeshes with their members. The counterpart of `ankra cluster mesh list`.",
		Attributes: map[string]schema.Attribute{
			"id": clusterSettingsComputedString("Placeholder identifier for the data source."),
			"meshes": schema.ListNestedAttribute{
				MarkdownDescription: "Meshes in the organisation.",
				Computed:            true,
				NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
					"id":     clusterSettingsComputedString("Identifier of the mesh."),
					"name":   clusterSettingsComputedString("Mesh name."),
					"slug":   clusterSettingsComputedString("Mesh slug."),
					"status": clusterSettingsComputedString("Mesh status."),
					"members": schema.ListNestedAttribute{
						MarkdownDescription: "Clusters in the mesh.",
						Computed:            true,
						NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
							"cluster_id":          clusterSettingsComputedString("Identifier of the cluster."),
							"cilium_cluster_id":   schema.Int64Attribute{MarkdownDescription: "Cilium cluster identity.", Computed: true},
							"cilium_cluster_name": clusterSettingsComputedString("Cilium cluster name."),
							"status":              clusterSettingsComputedString("Membership status."),
						}},
					},
				}},
			},
		},
	}
}

func (meshesSource *clusterMeshesDataSource) Read(ctx context.Context, _ datasource.ReadRequest, response *datasource.ReadResponse) {
	apiClient := meshesSource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	meshes, err := apiClient.ListClusterMeshes(ctx)
	if err != nil {
		response.Diagnostics.AddError("Unable to list cluster meshes", err.Error())
		return
	}
	state := clusterMeshesModel{ID: types.StringValue("ankra_cluster_meshes"), Meshes: make([]clusterMeshItemModel, 0, len(meshes))}
	for _, mesh := range meshes {
		item := clusterMeshItemModel{
			ID: types.StringValue(mesh.ID), Name: types.StringValue(mesh.Name), Slug: types.StringValue(mesh.Slug),
			Status: types.StringValue(mesh.Status), Members: make([]clusterMeshMemberItemModel, 0, len(mesh.Members)),
		}
		for _, member := range mesh.Members {
			item.Members = append(item.Members, clusterMeshMemberItemModel{
				ClusterID: types.StringValue(member.ClusterID), CiliumClusterID: types.Int64Value(member.CiliumClusterID),
				CiliumClusterName: types.StringValue(member.CiliumClusterName), Status: types.StringValue(member.Status),
			})
		}
		state.Meshes = append(state.Meshes, item)
	}
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
}
