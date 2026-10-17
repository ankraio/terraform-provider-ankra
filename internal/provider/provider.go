// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"os"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"ankra.io/terraform-provider-ankra/internal/client"
)

const (
	tokenEnvironmentVariable          = "ANKRA_TOKEN"
	baseURLEnvironmentVariable        = "ANKRA_BASE_URL"
	organisationIDEnvironmentVariable = "ANKRA_ORGANISATION_ID"
)

// Ensure ankraProvider satisfies the provider.Provider interface.
var _ provider.Provider = (*ankraProvider)(nil)

type ankraProvider struct {
	version string
}

type ankraProviderModel struct {
	Token          types.String `tfsdk:"token"`
	BaseURL        types.String `tfsdk:"base_url"`
	OrganisationID types.String `tfsdk:"organisation_id"`
}

// New returns a function that constructs the Ankra provider for the given
// build version.
func New(version string) func() provider.Provider {
	return func() provider.Provider {
		return &ankraProvider{version: version}
	}
}

func (providerInstance *ankraProvider) Metadata(_ context.Context, _ provider.MetadataRequest, response *provider.MetadataResponse) {
	response.TypeName = "ankra"
	response.Version = providerInstance.version
}

func (providerInstance *ankraProvider) Schema(_ context.Context, _ provider.SchemaRequest, response *provider.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: "The Ankra provider manages clusters and the organisation resources around them on the Ankra platform.",
		Attributes: map[string]schema.Attribute{
			"token": schema.StringAttribute{
				MarkdownDescription: "Ankra API token. May also be set with the `ANKRA_TOKEN` environment variable.",
				Optional:            true,
				Sensitive:           true,
			},
			"base_url": schema.StringAttribute{
				MarkdownDescription: "Base URL of the Ankra platform API. May also be set with the `ANKRA_BASE_URL` environment variable. Defaults to `" + client.DefaultBaseURL + "`.",
				Optional:            true,
			},
			"organisation_id": schema.StringAttribute{
				MarkdownDescription: "Organisation to act in, when the token's user belongs to more than one. Sent as the `" + client.OrganisationHeader + "` header, the same override as the CLI's `--org` flag. May also be set with the `ANKRA_ORGANISATION_ID` environment variable. Defaults to the organisation the token is bound to.",
				Optional:            true,
			},
		},
	}
}

func (providerInstance *ankraProvider) Configure(ctx context.Context, request provider.ConfigureRequest, response *provider.ConfigureResponse) {
	var config ankraProviderModel
	response.Diagnostics.Append(request.Config.Get(ctx, &config)...)
	if response.Diagnostics.HasError() {
		return
	}

	token := strings.TrimSpace(os.Getenv(tokenEnvironmentVariable))
	if !config.Token.IsNull() && !config.Token.IsUnknown() {
		token = strings.TrimSpace(config.Token.ValueString())
	}

	baseURL := strings.TrimSpace(os.Getenv(baseURLEnvironmentVariable))
	if !config.BaseURL.IsNull() && !config.BaseURL.IsUnknown() {
		baseURL = strings.TrimSpace(config.BaseURL.ValueString())
	}

	// No provider token is not an error here. The deprecated per-resource
	// ankra_token still supplies one, and a 0.1.x configuration sets only that
	// (no provider block at all), so failing at configure time breaks every
	// such configuration on upgrade. Each resource and the data source report
	// "Missing API token" when no token reaches them, and never send a request
	// without one. A token still unknown at configure time resolves before
	// apply the same way.
	organisationID := strings.TrimSpace(os.Getenv(organisationIDEnvironmentVariable))
	if !config.OrganisationID.IsNull() && !config.OrganisationID.IsUnknown() {
		organisationID = strings.TrimSpace(config.OrganisationID.ValueString())
	}

	userAgent := "terraform-provider-ankra/" + providerInstance.version
	apiClient := client.NewClient(baseURL, token, userAgent)
	apiClient.OrganisationID = organisationID

	response.ResourceData = apiClient
	response.DataSourceData = apiClient
}

func (providerInstance *ankraProvider) Resources(_ context.Context) []func() resource.Resource {
	return concatenate(
		clusterResources(),
		credentialResources(),
		configurationResources(),
		clusterLaneResources(),
		clusterSettingResources(),
		alertAndDNSResources(),
		organisationResources(),
		applicationResources(),
		catalogueAndStorageResources(),
		platformServiceResources(),
	)
}

func (providerInstance *ankraProvider) DataSources(_ context.Context) []func() datasource.DataSource {
	return concatenate(
		clusterDataSources(),
		credentialDataSources(),
		configurationDataSources(),
		clusterLaneDataSources(),
		clusterSettingDataSources(),
		alertAndDNSDataSources(),
		organisationDataSources(),
		applicationDataSources(),
		catalogueAndStorageDataSources(),
		platformServiceDataSources(),
	)
}

// concatenate joins the per-domain factory lists. Each domain registers its
// resources and data sources in its own register_<domain>.go file, so domains
// can be added side by side without touching this file.
func concatenate[Factory any](groups ...[]Factory) []Factory {
	var joined []Factory
	for _, group := range groups {
		joined = append(joined, group...)
	}
	return joined
}
