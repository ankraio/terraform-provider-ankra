// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"ankra.io/terraform-provider-ankra/internal/client"
)

// laneCatalogueKind is the Terraform type of one catalogue entry field.
type laneCatalogueKind int

const (
	laneCatalogueString laneCatalogueKind = iota
	laneCatalogueInt64
	laneCatalogueFloat64
	laneCatalogueBool
	laneCatalogueStringList
)

// laneCatalogueField is one attribute of a catalogue entry. source is the
// dot-separated JSON path in the platform's answer, when it differs from
// name.
type laneCatalogueField struct {
	name        string
	description string
	kind        laneCatalogueKind
	source      string
}

// laneCatalogueFilter is one argument of a catalogue data source, sent as a
// query parameter of the same name.
type laneCatalogueFilter struct {
	name        string
	description string
	required    bool
}

// laneCatalogueDefinition describes one catalogue data source: its
// arguments, the list it returns, and the client call behind it.
type laneCatalogueDefinition struct {
	typeSuffix      string
	description     string
	filters         []laneCatalogueFilter
	listName        string
	listDescription string
	fields          []laneCatalogueField
	fetch           func(ctx context.Context, apiClient *client.Client, filters map[string]string) (any, error)
	// fetchPriced replaces fetch for a catalogue that reports whether its
	// prices are complete; the data source then carries pricing_complete and
	// incomplete_reasons.
	fetchPriced func(ctx context.Context, apiClient *client.Client, filters map[string]string) (any, client.CataloguePricing, error)
	// pricingDescription says what pricing_complete covers for this
	// catalogue.
	pricingDescription string
}

// laneCatalogueDataSource is the shared implementation of the per-lane
// catalogue data sources. Every read is a live call through the named
// credential, so the result is what the cloud reports at plan time.
type laneCatalogueDataSource struct {
	configuredDataSourceClient
	definition laneCatalogueDefinition
}

var _ datasource.DataSourceWithConfigure = (*laneCatalogueDataSource)(nil)

func (catalogue *laneCatalogueDataSource) Metadata(_ context.Context, request datasource.MetadataRequest, response *datasource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + catalogue.definition.typeSuffix
}

func (catalogue *laneCatalogueDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, response *datasource.SchemaResponse) {
	attributes := map[string]schema.Attribute{}
	for _, filter := range catalogue.definition.filters {
		attributes[filter.name] = schema.StringAttribute{
			MarkdownDescription: filter.description,
			Required:            filter.required,
			Optional:            !filter.required,
			Validators:          []validator.String{laneCatalogueNonEmpty{}},
		}
	}
	entryAttributes := map[string]schema.Attribute{}
	for _, field := range catalogue.definition.fields {
		entryAttributes[field.name] = laneCatalogueFieldAttribute(field)
	}
	if catalogue.definition.fetchPriced != nil {
		attributes["pricing_complete"] = schema.BoolAttribute{
			MarkdownDescription: catalogue.definition.pricingDescription,
			Computed:            true,
		}
		attributes["incomplete_reasons"] = schema.ListAttribute{
			MarkdownDescription: "Why `pricing_complete` is false; empty when it is true.",
			Computed:            true,
			ElementType:         types.StringType,
		}
	}
	attributes[catalogue.definition.listName] = schema.ListNestedAttribute{
		MarkdownDescription: catalogue.definition.listDescription,
		Computed:            true,
		NestedObject:        schema.NestedAttributeObject{Attributes: entryAttributes},
	}
	response.Schema = schema.Schema{
		MarkdownDescription: catalogue.definition.description,
		Attributes:          attributes,
	}
}

func laneCatalogueFieldAttribute(field laneCatalogueField) schema.Attribute {
	switch field.kind {
	case laneCatalogueInt64:
		return schema.Int64Attribute{MarkdownDescription: field.description, Computed: true}
	case laneCatalogueFloat64:
		return schema.Float64Attribute{MarkdownDescription: field.description, Computed: true}
	case laneCatalogueBool:
		return schema.BoolAttribute{MarkdownDescription: field.description, Computed: true}
	case laneCatalogueStringList:
		return schema.ListAttribute{MarkdownDescription: field.description, Computed: true, ElementType: types.StringType}
	default:
		return schema.StringAttribute{MarkdownDescription: field.description, Computed: true}
	}
}

