// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
)

// reservedOrganisationVariableNames mirrors the platform's reserved list
// (cluster go/internal/usecase/variables/reserved.go): the AI settings own
// these rows, and the API refuses to create, update or delete them.
var reservedOrganisationVariableNames = map[string]struct{}{
	"anthropic_api_key_configured":       {},
	"openrouter_api_key_configured":      {},
	"openai_compatible_configured":       {},
	"llm_provider":                       {},
	"ai_model_catalog_configured":        {},
	"ai_output_style":                    {},
	"ai_on_endpoint_failure":             {},
	"ai_board_identity_auto_provisioned": {},
}

var reservedOrganisationVariablePrefixes = []string{"ai_default_agents_"}

// organisationVariableNameValidator rejects at plan time the names the
// platform would refuse at apply, and names containing "/". The API addresses
// a variable by name in the URL path and does not route an escaped slash, so
// such a variable could be created but never updated or deleted.
type organisationVariableNameValidator struct{}

func (organisationVariableNameValidator) Description(_ context.Context) string {
	return "name must not be reserved by the platform or contain \"/\""
}

func (nameValidator organisationVariableNameValidator) MarkdownDescription(ctx context.Context) string {
	return nameValidator.Description(ctx)
}

func (organisationVariableNameValidator) ValidateString(_ context.Context, request validator.StringRequest, response *validator.StringResponse) {
	if request.ConfigValue.IsNull() || request.ConfigValue.IsUnknown() {
		return
	}
	name := request.ConfigValue.ValueString()
	if strings.Contains(name, "/") {
		response.Diagnostics.AddAttributeError(request.Path, "Invalid variable name",
			"Variable names cannot contain \"/\": the platform could not update or delete the variable afterwards.")
		return
	}
	if isReservedOrganisationVariableName(name) {
		response.Diagnostics.AddAttributeError(request.Path, "Reserved variable name",
			"\""+name+"\" is reserved for platform settings. Configure it through the AI settings instead.")
	}
}

func isReservedOrganisationVariableName(name string) bool {
	if _, reserved := reservedOrganisationVariableNames[name]; reserved {
		return true
	}
	for _, prefix := range reservedOrganisationVariablePrefixes {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}
