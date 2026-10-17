// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ datasource.DataSource              = (*costBudgetsDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*costBudgetsDataSource)(nil)
)

type costBudgetsDataSource struct {
	configuredDataSourceClient
}

type costBudgetItemModel struct {
	ID                     types.String `tfsdk:"id"`
	Name                   types.String `tfsdk:"name"`
	ScopeKind              types.String `tfsdk:"scope_kind"`
	ScopeID                types.String `tfsdk:"scope_id"`
	ScopeName              types.String `tfsdk:"scope_name"`
	MonthlyCents           types.Int64  `tfsdk:"monthly_cents"`
	Currency               types.String `tfsdk:"currency"`
	NotifyAtPct            types.Int64  `tfsdk:"notify_at_pct"`
	OwnerUserID            types.String `tfsdk:"owner_user_id"`
	Status                 types.String `tfsdk:"status"`
	MonthToDateCents       types.Int64  `tfsdk:"month_to_date_cents"`
	ProjectedMonthEndCents types.Int64  `tfsdk:"projected_month_end_cents"`
}

type costBudgetsDataSourceModel struct {
	ID      types.String          `tfsdk:"id"`
	Budgets []costBudgetItemModel `tfsdk:"budgets"`
}

// NewCostBudgetsDataSource returns the ankra_cost_budgets data source, the
// counterpart of `ankra cost budgets list`.
func NewCostBudgetsDataSource() datasource.DataSource {
	return &costBudgetsDataSource{}
}

func (budgetsDataSource *costBudgetsDataSource) Metadata(_ context.Context, request datasource.MetadataRequest, response *datasource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_cost_budgets"
}

func (budgetsDataSource *costBudgetsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, response *datasource.SchemaResponse) {
	computedString := func(description string) schema.StringAttribute {
		return schema.StringAttribute{MarkdownDescription: description, Computed: true}
	}
	computedInt64 := func(description string) schema.Int64Attribute {
		return schema.Int64Attribute{MarkdownDescription: description, Computed: true}
	}
	response.Schema = schema.Schema{
		MarkdownDescription: "Lists the organisation's cost budgets, largest first, with this month's projection. " +
			"The counterpart of `ankra cost budgets list`.",
		Attributes: map[string]schema.Attribute{
			"id": computedString("Placeholder identifier for the data source."),
			"budgets": schema.ListNestedAttribute{
				MarkdownDescription: "Budgets in the organisation.",
				Computed:            true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id":                        computedString("Identifier of the budget."),
						"name":                      computedString("Budget name."),
						"scope_kind":                computedString("`organisation`, `project`, `cluster`, `environment` or `application`."),
						"scope_id":                  computedString("Scope id or environment label; null for the organisation."),
						"scope_name":                computedString("Display name of the scope."),
						"monthly_cents":             computedInt64("Monthly budget in cents of `currency`."),
						"currency":                  computedString("Currency of the budget."),
						"notify_at_pct":             computedInt64("Percent of the budget at which the owner is told."),
						"owner_user_id":             computedString("Ankra user id of the owner, if any."),
						"status":                    computedString("This month: `under`, `approaching`, `crossing`, `over` or `unknown`."),
						"month_to_date_cents":       computedInt64("Spend so far this month; null while nothing in scope is priced."),
						"projected_month_end_cents": computedInt64("Projected spend at month end; null while nothing in scope is priced."),
					},
				},
			},
		},
	}
}

func (budgetsDataSource *costBudgetsDataSource) Read(ctx context.Context, _ datasource.ReadRequest, response *datasource.ReadResponse) {
	apiClient := budgetsDataSource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}

	budgets, err := apiClient.ListCostBudgets(ctx)
	if err != nil {
		response.Diagnostics.AddError("Unable to list cost budgets", err.Error())
		return
	}

	state := costBudgetsDataSourceModel{
		ID:      types.StringValue("ankra_cost_budgets"),
		Budgets: make([]costBudgetItemModel, 0, len(budgets)),
	}
	for _, budget := range budgets {
		state.Budgets = append(state.Budgets, costBudgetItemModel{
			ID:                     types.StringValue(budget.ID),
			Name:                   types.StringValue(budget.Name),
			ScopeKind:              types.StringValue(budget.ScopeKind),
			ScopeID:                types.StringPointerValue(budget.ScopeID),
			ScopeName:              types.StringValue(budget.ScopeName),
			MonthlyCents:           types.Int64Value(budget.MonthlyCents),
			Currency:               types.StringValue(budget.Currency),
			NotifyAtPct:            types.Int64Value(budget.NotifyAtPct),
			OwnerUserID:            types.StringPointerValue(budget.OwnerUserID),
			Status:                 types.StringValue(budget.Projection.Status),
			MonthToDateCents:       types.Int64PointerValue(budget.Projection.MonthToDateCents),
			ProjectedMonthEndCents: types.Int64PointerValue(budget.Projection.ProjectedMonthEndCents),
		})
	}
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
}
