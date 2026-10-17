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
	_ datasource.DataSource              = (*organisationDNSZoneDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*organisationDNSZoneDataSource)(nil)
	_ datasource.DataSource              = (*organisationDNSRecordsDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*organisationDNSRecordsDataSource)(nil)
	_ datasource.DataSource              = (*organisationCustomDNSZonesDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*organisationCustomDNSZonesDataSource)(nil)
	_ datasource.DataSource              = (*dnsCredentialsDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*dnsCredentialsDataSource)(nil)
	_ datasource.DataSource              = (*cloudflareCredentialsDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*cloudflareCredentialsDataSource)(nil)
	_ datasource.DataSource              = (*cloudflareDomainsDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*cloudflareDomainsDataSource)(nil)
)

func alertDNSComputedString(description string) schema.StringAttribute {
	return schema.StringAttribute{MarkdownDescription: description, Computed: true}
}

func alertDNSComputedBool(description string) schema.BoolAttribute {
	return schema.BoolAttribute{MarkdownDescription: description, Computed: true}
}

func alertDNSListAttribute(description string, attributes map[string]schema.Attribute) schema.ListNestedAttribute {
	return schema.ListNestedAttribute{
		MarkdownDescription: description,
		Computed:            true,
		NestedObject:        schema.NestedAttributeObject{Attributes: attributes},
	}
}

// organisationDNSZoneDataSource is ankra_organisation_dns_zone.
type organisationDNSZoneDataSource struct {
	configuredDataSourceClient
}

type organisationDNSZoneDataSourceModel struct {
	ID    types.String `tfsdk:"id"`
	FQDN  types.String `tfsdk:"fqdn"`
	State types.String `tfsdk:"state"`
}

// NewOrganisationDNSZoneDataSource returns the ankra_organisation_dns_zone
// data source, the counterpart of `ankra org dns zone`.
func NewOrganisationDNSZoneDataSource() datasource.DataSource {
	return &organisationDNSZoneDataSource{}
}

func (zoneDataSource *organisationDNSZoneDataSource) Metadata(_ context.Context, request datasource.MetadataRequest, response *datasource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_organisation_dns_zone"
}

func (zoneDataSource *organisationDNSZoneDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, response *datasource.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: "The organisation's own delegated DNS zone, which `ankra_organisation_dns_record` names hang off. The counterpart of `ankra org dns zone`.",
		Attributes: map[string]schema.Attribute{
			"id":    alertDNSComputedString("The zone FQDN."),
			"fqdn":  alertDNSComputedString("Zone FQDN; empty while no zone exists."),
			"state": alertDNSComputedString("`active`, `pending`, or `none` when the organisation has no zone yet."),
		},
	}
}

func (zoneDataSource *organisationDNSZoneDataSource) Read(ctx context.Context, _ datasource.ReadRequest, response *datasource.ReadResponse) {
	apiClient := zoneDataSource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	zone, err := apiClient.GetOrganisationDNSZone(ctx)
	if err != nil {
		response.Diagnostics.AddError("Unable to read the organisation's DNS zone", err.Error())
		return
	}
	state := organisationDNSZoneDataSourceModel{
		ID:    types.StringValue("ankra_organisation_dns_zone"),
		FQDN:  types.StringValue(zone.FQDN),
		State: types.StringValue(zone.State),
	}
	if zone.FQDN != "" {
		state.ID = types.StringValue(zone.FQDN)
	}
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
}

// organisationDNSRecordsDataSource is ankra_organisation_dns_records.
type organisationDNSRecordsDataSource struct {
	configuredDataSourceClient
}

type organisationDNSRecordItemModel struct {
	ID         types.String `tfsdk:"id"`
	Name       types.String `tfsdk:"name"`
	RecordType types.String `tfsdk:"record_type"`
	Content    types.String `tfsdk:"content"`
	TTL        types.Int64  `tfsdk:"ttl"`
	State      types.String `tfsdk:"state"`
}

