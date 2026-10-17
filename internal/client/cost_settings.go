// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package client

import (
	"context"
	"net/http"
)

const costSettingsPath = "/api/v1/org/cloud-cost/settings"

// CostSettingsDefaultCurrency is the display currency of an organisation that
// never saved its cost settings.
const CostSettingsDefaultCurrency = "eur"

// CostSettings is the organisation's pricing configuration.
// AnalysedClusterLimit is the limit in effect: the organisation's own, or the
// platform default (8) when it has not set one, so the read cannot tell an
// explicit 8 from the default.
type CostSettings struct {
	EffectiveDiscountPct         float64 `json:"effective_discount_pct"`
	Currency                     string  `json:"currency"`
	IncludeNetworkEgressEstimate bool    `json:"include_network_egress_estimate"`
	AnalysedClusterLimit         *int64  `json:"analysed_cluster_limit"`
}

// CostSettingsUpdate is the settings write. The first three fields are a full
// replace. AnalysedClusterLimit is always sent: null returns the limit to the
// platform default, while an absent key would keep the stored limit.
type CostSettingsUpdate struct {
	EffectiveDiscountPct         float64 `json:"effective_discount_pct"`
	Currency                     string  `json:"currency"`
	IncludeNetworkEgressEstimate bool    `json:"include_network_egress_estimate"`
	AnalysedClusterLimit         *int64  `json:"analysed_cluster_limit"`
}

// GetCostSettings returns the organisation's pricing configuration; an
// organisation that never saved one reads the platform defaults.
func (client *Client) GetCostSettings(ctx context.Context) (*CostSettings, error) {
	var settings CostSettings
	if err := client.doRequest(ctx, http.MethodGet, costSettingsPath, nil, &settings); err != nil {
		return nil, err
	}
	return &settings, nil
}

// UpdateCostSettings replaces the organisation's pricing configuration.
func (client *Client) UpdateCostSettings(ctx context.Context, request CostSettingsUpdate) (*CostSettings, error) {
	var settings CostSettings
	if err := client.doRequest(ctx, http.MethodPut, costSettingsPath, request, &settings); err != nil {
		return nil, err
	}
	return &settings, nil
}
