// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"
	"regexp"

	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
)

// deployPatternValidator rejects at plan time a string the platform refuses
// because it does not match the platform's own pattern.
type deployPatternValidator struct {
	pattern *regexp.Regexp
	subject string
}

func (patternValidator deployPatternValidator) Description(_ context.Context) string {
	return fmt.Sprintf("%s must match %s", patternValidator.subject, patternValidator.pattern.String())
}

func (patternValidator deployPatternValidator) MarkdownDescription(ctx context.Context) string {
	return patternValidator.Description(ctx)
}

func (patternValidator deployPatternValidator) ValidateString(ctx context.Context, request validator.StringRequest, response *validator.StringResponse) {
	if request.ConfigValue.IsNull() || request.ConfigValue.IsUnknown() {
		return
	}
	if !patternValidator.pattern.MatchString(request.ConfigValue.ValueString()) {
		response.Diagnostics.AddAttributeError(request.Path, "Invalid "+patternValidator.subject,
			"The platform refuses it: "+patternValidator.Description(ctx)+".")
	}
}

// deployInt64Range rejects at plan time an integer outside the platform's
// bounds.
type deployInt64Range struct {
	minimum int64
	maximum int64
}

func (rangeValidator deployInt64Range) Description(_ context.Context) string {
	return fmt.Sprintf("value must be between %d and %d", rangeValidator.minimum, rangeValidator.maximum)
}

func (rangeValidator deployInt64Range) MarkdownDescription(ctx context.Context) string {
	return rangeValidator.Description(ctx)
}

func (rangeValidator deployInt64Range) ValidateInt64(ctx context.Context, request validator.Int64Request, response *validator.Int64Response) {
	if request.ConfigValue.IsNull() || request.ConfigValue.IsUnknown() {
		return
	}
	value := request.ConfigValue.ValueInt64()
	if value < rangeValidator.minimum || value > rangeValidator.maximum {
		response.Diagnostics.AddAttributeError(request.Path, "Value out of range", "The platform refuses it: "+rangeValidator.Description(ctx)+".")
	}
}
