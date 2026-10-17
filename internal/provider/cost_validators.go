// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// costCurrencies mirrors the platform's display currencies
// (enginekit/costcurrency AllowedDisplayCurrencies), which budgets and the
// pricing settings both accept, case-insensitively.
var costCurrencies = []string{"usd", "eur", "gbp", "sek", "nok", "dkk", "chf", "pln", "czk", "jpy", "cad", "aud", "inr"}

// costBudgetScopeKinds mirrors enginekit/costbudget's scopes.
var costBudgetScopeKinds = []string{"organisation", "project", "cluster", "environment", "application"}

// costAutopilotTiers and costAutopilotKinds mirror enginekit/costautopilot.
var (
	costAutopilotTiers = []string{"hands-off", "scheduled", "managed", "ephemeral"}
	costAutopilotKinds = []string{"production", "staging", "development", "preview", "unknown"}
)

func costContains(values []string, candidate string) bool {
	for _, value := range values {
		if value == candidate {
			return true
		}
	}
	return false
}

// costOneOfValidator refuses a string outside a fixed vocabulary;
// isCaseInsensitive matches the platform lower-casing the value first.
type costOneOfValidator struct {
	values            []string
	isCaseInsensitive bool
}

func (oneOf costOneOfValidator) Description(_ context.Context) string {
	return "value must be one of: " + strings.Join(oneOf.values, ", ")
}

func (oneOf costOneOfValidator) MarkdownDescription(ctx context.Context) string {
	return oneOf.Description(ctx)
}

func (oneOf costOneOfValidator) ValidateString(_ context.Context, request validator.StringRequest, response *validator.StringResponse) {
	if request.ConfigValue.IsNull() || request.ConfigValue.IsUnknown() {
		return
	}
	value := request.ConfigValue.ValueString()
	if oneOf.isCaseInsensitive {
		value = strings.ToLower(strings.TrimSpace(value))
	}
	if !costContains(oneOf.values, value) {
		response.Diagnostics.AddAttributeError(request.Path, "Invalid value",
			fmt.Sprintf("%q is not accepted; the platform accepts %s.", request.ConfigValue.ValueString(), strings.Join(oneOf.values, ", ")))
	}
}

// costStringLengthValidator bounds a string's length in characters after the
// platform trims it.
type costStringLengthValidator struct {
	minimum int
	maximum int
}

func (length costStringLengthValidator) Description(_ context.Context) string {
	return fmt.Sprintf("value must be between %d and %d characters", length.minimum, length.maximum)
}

func (length costStringLengthValidator) MarkdownDescription(ctx context.Context) string {
	return length.Description(ctx)
}

func (length costStringLengthValidator) ValidateString(ctx context.Context, request validator.StringRequest, response *validator.StringResponse) {
	if request.ConfigValue.IsNull() || request.ConfigValue.IsUnknown() {
		return
	}
	count := len([]rune(strings.TrimSpace(request.ConfigValue.ValueString())))
	if count < length.minimum || count > length.maximum {
		response.Diagnostics.AddAttributeError(request.Path, "Invalid length", length.Description(ctx)+".")
	}
}

// costInt64RangeValidator bounds an integer inclusively.
type costInt64RangeValidator struct {
	minimum int64
	maximum int64
}

func (bounds costInt64RangeValidator) Description(_ context.Context) string {
	return fmt.Sprintf("value must be between %d and %d", bounds.minimum, bounds.maximum)
}

func (bounds costInt64RangeValidator) MarkdownDescription(ctx context.Context) string {
	return bounds.Description(ctx)
}

func (bounds costInt64RangeValidator) ValidateInt64(ctx context.Context, request validator.Int64Request, response *validator.Int64Response) {
	if request.ConfigValue.IsNull() || request.ConfigValue.IsUnknown() {
		return
	}
	value := request.ConfigValue.ValueInt64()
	if value < bounds.minimum || value > bounds.maximum {
		response.Diagnostics.AddAttributeError(request.Path, "Value out of range", bounds.Description(ctx)+".")
	}
}