type organisationDNSRecordsDataSourceModel struct {
	ID      types.String                     `tfsdk:"id"`
	Records []organisationDNSRecordItemModel `tfsdk:"records"`
}

// NewOrganisationDNSRecordsDataSource returns the
// ankra_organisation_dns_records data source, the counterpart of
// `ankra org dns list`.
func NewOrganisationDNSRecordsDataSource() datasource.DataSource {
	return &organisationDNSRecordsDataSource{}
}

func (recordsDataSource *organisationDNSRecordsDataSource) Metadata(_ context.Context, request datasource.MetadataRequest, response *datasource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_organisation_dns_records"
}

func (recordsDataSource *organisationDNSRecordsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, response *datasource.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: "Lists the records in the organisation's DNS zone, including those clusters publish. The counterpart of `ankra org dns list`.",
		Attributes: map[string]schema.Attribute{
			"id": alertDNSComputedString("Placeholder identifier for the data source."),
			"records": alertDNSListAttribute("Records in the zone.", map[string]schema.Attribute{
				"id":          alertDNSComputedString("Identifier of the record."),
				"name":        alertDNSComputedString("Full name of the record."),
				"record_type": alertDNSComputedString("Record type."),
				"content":     alertDNSComputedString("What the record points at."),
				"ttl":         schema.Int64Attribute{MarkdownDescription: "TTL in seconds; null is Auto.", Computed: true},
				"state":       alertDNSComputedString("`pending`, `active`, `deleting` or `failed`."),
			}),
		},
	}
}

func (recordsDataSource *organisationDNSRecordsDataSource) Read(ctx context.Context, _ datasource.ReadRequest, response *datasource.ReadResponse) {
	apiClient := recordsDataSource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	records, err := apiClient.ListOrganisationDNSRecords(ctx)
	if err != nil {
		response.Diagnostics.AddError("Unable to list organisation DNS records", err.Error())
		return
	}
	state := organisationDNSRecordsDataSourceModel{
		ID:      types.StringValue("ankra_organisation_dns_records"),
		Records: make([]organisationDNSRecordItemModel, 0, len(records)),
	}
	for _, record := range records {
		state.Records = append(state.Records, organisationDNSRecordItemModel{
			ID:         types.StringValue(record.ID),
			Name:       types.StringValue(record.Name),
			RecordType: types.StringValue(record.RecordType),
			Content:    types.StringValue(record.Content),
			TTL:        types.Int64PointerValue(record.TTL),
			State:      types.StringValue(record.State),
		})
	}
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
}

// organisationCustomDNSZonesDataSource is ankra_organisation_custom_dns_zones.
type organisationCustomDNSZonesDataSource struct {
	configuredDataSourceClient
}

type organisationCustomDNSZonesDataSourceModel struct {
	ID    types.String                     `tfsdk:"id"`
	Zones []organisationCustomDNSZoneModel `tfsdk:"zones"`
}

// NewOrganisationCustomDNSZonesDataSource returns the
// ankra_organisation_custom_dns_zones data source, the counterpart of
// `ankra org custom-dns-zones list`.
func NewOrganisationCustomDNSZonesDataSource() datasource.DataSource {
	return &organisationCustomDNSZonesDataSource{}
}

func (zonesDataSource *organisationCustomDNSZonesDataSource) Metadata(_ context.Context, request datasource.MetadataRequest, response *datasource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_organisation_custom_dns_zones"
}

func (zonesDataSource *organisationCustomDNSZonesDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, response *datasource.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: "Lists the zones every cluster in the organisation serves with the organisation's own credential. The counterpart of `ankra org custom-dns-zones list`.",
		Attributes: map[string]schema.Attribute{
			"id": alertDNSComputedString("Placeholder identifier for the data source."),
			"zones": alertDNSListAttribute("Declared zones.", map[string]schema.Attribute{
				"id":              alertDNSComputedString("The zone name."),
				"zone":            alertDNSComputedString("Zone name."),
				"credential_name": alertDNSComputedString("Credential that publishes into the zone."),
				"credential_provider":   alertDNSComputedString("Credential family: `webhook`, `cloudflare` or `avura`."),
				"policy":          alertDNSComputedString("external-dns policy the clusters run for the zone."),
			}),
		},
	}
}

