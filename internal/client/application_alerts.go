// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package client

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
)

// The alerts of one application on one of its environments, under
// /api/v1/org/applications/{application_id}/alerts. An application alert is
// stored as an organisation alert with one rule, so it also shows under
// ankra_alert_rule's routes; these are the routes that may change it.

// ApplicationAlertCondition is the one condition an application alert has:
// it fires when Metric reads above Threshold for HoldMinutes in a row.
// ConditionType is app_metric; Metric is failed_share_pct, p95_ms or
// restarts; Operator is gt, and an empty one is left out and reads as gt.
type ApplicationAlertCondition struct {
	ConditionType string  `json:"condition_type"`
	Metric        string  `json:"metric"`
	Operator      string  `json:"operator,omitempty"`
	Threshold     float64 `json:"threshold"`
	HoldMinutes   int64   `json:"hold_minutes"`
}

// ApplicationAlertDestination is one place an alert sends to: one of the
// organisation's alert destinations.
type ApplicationAlertDestination struct {
	IntegrationID string `json:"integration_id"`
	Type          string `json:"type"`
	Label         string `json:"label"`
}

// ApplicationAlert is one alert of an application's list. Own is true for an
// alert the application's routes made and may change; false for an
// organisation alert rule that watches the environment, which has no
// Condition or EnvironmentID.
type ApplicationAlert struct {
	AlertID       string                        `json:"alert_id"`
	RuleID        string                        `json:"rule_id"`
	Name          string                        `json:"name"`
	Own           bool                          `json:"own"`
	EnvironmentID *string                       `json:"environment_id"`
	Condition     *ApplicationAlertCondition    `json:"condition"`
	Severity      string                        `json:"severity"`
	SendTo        []ApplicationAlertDestination `json:"send_to"`
	Enabled       bool                          `json:"enabled"`
}

// ApplicationAlertList is an application's alerts, own alerts first and
// oldest first, at most Limit of them; Truncated says there were more.
type ApplicationAlertList struct {
	Alerts    []ApplicationAlert `json:"alerts"`
	Truncated bool               `json:"truncated"`
	Limit     int                `json:"limit"`
}

// ApplicationAlertWrite is the body of a create or a full update. SendTo
// holds 1 to 5 alert destination ids. A nil Severity is left out: a create
// then stores warning, an update keeps the stored one.
type ApplicationAlertWrite struct {
	Name          string                    `json:"name"`
	EnvironmentID string                    `json:"environment_id"`
	Condition     ApplicationAlertCondition `json:"condition"`
	SendTo        []string                  `json:"send_to"`
	Severity      *string                   `json:"severity,omitempty"`
}

func applicationAlertsPath(applicationID string) string {
	return applicationPath(applicationID, "/alerts")
}

func applicationAlertPath(applicationID string, alertID string) string {
	return applicationAlertsPath(applicationID) + "/" + url.PathEscape(alertID)
}

// ListApplicationAlerts reads the alerts of an application on every one of
// its environments. An application that is gone, or outside a
// project-confined token's reach, is a NotFound APIError.
func (client *Client) ListApplicationAlerts(ctx context.Context, applicationID string) (*ApplicationAlertList, error) {
	var list ApplicationAlertList
	if requestError := client.doRequest(ctx, http.MethodGet, applicationAlertsPath(applicationID), nil, &list); requestError != nil {
		return nil, requestError
	}
	return &list, nil
}

// GetApplicationAlert reads one of the application's own alerts. The
// platform has no route for one alert, so it is found in the application's
// list. It is a NotFound APIError only when the platform says so: the
// application is not found, or a complete list does not hold the alert. A
// list cut short without it, or an alert the list reports as not the
// application's own, is an ordinary error, because neither says the alert is
// gone.
func (client *Client) GetApplicationAlert(ctx context.Context, applicationID string, alertID string) (*ApplicationAlert, error) {
	list, listError := client.ListApplicationAlerts(ctx, applicationID)
	if listError != nil {
		return nil, listError
	}
	for index := range list.Alerts {
		alert := &list.Alerts[index]
		if alert.AlertID != alertID {
			continue
		}
		if !alert.Own {
			return nil, fmt.Errorf("alert %s is listed for application %s as an organisation alert rule, not one of the application's own alerts", alertID, applicationID)
		}
		return alert, nil
	}
	if list.Truncated {
		return nil, fmt.Errorf("alert %s is not among the first %d alerts the platform lists for application %s, so whether it still exists cannot be read",
			alertID, list.Limit, applicationID)
	}
	return nil, &APIError{Method: http.MethodGet, Path: applicationAlertsPath(applicationID), StatusCode: http.StatusNotFound,
		Body: fmt.Sprintf("the application's alerts do not include %s", alertID)}
}

// CreateApplicationAlert adds an alert on one environment of the
// application and answers the alert as stored.
func (client *Client) CreateApplicationAlert(ctx context.Context, applicationID string, request ApplicationAlertWrite) (*ApplicationAlert, error) {
	var created ApplicationAlert
	if requestError := client.doRequest(ctx, http.MethodPost, applicationAlertsPath(applicationID), request, &created); requestError != nil {
		return nil, requestError
	}
	return &created, nil
}

// UpdateApplicationAlert replaces the name, environment, condition and
// destinations of one of the application's own alerts, and its severity when
// request carries one, and answers the alert as stored.
func (client *Client) UpdateApplicationAlert(ctx context.Context, applicationID string, alertID string, request ApplicationAlertWrite) (*ApplicationAlert, error) {
	var updated ApplicationAlert
	if requestError := client.doRequest(ctx, http.MethodPut, applicationAlertPath(applicationID, alertID), request, &updated); requestError != nil {
		return nil, requestError
	}
	return &updated, nil
}

// DeleteApplicationAlert deletes one of the application's own alerts with
// its rule. An alert or application that is gone is a NotFound APIError.
func (client *Client) DeleteApplicationAlert(ctx context.Context, applicationID string, alertID string) error {
	return client.doRequest(ctx, http.MethodDelete, applicationAlertPath(applicationID, alertID), nil, nil)
}
