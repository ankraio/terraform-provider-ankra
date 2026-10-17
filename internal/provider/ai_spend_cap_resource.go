// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/float64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"ankra.io/terraform-provider-ankra/internal/client"
)

var (
	_ resource.Resource                = (*aiSpendCapResource)(nil)
	_ resource.ResourceWithConfigure   = (*aiSpendCapResource)(nil)
	_ resource.ResourceWithImportState = (*aiSpendCapResource)(nil)
	_ resource.ResourceWithModifyPlan  = (*aiSpendCapResource)(nil)
)

const aiSpendCapID = "ai_spend_cap"

// aiSpendCapDefaults is the uncapped state destroy restores for the caps the
// configuration set: no money override (the plan default applies) and no
// token budget.
var aiSpendCapDefaults = map[string]any{
	"daily_chat_usd_cap":   nil,
	"daily_token_soft_cap": nil,
	"daily_token_hard_cap": nil,
}

type aiSpendCapResource struct {
	configuredClient
}

type aiSpendCapModel struct {
	ID                types.String  `tfsdk:"id"`
	DailyChatUSDCap   types.Float64 `tfsdk:"daily_chat_usd_cap"`
	DailyTokenSoftCap types.Int64   `tfsdk:"daily_token_soft_cap"`
	DailyTokenHardCap types.Int64   `tfsdk:"daily_token_hard_cap"`
	PlanDefaultUSD    types.Float64 `tfsdk:"plan_default_usd"`
	EffectiveUSD      types.Float64 `tfsdk:"effective_usd"`
}

// NewAISpendCapResource returns the ankra_ai_spend_cap resource, the
// counterpart of `ankra org ai-spend-cap set`.
func NewAISpendCapResource() resource.Resource {
	return &aiSpendCapResource{}
}

func (spendCap *aiSpendCapResource) Metadata(_ context.Context, request resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_ai_spend_cap"
}

func (spendCap *aiSpendCapResource) Schema(_ context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: "The organisation's daily AI caps. The counterpart of `ankra org ai-spend-cap set`. One per organisation; " +
			"changes need the `billing.manage` permission.\n\n" +
			"Only the caps you set are managed: a cap left unset keeps whatever the organisation holds and is read back as a computed " +
			"value, and removing a cap from configuration stops managing it without clearing it. Destroy clears the caps the " +
			"configuration set, which leaves the money cap at the plan default and the token budget unlimited.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Always `ai_spend_cap`.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"daily_chat_usd_cap": schema.Float64Attribute{
				MarkdownDescription: "Daily money cap on chat, in USD; null means the plan default applies.",
				Optional:            true,
				Computed:            true,
				PlanModifiers:       []planmodifier.Float64{float64planmodifier.UseStateForUnknown()},
			},
			"daily_token_soft_cap": schema.Int64Attribute{
				MarkdownDescription: "Daily token soft cap: past it, unattended runs degrade to the quick tier for the rest of the UTC day.",
				Optional:            true,
				Computed:            true,
				PlanModifiers:       []planmodifier.Int64{int64planmodifier.UseStateForUnknown()},
			},
			"daily_token_hard_cap": schema.Int64Attribute{
				MarkdownDescription: "Daily token hard cap: past it, new unattended runs are refused until midnight UTC. It may not be below the soft cap.",
				Optional:            true,
				Computed:            true,
				PlanModifiers:       []planmodifier.Int64{int64planmodifier.UseStateForUnknown()},
			},
			"plan_default_usd": schema.Float64Attribute{
				MarkdownDescription: "The plan's default daily money cap.",
				Computed:            true,
			},
			"effective_usd": schema.Float64Attribute{
				MarkdownDescription: "The daily money cap in force.",
				Computed:            true,
			},
		},
	}
}

// aiSpendCapBody names the configured caps only.
func aiSpendCapBody(model aiSpendCapModel) map[string]any {
	body := map[string]any{}
	if !model.DailyChatUSDCap.IsNull() && !model.DailyChatUSDCap.IsUnknown() {
		body["daily_chat_usd_cap"] = model.DailyChatUSDCap.ValueFloat64()
	}
	if !model.DailyTokenSoftCap.IsNull() && !model.DailyTokenSoftCap.IsUnknown() {
		body["daily_token_soft_cap"] = model.DailyTokenSoftCap.ValueInt64()
	}
	if !model.DailyTokenHardCap.IsNull() && !model.DailyTokenHardCap.IsUnknown() {
		body["daily_token_hard_cap"] = model.DailyTokenHardCap.ValueInt64()
	}
	return body
}