func (zonesDataSource *organisationCustomDNSZonesDataSource) Read(ctx context.Context, _ datasource.ReadRequest, response *datasource.ReadResponse) {
	apiClient := zonesDataSource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	zones, err := apiClient.ListOrganisationCustomDNSZones(ctx)
	if err != nil {
		response.Diagnostics.AddError("Unable to list organisation custom DNS zones", err.Error())
		return
	}
	state := organisationCustomDNSZonesDataSourceModel{
		ID:    types.StringValue("ankra_organisation_custom_dns_zones"),
		Zones: make([]organisationCustomDNSZoneModel, 0, len(zones)),
	}
	for index := range zones {
		var model organisationCustomDNSZoneModel
		organisationCustomDNSZoneApply(&zones[index], &model)
		state.Zones = append(state.Zones, model)
	}
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
}

// dnsCredentialsDataSource is ankra_dns_credentials.
type dnsCredentialsDataSource struct {
	configuredDataSourceClient
}

type dnsCredentialItemModel struct {
	ID        types.String `tfsdk:"id"`
	Name      types.String `tfsdk:"name"`
	CreatedAt types.String `tfsdk:"created_at"`
}

type dnsCredentialsDataSourceModel struct {
	ID          types.String             `tfsdk:"id"`
	Credentials []dnsCredentialItemModel `tfsdk:"credentials"`
}

// NewDNSCredentialsDataSource returns the ankra_dns_credentials data source,
// the counterpart of `ankra org dns credentials list`.
func NewDNSCredentialsDataSource() datasource.DataSource {
	return &dnsCredentialsDataSource{}
}

func (credentialsDataSource *dnsCredentialsDataSource) Metadata(_ context.Context, request datasource.MetadataRequest, response *datasource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_dns_credentials"
}

func (credentialsDataSource *dnsCredentialsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, response *datasource.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: "Lists the organisation's DNS webhook credentials; the webhook URLs are never returned. The counterpart of `ankra org dns credentials list`.",
		Attributes: map[string]schema.Attribute{
			"id": alertDNSComputedString("Placeholder identifier for the data source."),
			"credentials": alertDNSListAttribute("DNS credentials.", map[string]schema.Attribute{
				"id":         alertDNSComputedString("Identifier of the credential."),
				"name":       alertDNSComputedString("Credential name."),
				"created_at": alertDNSComputedString("When the credential was stored."),
			}),
		},
	}
}

func (credentialsDataSource *dnsCredentialsDataSource) Read(ctx context.Context, _ datasource.ReadRequest, response *datasource.ReadResponse) {
	apiClient := credentialsDataSource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	credentials, err := apiClient.ListDNSCredentials(ctx)
	if err != nil {
		response.Diagnostics.AddError("Unable to list DNS credentials", err.Error())
		return
	}
	state := dnsCredentialsDataSourceModel{
		ID:          types.StringValue("ankra_dns_credentials"),
		Credentials: make([]dnsCredentialItemModel, 0, len(credentials)),
	}
	for _, credential := range credentials {
		state.Credentials = append(state.Credentials, dnsCredentialItemModel{
			ID:        types.StringValue(credential.ID),
			Name:      types.StringValue(credential.Name),
			CreatedAt: types.StringValue(credential.CreatedAt),
		})
	}
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
}

// cloudflareCredentialsDataSource is ankra_cloudflare_credentials.
type cloudflareCredentialsDataSource struct {
	configuredDataSourceClient
}

