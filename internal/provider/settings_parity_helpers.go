// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// optionalStringFromPointer maps an API string that may be null or empty
// to state: both become null, so an unset optional attribute plans no diff.
func optionalStringFromPointer(value *string) types.String {
	if value == nil || *value == "" {
		return types.StringNull()
	}
	return types.StringValue(*value)
}

// stringMapFromAny renders an API object of scalar values as a map of
// strings: strings as they are, anything else as its JSON text. An empty
// object is null while the state holds null.
func stringMapFromAny(stateValue types.Map, values map[string]any) types.Map {
	if len(values) == 0 && stateValue.IsNull() {
		return stateValue
	}
	elements := make(map[string]attr.Value, len(values))
	for key, value := range values {
		switch typed := value.(type) {
		case string:
			elements[key] = types.StringValue(typed)
		default:
			encoded, encodeError := json.Marshal(typed)
			if encodeError != nil {
				elements[key] = types.StringValue(fmt.Sprint(typed))
				continue
			}
			elements[key] = types.StringValue(string(encoded))
		}
	}
	return types.MapValueMust(types.StringType, elements)
}

// writeOnlyCredentialMap reads the write-only credential_wo map, which only
// the configuration carries.
func writeOnlyCredentialMap(ctx context.Context, config tfsdk.Config, diagnostics *diag.Diagnostics) map[string]string {
	var value types.Map
	diagnostics.Append(config.GetAttribute(ctx, path.Root("credential_wo"), &value)...)
	return applicationStringMap(ctx, value, diagnostics)
}

// writeOnlyString reads a write-only string attribute, which only the
// configuration carries.
func writeOnlyString(ctx context.Context, config tfsdk.Config, attribute string, diagnostics *diag.Diagnostics) string {
	var value types.String
	diagnostics.Append(config.GetAttribute(ctx, path.Root(attribute), &value)...)
	return value.ValueString()
}
