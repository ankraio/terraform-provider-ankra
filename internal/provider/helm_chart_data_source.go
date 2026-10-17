// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ datasource.DataSource              = (*helmChartDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*helmChartDataSource)(nil)
)

type helmChartDataSource struct {
	configuredDataSourceClient
}

type helmChartDataSourceModel struct {
	ID            types.String `tfsdk:"id"`
	ChartName     types.String `tfsdk:"chart_name"`
	RegistryURL   types.String `tfsdk:"registry_url"`
	RegistryName  types.String `tfsdk:"registry_name"`
	Versions      types.List   `tfsdk:"versions"`
	LatestVersion types.String `tfsdk:"latest_version"`
}

// NewHelmChartDataSource returns the ankra_helm_chart data source, the
// counterpart of `ankra charts info`.
func NewHelmChartDataSource() datasource.DataSource {
	return &helmChartDataSource{}
}

func (chartDataSource *helmChartDataSource) Metadata(_ context.Context, request datasource.MetadataRequest, response *datasource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_helm_chart"
}

func (chartDataSource *helmChartDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, response *datasource.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: "Looks a chart up in the organisation's catalogue to pin an `ankra_cluster` addon's `chart_version`. " +
			"The counterpart of `ankra charts info`.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "`<registry_url>/<chart_name>`.",
				Computed:            true,
			},
			"chart_name": schema.StringAttribute{
				MarkdownDescription: "Chart name.",
				Required:            true,
			},
			"registry_url": schema.StringAttribute{
				MarkdownDescription: "URL of the registry that serves the chart; a trailing slash is ignored.",
				Required:            true,
			},
			"registry_name": schema.StringAttribute{
				MarkdownDescription: "Name of that registry in the organisation.",
				Computed:            true,
			},
			"versions": schema.ListAttribute{
				MarkdownDescription: "Indexed versions, newest first, prereleases after every release.",
				Computed:            true,
				ElementType:         types.StringType,
			},
			"latest_version": schema.StringAttribute{
				MarkdownDescription: "Newest version that is not a prerelease, or null when there is none.",
				Computed:            true,
			},
		},
	}
}

func (chartDataSource *helmChartDataSource) Read(ctx context.Context, request datasource.ReadRequest, response *datasource.ReadResponse) {
	apiClient := chartDataSource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var config helmChartDataSourceModel
	response.Diagnostics.Append(request.Config.Get(ctx, &config)...)
	if response.Diagnostics.HasError() {
		return
	}

	chart, err := apiClient.GetHelmChart(ctx, config.ChartName.ValueString(), config.RegistryURL.ValueString())
	if err != nil {
		response.Diagnostics.AddError("Unable to read Helm chart", err.Error())
		return
	}

	config.ID = types.StringValue(strings.TrimSuffix(config.RegistryURL.ValueString(), "/") + "/" + config.ChartName.ValueString())
	config.RegistryName = types.StringValue(chart.RepositoryName)
	versions := chart.Versions
	if versions == nil {
		versions = []string{}
	}
	config.Versions = listFromStrings(versions)
	config.LatestVersion = types.StringNull()
	for _, version := range versions {
		if !strings.Contains(version, "-") {
			config.LatestVersion = types.StringValue(version)
			break
		}
	}
	response.Diagnostics.Append(response.State.Set(ctx, &config)...)
}
