// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"encoding/json"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
)

// costConfiguredAttributesKey is the private state key under which an
// organisation-wide singleton records which of its attributes the
// configuration set, so destroy resets those and nothing someone else set.
const costConfiguredAttributesKey = "configured_attributes"

// costPrivateWriter is the private state of a Create or Update response.
type costPrivateWriter interface {
	SetKey(ctx context.Context, key string, value []byte) diag.Diagnostics
}

// costPrivateReader is the private state of a Delete request.
type costPrivateReader interface {
	GetKey(ctx context.Context, key string) ([]byte, diag.Diagnostics)
}

// costRecordConfiguredAttributes records which of names are set in config.
func costRecordConfiguredAttributes(ctx context.Context, config tfsdk.Config, names []string, private costPrivateWriter, diagnostics *diag.Diagnostics) {
	configured := []string{}
	for _, name := range names {
		var value attr.Value
		diagnostics.Append(config.GetAttribute(ctx, path.Root(name), &value)...)
		if value != nil && !value.IsNull() {
			configured = append(configured, name)
		}
	}
	encoded, err := json.Marshal(configured)
	if err != nil {
		diagnostics.AddError("Unable to record configured attributes", err.Error())
		return
	}
	diagnostics.Append(private.SetKey(ctx, costConfiguredAttributesKey, encoded)...)
}

// costConfiguredAttributes reads what costRecordConfiguredAttributes stored.
// A resource that never recorded it (one that was imported and never
// applied since) reports every attribute as configured: importing it was
// taking the whole singleton over.
func costConfiguredAttributes(ctx context.Context, names []string, private costPrivateReader, diagnostics *diag.Diagnostics) map[string]bool {
	configured := map[string]bool{}
	encoded, readDiagnostics := private.GetKey(ctx, costConfiguredAttributesKey)
	diagnostics.Append(readDiagnostics...)
	var recorded []string
	if len(encoded) == 0 || json.Unmarshal(encoded, &recorded) != nil {
		recorded = names
	}
	for _, name := range recorded {
		configured[name] = true
	}
	return configured
}
