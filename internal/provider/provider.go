// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"ankra.io/terraform-provider-ankra/internal/client"
)

const (
	tokenEnvironmentVariable          = "ANKRA_TOKEN"
	baseURLEnvironmentVariable        = "ANKRA_BASE_URL"
	organisationIDEnvironmentVariable = "ANKRA_ORG"
	projectIDEnvironmentVariable      = "ANKRA_PROJECT_ID"
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
	ProjectID      types.String `tfsdk:"project_id"`
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
				MarkdownDescription: "ID (UUID) of the organisation to act in, when the token's user belongs to more than one. Sent as the `" + client.OrganisationHeader + "` header, the same override as the CLI's `--org` flag. May also be set with the `ANKRA_ORG` environment variable, the variable the CLI reads for the same override (an ID here, not a slug). Defaults to the organisation the token is bound to.",
				Optional:            true,
			},
			"project_id": schema.StringAttribute{
				MarkdownDescription: "Project to act in, for organisations that use projects: its ID (UUID) or its slug. Sent as the `" + client.ProjectHeader + "` header. May also be set with the `ANKRA_PROJECT_ID` environment variable. " +
					"A slug is resolved to the project's ID once, from the organisation's project list, before the first request; a slug that matches no project the token can see fails every resource and data source rather than falling back to another project. " +
					"Set it whenever the organisation has projects: without it the platform scopes every request to the project the token's user last selected in the portal, so switching project there would change what Terraform sees.",
				Optional: true,
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

	organisationID, organisationKnown := scopeIdentifier(config.OrganisationID, organisationIDEnvironmentVariable, "organisation_id", false, &response.Diagnostics)
	projectReference, projectKnown := scopeIdentifier(config.ProjectID, projectIDEnvironmentVariable, "project_id", true, &response.Diagnostics)
	if response.Diagnostics.HasError() {
		return
	}

	// No provider token is not an error here. The deprecated per-resource
	// ankra_token still supplies one, and a 0.1.x configuration sets only that
	// (no provider block at all), so failing at configure time breaks every
	// such configuration on upgrade. Each resource and the data source report
	// "Missing API token" when no token reaches them, and never send a request
	// without one. A token still unknown at configure time resolves before
	// apply the same way.
	userAgent := "terraform-provider-ankra/" + providerInstance.version
	apiClient := client.NewClient(baseURL, token, userAgent)
	apiClient.OrganisationID = organisationID
	applyProjectReference(apiClient, projectReference)
	switch {
	case !organisationKnown:
		apiClient.UnresolvedScope = "organisation_id"
	case !projectKnown:
		apiClient.UnresolvedScope = "project_id"
	}

	response.ResourceData = apiClient
	response.DataSourceData = apiClient
}

var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// projectSlugPattern is the platform's project slug rule.
var projectSlugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

// scopeIdentifier resolves an organisation or project override from the
// configuration, falling back to its environment variable. It reports false
// when the configured value is not known yet: the caller must then refuse to
// send requests, because leaving the header off silently scopes them to a
// different organisation or project. The organisation must be an ID; a
// project may also be a slug (allowSlug), which the client resolves to its ID
// before the first request. Anything else is rejected at configure time
// instead of failing every request.
func scopeIdentifier(configured types.String, environmentVariable, attributeName string, allowSlug bool, diagnostics *diag.Diagnostics) (string, bool) {
	if configured.IsUnknown() {
		return "", false
	}
	value := strings.TrimSpace(os.Getenv(environmentVariable))
	if !configured.IsNull() {
		value = strings.TrimSpace(configured.ValueString())
	}
	switch {
	case value == "" || uuidPattern.MatchString(value):
	case allowSlug && projectSlugPattern.MatchString(value):
	case allowSlug:
		diagnostics.AddAttributeError(path.Root(attributeName), "Invalid "+attributeName,
			fmt.Sprintf("%q is neither a project ID nor a project slug (lowercase letters, digits and \"-\", starting with a letter or digit, at most 63 characters). "+
				"Set the project's ID or slug as the portal shows it.", value))
	default:
		diagnostics.AddAttributeError(path.Root(attributeName), "Invalid "+attributeName,
			fmt.Sprintf("%q is not an ID. Set the %s UUID (`ankra org list -o json`), not a name or slug.", value, strings.TrimSuffix(attributeName, "_id")))
	}
	return value, true
}

// applyProjectReference scopes the client to a project given by ID or slug.
// A slug is held until the client resolves it, so it never reaches the wire.
func applyProjectReference(apiClient *client.Client, reference string) {
	if uuidPattern.MatchString(reference) {
		apiClient.ProjectID = reference
		return
	}
	apiClient.ProjectSlug = reference
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
