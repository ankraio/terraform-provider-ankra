// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/float64default"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"ankra.io/terraform-provider-ankra/internal/client"
)

const (
	// costSettingsID is the fixed identifier of the organisation's one set of
	// pricing settings.
	costSettingsID = "cost_settings"
	// costSettingsDefaultAnalysedClusterLimit is the limit the platform applies
	// while an organisation has not set its own.
	costSettingsDefaultAnalysedClusterLimit = 8
)

var (
	_ resource.Resource                = (*costSettingsResource)(nil)
	_ resource.ResourceWithConfigure   = (*costSettingsResource)(nil)
	_ resource.ResourceWithImportState = (*costSettingsResource)(nil)
)

type costSettingsResource struct {
	configuredClient
}

type costSettingsModel struct {
	ID                           types.String  `tfsdk:"id"`
	EffectiveDiscountPct         types.Float64 `tfsdk:"effective_discount_pct"`
	Currency                     types.String  `tfsdk:"currency"`
	IncludeNetworkEgressEstimate types.Bool    `tfsdk:"include_network_egress_estimate"`
	AnalysedClusterLimit         types.Int64   `tfsdk:"analysed_cluster_limit"`
}

// NewCostSettingsResource returns the ankra_cost_settings resource, the
// counterpart of `ankra cost settings set`.
func NewCostSettingsResource() resource.Resource {
	return &costSettingsResource{}
}

func (settingsResource *costSettingsResource) Metadata(_ context.Context, request resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_cost_settings"
}

func (settingsResource *costSettingsResource) Schema(_ context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: "The organisation's cloud cost pricing settings. There is one set per organisation, so declare this resource at most once. " +
			"Creating it while the settings were already customised outside Terraform (and differ from the configuration) is refused: import it instead. " +
			"Destroying it restores the platform default (no discount, `eur`, no egress estimate, the default analysed-cluster limit) of each setting " +
			"the configuration set, and leaves the others as they are. After an import, before the first apply, every setting counts as set. " +
			"The counterpart of `ankra cost settings set`; it needs organisation admin.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Fixed identifier of the settings, `" + costSettingsID + "`.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"effective_discount_pct": schema.Float64Attribute{
				MarkdownDescription: "Discount in percent applied on top of list prices, 0 to 100. Defaults to `0`.",
				Optional:            true,
				Computed:            true,
				Default:             float64default.StaticFloat64(0),
				Validators:          []validator.Float64{costFloat64RangeValidator{minimum: 0, maximum: 100}},
			},
			"currency": schema.StringAttribute{
				MarkdownDescription: "Organisation display currency, lower case: `usd`, `eur`, `gbp`, `sek`, `nok`, `dkk`, `chf`, `pln`, `czk`, `jpy`, `cad`, `aud` or `inr`. Defaults to `eur`.",
				Optional:            true,
				Computed:            true,
				Default:             stringdefault.StaticString(client.CostSettingsDefaultCurrency),
				Validators:          []validator.String{costOneOfValidator{values: costCurrencies, isCaseInsensitive: true}},
			},
			"include_network_egress_estimate": schema.BoolAttribute{
				MarkdownDescription: "Whether a network egress estimate is included. Defaults to `false`.",
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(false),
			},
			"analysed_cluster_limit": schema.Int64Attribute{
				MarkdownDescription: "How many of the costliest priced clusters the savings model analyses, 1 to 50. " +
					"Unset uses the platform default (8). The platform reads back the limit in effect, so an explicit `8` " +
					"cannot be told apart from the default.",
				Optional:   true,
				Validators: []validator.Int64{costInt64RangeValidator{minimum: 1, maximum: 50}},
			},
		},
	}
}

func (settingsResource *costSettingsResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	apiClient := settingsResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var plan costSettingsModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}
	current, err := apiClient.GetCostSettings(ctx)
	if err != nil {
		response.Diagnostics.AddError("Unable to read cost settings", err.Error())
		return
	}
	if costSettingsCustomised(current) && !costSettingsMatch(current, plan) {
		response.Diagnostics.AddError("Cost settings already customised",
			"The organisation's cost settings were changed outside Terraform. Creating this resource would overwrite them, so import it instead: "+
				"terraform import <address> "+costSettingsID)
		return
	}
	if _, err := apiClient.UpdateCostSettings(ctx, costSettingsUpdateFromModel(plan)); err != nil {
		response.Diagnostics.AddError("Unable to write cost settings", err.Error())
		return
	}
	plan.ID = types.StringValue(costSettingsID)
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
	costRecordConfiguredAttributes(ctx, request.Config, costSettingsAttributes, response.Private, &response.Diagnostics)
}

// costSettingsAttributes are the settings the configuration may set.
var costSettingsAttributes = []string{"effective_discount_pct", "currency", "include_network_egress_estimate", "analysed_cluster_limit"}

// costSettingsCustomised reports whether the organisation's settings differ
// from the platform defaults.
func costSettingsCustomised(settings *client.CostSettings) bool {
	return settings.EffectiveDiscountPct != 0 || !strings.EqualFold(settings.Currency, client.CostSettingsDefaultCurrency) ||
		settings.IncludeNetworkEgressEstimate ||
		(settings.AnalysedClusterLimit != nil && *settings.AnalysedClusterLimit != costSettingsDefaultAnalysedClusterLimit)
}

