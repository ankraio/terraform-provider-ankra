// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"errors"
	"math"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64default"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"ankra.io/terraform-provider-ankra/internal/client"
)

// costBudgetDefaultNotifyAtPct is the threshold the platform stores when a
// create names none.
const costBudgetDefaultNotifyAtPct = 80

var (
	_ resource.Resource                   = (*costBudgetResource)(nil)
	_ resource.ResourceWithConfigure      = (*costBudgetResource)(nil)
	_ resource.ResourceWithImportState    = (*costBudgetResource)(nil)
	_ resource.ResourceWithValidateConfig = (*costBudgetResource)(nil)
)

type costBudgetResource struct {
	configuredClient
}

type costBudgetModel struct {
	ID           types.String `tfsdk:"id"`
	Name         types.String `tfsdk:"name"`
	ScopeKind    types.String `tfsdk:"scope_kind"`
	ScopeID      types.String `tfsdk:"scope_id"`
	ScopeName    types.String `tfsdk:"scope_name"`
	MonthlyCents types.Int64  `tfsdk:"monthly_cents"`
	Currency     types.String `tfsdk:"currency"`
	NotifyAtPct  types.Int64  `tfsdk:"notify_at_pct"`
	OwnerUserID  types.String `tfsdk:"owner_user_id"`
}

// NewCostBudgetResource returns the ankra_cost_budget resource, the
// counterpart of `ankra cost budgets set` and `ankra cost budgets delete`.
func NewCostBudgetResource() resource.Resource {
	return &costBudgetResource{}
}

func (budgetResource *costBudgetResource) Metadata(_ context.Context, request resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_cost_budget"
}

func (budgetResource *costBudgetResource) Schema(_ context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: "A monthly cost budget for one scope: the organisation, a project, a cluster, an environment label or an application. " +
			"Each scope has at most one budget. The counterpart of `ankra cost budgets set` and `ankra cost budgets delete`; " +
			"changing a budget needs the `billing.manage` permission.\n\n" +
			"The platform has no read by id, so the budget is read from the listing. A token that may not list the whole organisation " +
			"sees only its own project's budgets; a budget missing from that partial listing is reported as an error rather than re-created. " +
			"A cluster or application budget whose scope is deleted drops out of the listing, so the next plan re-creates it and the platform " +
			"refuses that: remove such a budget from configuration together with its scope.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Identifier of the budget.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				MarkdownDescription: "Budget name, 1 to 120 characters.",
				Required:            true,
				Validators:          []validator.String{costStringLengthValidator{minimum: 1, maximum: 120}},
			},
			"scope_kind": schema.StringAttribute{
				MarkdownDescription: "What the budget covers: `organisation`, `project`, `cluster`, `environment` or `application`. " +
					"A budget's scope cannot change, so changing it replaces the budget.",
				Required:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
				Validators:    []validator.String{costOneOfValidator{values: costBudgetScopeKinds}},
			},
			"scope_id": schema.StringAttribute{
				MarkdownDescription: "The project, cluster or application id, or the environment label (stored lower-cased). " +
					"Leave unset for an `organisation` budget. Changing it replaces the budget.",
				Optional:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
				Validators:    []validator.String{platformServicesTrimmed{}},
			},
			"scope_name": schema.StringAttribute{
				MarkdownDescription: "Display name of the scope; empty for a cluster or application the organisation no longer has.",
				Computed:            true,
			},
			"monthly_cents": schema.Int64Attribute{
				MarkdownDescription: "Monthly budget in cents of `currency`. Must be positive.",
				Required:            true,
				Validators:          []validator.Int64{costInt64RangeValidator{minimum: 1, maximum: math.MaxInt64}},
			},
			"currency": schema.StringAttribute{
				MarkdownDescription: "Currency code of the budget, such as `eur` or `usd`; the platform stores it lower-cased.",
				Required:            true,
				Validators:          []validator.String{costOneOfValidator{values: costCurrencies, isCaseInsensitive: true}},
			},
			"notify_at_pct": schema.Int64Attribute{
				MarkdownDescription: "Percent of the budget at which the owner is told, 1 to 100. Defaults to `80`.",
				Optional:            true,
				Computed:            true,
				Default:             int64default.StaticInt64(costBudgetDefaultNotifyAtPct),
				Validators:          []validator.Int64{costInt64RangeValidator{minimum: 1, maximum: 100}},
			},
			"owner_user_id": schema.StringAttribute{
				MarkdownDescription: "Ankra user id of the member told about the budget. Removing it clears the owner.",
				Optional:            true,
			},
		},
	}
}