// ModifyPlan plans an update when the set of configured caps changed, so
// the apply records which ones destroy clears.
func (spendCap *aiSpendCapResource) ModifyPlan(ctx context.Context, request resource.ModifyPlanRequest, response *resource.ModifyPlanResponse) {
	if request.State.Raw.IsNull() || request.Plan.Raw.IsNull() {
		return
	}
	var config aiSpendCapModel
	response.Diagnostics.Append(request.Config.Get(ctx, &config)...)
	if response.Diagnostics.HasError() {
		return
	}
	if organisationManagedKeysChanged(ctx, aiSpendCapBody(config), request.Private) {
		response.Diagnostics.Append(response.Plan.SetAttribute(ctx, path.Root("effective_usd"), types.Float64Unknown())...)
	}
}

func (spendCap *aiSpendCapResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	spendCap.write(ctx, request.Config, &response.State, response.Private, &response.Diagnostics)
}

func (spendCap *aiSpendCapResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	spendCap.write(ctx, request.Config, &response.State, response.Private, &response.Diagnostics)
}

func (spendCap *aiSpendCapResource) write(ctx context.Context, config tfsdk.Config, state *tfsdk.State, private privateStateWriter, diagnostics *diag.Diagnostics) {
	apiClient := spendCap.requireClient(diagnostics)
	if apiClient == nil {
		return
	}
	var model aiSpendCapModel
	diagnostics.Append(config.Get(ctx, &model)...)
	if diagnostics.HasError() {
		return
	}
	body := aiSpendCapBody(model)

	var stored *client.OrganisationAISpendCap
	var err error
	if len(body) == 0 {
		stored, err = apiClient.GetAISpendCap(ctx)
	} else {
		stored, err = apiClient.UpdateAISpendCap(ctx, body)
	}
	if err != nil {
		diagnostics.AddError("Unable to write AI spend cap", err.Error())
		return
	}
	aiSpendCapApply(&model, stored)
	diagnostics.Append(state.Set(ctx, &model)...)
	diagnostics.Append(organisationRecordManagedKeys(ctx, body, private)...)
	diagnostics.Append(trackScope(ctx, apiClient, private)...)
}

func (spendCap *aiSpendCapResource) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	apiClient := spendCap.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state aiSpendCapModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	stored, err := apiClient.GetAISpendCap(ctx)
	if err != nil {
		response.Diagnostics.AddError("Unable to read AI spend cap", err.Error())
		return
	}
	aiSpendCapApply(&state, stored)
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
	response.Diagnostics.Append(trackScope(ctx, apiClient, response.Private)...)
}

// Delete clears the caps the configuration set.
func (spendCap *aiSpendCapResource) Delete(ctx context.Context, request resource.DeleteRequest, response *resource.DeleteResponse) {
	apiClient := spendCap.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	body := organisationResetBody(organisationManagedKeys(ctx, request.Private), aiSpendCapDefaults)
	if len(body) == 0 {
		return
	}
	if _, err := apiClient.UpdateAISpendCap(ctx, body); err != nil {
		response.Diagnostics.AddError("Unable to clear AI spend cap", err.Error())
	}
}

// ImportState accepts any id: there is one cap per organisation.
func (spendCap *aiSpendCapResource) ImportState(ctx context.Context, _ resource.ImportStateRequest, response *resource.ImportStateResponse) {
	response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("id"), aiSpendCapID)...)
}

func aiSpendCapApply(model *aiSpendCapModel, stored *client.OrganisationAISpendCap) {
	model.ID = types.StringValue(aiSpendCapID)
	model.DailyChatUSDCap = types.Float64PointerValue(stored.DailyChatUSDCap)
	model.DailyTokenSoftCap = types.Int64PointerValue(stored.DailyTokenSoftCap)
	model.DailyTokenHardCap = types.Int64PointerValue(stored.DailyTokenHardCap)
	model.PlanDefaultUSD = types.Float64PointerValue(stored.PlanDefaultUSD)
	model.EffectiveUSD = types.Float64PointerValue(stored.EffectiveUSD)
}
