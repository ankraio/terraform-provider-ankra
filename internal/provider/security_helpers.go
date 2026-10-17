// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// securityUUIDPattern matches the canonical UUID form the platform parses
// from request bodies and paths; anything else is refused with a 422.
var securityUUIDPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// securityUUIDValidator rejects at plan time an identifier the platform
// could not parse as a UUID.
type securityUUIDValidator struct{}

func (securityUUIDValidator) Description(_ context.Context) string {
	return "value must be a UUID"
}

func (uuidValidator securityUUIDValidator) MarkdownDescription(ctx context.Context) string {
	return uuidValidator.Description(ctx)
}

func (securityUUIDValidator) ValidateString(_ context.Context, request validator.StringRequest, response *validator.StringResponse) {
	if request.ConfigValue.IsNull() || request.ConfigValue.IsUnknown() {
		return
	}
	if !securityUUIDPattern.MatchString(request.ConfigValue.ValueString()) {
		response.Diagnostics.AddAttributeError(request.Path, "Invalid identifier",
			fmt.Sprintf("%q is not a UUID.", request.ConfigValue.ValueString()))
	}
}

// securityReasonMaximumLength is the platform's limit on a disposition reason.
const securityReasonMaximumLength = 2000

// securityReasonValidator mirrors the platform's reason rule (required after
// trimming, at most 2000 characters) and refuses surrounding whitespace,
// which the platform trims and would read back as a change.
type securityReasonValidator struct{}

func (securityReasonValidator) Description(_ context.Context) string {
	return "value must be non-blank, at most 2000 characters, without surrounding whitespace"
}

func (reasonValidator securityReasonValidator) MarkdownDescription(ctx context.Context) string {
	return reasonValidator.Description(ctx)
}

func (securityReasonValidator) ValidateString(_ context.Context, request validator.StringRequest, response *validator.StringResponse) {
	if request.ConfigValue.IsNull() || request.ConfigValue.IsUnknown() {
		return
	}
	value := request.ConfigValue.ValueString()
	trimmed := strings.TrimSpace(value)
	if trimmed == "" || utf8.RuneCountInString(trimmed) > securityReasonMaximumLength || trimmed != value {
		response.Diagnostics.AddAttributeError(request.Path, "Invalid reason",
			"The reason must not be blank, must be at most 2000 characters and must have no surrounding whitespace.")
	}
}

// securityReportMaximumRecipients is the platform's recipient limit.
const securityReportMaximumRecipients = 20

// securityRecipientsValidator mirrors the platform's recipient checks: at
// most 20 addresses, each containing "@", without whitespace or control
// characters, and at most 320 characters long.
type securityRecipientsValidator struct{}

func (securityRecipientsValidator) Description(_ context.Context) string {
	return "at most 20 email addresses"
}

func (recipientsValidator securityRecipientsValidator) MarkdownDescription(ctx context.Context) string {
	return recipientsValidator.Description(ctx)
}

func (securityRecipientsValidator) ValidateList(_ context.Context, request validator.ListRequest, response *validator.ListResponse) {
	if request.ConfigValue.IsNull() || request.ConfigValue.IsUnknown() {
		return
	}
	elements := request.ConfigValue.Elements()
	if len(elements) > securityReportMaximumRecipients {
		response.Diagnostics.AddAttributeError(request.Path, "Too many recipients",
			fmt.Sprintf("At most %d recipients are allowed, got %d.", securityReportMaximumRecipients, len(elements)))
	}
	for _, element := range elements {
		address, isString := element.(types.String)
		if !isString || address.IsNull() || address.IsUnknown() {
			continue
		}
		value := address.ValueString()
		hasControlCharacter := strings.ContainsFunc(value, func(character rune) bool {
			return character <= ' ' || character == 0x7f
		})
		if len(value) > 320 || !strings.Contains(value, "@") || hasControlCharacter {
			response.Diagnostics.AddAttributeError(request.Path, "Invalid recipient",
				fmt.Sprintf("%q is not a valid email address.", value))
		}
	}
}

// securityOneOfValidator accepts a string only when it is one of the values
// the platform stores verbatim. The platform lower-cases some of these
// fields, so a value in another case would read back as a perpetual diff.
type securityOneOfValidator struct {
	allowed []string
}

func securityOneOf(allowed ...string) validator.String {
	return securityOneOfValidator{allowed: allowed}
}

func (oneOf securityOneOfValidator) Description(_ context.Context) string {
	return "value must be one of: " + strings.Join(oneOf.allowed, ", ")
}

func (oneOf securityOneOfValidator) MarkdownDescription(ctx context.Context) string {
	return oneOf.Description(ctx)
}

func (oneOf securityOneOfValidator) ValidateString(ctx context.Context, request validator.StringRequest, response *validator.StringResponse) {
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

// securityTimestampValidator accepts an RFC 3339 timestamp or a date
// (2026-12-31, read as midnight UTC), the two forms the CLI takes.
type securityTimestampValidator struct{}

func (securityTimestampValidator) Description(_ context.Context) string {
	return "value must be an RFC 3339 timestamp or a YYYY-MM-DD date"
}

func (timestamp securityTimestampValidator) MarkdownDescription(ctx context.Context) string {
	return timestamp.Description(ctx)
}

func (securityTimestampValidator) ValidateString(_ context.Context, request validator.StringRequest, response *validator.StringResponse) {
	if request.ConfigValue.IsNull() || request.ConfigValue.IsUnknown() {
		return
	}
	if _, err := securityParseTimestamp(request.ConfigValue.ValueString()); err != nil {
		response.Diagnostics.AddAttributeError(request.Path, "Invalid timestamp", err.Error())
	}
}

// securityParseTimestamp reads an RFC 3339 timestamp or a YYYY-MM-DD date.
func securityParseTimestamp(value string) (time.Time, error) {
	trimmed := strings.TrimSpace(value)
	if parsed, err := time.Parse(time.RFC3339, trimmed); err == nil {
		return parsed, nil
	}
	parsed, err := time.Parse(time.DateOnly, trimmed)
	if err != nil {
		return time.Time{}, fmt.Errorf("%q is neither an RFC 3339 timestamp nor a YYYY-MM-DD date", value)
	}
	return parsed, nil
}