type cloudflareCredentialItemModel struct {
	Name           types.String `tfsdk:"name"`
	AccountID      types.String `tfsdk:"account_id"`
	AccountName    types.String `tfsdk:"account_name"`
	TokenKind      types.String `tfsdk:"token_kind"`
	TokenExpiresAt types.String `tfsdk:"token_expires_at"`
	State          types.String `tfsdk:"state"`
	NeedsReconnect types.Bool   `tfsdk:"needs_reconnect"`
}

type cloudflareCredentialsDataSourceModel struct {
	ID          types.String                    `tfsdk:"id"`
	Credentials []cloudflareCredentialItemModel `tfsdk:"credentials"`
}

// NewCloudflareCredentialsDataSource returns the ankra_cloudflare_credentials
// data source, the counterpart of `ankra org cloudflare credentials`.
func NewCloudflareCredentialsDataSource() datasource.DataSource {
	return &cloudflareCredentialsDataSource{}
}

func (credentialsDataSource *cloudflareCredentialsDataSource) Metadata(_ context.Context, request datasource.MetadataRequest, response *datasource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_cloudflare_credentials"
}

func (credentialsDataSource *cloudflareCredentialsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, response *datasource.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: "Lists the organisation's connected Cloudflare credentials; tokens are never returned. The counterpart of `ankra org cloudflare credentials`.",
		Attributes: map[string]schema.Attribute{
			"id": alertDNSComputedString("Placeholder identifier for the data source."),
			"credentials": alertDNSListAttribute("Cloudflare credentials.", map[string]schema.Attribute{
				"name":             alertDNSComputedString("Credential name."),
				"account_id":       alertDNSComputedString("Cloudflare account id."),
				"account_name":     alertDNSComputedString("Cloudflare account name."),
				"token_kind":       alertDNSComputedString("Whether the token is user-owned or account-owned."),
				"token_expires_at": alertDNSComputedString("When the token expires, if it does."),
				"state":            alertDNSComputedString("Health of the credential."),
				"needs_reconnect":  alertDNSComputedBool("Whether the token is gone and must be connected again."),
			}),
		},
	}
}

func (credentialsDataSource *cloudflareCredentialsDataSource) Read(ctx context.Context, _ datasource.ReadRequest, response *datasource.ReadResponse) {
	apiClient := credentialsDataSource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	credentials, err := apiClient.ListCloudflareCredentials(ctx)
	if err != nil {
		response.Diagnostics.AddError("Unable to list Cloudflare credentials", err.Error())
		return
	}
	state := cloudflareCredentialsDataSourceModel{
		ID:          types.StringValue("ankra_cloudflare_credentials"),
		Credentials: make([]cloudflareCredentialItemModel, 0, len(credentials)),
	}
	for _, credential := range credentials {
		state.Credentials = append(state.Credentials, cloudflareCredentialItemModel{
			Name:           types.StringValue(credential.Name),
			AccountID:      optionalString(credential.AccountID),
			AccountName:    optionalString(credential.AccountName),
			TokenKind:      optionalString(credential.TokenKind),
			TokenExpiresAt: optionalString(credential.TokenExpiresAt),
			State:          types.StringValue(credential.State),
			NeedsReconnect: types.BoolValue(credential.NeedsReconnect),
		})
	}
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
}

// cloudflareDomainsDataSource is ankra_cloudflare_domains.
type cloudflareDomainsDataSource struct {
	configuredDataSourceClient
}

type cloudflareDomainItemModel struct {
	ID          types.String `tfsdk:"id"`
	Name        types.String `tfsdk:"name"`
	Status      types.String `tfsdk:"status"`
	Paused      types.Bool   `tfsdk:"paused"`
	Type        types.String `tfsdk:"type"`
	AccountID   types.String `tfsdk:"account_id"`
	NameServers types.List   `tfsdk:"name_servers"`
}

type cloudflareDomainsDataSourceModel struct {
	ID             types.String                `tfsdk:"id"`
	CredentialName types.String                `tfsdk:"credential_name"`
	IsTruncated    types.Bool                  `tfsdk:"is_truncated"`
	Domains        []cloudflareDomainItemModel `tfsdk:"domains"`
}