// costSettingsMatch reports whether the stored settings already are what the
// plan declares, in which case adopting them overwrites nothing.
func costSettingsMatch(settings *client.CostSettings, plan costSettingsModel) bool {
	limit := int64(costSettingsDefaultAnalysedClusterLimit)
	if !plan.AnalysedClusterLimit.IsNull() {
		limit = plan.AnalysedClusterLimit.ValueInt64()
	}
	storedLimit := int64(costSettingsDefaultAnalysedClusterLimit)
	if settings.AnalysedClusterLimit != nil {
		storedLimit = *settings.AnalysedClusterLimit
	}
	return settings.EffectiveDiscountPct == plan.EffectiveDiscountPct.ValueFloat64() &&
		strings.EqualFold(settings.Currency, plan.Currency.ValueString()) &&
		settings.IncludeNetworkEgressEstimate == plan.IncludeNetworkEgressEstimate.ValueBool() &&
		storedLimit == limit
}

func (settingsResource *costSettingsResource) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	apiClient := settingsResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state costSettingsModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}

	settings, err := apiClient.GetCostSettings(ctx)
	if err != nil {
		response.Diagnostics.AddError("Unable to read cost settings", err.Error())
		return
	}

	state.ID = types.StringValue(costSettingsID)
	state.EffectiveDiscountPct = types.Float64Value(settings.EffectiveDiscountPct)
	if state.Currency.IsNull() || !strings.EqualFold(state.Currency.ValueString(), settings.Currency) {
		state.Currency = types.StringValue(settings.Currency)
	}
	state.IncludeNetworkEgressEstimate = types.BoolValue(settings.IncludeNetworkEgressEstimate)
	state.AnalysedClusterLimit = costObservedAnalysedClusterLimit(state.AnalysedClusterLimit, settings.AnalysedClusterLimit)
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
}

func (settingsResource *costSettingsResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	apiClient := settingsResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var plan costSettingsModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}
	if _, err := apiClient.UpdateCostSettings(ctx, costSettingsUpdateFromModel(plan)); err != nil {
		response.Diagnostics.AddError("Unable to write cost settings", err.Error())
		return
	}
	plan.ID = types.StringValue(costSettingsID)
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
	costRecordConfiguredAttributes(ctx, request.Config, costSettingsAttributes, response.Private, &response.Diagnostics)
}

// Delete restores the platform default of every setting the configuration
// set, and leaves the others as they are now.
func (settingsResource *costSettingsResource) Delete(ctx context.Context, request resource.DeleteRequest, response *resource.DeleteResponse) {
	apiClient := settingsResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	configured := costConfiguredAttributes(ctx, costSettingsAttributes, request.Private, &response.Diagnostics)
	current, err := apiClient.GetCostSettings(ctx)
	if err != nil {
		response.Diagnostics.AddError("Unable to read cost settings", err.Error())
		return
	}
	reset := client.CostSettingsUpdate{
		EffectiveDiscountPct:         current.EffectiveDiscountPct,
		Currency:                     current.Currency,
		IncludeNetworkEgressEstimate: current.IncludeNetworkEgressEstimate,
	}
	if current.AnalysedClusterLimit != nil && *current.AnalysedClusterLimit != costSettingsDefaultAnalysedClusterLimit {
		reset.AnalysedClusterLimit = current.AnalysedClusterLimit
	}
	if configured["effective_discount_pct"] {
		reset.EffectiveDiscountPct = 0
	}
	if configured["currency"] {
		reset.Currency = client.CostSettingsDefaultCurrency
	}
	if configured["include_network_egress_estimate"] {
		reset.IncludeNetworkEgressEstimate = false
	}
	if configured["analysed_cluster_limit"] {
		reset.AnalysedClusterLimit = nil
	}
	if _, err := apiClient.UpdateCostSettings(ctx, reset); err != nil {
		response.Diagnostics.AddError("Unable to reset cost settings", err.Error())
	}
}

// ImportState accepts any identifier; the organisation has one set of
// settings.
func (settingsResource *costSettingsResource) ImportState(ctx context.Context, _ resource.ImportStateRequest, response *resource.ImportStateResponse) {
	response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("id"), costSettingsID)...)
}

func costSettingsUpdateFromModel(model costSettingsModel) client.CostSettingsUpdate {
	return client.CostSettingsUpdate{
		EffectiveDiscountPct:         model.EffectiveDiscountPct.ValueFloat64(),
		Currency:                     strings.ToLower(model.Currency.ValueString()),
		IncludeNetworkEgressEstimate: model.IncludeNetworkEgressEstimate.ValueBool(),
		AnalysedClusterLimit:         int64Pointer(model.AnalysedClusterLimit),
	}
}

// costObservedAnalysedClusterLimit maps the limit in effect to state. The
// platform answers the default (8) when none is set, so an unset limit stays
// unset while the platform reads 8; any other value is drift.
func costObservedAnalysedClusterLimit(current types.Int64, remote *int64) types.Int64 {
	if remote == nil {
		return current
	}
	if current.IsNull() && *remote == costSettingsDefaultAnalysedClusterLimit {
		return current
	}
	return types.Int64Value(*remote)
}
