// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"
	"math/big"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"ankra.io/terraform-provider-ankra/internal/client"
)

// costRateCardMaximumAmount is the platform's ceiling on one monthly rate.
var costRateCardMaximumAmount = big.NewRat(100_000_000_000, 1)

var (
	_ resource.Resource                   = (*costRateCardResource)(nil)
	_ resource.ResourceWithConfigure      = (*costRateCardResource)(nil)
	_ resource.ResourceWithImportState    = (*costRateCardResource)(nil)
	_ resource.ResourceWithValidateConfig = (*costRateCardResource)(nil)
)

type costRateCardResource struct {
	configuredClient
}

type costRateCardModel struct {
	ID                     types.String `tfsdk:"id"`
	CredentialID           types.String `tfsdk:"credential_id"`
	Currency               types.String `tfsdk:"currency"`
	VCPUMonthlyAmount      types.String `tfsdk:"vcpu_monthly_amount"`
	MemoryGBMonthlyAmount  types.String `tfsdk:"memory_gb_monthly_amount"`
	StorageGBMonthlyAmount types.String `tfsdk:"storage_gb_monthly_amount"`
}

// NewCostRateCardResource returns the ankra_cost_rate_card resource: the
// monthly rates that price clusters on private-cloud credentials (Proxmox,
// Morpheus, vSphere, MAAS), per credential or as the organisation fallback.
func NewCostRateCardResource() resource.Resource {
	return &costRateCardResource{}
}

func (card *costRateCardResource) Metadata(_ context.Context, request resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_cost_rate_card"
}

func (card *costRateCardResource) Schema(_ context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	amount := func(description string) schema.StringAttribute {
		return schema.StringAttribute{
			MarkdownDescription: description + " A decimal string, such as `\"12.50\"`. Defaults to `\"0\"`.",
			Optional:            true,
			Computed:            true,
			Default:             stringdefault.StaticString("0"),
			Validators:          []validator.String{costRateCardAmountValidator{}},
		}
	}
	response.Schema = schema.Schema{
		MarkdownDescription: "A cost rate card: the monthly rates that price clusters on private-cloud credentials (Proxmox, Morpheus, " +
			"vSphere and MAAS), where no provider price list exists. A card set on a credential prices that credential's clusters; " +
			"the organisation card (no `credential_id`) prices every private-cloud cluster whose credential has none. " +
			"Changes need organisation admin. Destroy removes the card, so its clusters fall back to the organisation card, or go unpriced.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "The credential id, or `organisation` for the organisation card.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"credential_id": schema.StringAttribute{
				MarkdownDescription: "Private-cloud credential the card prices. Leave unset for the organisation card. Changing it replaces the card.",
				Optional:            true,
				Validators:          []validator.String{securityUUIDValidator{}},
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"currency": schema.StringAttribute{
				MarkdownDescription: "Currency of the rates: " + strings.Join(costCurrencies, ", ") + ". Stored lowercased.",
				Required:            true,
				Validators:          []validator.String{costOneOfValidator{values: costCurrencies, isCaseInsensitive: true}},
			},
			"vcpu_monthly_amount":       amount("Monthly price of one vCPU."),
			"memory_gb_monthly_amount":  amount("Monthly price of one GB of memory."),
			"storage_gb_monthly_amount": amount("Monthly price of one GB of storage."),
		},
	}
}

// ValidateConfig refuses a card whose rates are all zero, which the platform
// rejects.
func (card *costRateCardResource) ValidateConfig(ctx context.Context, request resource.ValidateConfigRequest, response *resource.ValidateConfigResponse) {
	var config costRateCardModel
	response.Diagnostics.Append(request.Config.Get(ctx, &config)...)
	if response.Diagnostics.HasError() {
		return
	}
	total := new(big.Rat)
	for _, value := range []types.String{config.VCPUMonthlyAmount, config.MemoryGBMonthlyAmount, config.StorageGBMonthlyAmount} {
		if value.IsUnknown() {
			return
		}
		if value.IsNull() {
			continue
		}
		parsed, isNumber := new(big.Rat).SetString(strings.TrimSpace(value.ValueString()))
		if !isNumber {
			return
		}
		total.Add(total, parsed)
	}
	if total.Sign() <= 0 {
		response.Diagnostics.AddError("Rate card prices nothing",
			"At least one of vcpu_monthly_amount, memory_gb_monthly_amount and storage_gb_monthly_amount must be above zero.")
	}
}

func (card *costRateCardResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	card.write(ctx, request.Plan, &response.State, response.Private, &response.Diagnostics)
}

func (card *costRateCardResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	card.write(ctx, request.Plan, &response.State, response.Private, &response.Diagnostics)
}

func (card *costRateCardResource) write(ctx context.Context, plan tfsdk.Plan, state *tfsdk.State, private privateStateWriter, diagnostics *diag.Diagnostics) {
	apiClient := card.requireClient(diagnostics)
	if apiClient == nil {
		return
	}
	var model costRateCardModel
	diagnostics.Append(plan.Get(ctx, &model)...)
	if diagnostics.HasError() {
		return
	}
	stored, err := apiClient.PutCostRateCard(ctx, client.CostRateCardWrite{
		CredentialID: stringPointer(model.CredentialID),
		CostRateCardRates: client.CostRateCardRates{
			Currency:               strings.ToLower(strings.TrimSpace(model.Currency.ValueString())),
			VCPUMonthlyAmount:      strings.TrimSpace(model.VCPUMonthlyAmount.ValueString()),
			MemoryGBMonthlyAmount:  strings.TrimSpace(model.MemoryGBMonthlyAmount.ValueString()),
			StorageGBMonthlyAmount: strings.TrimSpace(model.StorageGBMonthlyAmount.ValueString()),
		},
	})
	if err != nil {
		diagnostics.AddError("Unable to write cost rate card",
			err.Error()+"\n\nA 400 naming the credential means it is not a private-cloud credential of the organisation.")
		return
	}
	model.apply(stored)
	diagnostics.Append(state.Set(ctx, &model)...)
	diagnostics.Append(trackScope(ctx, apiClient, private)...)
}

