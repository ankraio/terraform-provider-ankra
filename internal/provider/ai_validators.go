// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"regexp"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
)

// aiKeepKnownIdentifier is UseStateForUnknown for an identifier that may be
// null in state: a built-in catalogue entry has no row id until its first
// change, so a null id must stay unknown in the plan for the platform to
// fill it, while a known id is kept.
type aiKeepKnownIdentifier struct{}

func (aiKeepKnownIdentifier) Description(_ context.Context) string {
	return "Once the platform assigns the identifier, it does not change."
}

func (identifier aiKeepKnownIdentifier) MarkdownDescription(ctx context.Context) string {
	return identifier.Description(ctx)
}

func (aiKeepKnownIdentifier) PlanModifyString(_ context.Context, request planmodifier.StringRequest, response *planmodifier.StringResponse) {
	if request.State.Raw.IsNull() || request.StateValue.IsNull() || !request.PlanValue.IsUnknown() || request.ConfigValue.IsUnknown() {
		return
	}
	response.PlanValue = request.StateValue
}

// aiCatalogueKeyPattern mirrors the platform's catalogue key rule
// (aisettings.catalogKeyPattern); "auto" is reserved separately.
var aiCatalogueKeyPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,127}$`)

// aiStringOneOf rejects at plan time a value outside the set the platform
// accepts.
type aiStringOneOf struct {
	allowed []string
}

func (oneOf aiStringOneOf) Description(_ context.Context) string {
	return "value must be one of: " + strings.Join(oneOf.allowed, ", ")
}

func (oneOf aiStringOneOf) MarkdownDescription(ctx context.Context) string {
	return oneOf.Description(ctx)
}

func (oneOf aiStringOneOf) ValidateString(_ context.Context, request validator.StringRequest, response *validator.StringResponse) {
	if request.ConfigValue.IsNull() || request.ConfigValue.IsUnknown() {
		return
	}
	for _, allowed := range oneOf.allowed {
		if request.ConfigValue.ValueString() == allowed {
			return
		}
	}
	response.Diagnostics.AddAttributeError(request.Path, "Invalid value",
		"\""+request.ConfigValue.ValueString()+"\" is not accepted by the platform; use one of: "+strings.Join(oneOf.allowed, ", ")+".")
}

// aiStringPrefix rejects a value that does not start with one of the given
// prefixes, as the platform does for provider keys and base URLs.
type aiStringPrefix struct {
	prefixes []string
	subject  string
}

func (prefix aiStringPrefix) Description(_ context.Context) string {
	return prefix.subject + " must start with " + strings.Join(prefix.prefixes, " or ")
}

func (prefix aiStringPrefix) MarkdownDescription(ctx context.Context) string {
	return prefix.Description(ctx)
}

func (prefix aiStringPrefix) ValidateString(ctx context.Context, request validator.StringRequest, response *validator.StringResponse) {
	if request.ConfigValue.IsNull() || request.ConfigValue.IsUnknown() {
		return
	}
	value := strings.TrimSpace(request.ConfigValue.ValueString())
	for _, candidate := range prefix.prefixes {
		if strings.HasPrefix(value, candidate) {
			return
		}
	}
	response.Diagnostics.AddAttributeError(request.Path, "Invalid "+prefix.subject, "The platform refuses it: "+prefix.Description(ctx)+".")
}

// aiCatalogueKeyValidator rejects a catalogue key the platform would refuse:
// anything but a lower-case slug, and the reserved "auto".
type aiCatalogueKeyValidator struct{}

func (aiCatalogueKeyValidator) Description(_ context.Context) string {
	return "key must be lower-case letters, digits, '-' or '_' (at most 128 characters) and not \"auto\""
}

func (keyValidator aiCatalogueKeyValidator) MarkdownDescription(ctx context.Context) string {
	return keyValidator.Description(ctx)
}

func (keyValidator aiCatalogueKeyValidator) ValidateString(ctx context.Context, request validator.StringRequest, response *validator.StringResponse) {
	if request.ConfigValue.IsNull() || request.ConfigValue.IsUnknown() {
		return
	}
	key := request.ConfigValue.ValueString()
	if key == "auto" || !aiCatalogueKeyPattern.MatchString(key) {
		response.Diagnostics.AddAttributeError(request.Path, "Invalid catalogue key", "The platform refuses it: "+keyValidator.Description(ctx)+".")
	}
}

// aiNoSlash rejects a value containing "/", which the platform's path
// routing cannot address.
type aiNoSlash struct{}

func (aiNoSlash) Description(_ context.Context) string {
	return "value must not contain \"/\""
}

func (slash aiNoSlash) MarkdownDescription(ctx context.Context) string {
	return slash.Description(ctx)
}

func (aiNoSlash) ValidateString(_ context.Context, request validator.StringRequest, response *validator.StringResponse) {
	if request.ConfigValue.IsNull() || request.ConfigValue.IsUnknown() {
		return
	}
	if strings.Contains(request.ConfigValue.ValueString(), "/") {
		response.Diagnostics.AddAttributeError(request.Path, "Invalid value", "The value cannot contain \"/\": the platform addresses it in the URL path.")
	}
}

var aiBaseURLPrefix = aiStringPrefix{prefixes: []string{"http://", "https://"}, subject: "base URL"}
