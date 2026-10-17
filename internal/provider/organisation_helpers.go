// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// organisationTimestampLayouts are the shapes the platform renders a
// timestamp in: RFC 3339 with an offset or Z, or a naive UTC value with
// optional fractional seconds, which is how token listings answer.
var organisationTimestampLayouts = []string{
	time.RFC3339Nano,
	"2006-01-02T15:04:05.999999999",
	"2006-01-02T15:04:05",
	"2006-01-02",
}

// organisationParseTimestamp reads a platform or configured timestamp; a
// naive value is UTC.
func organisationParseTimestamp(value string) (time.Time, bool) {
	for _, layout := range organisationTimestampLayouts {
		if parsed, err := time.Parse(layout, strings.TrimSpace(value)); err == nil {
			return parsed.UTC(), true
		}
	}
	return time.Time{}, false
}

// organisationTimestampState keeps the state's spelling of a timestamp when
// the platform's answer is the same instant, so a configured
// "2027-01-01T00:00:00Z" read back as the naive "2027-01-01T00:00:00" is not
// drift. A different instant is reported in RFC 3339.
func organisationTimestampState(stateValue types.String, platformValue string) types.String {
	if platformValue == "" {
		return stateValue
	}
	platformTime, isPlatformParsed := organisationParseTimestamp(platformValue)
	if !isPlatformParsed {
		return types.StringValue(platformValue)
	}
	if !stateValue.IsNull() && !stateValue.IsUnknown() {
		if stateTime, isStateParsed := organisationParseTimestamp(stateValue.ValueString()); isStateParsed && stateTime.Equal(platformTime) {
			return stateValue
		}
	}
	return types.StringValue(platformTime.Format(time.RFC3339Nano))
}

// organisationSetFromStrings is listFromStrings for a set attribute.
func organisationSetFromStrings(values []string) types.Set {
	if values == nil {
		return types.SetNull(types.StringType)
	}
	elements := make([]attr.Value, 0, len(values))
	for _, value := range values {
		elements = append(elements, types.StringValue(value))
	}
	return types.SetValueMust(types.StringType, elements)
}

// organisationStringsFromSet is stringsFromList for a set attribute.
func organisationStringsFromSet(ctx context.Context, set types.Set, diagnostics *diag.Diagnostics) []string {
	if set.IsNull() || set.IsUnknown() {
		return nil
	}
	var elements []types.String
	diagnostics.Append(set.ElementsAs(ctx, &elements, false)...)
	values := make([]string, 0, len(elements))
	for _, element := range elements {
		if element.IsNull() || element.IsUnknown() {
			continue
		}
		values = append(values, element.ValueString())
	}
	return values
}

// organisationOptionalSet maps a listed set the platform always answers
// (empty when unset) to state: an empty answer stays null while the state
// holds null, so an unset optional set does not plan a diff.
func organisationOptionalSet(stateValue types.Set, values []string) types.Set {
	if len(values) == 0 && stateValue.IsNull() {
		return stateValue
	}
	if values == nil {
		values = []string{}
	}
	return organisationSetFromStrings(values)
}

// organisationNonNilStrings returns values, or an empty slice for nil, for a
// request member the platform refuses as null.
func organisationNonNilStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

// organisationSplitImportID splits a composite import id into exactly count
// non-empty parts, naming the expected shape in the error.
func organisationSplitImportID(importID string, count int, shape string) ([]string, error) {
	parts := strings.SplitN(importID, "/", count)
	if len(parts) != count {
		return nil, fmt.Errorf("expected an import id of the form %s, got %q", shape, importID)
	}
	for _, part := range parts {
		if part == "" {
			return nil, fmt.Errorf("expected an import id of the form %s, got %q", shape, importID)
		}
	}
	return parts, nil
}

// organisationStringOneOf validates that a string is one of a closed set of
// values the platform accepts.
type organisationStringOneOf struct {
	values []string
}

func (oneOf organisationStringOneOf) Description(_ context.Context) string {
	return "value must be one of: " + strings.Join(oneOf.values, ", ")
}

func (oneOf organisationStringOneOf) MarkdownDescription(ctx context.Context) string {
	return oneOf.Description(ctx)
}

func (oneOf organisationStringOneOf) ValidateString(ctx context.Context, request validator.StringRequest, response *validator.StringResponse) {
	if request.ConfigValue.IsNull() || request.ConfigValue.IsUnknown() {
		return
	}
	value := request.ConfigValue.ValueString()
	for _, allowed := range oneOf.values {
		if value == allowed {
			return
		}
	}
	response.Diagnostics.AddAttributeError(request.Path, "Invalid value", fmt.Sprintf("%q is not accepted: %s.", value, oneOf.Description(ctx)))
}

// organisationTimestampValidator requires an RFC 3339 timestamp.
type organisationTimestampValidator struct{}

func (organisationTimestampValidator) Description(_ context.Context) string {
	return "value must be an RFC 3339 timestamp, such as 2027-01-01T00:00:00Z"
}

func (timestampValidator organisationTimestampValidator) MarkdownDescription(ctx context.Context) string {
	return timestampValidator.Description(ctx)
}

func (timestampValidator organisationTimestampValidator) ValidateString(ctx context.Context, request validator.StringRequest, response *validator.StringResponse) {
	if request.ConfigValue.IsNull() || request.ConfigValue.IsUnknown() {
		return
	}
	if _, err := time.Parse(time.RFC3339, request.ConfigValue.ValueString()); err != nil {
		response.Diagnostics.AddAttributeError(request.Path, "Invalid timestamp", timestampValidator.Description(ctx)+".")
	}
}
