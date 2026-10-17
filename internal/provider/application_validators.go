// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
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
