// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ datasource.DataSource              = (*alertDestinationsDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*alertDestinationsDataSource)(nil)
	_ datasource.DataSource              = (*alertRoutesDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*alertRoutesDataSource)(nil)
)

type alertDestinationsDataSource struct {
	configuredDataSourceClient
}

type alertDestinationItemModel struct {
	ID              types.String `tfsdk:"id"`
	Name            types.String `tfsdk:"name"`
	IntegrationType types.String `tfsdk:"integration_type"`
	ChannelID       types.String `tfsdk:"channel_id"`
	ChannelName     types.String `tfsdk:"channel_name"`
	TeamsTenantID   types.String `tfsdk:"teams_tenant_id"`
	Description     types.String `tfsdk:"description"`
	Enabled         types.Bool   `tfsdk:"enabled"`
}

type alertDestinationsDataSourceModel struct {
	ID           types.String                `tfsdk:"id"`
	Destinations []alertDestinationItemModel `tfsdk:"destinations"`
}

// NewAlertDestinationsDataSource returns the ankra_alert_destinations data
// source, the counterpart of `ankra alerts destinations list`.
func NewAlertDestinationsDataSource() datasource.DataSource {
	return &alertDestinationsDataSource{}
}

func (destinationsDataSource *alertDestinationsDataSource) Metadata(_ context.Context, request datasource.MetadataRequest, response *datasource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_alert_destinations"
}

func (destinationsDataSource *alertDestinationsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, response *datasource.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: "Lists the organisation's alert destinations, every page of them. Webhook URLs are left out because they carry secrets. " +
			"The counterpart of `ankra alerts destinations list`.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Placeholder identifier for the data source.",
				Computed:            true,
			},
			"destinations": schema.ListNestedAttribute{
				MarkdownDescription: "Destinations, newest first.",
				Computed:            true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id":               schema.StringAttribute{MarkdownDescription: "Identifier of the destination.", Computed: true},
						"name":             schema.StringAttribute{MarkdownDescription: "Destination name.", Computed: true},
						"integration_type": schema.StringAttribute{MarkdownDescription: "Receiver kind.", Computed: true},
						"channel_id":       schema.StringAttribute{MarkdownDescription: "Channel the Ankra bot posts to; null for a webhook destination.", Computed: true},
						"channel_name":     schema.StringAttribute{MarkdownDescription: "Display name of the channel.", Computed: true},
						"teams_tenant_id":  schema.StringAttribute{MarkdownDescription: "Teams tenant of a Teams channel destination.", Computed: true},
						"description":      schema.StringAttribute{MarkdownDescription: "Free-text description.", Computed: true},
						"enabled":          schema.BoolAttribute{MarkdownDescription: "Whether alerts are delivered.", Computed: true},
					},
				},
			},
		},
	}
}

func (destinationsDataSource *alertDestinationsDataSource) Read(ctx context.Context, _ datasource.ReadRequest, response *datasource.ReadResponse) {
	apiClient := destinationsDataSource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}

	destinations, err := apiClient.ListAlertDestinations(ctx)
	if err != nil {
		response.Diagnostics.AddError("Unable to list alert destinations", err.Error())
		return
	}

	state := alertDestinationsDataSourceModel{
		ID:           types.StringValue("ankra_alert_destinations"),
		Destinations: make([]alertDestinationItemModel, 0, len(destinations)),
	}
	for _, destination := range destinations {
		state.Destinations = append(state.Destinations, alertDestinationItemModel{
			ID:              types.StringValue(destination.ID),
			Name:            types.StringValue(destination.Name),
			IntegrationType: types.StringValue(destination.IntegrationType),
			ChannelID:       optionalString(alertDNSStringValue(destination.ChannelID)),
			ChannelName:     optionalString(alertDNSStringValue(destination.ChannelName)),
			TeamsTenantID:   optionalString(alertDNSStringValue(destination.TeamsTenantID)),
			Description:     optionalString(alertDNSStringValue(destination.Description)),
			Enabled:         types.BoolValue(destination.Enabled),
		})
	}
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
}

type alertRoutesDataSource struct {
	configuredDataSourceClient
}

type alertRoutesDataSourceModel struct {
	ID     types.String      `tfsdk:"id"`
	Routes []alertRouteModel `tfsdk:"routes"`
}

// NewAlertRoutesDataSource returns the ankra_alert_routes data source, the
// counterpart of `ankra alerts routes list`.
func NewAlertRoutesDataSource() datasource.DataSource {
	return &alertRoutesDataSource{}
}

func (routesDataSource *alertRoutesDataSource) Metadata(_ context.Context, request datasource.MetadataRequest, response *datasource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_alert_routes"
}

func (routesDataSource *alertRoutesDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, response *datasource.SchemaResponse) {
	computedString := func(description string) schema.Attribute {
		return schema.StringAttribute{MarkdownDescription: description, Computed: true}
	}
	computedBool := func(description string) schema.Attribute {
		return schema.BoolAttribute{MarkdownDescription: description, Computed: true}
	}
	response.Schema = schema.Schema{
		MarkdownDescription: "Lists the organisation's notification routing rules in evaluation order. The counterpart of `ankra alerts routes list`.",
		Attributes: map[string]schema.Attribute{
			"id": computedString("Placeholder identifier for the data source."),
			"routes": schema.ListNestedAttribute{
				MarkdownDescription: "Routes, by ascending priority.",
				Computed:            true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id":                computedString("Identifier of the route."),
						"destination_id":    computedString("Destination id, or `push:<audience>`."),
						"destination_type":  computedString("`webhook` or `push`."),
						"kinds":             schema.ListAttribute{MarkdownDescription: "Kinds matched; null matches every kind.", Computed: true, ElementType: types.StringType},
						"kinds_negated":     computedBool("Whether the route matches every kind except `kinds`."),
						"kind":              computedString("Legacy single-kind filter."),
						"severity":          computedString("Severity matched."),
						"cluster_id":        computedString("Cluster matched."),
						"source_id":         computedString("Source matched."),
						"application_id":    computedString("Application matched."),
						"reference_pattern": computedString("Reference glob matched."),
						"priority":          schema.Int64Attribute{MarkdownDescription: "Evaluation order.", Computed: true},
						"stop_on_match":     computedBool("Whether a match ends the walk."),
						"mode":              computedString("`include` or `exclude`."),
						"enabled":           computedBool("Whether the route is evaluated."),
					},
				},
			},
		},
	}
}

func (routesDataSource *alertRoutesDataSource) Read(ctx context.Context, _ datasource.ReadRequest, response *datasource.ReadResponse) {
	apiClient := routesDataSource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}

	routes, err := apiClient.ListAlertRoutes(ctx)
	if err != nil {
		response.Diagnostics.AddError("Unable to list alert routes", err.Error())
		return
	}

	state := alertRoutesDataSourceModel{
		ID:     types.StringValue("ankra_alert_routes"),
		Routes: make([]alertRouteModel, 0, len(routes)),
	}
	for index := range routes {
		var model alertRouteModel
		alertRouteApply(&routes[index], &model)
		state.Routes = append(state.Routes, model)
	}
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
}
