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
	_ datasource.DataSource              = (*clusterMetricsSourceDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*clusterMetricsSourceDataSource)(nil)
)

type clusterMetricsSourceDataSource struct {
	configuredDataSourceClient
}

type clusterMetricsSourceDataSourceModel struct {
	ID        types.String               `tfsdk:"id"`
	ClusterID types.String               `tfsdk:"cluster_id"`
	Metrics   *clusterMetricsSourceModel `tfsdk:"metrics"`
	Logs      *clusterLogSourceModel     `tfsdk:"logs"`
}

type clusterMetricsSourceModel struct {
	Provider       types.String `tfsdk:"provider_name"`
	Endpoint       types.String `tfsdk:"endpoint"`
	State          types.String `tfsdk:"state"`
	Flavor         types.String `tfsdk:"flavor"`
	CredentialName types.String `tfsdk:"credential_name"`
}

type clusterLogSourceModel struct {
	Provider       types.String `tfsdk:"provider_name"`
	Endpoint       types.String `tfsdk:"endpoint"`
	State          types.String `tfsdk:"state"`
	VerifySSL      types.Bool   `tfsdk:"verify_ssl"`
	IndexPattern   types.String `tfsdk:"index_pattern"`
	CredentialName types.String `tfsdk:"credential_name"`
}

// NewClusterMetricsSourceDataSource returns the ankra_cluster_metrics_source
// data source, the counterpart of `ankra cluster metrics source`.
func NewClusterMetricsSourceDataSource() datasource.DataSource {
	return &clusterMetricsSourceDataSource{}
}

func (source *clusterMetricsSourceDataSource) Metadata(_ context.Context, request datasource.MetadataRequest, response *datasource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_cluster_metrics_source"
}

func (source *clusterMetricsSourceDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, response *datasource.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: "A cluster's metrics and log sources (`ankra cluster metrics source`): the backends Ankra reads the " +
			"cluster's metrics and log history from, and the credential each resolves to, never its secret. Set them with the " +
			"cluster's `spec.prometheus_metrics` and `spec.log_source` and an `ankra_datasource_credential`. Reading needs " +
			"`clusters.read` on the cluster.",
		Attributes: map[string]schema.Attribute{
			"id": computedStringAttribute("The cluster id."),
			"cluster_id": schema.StringAttribute{
				MarkdownDescription: "Identifier of the cluster.",
				Required:            true,
			},
			"metrics": schema.SingleNestedAttribute{
				MarkdownDescription: "The metrics source; null when the cluster has none.",
				Computed:            true,
				Attributes: map[string]schema.Attribute{
					"provider_name":   computedStringAttribute("Metrics backend, such as `prometheus`."),
					"endpoint":        computedStringAttribute("Address of the backend, without any user or password."),
					"state":           computedStringAttribute("State of the source."),
					"flavor":          computedStringAttribute("Backend flavour, such as `mimir` or `thanos`."),
					"credential_name": computedStringAttribute("Name of the credential the source resolves to."),
				},
			},
			"logs": schema.SingleNestedAttribute{
				MarkdownDescription: "The log source; null when the cluster has none.",
				Computed:            true,
				Attributes: map[string]schema.Attribute{
					"provider_name":   computedStringAttribute("Log backend, such as `loki` or `opensearch`."),
					"endpoint":        computedStringAttribute("Address of the backend, without any user or password."),
					"state":           computedStringAttribute("State of the source."),
					"verify_ssl":      computedBoolAttribute("Whether the backend's TLS certificate is verified."),
					"index_pattern":   computedStringAttribute("Index pattern logs are read from."),
					"credential_name": computedStringAttribute("Name of the credential the source resolves to."),
				},
			},
		},
	}
}

func (source *clusterMetricsSourceDataSource) Read(ctx context.Context, request datasource.ReadRequest, response *datasource.ReadResponse) {
	apiClient := source.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var model clusterMetricsSourceDataSourceModel
	response.Diagnostics.Append(request.Config.Get(ctx, &model)...)
	if response.Diagnostics.HasError() {
		return
	}
	clusterID := model.ClusterID.ValueString()
	metrics, metricsError := apiClient.GetClusterMetricsSource(ctx, clusterID)
	switch {
	case metricsError == nil:
		model.Metrics = &clusterMetricsSourceModel{
			Provider: types.StringValue(metrics.Provider), Endpoint: types.StringValue(metrics.Endpoint), State: types.StringValue(metrics.State),
			Flavor: types.StringPointerValue(metrics.Flavor), CredentialName: dataSourceCredentialName(metrics.CredentialName, metrics.Credential),
		}
	case !client.IsNotFound(metricsError) || clusterSettingsScopeError(metricsError, clusterID) != nil:
		response.Diagnostics.AddError("Unable to read the cluster's metrics source", clusterSettingsErrorText(metricsError, clusterID))
		return
	}
	logs, logsError := apiClient.GetClusterLogSource(ctx, clusterID)
	switch {
	case logsError == nil:
		model.Logs = &clusterLogSourceModel{
			Provider: types.StringValue(logs.Provider), Endpoint: types.StringValue(logs.Endpoint), State: types.StringValue(logs.State),
			VerifySSL: types.BoolValue(logs.VerifySSL), IndexPattern: types.StringPointerValue(logs.IndexPattern),
			CredentialName: dataSourceCredentialName(logs.CredentialName, logs.Credential),
		}
	case !client.IsNotFound(logsError) || clusterSettingsScopeError(logsError, clusterID) != nil:
		response.Diagnostics.AddError("Unable to read the cluster's log source", clusterSettingsErrorText(logsError, clusterID))
		return
	}
	model.ID = model.ClusterID
	response.Diagnostics.Append(response.State.Set(ctx, &model)...)
}

// dataSourceCredentialName is the credential a source resolves to: the
// resolved credential's name, else the name the source stores.
func dataSourceCredentialName(storedName *string, credential *client.ClusterDataSourceCredential) types.String {
	if credential != nil && credential.Name != "" {
		return types.StringValue(credential.Name)
	}
	return types.StringPointerValue(storedName)
}