// ValidateConfig refuses at plan time the scope the platform would refuse at
// apply: an organisation budget names no scope_id, every other kind needs one.
func (budgetResource *costBudgetResource) ValidateConfig(ctx context.Context, request resource.ValidateConfigRequest, response *resource.ValidateConfigResponse) {
	var config costBudgetModel
	response.Diagnostics.Append(request.Config.Get(ctx, &config)...)
	if response.Diagnostics.HasError() || config.ScopeKind.IsUnknown() || config.ScopeKind.IsNull() || config.ScopeID.IsUnknown() {
		return
	}
	hasScopeID := !config.ScopeID.IsNull() && strings.TrimSpace(config.ScopeID.ValueString()) != ""
	switch {
	case config.ScopeKind.ValueString() == "organisation" && hasScopeID:
		response.Diagnostics.AddAttributeError(path.Root("scope_id"), "Unexpected scope_id", "An organisation budget names no scope_id.")
	case config.ScopeKind.ValueString() != "organisation" && !hasScopeID:
		response.Diagnostics.AddAttributeError(path.Root("scope_id"), "Missing scope_id",
			"A "+config.ScopeKind.ValueString()+" budget needs a scope_id.")
	}
}

func (budgetResource *costBudgetResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	apiClient := budgetResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var plan costBudgetModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}

	created, err := apiClient.CreateCostBudget(ctx, client.CostBudgetCreate{
		Name:         plan.Name.ValueString(),
		ScopeKind:    plan.ScopeKind.ValueString(),
		ScopeID:      stringPointer(plan.ScopeID),
		MonthlyCents: plan.MonthlyCents.ValueInt64(),
		Currency:     plan.Currency.ValueString(),
		NotifyAtPct:  plan.NotifyAtPct.ValueInt64(),
		OwnerUserID:  stringPointer(plan.OwnerUserID),
	})
	if err != nil {
		response.Diagnostics.AddError("Unable to create cost budget", err.Error())
		return
	}

	plan.ID = types.StringValue(created.ID)
	plan.ScopeName = types.StringValue(created.ScopeName)
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
}

func (budgetResource *costBudgetResource) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	apiClient := budgetResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state costBudgetModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}

	budget, err := apiClient.GetCostBudget(ctx, state.ID.ValueString())
	if removeWhenNotFound(ctx, err, response) {
		return
	}
	if errors.Is(err, client.ErrCostBudgetNotVisible) {
		response.Diagnostics.AddError("Cost budget not visible to this token",
			"The token may not list the whole organisation's budgets (project_id=all answered 403), and budget "+state.ID.ValueString()+
				" is not in the listing of its own project. It is most likely an organisation, environment or other-project budget that still exists, "+
				"so it is kept in state rather than re-created. Use a token that can read the whole organisation, or remove the resource with `terraform state rm` if the budget is really gone.")
		return
	}
	if err != nil {
		response.Diagnostics.AddError("Unable to read cost budget", err.Error())
		return
	}

	state.Name = types.StringValue(budget.Name)
	state.ScopeKind = types.StringValue(budget.ScopeKind)
	state.ScopeID = costKeepCaseInsensitive(state.ScopeID, budget.ScopeID)
	state.ScopeName = types.StringValue(budget.ScopeName)
	state.MonthlyCents = types.Int64Value(budget.MonthlyCents)
	state.Currency = costKeepCaseInsensitive(state.Currency, &budget.Currency)
	state.NotifyAtPct = types.Int64Value(budget.NotifyAtPct)
	state.OwnerUserID = costKeepCaseInsensitive(state.OwnerUserID, budget.OwnerUserID)
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
}

func (budgetResource *costBudgetResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	apiClient := budgetResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var plan costBudgetModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}

	updated, err := apiClient.UpdateCostBudget(ctx, plan.ID.ValueString(), client.CostBudgetUpdate{
		Name:         plan.Name.ValueString(),
		MonthlyCents: plan.MonthlyCents.ValueInt64(),
		Currency:     plan.Currency.ValueString(),
		NotifyAtPct:  plan.NotifyAtPct.ValueInt64(),
		OwnerUserID:  stringPointer(plan.OwnerUserID),
	})
	if err != nil {
		response.Diagnostics.AddError("Unable to update cost budget", err.Error())
		return
	}

	plan.ScopeName = types.StringValue(updated.ScopeName)
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
}

func (budgetResource *costBudgetResource) Delete(ctx context.Context, request resource.DeleteRequest, response *resource.DeleteResponse) {
	apiClient := budgetResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state costBudgetModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}

	if err := apiClient.DeleteCostBudget(ctx, state.ID.ValueString()); err != nil {
		response.Diagnostics.AddError("Unable to delete cost budget", err.Error())
	}
}

// ImportState imports by budget id, as `ankra cost budgets list` prints it.
func (budgetResource *costBudgetResource) ImportState(ctx context.Context, request resource.ImportStateRequest, response *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), request, response)
}

// costKeepCaseInsensitive maps a value the platform may have normalised to
// lower case: the configured spelling stays in state when it differs only in
// case, so it does not plan a diff, and any other difference is drift.
func costKeepCaseInsensitive(current types.String, remote *string) types.String {
	if remote == nil || *remote == "" {
		return types.StringNull()
	}
	if !current.IsNull() && !current.IsUnknown() && strings.EqualFold(current.ValueString(), *remote) {
		return current
	}
	return types.StringValue(*remote)
}