func laneCatalogueFieldType(field laneCatalogueField) attr.Type {
	switch field.kind {
	case laneCatalogueInt64:
		return types.Int64Type
	case laneCatalogueFloat64:
		return types.Float64Type
	case laneCatalogueBool:
		return types.BoolType
	case laneCatalogueStringList:
		return types.ListType{ElemType: types.StringType}
	default:
		return types.StringType
	}
}

func (catalogue *laneCatalogueDataSource) Read(ctx context.Context, request datasource.ReadRequest, response *datasource.ReadResponse) {
	apiClient := catalogue.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	filters := map[string]string{}
	for _, filter := range catalogue.definition.filters {
		var value types.String
		response.Diagnostics.Append(request.Config.GetAttribute(ctx, path.Root(filter.name), &value)...)
		if response.Diagnostics.HasError() {
			return
		}
		filters[filter.name] = value.ValueString()
		response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root(filter.name), value)...)
	}

	var entries any
	var pricing client.CataloguePricing
	var err error
	if catalogue.definition.fetchPriced != nil {
		entries, pricing, err = catalogue.definition.fetchPriced(ctx, apiClient, filters)
	} else {
		entries, err = catalogue.definition.fetch(ctx, apiClient, filters)
	}
	if err != nil {
		response.Diagnostics.AddError("Unable to read "+strings.TrimPrefix(catalogue.definition.typeSuffix, "_"), err.Error())
		return
	}
	list, conversionError := laneCatalogueList(catalogue.definition.fields, entries)
	if conversionError != nil {
		response.Diagnostics.AddError("Unexpected catalogue response", conversionError.Error())
		return
	}
	response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root(catalogue.definition.listName), list)...)
	if catalogue.definition.fetchPriced != nil {
		reasons := pricing.IncompleteReasons
		if reasons == nil {
			reasons = []string{}
		}
		response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("pricing_complete"), types.BoolValue(pricing.Complete))...)
		response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("incomplete_reasons"), listFromStrings(reasons))...)
	}
}

