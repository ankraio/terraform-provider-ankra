// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
)

// pipelineCronMacros mirrors the macros the platform expands
// (enginekit/cischedule).
var pipelineCronMacros = map[string]string{
	"@yearly":   "0 0 1 1 *",
	"@annually": "0 0 1 1 *",
	"@monthly":  "0 0 1 * *",
	"@weekly":   "0 0 * * 0",
	"@daily":    "0 0 * * *",
	"@midnight": "0 0 * * *",
	"@hourly":   "0 * * * *",
}

var pipelineCronMonthNames = map[string]int{
	"jan": 1, "feb": 2, "mar": 3, "apr": 4, "may": 5, "jun": 6,
	"jul": 7, "aug": 8, "sep": 9, "oct": 10, "nov": 11, "dec": 12,
}

var pipelineCronDayNames = map[string]int{
	"sun": 0, "mon": 1, "tue": 2, "wed": 3, "thu": 4, "fri": 5, "sat": 6,
}

// pipelineCanonicalCron is the expression the platform stores for a cron
// expression: lower-cased, macros expanded, fields single-spaced.
func pipelineCanonicalCron(expression string) string {
	normalised := strings.ToLower(strings.TrimSpace(expression))
	if expanded, isMacro := pipelineCronMacros[normalised]; isMacro {
		normalised = expanded
	}
	return strings.Join(strings.Fields(normalised), " ")
}

// pipelineCronError reports why the platform would refuse an expression, or
// "" when it parses. It follows enginekit/cischedule Parse.
func pipelineCronError(expression string) string {
	canonical := pipelineCanonicalCron(expression)
	if canonical == "" {
		return "the expression is empty"
	}
	parts := strings.Fields(canonical)
	if len(parts) != 5 {
		return fmt.Sprintf("a cron expression has five fields, this one has %d", len(parts))
	}
	fields := []struct {
		name     string
		minimum  int
		maximum  int
		synonyms map[string]int
	}{
		{"minute", 0, 59, nil},
		{"hour", 0, 23, nil},
		{"day of month", 1, 31, nil},
		{"month", 1, 12, pipelineCronMonthNames},
		{"day of week", 0, 7, pipelineCronDayNames},
	}
	for index, definition := range fields {
		for _, term := range strings.Split(parts[index], ",") {
			if message := pipelineCronTermError(term, definition.minimum, definition.maximum, definition.synonyms, definition.name); message != "" {
				return message
			}
		}
	}
	return ""
}

func pipelineCronTermError(term string, minimum int, maximum int, synonyms map[string]int, fieldName string) string {
	if term == "" {
		return "the " + fieldName + " field has an empty entry"
	}
	rangePart := term
	if slashIndex := strings.Index(term, "/"); slashIndex >= 0 {
		rangePart = term[:slashIndex]
		step, stepError := strconv.Atoi(term[slashIndex+1:])
		if stepError != nil || step < 1 {
			return fmt.Sprintf("%q is not a step the %s field accepts", term[slashIndex+1:], fieldName)
		}
	}
	if rangePart == "*" {
		return ""
	}
	lowText, highText, isRange := strings.Cut(rangePart, "-")
	low, lowMessage := pipelineCronValue(lowText, minimum, maximum, synonyms, fieldName)
	if lowMessage != "" {
		return lowMessage
	}
	if !isRange {
		return ""
	}
	high, highMessage := pipelineCronValue(highText, minimum, maximum, synonyms, fieldName)
	if highMessage != "" {
		return highMessage
	}
	if high < low {
		return fmt.Sprintf("the %s range %q runs backwards", fieldName, rangePart)
	}
	return ""
}

func pipelineCronValue(text string, minimum int, maximum int, synonyms map[string]int, fieldName string) (int, string) {
	if named, isNamed := synonyms[text]; isNamed {
		return named, ""
	}
	value, parseError := strconv.Atoi(text)
	if parseError != nil {
		return 0, fmt.Sprintf("%q is not a value the %s field accepts", text, fieldName)
	}
	if value < minimum || value > maximum {
		return 0, fmt.Sprintf("the %s field takes %d to %d, not %d", fieldName, minimum, maximum, value)
	}
	return value, ""
}

// pipelineCronValidator refuses at plan time an expression the platform
// cannot read.
type pipelineCronValidator struct{}

func (pipelineCronValidator) Description(_ context.Context) string {
	return "a five-field cron expression or one of @yearly, @annually, @monthly, @weekly, @daily, @midnight, @hourly"
}

func (cronValidator pipelineCronValidator) MarkdownDescription(ctx context.Context) string {
	return cronValidator.Description(ctx)
}

func (pipelineCronValidator) ValidateString(_ context.Context, request validator.StringRequest, response *validator.StringResponse) {
	if request.ConfigValue.IsNull() || request.ConfigValue.IsUnknown() {
		return
	}
	if message := pipelineCronError(request.ConfigValue.ValueString()); message != "" {
		response.Diagnostics.AddAttributeError(request.Path, "Invalid cron expression",
			fmt.Sprintf("%q is not a cron expression Ankra can read: %s.", request.ConfigValue.ValueString(), message))
	}
}

// pipelineTimezoneValidator refuses at plan time a timezone the platform
// cannot load.
type pipelineTimezoneValidator struct{}

func (pipelineTimezoneValidator) Description(_ context.Context) string {
	return "an IANA timezone name such as Europe/Oslo"
}

func (timezoneValidator pipelineTimezoneValidator) MarkdownDescription(ctx context.Context) string {
	return timezoneValidator.Description(ctx)
}

func (pipelineTimezoneValidator) ValidateString(_ context.Context, request validator.StringRequest, response *validator.StringResponse) {
	if request.ConfigValue.IsNull() || request.ConfigValue.IsUnknown() {
		return
	}
	if _, loadError := time.LoadLocation(strings.TrimSpace(request.ConfigValue.ValueString())); loadError != nil {
		response.Diagnostics.AddAttributeError(request.Path, "Invalid timezone",
			fmt.Sprintf("%q is not a timezone Ankra can read.", request.ConfigValue.ValueString()))
	}
}
