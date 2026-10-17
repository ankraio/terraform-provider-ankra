// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"ankra.io/terraform-provider-ankra/internal/client"
)

// applicationSplitImportID splits a composite import id into exactly the
// named parts, refusing an id with a missing or empty part.
func applicationSplitImportID(importID string, partNames ...string) ([]string, error) {
	parts := strings.Split(importID, "/")
	if len(parts) != len(partNames) {
		return nil, fmt.Errorf("expected an import id of the form %s, got %q", strings.Join(partNames, "/"), importID)
	}
	for index, part := range parts {
		if strings.TrimSpace(part) == "" {
			return nil, fmt.Errorf("the %s part of import id %q is empty; expected %s", partNames[index], importID, strings.Join(partNames, "/"))
		}
	}
	return parts, nil
}

// applicationStringMap reads a map-of-strings attribute; null and unknown read
// as nil.
func applicationStringMap(ctx context.Context, value types.Map, diagnostics *diag.Diagnostics) map[string]string {
	if value.IsNull() || value.IsUnknown() {
		return nil
	}
	result := map[string]string{}
	diagnostics.Append(value.ElementsAs(ctx, &result, false)...)
	return result
}

// applicationMapValue is the inverse of applicationStringMap. An empty map is
// a null attribute, so an unset optional map does not plan a diff.
func applicationMapValue(values map[string]string) types.Map {
	if len(values) == 0 {
		return types.MapNull(types.StringType)
	}
	elements := make(map[string]attr.Value, len(values))
	for key, value := range values {
		elements[key] = types.StringValue(value)
	}
	return types.MapValueMust(types.StringType, elements)
}

// pipelineSelectorDescription documents the two ways a pipeline resource
// addresses its repository.
const pipelineSelectorDescription = "Set exactly one of `application_id` and `repository_id`."

// pipelineSelectorFrom builds the client selector from the two attributes.
func pipelineSelectorFrom(applicationID types.String, repositoryID types.String) client.PipelineSelector {
	return client.PipelineSelector{
		ApplicationID: applicationID.ValueString(),
		RepositoryID:  repositoryID.ValueString(),
	}
}

// pipelineValidateSelector reports a configuration that does not name exactly
// one of application_id and repository_id. Unknown values are left to apply.
func pipelineValidateSelector(applicationID types.String, repositoryID types.String, diagnostics *diag.Diagnostics) {
	if applicationID.IsUnknown() || repositoryID.IsUnknown() {
		return
	}
	if applicationID.IsNull() == repositoryID.IsNull() {
		diagnostics.AddAttributeError(path.Root("application_id"), "Invalid pipeline selector", pipelineSelectorDescription)
	}
}

// pipelineSelectorImport parses the selector prefix of a pipeline import id,
// "application/<id>" or "repository/<id>", and sets the matching attribute.
func pipelineSelectorImport(ctx context.Context, kind string, identifier string, response *resource.ImportStateResponse) {
	switch kind {
	case "application":
		response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("application_id"), identifier)...)
	case "repository":
		response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("repository_id"), identifier)...)
	default:
		response.Diagnostics.AddError("Invalid import id",
			fmt.Sprintf("the import id must start with application/ or repository/, got %q", kind+"/"+identifier))
	}
}
