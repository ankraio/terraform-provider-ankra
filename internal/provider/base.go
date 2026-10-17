// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/attr"
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

// boolPointer is stringPointer for booleans.
func boolPointer(value types.Bool) *bool {
	if value.IsNull() || value.IsUnknown() {
		return nil
	}
	flag := value.ValueBool()
	return &flag
}

// int64Pointer is stringPointer for integers.
func int64Pointer(value types.Int64) *int64 {
	if value.IsNull() || value.IsUnknown() {
		return nil
	}
	number := value.ValueInt64()
	return &number
}

// optionalString maps an API string that may be empty to a state value:
// empty becomes null, so an unset optional attribute does not plan a diff.
func optionalString(value string) types.String {
	if value == "" {
		return types.StringNull()
	}
	return types.StringValue(value)
}

// stringsFromList reads a list-of-strings attribute; null and unknown read as
// nil, and unknown elements are skipped.
func stringsFromList(ctx context.Context, list types.List, diagnostics *diag.Diagnostics) []string {
	if list.IsNull() || list.IsUnknown() {
		return nil
	}
	var elements []types.String
	diagnostics.Append(list.ElementsAs(ctx, &elements, false)...)
	values := make([]string, 0, len(elements))
	for _, element := range elements {
		if element.IsNull() || element.IsUnknown() {
			continue
		}
		values = append(values, element.ValueString())
	}
	return values
}

// listFromStrings is the inverse of stringsFromList. A nil slice is a null
// list, so an optional attribute the API leaves out stays unset.
func listFromStrings(values []string) types.List {
	if values == nil {
		return types.ListNull(types.StringType)
	}
	elements := make([]attr.Value, 0, len(values))
	for _, value := range values {
		elements = append(elements, types.StringValue(value))
	}
	return types.ListValueMust(types.StringType, elements)
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
