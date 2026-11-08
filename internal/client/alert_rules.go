// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package client

import (
	"context"
	"net/http"
	"net/url"
)

// alertRulesPath is the bearer surface of the organisation's alert
// definitions: the rules Ankra evaluates and opens incidents from.
const alertRulesPath = "/api/v1/org/alerts/alerts"

// AlertRule is one alert definition as the get route returns it. The bearer
// surface reports how many rules the alert holds, not the rules themselves.
type AlertRule struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description *string  `json:"description"`
	Enabled     bool     `json:"enabled"`
	State       string   `json:"state"`
	Status      string   `json:"status"`
	Severity    string   `json:"severity"`
	WebhookIDs  []string `json:"webhook_ids"`
	RuleCount   int      `json:"rule_count"`
}

// AlertRuleCondition is one condition of a rule; the conditions of a rule
// are joined by their logical operator.
type AlertRuleCondition struct {
	ConditionType     string   `json:"condition_type"`
	ConditionOperator string   `json:"condition_operator"`
	ConditionValue    string   `json:"condition_value"`
	ThresholdValue    *float64 `json:"threshold_value,omitempty"`
	LogicalOperator   *string  `json:"logical_operator,omitempty"`
}

// AlertRuleDefinition is one rule of an alert. A member left nil takes the
// platform's default: severity warning, enabled, a five-minute cooldown, and
// the evaluator's own minimum failure duration.
type AlertRuleDefinition struct {
	Name                      string               `json:"name"`
	Description               *string              `json:"description,omitempty"`
	ResourceType              string               `json:"resource_type"`
	ResourceID                *string              `json:"resource_id,omitempty"`
	ClusterIDs                []string             `json:"cluster_ids,omitempty"`
	AllClusters               *bool                `json:"all_clusters,omitempty"`
	Environments              []string             `json:"environments,omitempty"`
	ClusterResourceTypes      []string             `json:"cluster_resource_types,omitempty"`
	ResourceNamePattern       *string              `json:"resource_name_pattern,omitempty"`
	Severity                  *string              `json:"severity,omitempty"`
	Enabled                   *bool                `json:"enabled,omitempty"`
	CooldownMinutes           *int64               `json:"cooldown_minutes,omitempty"`
	MinFailureDurationMinutes *int64               `json:"min_failure_duration_minutes,omitempty"`
	Conditions                []AlertRuleCondition `json:"conditions"`
}

// AlertRuleWrite is the create and update body. On an update a nil member
// keeps the stored value; Rules, when sent, replaces the alert's whole rule
// set, and DestinationNames replaces the destinations it notifies.
type AlertRuleWrite struct {
	Name             string                `json:"name"`
	Description      *string               `json:"description,omitempty"`
	Enabled          *bool                 `json:"enabled,omitempty"`
	Rules            []AlertRuleDefinition `json:"rules,omitempty"`
	DestinationNames []string              `json:"integration_names"`
}

type alertRuleEnvelope struct {
	Item AlertRule `json:"item"`
}

func alertRulePath(alertID string) string {
	return alertRulesPath + "/" + url.PathEscape(alertID)
}

// GetAlertRule reads one alert definition. One that is gone, or outside a
// project-confined token's reach, is a NotFound APIError.
func (client *Client) GetAlertRule(ctx context.Context, alertID string) (*AlertRule, error) {
	var envelope alertRuleEnvelope
	if requestError := client.doRequest(ctx, http.MethodGet, alertRulePath(alertID), nil, &envelope); requestError != nil {
		return nil, requestError
	}
	return &envelope.Item, nil
}

// CreateAlertRule creates an alert definition and answers its id.
func (client *Client) CreateAlertRule(ctx context.Context, request AlertRuleWrite) (string, error) {
	var envelope alertRuleEnvelope
	if requestError := client.doRequest(ctx, http.MethodPost, alertRulesPath, request, &envelope); requestError != nil {
		return "", requestError
	}
	return envelope.Item.ID, nil
}

// UpdateAlertRule changes an alert definition. A rules member replaces the
// whole rule set: rules sent without an id are created and the stored ones
// are deleted.
func (client *Client) UpdateAlertRule(ctx context.Context, alertID string, request AlertRuleWrite) error {
	return client.doRequest(ctx, http.MethodPut, alertRulePath(alertID), request, nil)
}

// DeleteAlertRule deletes an alert definition and its rules.
func (client *Client) DeleteAlertRule(ctx context.Context, alertID string) error {
	return client.doRequest(ctx, http.MethodDelete, alertRulePath(alertID), nil, nil)
}