// NewCloudflareDomainsDataSource returns the ankra_cloudflare_domains data
// source, the counterpart of `ankra org cloudflare domains`.
func NewCloudflareDomainsDataSource() datasource.DataSource {
	return &cloudflareDomainsDataSource{}
}

func (domainsDataSource *cloudflareDomainsDataSource) Metadata(_ context.Context, request datasource.MetadataRequest, response *datasource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_cloudflare_domains"
}

func (domainsDataSource *cloudflareDomainsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, response *datasource.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: "Lists the Cloudflare zones a connected credential reaches, for the `zone_id` of `ankra_cloudflare_dns_record`. The counterpart of `ankra org cloudflare domains`.",
		Attributes: map[string]schema.Attribute{
			"id": alertDNSComputedString("Placeholder identifier for the data source."),
			"credential_name": schema.StringAttribute{
				MarkdownDescription: "Credential whose zones to list. Unset uses the organisation's only Cloudflare credential.",
				Optional:            true,
			},
			"is_truncated": alertDNSComputedBool("Whether the platform stopped walking Cloudflare's pages before the end, so `domains` is incomplete."),
			"domains": alertDNSListAttribute("Zones the credential reaches.", map[string]schema.Attribute{
				"id":           alertDNSComputedString("Cloudflare zone id."),
				"name":         alertDNSComputedString("Zone name."),
				"status":       alertDNSComputedString("Cloudflare zone status, such as `active` or `pending`."),
				"paused":       alertDNSComputedBool("Whether Cloudflare is paused for the zone."),
				"type":         alertDNSComputedString("Zone setup type, such as `full`."),
				"account_id":   alertDNSComputedString("Cloudflare account that owns the zone."),
				"name_servers": schema.ListAttribute{MarkdownDescription: "Cloudflare name servers to delegate to.", Computed: true, ElementType: types.StringType},
			}),
		},
	}
}

func (domainsDataSource *cloudflareDomainsDataSource) Read(ctx context.Context, request datasource.ReadRequest, response *datasource.ReadResponse) {
	apiClient := domainsDataSource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var configuration cloudflareDomainsDataSourceModel
	response.Diagnostics.Append(request.Config.Get(ctx, &configuration)...)
	if response.Diagnostics.HasError() {
		return
	}
	listing, err := apiClient.ListCloudflareDomains(ctx, configuration.CredentialName.ValueString())
	if err != nil {
		response.Diagnostics.AddError("Unable to list Cloudflare domains", err.Error())
		return
	}
	if listing.IsTruncated {
		response.Diagnostics.AddWarning("Cloudflare domain listing truncated",
			"The platform stopped walking Cloudflare's pages before the end; some zones are missing from domains.")
	}
	state := cloudflareDomainsDataSourceModel{
		ID:             types.StringValue("ankra_cloudflare_domains"),
		CredentialName: configuration.CredentialName,
		IsTruncated:    types.BoolValue(listing.IsTruncated),
		Domains:        make([]cloudflareDomainItemModel, 0, len(listing.Items)),
	}
	for _, domain := range listing.Items {
		state.Domains = append(state.Domains, cloudflareDomainItemFrom(domain))
	}
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
}

func cloudflareDomainItemFrom(domain client.CloudflareDomain) cloudflareDomainItemModel {
	nameServers := domain.NameServers
	if nameServers == nil {
		nameServers = []string{}
	}
	return cloudflareDomainItemModel{
		ID:          types.StringValue(domain.ID),
		Name:        types.StringValue(domain.Name),
		Status:      types.StringValue(domain.Status),
		Paused:      types.BoolValue(domain.Paused),
		Type:        optionalString(domain.Type),
		AccountID:   optionalString(domain.AccountID),
		NameServers: listFromStrings(nameServers),
	}
}
