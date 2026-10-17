// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// applicationEnvSecretKeyMaxLength mirrors the platform's bound on an env
// Secret key (enginekit/manifestenv MaxKeyLength).
const applicationEnvSecretKeyMaxLength = 63

// applicationStringOneOf rejects at plan time a value outside the set the
// platform accepts, which it would otherwise refuse at apply.
type applicationStringOneOf struct {
	values []string
}

func (oneOf applicationStringOneOf) Description(_ context.Context) string {
	return "value must be one of: " + strings.Join(oneOf.values, ", ")
}

func (oneOf applicationStringOneOf) MarkdownDescription(ctx context.Context) string {
	return oneOf.Description(ctx)
}

func (oneOf applicationStringOneOf) ValidateString(ctx context.Context, request validator.StringRequest, response *validator.StringResponse) {
	if request.ConfigValue.IsNull() || request.ConfigValue.IsUnknown() {
		return
	}
	for _, allowed := range oneOf.values {
		if request.ConfigValue.ValueString() == allowed {
			return
		}
	}
	response.Diagnostics.AddAttributeError(request.Path, "Invalid value",
		fmt.Sprintf("%q is not accepted: %s.", request.ConfigValue.ValueString(), oneOf.Description(ctx)))
}

// applicationEnvSecretKeyValidator applies the platform's env Secret key rule
// (a C identifier of at most 63 characters, enginekit/manifestenv IsValidKey)
// at plan time.
type applicationEnvSecretKeyValidator struct{}

func (applicationEnvSecretKeyValidator) Description(_ context.Context) string {
	return fmt.Sprintf("key must be letters, digits and underscores, start with a letter or underscore, and be at most %d characters", applicationEnvSecretKeyMaxLength)
}

func (keyValidator applicationEnvSecretKeyValidator) MarkdownDescription(ctx context.Context) string {
	return keyValidator.Description(ctx)
}

func (keyValidator applicationEnvSecretKeyValidator) ValidateString(ctx context.Context, request validator.StringRequest, response *validator.StringResponse) {
	if request.ConfigValue.IsNull() || request.ConfigValue.IsUnknown() {
		return
	}
	if !isValidApplicationEnvSecretKey(request.ConfigValue.ValueString()) {
		response.Diagnostics.AddAttributeError(request.Path, "Invalid environment secret key", "The "+keyValidator.Description(ctx)+".")
	}
}

func isValidApplicationEnvSecretKey(key string) bool {
	if key == "" || len(key) > applicationEnvSecretKeyMaxLength {
		return false
	}
	for index := 0; index < len(key); index++ {
		symbol := key[index]
		isLetter := (symbol >= 'A' && symbol <= 'Z') || (symbol >= 'a' && symbol <= 'z')
		isDigit := symbol >= '0' && symbol <= '9'
		if isLetter || symbol == '_' || (isDigit && index > 0) {
			continue
		}
		return false
	}
	return true
}

// applicationIntegerBetween rejects at plan time an integer outside an
// inclusive range the platform enforces.
type applicationIntegerBetween struct {
	minimum int64
	maximum int64
}

func (between applicationIntegerBetween) Description(_ context.Context) string {
	return fmt.Sprintf("value must be between %d and %d", between.minimum, between.maximum)
}

func (between applicationIntegerBetween) MarkdownDescription(ctx context.Context) string {
	return between.Description(ctx)
}

func (between applicationIntegerBetween) ValidateInt64(ctx context.Context, request validator.Int64Request, response *validator.Int64Response) {
	if request.ConfigValue.IsNull() || request.ConfigValue.IsUnknown() {
		return
	}
	value := request.ConfigValue.ValueInt64()
	if value < between.minimum || value > between.maximum {
		response.Diagnostics.AddAttributeError(request.Path, "Value out of range",
			fmt.Sprintf("%d is not accepted: %s.", value, between.Description(ctx)))
	}
}

// applicationRegistryURLValidator requires an image registry url of the form
// oci://<host>/<project>[/<prefix>], the form the platform stores and resolves
// (enginekit/appregistry ParseRegistryURL). Anything else reads back as no
// registry at all.
type applicationRegistryURLValidator struct{}

func (applicationRegistryURLValidator) Description(_ context.Context) string {
	return "url must be oci://<host>/<project>, with no surrounding whitespace"
}

func (urlValidator applicationRegistryURLValidator) MarkdownDescription(ctx context.Context) string {
	return urlValidator.Description(ctx)
}

