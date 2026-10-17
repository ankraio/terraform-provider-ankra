// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package client

import (
	"context"
	"errors"
	"net/http"
	"net/url"
)

const costBudgetsPath = "/api/v1/org/cloud-cost/budgets"

// CostBudgetProjection is a budget's month as of the read. Every figure is
// nil while nothing in the budget's scope is priced.
type CostBudgetProjection struct {
	Status                 string `json:"status"`
	MonthToDateCents       *int64 `json:"month_to_date_cents"`
	ProjectedMonthEndCents *int64 `json:"projected_month_end_cents"`
	PercentOfBudget        *int64 `json:"percent_of_budget"`
}

// CostBudget is a monthly cost budget for one scope: the organisation, a
// project, a cluster, an environment label or an application. ScopeID is nil
// for the organisation scope.
type CostBudget struct {
	ID           string               `json:"id"`
	Name         string               `json:"name"`
	ScopeKind    string               `json:"scope_kind"`
	ScopeID      *string              `json:"scope_id"`
	ScopeName    string               `json:"scope_name"`
	MonthlyCents int64                `json:"monthly_cents"`
	Currency     string               `json:"currency"`
	OwnerUserID  *string              `json:"owner_user_id"`
	NotifyAtPct  int64                `json:"notify_at_pct"`
	Projection   CostBudgetProjection `json:"projection"`
}

// CostBudgetCreate is the create body. The platform refuses a field a budget
// does not have, so only budget fields are sent.
type CostBudgetCreate struct {
	Name         string  `json:"name"`
	ScopeKind    string  `json:"scope_kind"`
	ScopeID      *string `json:"scope_id,omitempty"`
	MonthlyCents int64   `json:"monthly_cents"`
	Currency     string  `json:"currency"`
	NotifyAtPct  int64   `json:"notify_at_pct"`
	OwnerUserID  *string `json:"owner_user_id,omitempty"`
}

// CostBudgetUpdate is the update body. A budget's scope cannot change, so
// scope_kind and scope_id are never sent: the platform answers 400 when they
// are. OwnerUserID is always sent: an explicit null clears the owner, while
// an absent key would keep it.
type CostBudgetUpdate struct {
	Name         string  `json:"name"`
	MonthlyCents int64   `json:"monthly_cents"`
	Currency     string  `json:"currency"`
	NotifyAtPct  int64   `json:"notify_at_pct"`
	OwnerUserID  *string `json:"owner_user_id"`
}

type costBudgetListEnvelope struct {
	Budgets []CostBudget `json:"budgets"`
}

// ErrCostBudgetNotVisible is GetCostBudget's answer when a budget is missing
// from a partial listing: the token could not list the whole organisation,
// and the partial listing hides organisation, environment and other-project
// budgets, so a missing budget may well still exist.
var ErrCostBudgetNotVisible = errors.New("the budget is not in the listing this token may read")

// ListCostBudgets returns every budget of the organisation across all of its
// projects. A token pinned to one project may not read the whole
// organisation, so a 403 falls back to the token's own project; isPartial
// then reports that budgets of other scopes are missing from the answer.
func (client *Client) ListCostBudgets(ctx context.Context) (budgets []CostBudget, isPartial bool, err error) {
	var envelope costBudgetListEnvelope
	err = client.doRequest(ctx, http.MethodGet, costBudgetsPath+"?project_id=all", nil, &envelope)
	var apiError *APIError
	if errors.As(err, &apiError) && apiError.StatusCode == http.StatusForbidden {
		envelope = costBudgetListEnvelope{}
		isPartial = true
		err = client.doRequest(ctx, http.MethodGet, costBudgetsPath, nil, &envelope)
	}
	if err != nil {
		return nil, false, err
	}
	return envelope.Budgets, isPartial, nil
}

// GetCostBudget finds one budget by id in the listing, which is the only read
// the platform offers. A missing id is a NotFound APIError when the whole
// organisation was listed, and ErrCostBudgetNotVisible when only a partial
// listing was available.
func (client *Client) GetCostBudget(ctx context.Context, budgetID string) (*CostBudget, error) {
	budgets, isPartial, err := client.ListCostBudgets(ctx)
	if err != nil {
		return nil, err
	}
	for index := range budgets {
		if budgets[index].ID == budgetID {
			return &budgets[index], nil
		}
	}
	if isPartial {
		return nil, ErrCostBudgetNotVisible
	}
	return nil, &APIError{Method: http.MethodGet, Path: costBudgetsPath, StatusCode: http.StatusNotFound, Body: "budget " + budgetID + " not found"}
}

// CreateCostBudget creates a budget. A scope that already has a budget is a
// 409.
func (client *Client) CreateCostBudget(ctx context.Context, request CostBudgetCreate) (*CostBudget, error) {
	var budget CostBudget
	if err := client.doRequest(ctx, http.MethodPost, costBudgetsPath, request, &budget); err != nil {
		return nil, err
	}
	return &budget, nil
}

// UpdateCostBudget changes a budget's name, amount, currency, threshold and
// owner.
func (client *Client) UpdateCostBudget(ctx context.Context, budgetID string, request CostBudgetUpdate) (*CostBudget, error) {
	var budget CostBudget
	if err := client.doRequest(ctx, http.MethodPut, costBudgetsPath+"/"+url.PathEscape(budgetID), request, &budget); err != nil {
		return nil, err
	}
	return &budget, nil
}

// DeleteCostBudget deletes a budget. A budget already gone answers a
// NotFound APIError, which the caller tells apart from a scope move.
func (client *Client) DeleteCostBudget(ctx context.Context, budgetID string) error {
	return client.doRequest(ctx, http.MethodDelete, costBudgetsPath+"/"+url.PathEscape(budgetID), nil, nil)
}
