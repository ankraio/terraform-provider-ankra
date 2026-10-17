// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"ankra.io/terraform-provider-ankra/internal/client"
)

// providerTokenDetail is the missing-token detail for resources and data
// sources added after 0.2, which take the token from the provider only and
// carry no deprecated per-resource ankra_token.
const providerTokenDetail = "Set an API token on the provider block or the ANKRA_TOKEN environment variable."

// configuredClient holds the API client the provider hands to a resource or
// data source. Embed it to get Configure for free; call requireClient at the
// top of every CRUD method.
type configuredClient struct {
	client *client.Client
}

func (holder *configuredClient) Configure(_ context.Context, request resource.ConfigureRequest, response *resource.ConfigureResponse) {
	holder.client = clientFromProviderData(request.ProviderData, &response.Diagnostics)
}

// requireClient returns the configured client, or records "Missing API token"
// and returns nil so the caller sends no request.
func (holder *configuredClient) requireClient(diagnostics *diag.Diagnostics) *client.Client {
	if holder.client == nil || holder.client.Token == "" {
		diagnostics.AddError("Missing API token", providerTokenDetail)
		return nil
	}
	return holder.client
}

// configuredDataSourceClient is configuredClient for data sources, whose
// Configure takes the data source request types.
type configuredDataSourceClient struct {
	configuredClient
}

func (holder *configuredDataSourceClient) Configure(_ context.Context, request datasource.ConfigureRequest, response *datasource.ConfigureResponse) {
	holder.client = clientFromProviderData(request.ProviderData, &response.Diagnostics)
}

func clientFromProviderData(providerData any, diagnostics *diag.Diagnostics) *client.Client {
	if providerData == nil {
		return nil
	}
	apiClient, ok := providerData.(*client.Client)
	if !ok {
		diagnostics.AddError(
			"Unexpected provider data type",
			fmt.Sprintf("Expected *client.Client, got %T. This is a bug in the provider.", providerData),
		)
		return nil
	}
	return apiClient
}

// stringPointer maps an optional string attribute to the pointer a JSON
// request field expects: null and unknown become nil.
func stringPointer(value types.String) *string {
	if value.IsNull() || value.IsUnknown() {
		return nil
	}
	text := value.ValueString()
	return &text
}

// removeWhenNotFound drops a resource from state when the platform reports it
// gone, which plans its recreation. It reports whether it did so.
func removeWhenNotFound(ctx context.Context, err error, response *resource.ReadResponse) bool {
	if !client.IsNotFound(err) {
		return false
	}
	response.State.RemoveResource(ctx)
	return true
}