// laneCatalogueList converts the client's typed entries into the list
// attribute, field by field from their JSON form, so each catalogue only
// declares its fields once.
func laneCatalogueList(fields []laneCatalogueField, entries any) (types.List, error) {
	objectTypes := map[string]attr.Type{}
	for _, field := range fields {
		objectTypes[field.name] = laneCatalogueFieldType(field)
	}
	objectType := types.ObjectType{AttrTypes: objectTypes}

	encoded, err := json.Marshal(entries)
	if err != nil {
		return types.ListNull(objectType), err
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	var rows []map[string]any
	if err := decoder.Decode(&rows); err != nil {
		return types.ListNull(objectType), err
	}

	elements := make([]attr.Value, 0, len(rows))
	for _, row := range rows {
		values := map[string]attr.Value{}
		for _, field := range fields {
			source := field.source
			if source == "" {
				source = field.name
			}
			value, err := laneCatalogueValue(field, laneCatalogueLookup(row, source))
			if err != nil {
				return types.ListNull(objectType), err
			}
			values[field.name] = value
		}
		object, diagnostics := types.ObjectValue(objectTypes, values)
		if diagnostics.HasError() {
			return types.ListNull(objectType), fmt.Errorf("building catalogue entry: %v", diagnostics)
		}
		elements = append(elements, object)
	}
	list, diagnostics := types.ListValue(objectType, elements)
	if diagnostics.HasError() {
		return types.ListNull(objectType), fmt.Errorf("building catalogue list: %v", diagnostics)
	}
	return list, nil
}

func laneCatalogueLookup(row map[string]any, source string) any {
	var current any = row
	for _, segment := range strings.Split(source, ".") {
		object, ok := current.(map[string]any)
		if !ok {
			return nil
		}
		current = object[segment]
	}
	return current
}

func laneCatalogueValue(field laneCatalogueField, raw any) (attr.Value, error) {
	switch field.kind {
	case laneCatalogueInt64:
		if raw == nil {
			return types.Int64Null(), nil
		}
		number, ok := raw.(json.Number)
		if !ok {
			return nil, fmt.Errorf("%s: expected a number, got %T", field.name, raw)
		}
		integer, err := number.Int64()
		if err != nil {
			return nil, fmt.Errorf("%s: %w", field.name, err)
		}
		return types.Int64Value(integer), nil
	case laneCatalogueFloat64:
		if raw == nil {
			return types.Float64Null(), nil
		}
		number, ok := raw.(json.Number)
		if !ok {
			return nil, fmt.Errorf("%s: expected a number, got %T", field.name, raw)
		}
		decimal, err := number.Float64()
		if err != nil {
			return nil, fmt.Errorf("%s: %w", field.name, err)
		}
		return types.Float64Value(decimal), nil
	case laneCatalogueBool:
		if raw == nil {
			return types.BoolNull(), nil
		}
		flag, ok := raw.(bool)
		if !ok {
			return nil, fmt.Errorf("%s: expected a boolean, got %T", field.name, raw)
		}
		return types.BoolValue(flag), nil
	case laneCatalogueStringList:
		if raw == nil {
			return types.ListNull(types.StringType), nil
		}
		items, ok := raw.([]any)
		if !ok {
			return nil, fmt.Errorf("%s: expected a list, got %T", field.name, raw)
		}
		values := make([]string, 0, len(items))
		for _, item := range items {
			values = append(values, fmt.Sprint(item))
		}
		return listFromStrings(values), nil
	default:
		if raw == nil {
			return types.StringNull(), nil
		}
		text, ok := raw.(string)
		if !ok {
			return nil, fmt.Errorf("%s: expected a string, got %T", field.name, raw)
		}
		return types.StringValue(text), nil
	}
}

// laneCatalogueNonEmpty refuses an empty catalogue argument, which the
// platform would refuse as a missing query parameter.
type laneCatalogueNonEmpty struct{}

func (laneCatalogueNonEmpty) Description(_ context.Context) string {
	return "value must not be empty"
}

func (nonEmpty laneCatalogueNonEmpty) MarkdownDescription(ctx context.Context) string {
	return nonEmpty.Description(ctx)
}

func (nonEmpty laneCatalogueNonEmpty) ValidateString(ctx context.Context, request validator.StringRequest, response *validator.StringResponse) {
	if request.ConfigValue.IsNull() || request.ConfigValue.IsUnknown() {
		return
	}
	if strings.TrimSpace(request.ConfigValue.ValueString()) == "" {
		response.Diagnostics.AddAttributeError(request.Path, "Invalid value", nonEmpty.Description(ctx)+"; leave the attribute unset instead.")
	}
}

// ---------------------------------------------------------------------------
// Definitions

var (
	catalogueCredentialFilter           = laneCatalogueFilter{name: "credential_id", description: "Credential to read the catalogue through.", required: true}
	catalogueAWSRegionFilter            = laneCatalogueFilter{name: "region", description: "AWS region, e.g. `eu-central-1` (`data.ankra_aws_regions`).", required: true}
	catalogueProxmoxNodeFilter          = laneCatalogueFilter{name: "node", description: "Proxmox VE node (host) name (`data.ankra_proxmox_hosts`).", required: true}
	catalogueAnkraCloudCredentialFilter = laneCatalogueFilter{name: "credential_id",
		description: "Ankra Cloud credential to read the catalogue through. Unset uses the organisation's built-in Ankra Cloud credential."}
)

func laneCatalogueDataSourceFactory(definition laneCatalogueDefinition) func() datasource.DataSource {
	return func() datasource.DataSource {
		return &laneCatalogueDataSource{definition: definition}
	}
}

// laneCatalogueDataSources are the per-lane catalogue data sources: the
// values a cluster on that lane can be built with.
func laneCatalogueDataSources() []func() datasource.DataSource {
	definitions := []laneCatalogueDefinition{
		{
			typeSuffix:  "_aws_regions",
			description: "The AWS regions enabled for an AWS credential, to pick `region` on `ankra_aws_cluster`.",
			filters:     []laneCatalogueFilter{catalogueCredentialFilter},
			listName:    "regions", listDescription: "Enabled regions.",
			fields: []laneCatalogueField{
				{name: "slug", description: "Region code, e.g. `eu-central-1`.", kind: laneCatalogueString},
				{name: "name", description: "Display name.", kind: laneCatalogueString},
			},
			fetchPriced: func(ctx context.Context, apiClient *client.Client, filters map[string]string) (any, client.CataloguePricing, error) {
				return apiClient.ListAWSRegions(ctx, filters["credential_id"])
			},
			pricingDescription: "Whether the platform could read prices for the account's regions.",
		},
		{
			typeSuffix:  "_aws_instance_types",
			description: "The EC2 instance types offered in a region, for the `*_type` arguments of `ankra_aws_cluster` and `ankra_node_group`.",
			filters:     []laneCatalogueFilter{catalogueCredentialFilter, catalogueAWSRegionFilter},
			listName:    "instance_types", listDescription: "Instance types offered in the region.",
			fields: []laneCatalogueField{
				{name: "name", description: "Instance type, e.g. `m7i.large`.", kind: laneCatalogueString},
				{name: "vcpus", description: "vCPUs.", kind: laneCatalogueInt64},
				{name: "memory_gib", description: "Memory in GiB.", kind: laneCatalogueFloat64},
				{name: "architecture", description: "CPU architecture, e.g. `x86_64` or `arm64`.", kind: laneCatalogueString},
				{name: "category", description: "Instance family category.", kind: laneCatalogueString},
				{name: "hourly_price_usd", description: "On-demand hourly price in USD; null when the price is unknown.", kind: laneCatalogueFloat64},
				{name: "monthly_price_usd", description: "Monthly price in USD; null when the price is unknown.", kind: laneCatalogueFloat64},
				{name: "current_generation", description: "Whether the type is current generation.", kind: laneCatalogueBool},
			},
			fetchPriced: func(ctx context.Context, apiClient *client.Client, filters map[string]string) (any, client.CataloguePricing, error) {
				return apiClient.ListAWSInstanceTypes(ctx, filters["credential_id"], filters["region"])
			},
			pricingDescription: "Whether every instance type carries a price; when false, some `hourly_price_usd` and `monthly_price_usd` are null.",
		},
		{
			typeSuffix:  "_aws_vpcs",
			description: "The VPCs of an AWS region, for `vpc_id` on `ankra_aws_cluster`.",
			filters:     []laneCatalogueFilter{catalogueCredentialFilter, catalogueAWSRegionFilter},
			listName:    "vpcs", listDescription: "VPCs of the region.",
			fields: []laneCatalogueField{
				{name: "id", description: "VPC id.", kind: laneCatalogueString},
				{name: "name", description: "Name tag.", kind: laneCatalogueString},
				{name: "cidr", description: "Primary IPv4 CIDR.", kind: laneCatalogueString},
				{name: "is_default", description: "Whether this is the region's default VPC.", kind: laneCatalogueBool},
			},
			fetch: func(ctx context.Context, apiClient *client.Client, filters map[string]string) (any, error) {
				return apiClient.ListAWSVPCs(ctx, filters["credential_id"], filters["region"])
			},
		},
		{
			typeSuffix:  "_aws_subnets",
			description: "The subnets of an AWS VPC, for `node_subnet_ids` and `bastion_subnet_id` on `ankra_aws_cluster`.",
			filters: []laneCatalogueFilter{catalogueCredentialFilter, catalogueAWSRegionFilter,
				{name: "vpc_id", description: "VPC id (`data.ankra_aws_vpcs`).", required: true}},
			listName: "subnets", listDescription: "Subnets of the VPC.",
			fields: []laneCatalogueField{
				{name: "id", description: "Subnet id.", kind: laneCatalogueString},
				{name: "name", description: "Name tag.", kind: laneCatalogueString},
				{name: "cidr", description: "IPv4 CIDR.", kind: laneCatalogueString},
				{name: "availability_zone", description: "Availability zone.", kind: laneCatalogueString},
				{name: "map_public_ip_on_launch", description: "Whether instances get a public IPv4 address on launch.", kind: laneCatalogueBool},
				{name: "egress_kind", description: "Where the subnet's default route leads: `nat_gateway`, `nat_instance`, `internet_gateway`, `transit`, `none` or `unknown`.",
					kind: laneCatalogueString, source: "egress.kind"},
			},
			fetch: func(ctx context.Context, apiClient *client.Client, filters map[string]string) (any, error) {
				return apiClient.ListAWSSubnets(ctx, filters["credential_id"], filters["region"], filters["vpc_id"])
			},
		},
		{
			typeSuffix:  "_proxmox_hosts",
			description: "The nodes (hosts) of the Proxmox VE cluster behind a credential, for `node` and `placement_nodes` on `ankra_proxmox_cluster`. The counterpart of `ankra cluster proxmox hosts`.",
			filters:     []laneCatalogueFilter{catalogueCredentialFilter},
			listName:    "hosts", listDescription: "Proxmox VE nodes.",
			fields: []laneCatalogueField{
				{name: "node", description: "Node name.", kind: laneCatalogueString},
				{name: "status", description: "Node status, e.g. `online`.", kind: laneCatalogueString},
				{name: "cpu_count", description: "CPU count.", kind: laneCatalogueInt64},
				{name: "memory_bytes", description: "Memory in bytes.", kind: laneCatalogueInt64},
			},
			fetch: func(ctx context.Context, apiClient *client.Client, filters map[string]string) (any, error) {
				return apiClient.ListProxmoxHosts(ctx, filters["credential_id"])
			},
		},
		{
			typeSuffix:  "_proxmox_bridges",
			description: "The bridges and SDN vnets of a Proxmox VE node, for `bridge` on `ankra_proxmox_cluster`. The counterpart of `ankra cluster proxmox bridges`.",
			filters:     []laneCatalogueFilter{catalogueCredentialFilter, catalogueProxmoxNodeFilter},
			listName:    "bridges", listDescription: "Bridges of the node.",
			fields: []laneCatalogueField{
				{name: "name", description: "Bridge or vnet name.", kind: laneCatalogueString},
				{name: "active", description: "Whether the bridge is active.", kind: laneCatalogueBool},
				{name: "cidr", description: "CIDR, when the bridge has one.", kind: laneCatalogueString},
				{name: "comments", description: "Comments.", kind: laneCatalogueString},
				{name: "source", description: "`bridge` for a host Linux bridge, `sdn` for an Ankra-managed SDN vnet.", kind: laneCatalogueString},
			},
			fetch: func(ctx context.Context, apiClient *client.Client, filters map[string]string) (any, error) {
				return apiClient.ListProxmoxBridges(ctx, filters["credential_id"], filters["node"])
			},
		},
		{
			typeSuffix:  "_proxmox_storages",
			description: "The storages of a Proxmox VE node, for `storage` on `ankra_proxmox_cluster`. The counterpart of `ankra cluster proxmox storages`.",
			filters:     []laneCatalogueFilter{catalogueCredentialFilter, catalogueProxmoxNodeFilter},
			listName:    "storages", listDescription: "Storages of the node.",
			fields: []laneCatalogueField{
				{name: "storage", description: "Storage name.", kind: laneCatalogueString},
				{name: "type", description: "Storage type, e.g. `lvmthin`.", kind: laneCatalogueString},
				{name: "content", description: "Content types the storage holds, e.g. `images`.", kind: laneCatalogueStringList},
				{name: "active", description: "Whether the storage is active.", kind: laneCatalogueBool},
				{name: "shared", description: "Whether the storage is shared between nodes.", kind: laneCatalogueBool},
				{name: "available_bytes", description: "Free space in bytes.", kind: laneCatalogueInt64},
				{name: "total_bytes", description: "Total space in bytes.", kind: laneCatalogueInt64},
			},
			fetch: func(ctx context.Context, apiClient *client.Client, filters map[string]string) (any, error) {
				return apiClient.ListProxmoxStorages(ctx, filters["credential_id"], filters["node"])
			},
		},
		{
			typeSuffix:  "_proxmox_templates",
			description: "The VM templates of a Proxmox VE node, for `template` on `ankra_proxmox_cluster`. The counterpart of `ankra cluster proxmox templates`.",
			filters:     []laneCatalogueFilter{catalogueCredentialFilter, catalogueProxmoxNodeFilter},
			listName:    "templates", listDescription: "Templates of the node.",
			fields: []laneCatalogueField{
				{name: "vmid", description: "VM id of the template.", kind: laneCatalogueInt64},
				{name: "name", description: "Template name.", kind: laneCatalogueString},
				{name: "node", description: "Node holding the template.", kind: laneCatalogueString},
			},
			fetch: func(ctx context.Context, apiClient *client.Client, filters map[string]string) (any, error) {
				return apiClient.ListProxmoxTemplates(ctx, filters["credential_id"], filters["node"])
			},
		},
		{
			typeSuffix:  "_morpheus_groups",
			description: "The HPE Morpheus groups behind a credential, for `group_id` on `ankra_morpheus_cluster`. The counterpart of `ankra cluster morpheus groups`.",
			filters:     []laneCatalogueFilter{catalogueCredentialFilter},
			listName:    "groups", listDescription: "Groups.",
			fields: []laneCatalogueField{
				{name: "id", description: "Group id.", kind: laneCatalogueInt64},
				{name: "name", description: "Group name.", kind: laneCatalogueString},
				{name: "location", description: "Location, when set.", kind: laneCatalogueString},
			},
			fetch: func(ctx context.Context, apiClient *client.Client, filters map[string]string) (any, error) {
				return apiClient.ListMorpheusGroups(ctx, filters["credential_id"])
			},
		},
		{
			typeSuffix:  "_morpheus_clouds",
			description: "The HPE Morpheus clouds behind a credential, for `cloud_id` on `ankra_morpheus_cluster`. The counterpart of `ankra cluster morpheus clouds`.",
			filters:     []laneCatalogueFilter{catalogueCredentialFilter},
			listName:    "clouds", listDescription: "Clouds.",
			fields: []laneCatalogueField{
				{name: "id", description: "Cloud id.", kind: laneCatalogueInt64},
				{name: "name", description: "Cloud name.", kind: laneCatalogueString},
				{name: "cloud_type", description: "Cloud type, when reported.", kind: laneCatalogueString},
			},
			fetch: func(ctx context.Context, apiClient *client.Client, filters map[string]string) (any, error) {
				return apiClient.ListMorpheusClouds(ctx, filters["credential_id"])
			},
		},
		{
			typeSuffix:  "_morpheus_plans",
			description: "The HPE Morpheus service plans behind a credential, for the `*_plan_id` arguments of `ankra_morpheus_cluster`. The counterpart of `ankra cluster morpheus plans`.",
			filters:     []laneCatalogueFilter{catalogueCredentialFilter},
			listName:    "plans", listDescription: "Service plans.",
			fields: []laneCatalogueField{
				{name: "id", description: "Plan id.", kind: laneCatalogueInt64},
				{name: "name", description: "Plan name.", kind: laneCatalogueString},
				{name: "code", description: "Plan code, when set.", kind: laneCatalogueString},
				{name: "max_cores", description: "Cores the plan allows, when reported.", kind: laneCatalogueInt64},
				{name: "max_memory_bytes", description: "Memory the plan allows in bytes, when reported.", kind: laneCatalogueInt64},
			},
			fetch: func(ctx context.Context, apiClient *client.Client, filters map[string]string) (any, error) {
				return apiClient.ListMorpheusPlans(ctx, filters["credential_id"])
			},
		},
		{
			typeSuffix:  "_morpheus_layouts",
			description: "The HPE Morpheus instance-type layouts behind a credential, for `layout_id` on `ankra_morpheus_cluster`. The counterpart of `ankra cluster morpheus layouts`.",
			filters:     []laneCatalogueFilter{catalogueCredentialFilter},
			listName:    "layouts", listDescription: "Layouts.",
			fields: []laneCatalogueField{
				{name: "id", description: "Layout id.", kind: laneCatalogueInt64},
				{name: "name", description: "Layout name.", kind: laneCatalogueString},
				{name: "instance_type_id", description: "Id of the instance type the layout belongs to.", kind: laneCatalogueInt64},
				{name: "instance_type_name", description: "Name of the instance type the layout belongs to.", kind: laneCatalogueString},
				{name: "provision_type", description: "Provision type, when reported.", kind: laneCatalogueString},
			},
			fetch: func(ctx context.Context, apiClient *client.Client, filters map[string]string) (any, error) {
				return apiClient.ListMorpheusLayouts(ctx, filters["credential_id"])
			},
		},
		{
			typeSuffix:  "_morpheus_networks",
			description: "The HPE Morpheus networks behind a credential, for `network_id` on `ankra_morpheus_cluster`. The counterpart of `ankra cluster morpheus networks`.",
			filters:     []laneCatalogueFilter{catalogueCredentialFilter},
			listName:    "networks", listDescription: "Networks.",
			fields: []laneCatalogueField{
				{name: "id", description: "Network id.", kind: laneCatalogueInt64},
				{name: "name", description: "Network name.", kind: laneCatalogueString},
				{name: "cidr", description: "CIDR, when reported.", kind: laneCatalogueString},
				{name: "cloud_id", description: "Id of the cloud the network belongs to, when reported.", kind: laneCatalogueInt64},
			},
			fetch: func(ctx context.Context, apiClient *client.Client, filters map[string]string) (any, error) {
				return apiClient.ListMorpheusNetworks(ctx, filters["credential_id"])
			},
		},
		{
			typeSuffix:  "_ankracloud_zones",
			description: "The Ankra Cloud zones a cluster can be deployed in, for `zone` on `ankra_ankracloud_cluster`.",
			filters:     []laneCatalogueFilter{catalogueAnkraCloudCredentialFilter},
			listName:    "zones", listDescription: "Deployable zones.",
			fields: []laneCatalogueField{
				{name: "name", description: "Zone name, e.g. `se-sto1`.", kind: laneCatalogueString},
				{name: "region", description: "Region the zone is in.", kind: laneCatalogueString},
				{name: "display_name", description: "Display name.", kind: laneCatalogueString},
				{name: "country", description: "Country code.", kind: laneCatalogueString},
			},
			fetchPriced: func(ctx context.Context, apiClient *client.Client, filters map[string]string) (any, client.CataloguePricing, error) {
				return apiClient.ListAnkraCloudZones(ctx, filters["credential_id"])
			},
			pricingDescription: "Whether the platform could read every zone's capabilities; when false, a zone may be missing from `zones`.",
		},
		{
			typeSuffix:  "_ankracloud_plans",
			description: "The Ankra Cloud server plans, cheapest first, for the `*_plan` arguments of `ankra_ankracloud_cluster`.",
			filters:     []laneCatalogueFilter{catalogueAnkraCloudCredentialFilter},
			listName:    "plans", listDescription: "Server plans.",
			fields: []laneCatalogueField{
				{name: "name", description: "Plan name.", kind: laneCatalogueString},
				{name: "family", description: "Plan family.", kind: laneCatalogueString},
				{name: "category", description: "Plan family label.", kind: laneCatalogueString},
				{name: "cores", description: "vCPU cores.", kind: laneCatalogueInt64},
				{name: "memory_mebibytes", description: "Memory in MiB.", kind: laneCatalogueInt64},
				{name: "storage_gibibytes", description: "Root storage in GiB.", kind: laneCatalogueInt64},
				{name: "price_monthly_cents", description: "Monthly price in cents; 0 when the plan carries no price.", kind: laneCatalogueInt64},
				{name: "available", description: "Whether the plan can be ordered.", kind: laneCatalogueBool},
			},
			fetchPriced: func(ctx context.Context, apiClient *client.Client, filters map[string]string) (any, client.CataloguePricing, error) {
				return apiClient.ListAnkraCloudPlans(ctx, filters["credential_id"])
			},
			pricingDescription: "Whether every plan carries a monthly price; when false, some `price_monthly_cents` are 0.",
		},
	}
	factories := make([]func() datasource.DataSource, 0, len(definitions))
	for _, definition := range definitions {
		factories = append(factories, laneCatalogueDataSourceFactory(definition))
	}
	return factories
}
