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
	_ datasource.DataSource              = (*slackChannelsDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*slackChannelsDataSource)(nil)
	_ datasource.DataSource              = (*teamsChannelsDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*teamsChannelsDataSource)(nil)
	_ datasource.DataSource              = (*clusterDNSZonesDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*clusterDNSZonesDataSource)(nil)
	_ datasource.DataSource              = (*cloudflareDNSRecordsDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*cloudflareDNSRecordsDataSource)(nil)
	_ datasource.DataSource              = (*alertIngestCredentialsDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*alertIngestCredentialsDataSource)(nil)
)

// notConnectedDetail explains a 404 from a channel listing.
func notConnectedDetail(workspace string, err error) string {
	if client.IsNotFound(err) {
		return "The organisation has no connected " + workspace + ". Connect it in the Ankra portal first. " + err.Error()
	}
	return err.Error()
}

// slackChannelsDataSource is ankra_slack_channels.
type slackChannelsDataSource struct {
	configuredDataSourceClient
}

type slackChannelItemModel struct {
	ID        types.String `tfsdk:"id"`
	Name      types.String `tfsdk:"name"`
	IsPrivate types.Bool   `tfsdk:"is_private"`
}

type slackChannelsDataSourceModel struct {
	ID       types.String            `tfsdk:"id"`
	TeamID   types.String            `tfsdk:"team_id"`
	TeamName types.String            `tfsdk:"team_name"`
	Channels []slackChannelItemModel `tfsdk:"channels"`
}

// NewSlackChannelsDataSource returns the ankra_slack_channels data source.
func NewSlackChannelsDataSource() datasource.DataSource {
	return &slackChannelsDataSource{}
}

func (channelsDataSource *slackChannelsDataSource) Metadata(_ context.Context, request datasource.MetadataRequest, response *datasource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_slack_channels"
}

func (channelsDataSource *slackChannelsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, response *datasource.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: "Lists the channels the Ankra bot can post to in the organisation's connected Slack workspace, for the `channel_id` " +
			"and `channel_name` of a Slack `ankra_alert_destination`. Fails when no Slack workspace is connected, or when the Slack connection " +
			"lacks the permission to list channels (reconnect it in the portal).",
		Attributes: map[string]schema.Attribute{
			"id":        alertDNSComputedString("Placeholder identifier for the data source."),
			"team_id":   alertDNSComputedString("Slack workspace id."),
			"team_name": alertDNSComputedString("Slack workspace name."),
			"channels": alertDNSListAttribute("Channels the bot can post to.", map[string]schema.Attribute{
				"id":         alertDNSComputedString("Slack channel id, the destination's `channel_id`."),
				"name":       alertDNSComputedString("Channel name."),
				"is_private": alertDNSComputedBool("Whether the channel is private."),
			}),
		},
	}
}

func (channelsDataSource *slackChannelsDataSource) Read(ctx context.Context, _ datasource.ReadRequest, response *datasource.ReadResponse) {
	apiClient := channelsDataSource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	listing, err := apiClient.ListSlackChannels(ctx)
	if err != nil {
		response.Diagnostics.AddError("Unable to list Slack channels", notConnectedDetail("Slack workspace", err))
		return
	}
	state := slackChannelsDataSourceModel{
		ID:       types.StringValue("ankra_slack_channels"),
		TeamID:   types.StringValue(listing.TeamID),
		TeamName: optionalString(alertDNSStringValue(listing.TeamName)),
		Channels: make([]slackChannelItemModel, 0, len(listing.Channels)),
	}
	for _, channel := range listing.Channels {
		state.Channels = append(state.Channels, slackChannelItemModel{
			ID:        types.StringValue(channel.ID),
			Name:      types.StringValue(channel.Name),
			IsPrivate: types.BoolValue(channel.IsPrivate),
		})
	}
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
}

// teamsChannelsDataSource is ankra_teams_channels.
type teamsChannelsDataSource struct {
	configuredDataSourceClient
}

