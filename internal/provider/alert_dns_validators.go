// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
)

// alertDNSOneOfValidator rejects at plan time a value the platform refuses
// because it is not in its enumeration.
type alertDNSOneOfValidator struct {
	allowed []string
}

func (oneOf alertDNSOneOfValidator) Description(_ context.Context) string {
	return "value must be one of: " + strings.Join(oneOf.allowed, ", ")
}

func (oneOf alertDNSOneOfValidator) MarkdownDescription(ctx context.Context) string {
	return oneOf.Description(ctx)
}

func (oneOf alertDNSOneOfValidator) ValidateString(ctx context.Context, request validator.StringRequest, response *validator.StringResponse) {
	if request.ConfigValue.IsNull() || request.ConfigValue.IsUnknown() {
		return
	}
	value := request.ConfigValue.ValueString()
	for _, candidate := range oneOf.allowed {
		if value == candidate {
			return
		}
	}
	response.Diagnostics.AddAttributeError(request.Path, "Invalid value",
		fmt.Sprintf("%q is not accepted: %s.", value, oneOf.Description(ctx)))
}

// alertDNSLowerCaseValidator requires a lower-case name without "/", spaces
// or a trailing dot: the platform lower-cases DNS names, so a mixed-case value
// would plan a replacement after every refresh, and a "/" cannot be routed in
// the URL path that addresses the object.
type alertDNSLowerCaseValidator struct{}

func (alertDNSLowerCaseValidator) Description(_ context.Context) string {
	return "value must be lower case, without spaces, \"/\" or a trailing dot"
}

func (lowerCase alertDNSLowerCaseValidator) MarkdownDescription(ctx context.Context) string {
	return lowerCase.Description(ctx)
}

func (lowerCase alertDNSLowerCaseValidator) ValidateString(ctx context.Context, request validator.StringRequest, response *validator.StringResponse) {
	if request.ConfigValue.IsNull() || request.ConfigValue.IsUnknown() {
		return
	}
	value := request.ConfigValue.ValueString()
	if value == "" || value != strings.ToLower(value) || strings.ContainsAny(value, "/ \t") || strings.HasSuffix(value, ".") {
		response.Diagnostics.AddAttributeError(request.Path, "Invalid DNS name",
			fmt.Sprintf("%q is not accepted: %s.", value, lowerCase.Description(ctx)))
	}
}

// alertDNSRangeValidator bounds an integer the platform refuses outside a
// range.
type alertDNSRangeValidator struct {
	minimum int64
	maximum int64
}

func (bounds alertDNSRangeValidator) Description(_ context.Context) string {
	return fmt.Sprintf("value must be between %d and %d", bounds.minimum, bounds.maximum)
}

func (bounds alertDNSRangeValidator) MarkdownDescription(ctx context.Context) string {
	return bounds.Description(ctx)
}

func (bounds alertDNSRangeValidator) ValidateInt64(ctx context.Context, request validator.Int64Request, response *validator.Int64Response) {
	if request.ConfigValue.IsNull() || request.ConfigValue.IsUnknown() {
		return
	}
	if value := request.ConfigValue.ValueInt64(); value < bounds.minimum || value > bounds.maximum {
		response.Diagnostics.AddAttributeError(request.Path, "Value out of range",
			fmt.Sprintf("%d is not accepted: %s.", value, bounds.Description(ctx)))
	}
}