func (card *costRateCardResource) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	apiClient := card.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state costRateCardModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	stored, err := apiClient.GetCostRateCard(ctx, state.CredentialID.ValueString())
	if removeWhenNotFound(ctx, err, apiClient, response) {
		return
	}
	if err != nil {
		response.Diagnostics.AddError("Unable to read cost rate card", err.Error())
		return
	}
	state.apply(stored)
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
	response.Diagnostics.Append(trackScope(ctx, apiClient, response.Private)...)
}

func (card *costRateCardResource) Delete(ctx context.Context, request resource.DeleteRequest, response *resource.DeleteResponse) {
	apiClient := card.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state costRateCardModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	err := apiClient.DeleteCostRateCard(ctx, state.CredentialID.ValueString())
	if err == nil || deletedWhenNotFound(ctx, err, apiClient, request.Private, &response.Diagnostics) || response.Diagnostics.HasError() {
		return
	}
	response.Diagnostics.AddError("Unable to delete cost rate card", err.Error())
}

// ImportState imports `organisation` as the organisation card and any other
// id as that credential's card.
func (card *costRateCardResource) ImportState(ctx context.Context, request resource.ImportStateRequest, response *resource.ImportStateResponse) {
	if request.ID == client.CostRateCardOrganisation {
		response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("id"), client.CostRateCardOrganisation)...)
		return
	}
	response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("id"), request.ID)...)
	response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("credential_id"), request.ID)...)
}

func (model *costRateCardModel) apply(stored *client.CostRateCardRates) {
	if model.CredentialID.IsNull() || model.CredentialID.ValueString() == "" {
		model.CredentialID = types.StringNull()
		model.ID = types.StringValue(client.CostRateCardOrganisation)
	} else {
		model.ID = model.CredentialID
	}
	if !strings.EqualFold(strings.TrimSpace(model.Currency.ValueString()), stored.Currency) {
		model.Currency = types.StringValue(stored.Currency)
	}
	model.VCPUMonthlyAmount = costRateCardAmountState(model.VCPUMonthlyAmount, stored.VCPUMonthlyAmount)
	model.MemoryGBMonthlyAmount = costRateCardAmountState(model.MemoryGBMonthlyAmount, stored.MemoryGBMonthlyAmount)
	model.StorageGBMonthlyAmount = costRateCardAmountState(model.StorageGBMonthlyAmount, stored.StorageGBMonthlyAmount)
}

// costRateCardAmountState keeps the configured spelling of an amount the
// platform stores with another one ("3" read back as "3.000000"), so equal
// values never plan a change.
func costRateCardAmountState(prior types.String, stored string) types.String {
	if !prior.IsNull() && !prior.IsUnknown() {
		priorValue, isPriorNumber := new(big.Rat).SetString(strings.TrimSpace(prior.ValueString()))
		storedValue, isStoredNumber := new(big.Rat).SetString(strings.TrimSpace(stored))
		if isPriorNumber && isStoredNumber && priorValue.Cmp(storedValue) == 0 {
			return prior
		}
	}
	return types.StringValue(costRateCardCanonicalAmount(stored))
}

// costRateCardCanonicalAmount drops the trailing zeros the platform pads a
// stored amount with.
func costRateCardCanonicalAmount(stored string) string {
	parsed, isNumber := new(big.Rat).SetString(strings.TrimSpace(stored))
	if !isNumber {
		return stored
	}
	text := parsed.FloatString(6)
	text = strings.TrimRight(text, "0")
	return strings.TrimSuffix(text, ".")
}

// costRateCardAmountValidator refuses an amount the platform would: not a
// decimal, negative, or above its ceiling.
type costRateCardAmountValidator struct{}

func (costRateCardAmountValidator) Description(_ context.Context) string {
	return "must be a decimal number from 0 to 100000000000"
}

func (amountValidator costRateCardAmountValidator) MarkdownDescription(ctx context.Context) string {
	return amountValidator.Description(ctx)
}

func (amountValidator costRateCardAmountValidator) ValidateString(ctx context.Context, request validator.StringRequest, response *validator.StringResponse) {
	if request.ConfigValue.IsNull() || request.ConfigValue.IsUnknown() {
		return
	}
	text := strings.TrimSpace(request.ConfigValue.ValueString())
	parsed, isNumber := new(big.Rat).SetString(text)
	if !isNumber || strings.ContainsAny(text, "/eE") || parsed.Sign() < 0 || parsed.Cmp(costRateCardMaximumAmount) > 0 {
		response.Diagnostics.AddAttributeError(request.Path, "Invalid amount",
			fmt.Sprintf("%q %s.", request.ConfigValue.ValueString(), amountValidator.Description(ctx)))
	}
}