type teamsChannelItemModel struct {
	ID       types.String `tfsdk:"id"`
	Name     types.String `tfsdk:"name"`
	TeamID   types.String `tfsdk:"team_id"`
	TeamName types.String `tfsdk:"team_name"`
	TenantID types.String `tfsdk:"tenant_id"`
}

type teamsChannelsDataSourceModel struct {
	ID       types.String            `tfsdk:"id"`
	Channels []teamsChannelItemModel `tfsdk:"channels"`
}

// NewTeamsChannelsDataSource returns the ankra_teams_channels data source.
func NewTeamsChannelsDataSource() datasource.DataSource {
	return &teamsChannelsDataSource{}
}

func (channelsDataSource *teamsChannelsDataSource) Metadata(_ context.Context, request datasource.MetadataRequest, response *datasource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_teams_channels"
}

func (channelsDataSource *teamsChannelsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, response *datasource.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: "Lists the channels of every Microsoft Teams team the Ankra bot is installed in, across the organisation's connected " +
			"tenants, for the `channel_id`, `channel_name` and `teams_tenant_id` of a Teams `ankra_alert_destination`. Fails when no Teams tenant is connected.",
		Attributes: map[string]schema.Attribute{
			"id": alertDNSComputedString("Placeholder identifier for the data source."),
			"channels": alertDNSListAttribute("Channels the bot can post to.", map[string]schema.Attribute{
				"id":        alertDNSComputedString("Teams channel id, the destination's `channel_id`."),
				"name":      alertDNSComputedString("Channel name."),
				"team_id":   alertDNSComputedString("Team the channel belongs to."),
				"team_name": alertDNSComputedString("Team name."),
				"tenant_id": alertDNSComputedString("Microsoft tenant, the destination's `teams_tenant_id`."),
			}),
		},
	}
}

func (channelsDataSource *teamsChannelsDataSource) Read(ctx context.Context, _ datasource.ReadRequest, response *datasource.ReadResponse) {
	apiClient := channelsDataSource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	channels, err := apiClient.ListTeamsChannels(ctx)
	if err != nil {
		response.Diagnostics.AddError("Unable to list Microsoft Teams channels", notConnectedDetail("Microsoft Teams tenant", err))
		return
	}
	state := teamsChannelsDataSourceModel{
		ID:       types.StringValue("ankra_teams_channels"),
		Channels: make([]teamsChannelItemModel, 0, len(channels)),
	}
	for _, channel := range channels {
		state.Channels = append(state.Channels, teamsChannelItemModel{
			ID:       types.StringValue(channel.ID),
			Name:     types.StringValue(channel.Name),
			TeamID:   types.StringValue(channel.TeamID),
			TeamName: types.StringValue(channel.TeamName),
			TenantID: types.StringValue(channel.TenantID),
		})
	}
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
}

// clusterDNSZonesDataSource is ankra_cluster_dns_zones.
type clusterDNSZonesDataSource struct {
	configuredDataSourceClient
}

type clusterDNSZoneItemModel struct {
	ClusterID   types.String `tfsdk:"cluster_id"`
	ClusterName types.String `tfsdk:"cluster_name"`
	FQDN        types.String `tfsdk:"fqdn"`
	State       types.String `tfsdk:"state"`
	LastError   types.String `tfsdk:"last_error"`
}

type clusterDNSZonesDataSourceModel struct {
	ID    types.String              `tfsdk:"id"`
	Zones []clusterDNSZoneItemModel `tfsdk:"zones"`
}

// NewClusterDNSZonesDataSource returns the ankra_cluster_dns_zones data
// source.
func NewClusterDNSZonesDataSource() datasource.DataSource {
	return &clusterDNSZonesDataSource{}
}

func (zonesDataSource *clusterDNSZonesDataSource) Metadata(_ context.Context, request datasource.MetadataRequest, response *datasource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_cluster_dns_zones"
}

