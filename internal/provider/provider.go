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
	tokenEnvironmentVariable   = "ANKRA_TOKEN"
	baseURLEnvironmentVariable = "ANKRA_BASE_URL"
)

// Ensure ankraProvider satisfies the provider.Provider interface.
var _ provider.Provider = (*ankraProvider)(nil)

type ankraProvider struct {
	version string
}

type ankraProviderModel struct {
	Token   types.String `tfsdk:"token"`
	BaseURL types.String `tfsdk:"base_url"`
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
		MarkdownDescription: "The Ankra provider manages clusters on the Ankra platform.",
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

	userAgent := "terraform-provider-ankra/" + providerInstance.version
	apiClient := client.NewClient(baseURL, token, userAgent)

	response.ResourceData = apiClient
	response.DataSourceData = apiClient
}

func (providerInstance *ankraProvider) Resources(_ context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		NewClusterResource,
		NewHetznerClusterResource,
	}
}

func (providerInstance *ankraProvider) DataSources(_ context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{
		NewClustersDataSource,
	}
}