// costFloat64RangeValidator bounds a number inclusively.
type costFloat64RangeValidator struct {
	minimum float64
	maximum float64
}

func (bounds costFloat64RangeValidator) Description(_ context.Context) string {
	return fmt.Sprintf("value must be between %g and %g", bounds.minimum, bounds.maximum)
}

func (bounds costFloat64RangeValidator) MarkdownDescription(ctx context.Context) string {
	return bounds.Description(ctx)
}

func (bounds costFloat64RangeValidator) ValidateFloat64(ctx context.Context, request validator.Float64Request, response *validator.Float64Response) {
	if request.ConfigValue.IsNull() || request.ConfigValue.IsUnknown() {
		return
	}
	value := request.ConfigValue.ValueFloat64()
	if value < bounds.minimum || value > bounds.maximum {
		response.Diagnostics.AddAttributeError(request.Path, "Value out of range", bounds.Description(ctx)+".")
	}
}

// costTimeFormatValidator refuses a string that does not parse with layout:
// time.RFC3339 for instants, "15:04" for the quiet hours' clock times.
type costTimeFormatValidator struct {
	layout  string
	example string
}

func (format costTimeFormatValidator) Description(_ context.Context) string {
	return "value must look like " + format.example
}

func (format costTimeFormatValidator) MarkdownDescription(ctx context.Context) string {
	return format.Description(ctx)
}

func (format costTimeFormatValidator) ValidateString(ctx context.Context, request validator.StringRequest, response *validator.StringResponse) {
	if request.ConfigValue.IsNull() || request.ConfigValue.IsUnknown() {
		return
	}
	value := request.ConfigValue.ValueString()
	if _, parseError := time.Parse(format.layout, value); parseError != nil || (format.layout == "15:04" && len(value) != 5) {
		response.Diagnostics.AddAttributeError(request.Path, "Invalid time", format.Description(ctx)+".")
	}
}

// costAutopilotDefaultsValidator refuses an environment kind or tier the
// platform does not know.
type costAutopilotDefaultsValidator struct{}

func (costAutopilotDefaultsValidator) Description(_ context.Context) string {
	return "keys must be environment kinds (" + strings.Join(costAutopilotKinds, ", ") +
		") and values tiers (" + strings.Join(costAutopilotTiers, ", ") + ")"
}

func (defaultsValidator costAutopilotDefaultsValidator) MarkdownDescription(ctx context.Context) string {
	return defaultsValidator.Description(ctx)
}

func (costAutopilotDefaultsValidator) ValidateMap(ctx context.Context, request validator.MapRequest, response *validator.MapResponse) {
	if request.ConfigValue.IsNull() || request.ConfigValue.IsUnknown() {
		return
	}
	defaults := map[string]string{}
	for kind, value := range request.ConfigValue.Elements() {
		tier, isString := value.(types.String)
		if !isString || tier.IsUnknown() || tier.IsNull() {
			continue
		}
		defaults[kind] = tier.ValueString()
	}
	kinds := make([]string, 0, len(defaults))
	for kind := range defaults {
		kinds = append(kinds, kind)
	}
	sort.Strings(kinds)
	for _, kind := range kinds {
		if !costContains(costAutopilotKinds, kind) {
			response.Diagnostics.AddAttributeError(request.Path, "Unknown environment kind",
				fmt.Sprintf("%q is not an environment kind; the kinds are %s.", kind, strings.Join(costAutopilotKinds, ", ")))
		}
		if !costContains(costAutopilotTiers, defaults[kind]) {
			response.Diagnostics.AddAttributeError(request.Path, "Unknown tier",
				fmt.Sprintf("%q is not a tier; the tiers are %s.", defaults[kind], strings.Join(costAutopilotTiers, ", ")))
		}
	}
}