func (zonesDataSource *clusterDNSZonesDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, response *datasource.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: "Lists every cluster's generated DNS domain in the organisation (the per-cluster zone under the organisation's root domain) " +
			"with its reconciliation state.",
		Attributes: map[string]schema.Attribute{
			"id": alertDNSComputedString("Placeholder identifier for the data source."),
			"zones": alertDNSListAttribute("Cluster DNS domains.", map[string]schema.Attribute{
				"cluster_id":   alertDNSComputedString("Cluster the domain belongs to."),
				"cluster_name": alertDNSComputedString("Cluster name."),
				"fqdn":         alertDNSComputedString("The cluster's domain."),
				"state":        alertDNSComputedString("Reconciliation state, such as `active` or `pending`."),
				"last_error":   alertDNSComputedString("Last reconciliation error, if any."),
			}),
		},
	}
}

func (zonesDataSource *clusterDNSZonesDataSource) Read(ctx context.Context, _ datasource.ReadRequest, response *datasource.ReadResponse) {
	apiClient := zonesDataSource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	zones, err := apiClient.ListClusterDNSZones(ctx)
	if err != nil {
		response.Diagnostics.AddError("Unable to list cluster DNS zones", err.Error())
		return
	}
	state := clusterDNSZonesDataSourceModel{
		ID:    types.StringValue("ankra_cluster_dns_zones"),
		Zones: make([]clusterDNSZoneItemModel, 0, len(zones)),
	}
	for _, zone := range zones {
		state.Zones = append(state.Zones, clusterDNSZoneItemModel{
			ClusterID:   types.StringValue(zone.ClusterID),
			ClusterName: types.StringValue(zone.ClusterName),
			FQDN:        types.StringValue(zone.FQDN),
			State:       types.StringValue(zone.State),
			LastError:   optionalString(alertDNSStringValue(zone.LastError)),
		})
	}
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
}

// cloudflareDNSRecordsDataSource is ankra_cloudflare_dns_records.
type cloudflareDNSRecordsDataSource struct {
	configuredDataSourceClient
}

type cloudflareDNSRecordItemModel struct {
	ID         types.String `tfsdk:"id"`
	Name       types.String `tfsdk:"name"`
	RecordType types.String `tfsdk:"record_type"`
	Content    types.String `tfsdk:"content"`
	TTL        types.Int64  `tfsdk:"ttl"`
	Proxied    types.Bool   `tfsdk:"proxied"`
	Proxiable  types.Bool   `tfsdk:"proxiable"`
	Priority   types.Int64  `tfsdk:"priority"`
	Comment    types.String `tfsdk:"comment"`
	ManagedBy  types.String `tfsdk:"managed_by"`
}

type cloudflareDNSRecordsDataSourceModel struct {
	ID             types.String                   `tfsdk:"id"`
	ZoneID         types.String                   `tfsdk:"zone_id"`
	CredentialName types.String                   `tfsdk:"credential_name"`
	Name           types.String                   `tfsdk:"name"`
	RecordType     types.String                   `tfsdk:"record_type"`
	Search         types.String                   `tfsdk:"search"`
	IsTruncated    types.Bool                     `tfsdk:"is_truncated"`
	Records        []cloudflareDNSRecordItemModel `tfsdk:"records"`
}

// NewCloudflareDNSRecordsDataSource returns the ankra_cloudflare_dns_records
// data source.
func NewCloudflareDNSRecordsDataSource() datasource.DataSource {
	return &cloudflareDNSRecordsDataSource{}
}

func (recordsDataSource *cloudflareDNSRecordsDataSource) Metadata(_ context.Context, request datasource.MetadataRequest, response *datasource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_cloudflare_dns_records"
}

