// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"ankra.io/terraform-provider-ankra/internal/client"
)

var (
	_ datasource.DataSource              = (*clustersDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*clustersDataSource)(nil)
)

type clustersDataSource struct {
	client *client.Client
}

type clusterItemModel struct {
	ID   types.String `tfsdk:"id"`
	Name types.String `tfsdk:"name"`
}

type clustersDataSourceModel struct {
	ID         types.String       `tfsdk:"id"`
	AnkraToken types.String       `tfsdk:"ankra_token"`
	Clusters   []clusterItemModel `tfsdk:"clusters"`
}

// NewClustersDataSource returns a new ankra_clusters data source.
func NewClustersDataSource() datasource.DataSource {
	return &clustersDataSource{}
}

func (clustersDataSourceInstance *clustersDataSource) Metadata(_ context.Context, request datasource.MetadataRequest, response *datasource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_clusters"
}

func (clustersDataSourceInstance *clustersDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, response *datasource.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: "Lists the clusters visible to the authenticated Ankra token.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Placeholder identifier for the data source.",
				Computed:            true,
			},
			"ankra_token": schema.StringAttribute{
				MarkdownDescription: "Deprecated per-data-source API token. " + tokenDeprecationMessage,
				Optional:            true,
				Sensitive:           true,
				DeprecationMessage:  tokenDeprecationMessage,
			},
			"clusters": schema.ListNestedAttribute{
				MarkdownDescription: "Clusters returned by the platform.",
				Computed:            true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id": schema.StringAttribute{
							MarkdownDescription: "Identifier of the cluster.",
							Computed:            true,
						},
						"name": schema.StringAttribute{
							MarkdownDescription: "Name of the cluster.",
							Computed:            true,
						},
					},
				},
			},
		},
	}
}

func (clustersDataSourceInstance *clustersDataSource) Configure(_ context.Context, request datasource.ConfigureRequest, response *datasource.ConfigureResponse) {
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
	clustersDataSourceInstance.client = apiClient
}

func (clustersDataSourceInstance *clustersDataSource) Read(ctx context.Context, request datasource.ReadRequest, response *datasource.ReadResponse) {
	var config clustersDataSourceModel
	response.Diagnostics.Append(request.Config.Get(ctx, &config)...)
	if response.Diagnostics.HasError() {
		return
	}

	apiClient := clustersDataSourceInstance.client
	if !config.AnkraToken.IsNull() && !config.AnkraToken.IsUnknown() && config.AnkraToken.ValueString() != "" {
		override := *apiClient
		override.Token = config.AnkraToken.ValueString()
		apiClient = &override
	}
	if apiClient.Token == "" {
		response.Diagnostics.AddError("Missing API token", missingTokenDetail)
		return
	}

	clusters, err := apiClient.ListClusters(ctx)
	if err != nil {
		response.Diagnostics.AddError("Unable to list clusters", err.Error())
		return
	}

	config.Clusters = make([]clusterItemModel, 0, len(clusters))
	for _, cluster := range clusters {
		config.Clusters = append(config.Clusters, clusterItemModel{
			ID:   types.StringValue(cluster.ID),
			Name: types.StringValue(cluster.Name),
		})
	}
	config.ID = types.StringValue("ankra_clusters")

	response.Diagnostics.Append(response.State.Set(ctx, &config)...)
}