func (urlValidator applicationRegistryURLValidator) ValidateString(ctx context.Context, request validator.StringRequest, response *validator.StringResponse) {
	if request.ConfigValue.IsNull() || request.ConfigValue.IsUnknown() {
		return
	}
	value := request.ConfigValue.ValueString()
	parsed, parseError := url.Parse(value)
	if value != strings.TrimSpace(value) || parseError != nil || parsed.Scheme != "oci" || parsed.Host == "" ||
		strings.Trim(parsed.Path, "/") == "" {
		response.Diagnostics.AddAttributeError(request.Path, "Invalid registry url",
			fmt.Sprintf("%q is not accepted: %s, as in oci://registry.example.com/my-project.", value, urlValidator.Description(ctx)))
	}
}

// applicationHTTPURLValidator requires an http:// or https:// url, which the
// platform demands of a registry api_url.
type applicationHTTPURLValidator struct{}

func (applicationHTTPURLValidator) Description(_ context.Context) string {
	return "value must start with http:// or https://"
}

func (urlValidator applicationHTTPURLValidator) MarkdownDescription(ctx context.Context) string {
	return urlValidator.Description(ctx)
}

func (urlValidator applicationHTTPURLValidator) ValidateString(ctx context.Context, request validator.StringRequest, response *validator.StringResponse) {
	if request.ConfigValue.IsNull() || request.ConfigValue.IsUnknown() {
		return
	}
	value := request.ConfigValue.ValueString()
	hasScheme := strings.HasPrefix(value, "http://") || strings.HasPrefix(value, "https://")
	if !hasScheme || value != strings.TrimSpace(value) {
		response.Diagnostics.AddAttributeError(request.Path, "Invalid url",
			fmt.Sprintf("%q is not accepted: %s.", value, urlValidator.Description(ctx)))
	}
}

// applicationTrimmedValidator refuses surrounding whitespace, which the
// platform trims away on store and would then read back as a change.
type applicationTrimmedValidator struct{}

func (applicationTrimmedValidator) Description(_ context.Context) string {
	return "value must not start or end with whitespace"
}

func (trimmedValidator applicationTrimmedValidator) MarkdownDescription(ctx context.Context) string {
	return trimmedValidator.Description(ctx)
}

func (trimmedValidator applicationTrimmedValidator) ValidateString(ctx context.Context, request validator.StringRequest, response *validator.StringResponse) {
	if request.ConfigValue.IsNull() || request.ConfigValue.IsUnknown() {
		return
	}
	if value := request.ConfigValue.ValueString(); value != strings.TrimSpace(value) {
		response.Diagnostics.AddAttributeError(request.Path, "Surrounding whitespace",
			fmt.Sprintf("%q is not accepted: %s.", value, trimmedValidator.Description(ctx)))
	}
}

// applicationComponentRepositoriesValidator accepts only the form the
// platform stores (enginekit/appregistry NormaliseComponentRepositories):
// lower-case, trimmed keys and values, and values without leading or
// trailing slashes. Any other entry would read back changed or dropped.
type applicationComponentRepositoriesValidator struct{}

func (applicationComponentRepositoriesValidator) Description(_ context.Context) string {
	return "component names and repositories must be non-empty, lower-case and without surrounding whitespace, and repositories must not start or end with /"
}

func (mapValidator applicationComponentRepositoriesValidator) MarkdownDescription(ctx context.Context) string {
	return mapValidator.Description(ctx)
}

func (mapValidator applicationComponentRepositoriesValidator) ValidateMap(ctx context.Context, request validator.MapRequest, response *validator.MapResponse) {
	if request.ConfigValue.IsNull() || request.ConfigValue.IsUnknown() {
		return
	}
	for componentName, element := range request.ConfigValue.Elements() {
		repository, isString := element.(types.String)
		if !isString || repository.IsUnknown() || repository.IsNull() {
			continue
		}
		value := repository.ValueString()
		normalisedValue := strings.ToLower(strings.Trim(strings.TrimSpace(value), "/"))
		if componentName == "" || componentName != strings.ToLower(strings.TrimSpace(componentName)) ||
			normalisedValue == "" || value != normalisedValue {
			response.Diagnostics.AddAttributeError(request.Path.AtMapKey(componentName), "Invalid component repository",
				fmt.Sprintf("%q = %q is not accepted: %s.", componentName, value, mapValidator.Description(ctx)))
		}
	}
}