func (recordsDataSource *cloudflareDNSRecordsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, response *datasource.SchemaResponse) {
	optionalFilter := func(description string) schema.StringAttribute {
		return schema.StringAttribute{MarkdownDescription: description, Optional: true}
	}
	response.Schema = schema.Schema{
		MarkdownDescription: "Lists the DNS records in a Cloudflare zone, including records managed outside Ankra. The platform walks Cloudflare's " +
			"pages for you up to a fixed bound; when a zone holds more records than that, `is_truncated` is `true` and a warning is shown, so narrow " +
			"the listing with `name`, `record_type` or `search`.",
		Attributes: map[string]schema.Attribute{
			"id": alertDNSComputedString("Placeholder identifier for the data source."),
			"zone_id": schema.StringAttribute{
				MarkdownDescription: "Cloudflare zone id, from `ankra_cloudflare_domains`.",
				Required:            true,
			},
			"credential_name": optionalFilter("Cloudflare credential to list with. Unset uses the organisation's only Cloudflare credential."),
			"name":            optionalFilter("Only records with this exact FQDN."),
			"record_type":     optionalFilter("Only records of this type, such as `A` or `CNAME`."),
			"search":          optionalFilter("Only records whose name or content contains this text."),
			"is_truncated":    alertDNSComputedBool("Whether the platform stopped walking Cloudflare's pages before the end, so `records` is incomplete."),
			"records": alertDNSListAttribute("Records in the zone.", map[string]schema.Attribute{
				"id":          alertDNSComputedString("Cloudflare record id."),
				"name":        alertDNSComputedString("Record FQDN."),
				"record_type": alertDNSComputedString("Record type."),
				"content":     alertDNSComputedString("Record content."),
				"ttl":         schema.Int64Attribute{MarkdownDescription: "TTL in seconds; `1` is automatic.", Computed: true},
				"proxied":     alertDNSComputedBool("Whether Cloudflare proxies the record."),
				"proxiable":   alertDNSComputedBool("Whether the record can be proxied."),
				"priority":    schema.Int64Attribute{MarkdownDescription: "MX priority; null for other types.", Computed: true},
				"comment":     alertDNSComputedString("Record comment."),
				"managed_by":  alertDNSComputedString("`ankra` for records Ankra created, `external` for the rest."),
			}),
		},
	}
}

func (recordsDataSource *cloudflareDNSRecordsDataSource) Read(ctx context.Context, request datasource.ReadRequest, response *datasource.ReadResponse) {
	apiClient := recordsDataSource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var configuration cloudflareDNSRecordsDataSourceModel
	response.Diagnostics.Append(request.Config.Get(ctx, &configuration)...)
	if response.Diagnostics.HasError() {
		return
	}
	listing, err := apiClient.ListCloudflareRecords(ctx, configuration.CredentialName.ValueString(), configuration.ZoneID.ValueString(),
		client.CloudflareRecordFilter{
			Name:       configuration.Name.ValueString(),
			RecordType: configuration.RecordType.ValueString(),
			Search:     configuration.Search.ValueString(),
		})
	if err != nil {
		response.Diagnostics.AddError("Unable to list Cloudflare DNS records", err.Error())
		return
	}
	if listing.IsTruncated {
		response.Diagnostics.AddWarning("Cloudflare DNS record listing truncated",
			"The platform stopped walking Cloudflare's pages before the end; some records are missing from records. Narrow the listing with name, record_type or search.")
	}
	state := configuration
	state.ID = types.StringValue("ankra_cloudflare_dns_records/" + configuration.ZoneID.ValueString())
	state.IsTruncated = types.BoolValue(listing.IsTruncated)
	state.Records = make([]cloudflareDNSRecordItemModel, 0, len(listing.Items))
	for _, record := range listing.Items {
		state.Records = append(state.Records, cloudflareDNSRecordItemModel{
			ID:         types.StringValue(record.ID),
			Name:       types.StringValue(record.Name),
			RecordType: types.StringValue(record.RecordType),
			Content:    types.StringValue(record.Content),
			TTL:        types.Int64Value(record.TTL),
			Proxied:    types.BoolValue(record.Proxied),
			Proxiable:  types.BoolValue(record.Proxiable),
			Priority:   types.Int64PointerValue(record.Priority),
			Comment:    optionalString(record.Comment),
			ManagedBy:  optionalString(record.ManagedBy),
		})
	}
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
}

// alertIngestCredentialsDataSource is ankra_alert_ingest_credentials.
type alertIngestCredentialsDataSource struct {
	configuredDataSourceClient
}

type alertIngestCredentialItemModel struct {
	ID          types.String `tfsdk:"id"`
	Name        types.String `tfsdk:"name"`
	ClusterID   types.String `tfsdk:"cluster_id"`
	Scope       types.String `tfsdk:"scope"`
	Enabled     types.Bool   `tfsdk:"enabled"`
	WebhookPath types.String `tfsdk:"webhook_path"`
	CreatedAt   types.String `tfsdk:"created_at"`
	UpdatedAt   types.String `tfsdk:"updated_at"`
	LastUsedAt  types.String `tfsdk:"last_used_at"`
}

type alertIngestCredentialsDataSourceModel struct {
	ID          types.String                     `tfsdk:"id"`
	Credentials []alertIngestCredentialItemModel `tfsdk:"credentials"`
}

// NewAlertIngestCredentialsDataSource returns the
// ankra_alert_ingest_credentials data source.
func NewAlertIngestCredentialsDataSource() datasource.DataSource {
	return &alertIngestCredentialsDataSource{}
}

func (credentialsDataSource *alertIngestCredentialsDataSource) Metadata(_ context.Context, request datasource.MetadataRequest, response *datasource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_alert_ingest_credentials"
}

func (credentialsDataSource *alertIngestCredentialsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, response *datasource.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: "Lists the organisation's alert ingest credentials: the endpoints external notifiers such as Prometheus Alertmanager " +
			"deliver alerts into Ankra through. The token of a credential is shown only once, when it is created, and is never part of this listing.",
		Attributes: map[string]schema.Attribute{
			"id": alertDNSComputedString("Placeholder identifier for the data source."),
			"credentials": alertDNSListAttribute("Ingest credentials.", map[string]schema.Attribute{
				"id":           alertDNSComputedString("Credential id."),
				"name":         alertDNSComputedString("Credential name."),
				"cluster_id":   alertDNSComputedString("Cluster the credential is pinned to; null when it is not pinned."),
				"scope":        alertDNSComputedString("`cluster`, `platform` or `mixed`: which alerts the credential accepts."),
				"enabled":      alertDNSComputedBool("Whether the credential accepts alerts."),
				"webhook_path": alertDNSComputedString("Path on the Ankra API the notifier posts to, `/webhooks/alerts/<id>`."),
				"created_at":   alertDNSComputedString("Creation time, RFC 3339."),
				"updated_at":   alertDNSComputedString("Last change, RFC 3339."),
				"last_used_at": alertDNSComputedString("Last delivery, RFC 3339; null when never used."),
			}),
		},
	}
}

func (credentialsDataSource *alertIngestCredentialsDataSource) Read(ctx context.Context, _ datasource.ReadRequest, response *datasource.ReadResponse) {
	apiClient := credentialsDataSource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	credentials, err := apiClient.ListAlertIngestCredentials(ctx)
	if err != nil {
		response.Diagnostics.AddError("Unable to list alert ingest credentials", err.Error())
		return
	}
	state := alertIngestCredentialsDataSourceModel{
		ID:          types.StringValue("ankra_alert_ingest_credentials"),
		Credentials: make([]alertIngestCredentialItemModel, 0, len(credentials)),
	}
	for _, credential := range credentials {
		state.Credentials = append(state.Credentials, alertIngestCredentialItemModel{
			ID:          types.StringValue(credential.ID),
			Name:        types.StringValue(credential.Name),
			ClusterID:   optionalString(alertDNSStringValue(credential.ClusterID)),
			Scope:       optionalString(alertDNSStringValue(credential.Scope)),
			Enabled:     types.BoolValue(credential.Enabled),
			WebhookPath: types.StringValue(client.AlertIngestWebhookPathPrefix + credential.ID),
			CreatedAt:   optionalString(credential.CreatedAt),
			UpdatedAt:   optionalString(credential.UpdatedAt),
			LastUsedAt:  optionalString(alertDNSStringValue(credential.LastUsedAt)),
		})
	}
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
}
