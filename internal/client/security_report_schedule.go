// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package client

import (
	"context"
	"net/http"
	"net/url"
)

// SecurityReportSchedule is the recurring security report a cluster emails.
type SecurityReportSchedule struct {
	Frequency  string   `json:"frequency"`
	Recipients []string `json:"recipients"`
	Enabled    bool     `json:"enabled"`
	LastSentAt *string  `json:"last_sent_at"`
	NextDueAt  *string  `json:"next_due_at"`
}

// SecurityReportScheduleStatus is the read: Schedule is nil, and Configured
// false, until a schedule has been stored.
type SecurityReportScheduleStatus struct {
	Configured bool                    `json:"configured"`
	Schedule   *SecurityReportSchedule `json:"schedule"`
}

// SecurityReportScheduleWrite replaces a cluster's schedule: weekly or
// monthly, at most 20 recipients, and at least one while enabled. Recipients
// is always sent as a list, never null.
type SecurityReportScheduleWrite struct {
	Frequency  string   `json:"frequency"`
	Recipients []string `json:"recipients"`
	Enabled    bool     `json:"enabled"`
}

func securityReportSchedulePath(clusterID string) string {
	return "/api/v1/org/clusters/imported/" + url.PathEscape(clusterID) + "/security/report-schedule"
}

// GetSecurityReportSchedule reads a cluster's report schedule. A cluster
// outside the organisation is a 404.
func (client *Client) GetSecurityReportSchedule(ctx context.Context, clusterID string) (*SecurityReportScheduleStatus, error) {
	var status SecurityReportScheduleStatus
	if err := client.doRequest(ctx, http.MethodGet, securityReportSchedulePath(clusterID), nil, &status); err != nil {
		return nil, err
	}
	return &status, nil
}

// SetSecurityReportSchedule creates or replaces a cluster's report schedule
// and returns it as stored.
func (client *Client) SetSecurityReportSchedule(ctx context.Context, clusterID string, request SecurityReportScheduleWrite, acceptableStatuses ...int) (*SecurityReportScheduleStatus, error) {
	if request.Recipients == nil {
		request.Recipients = []string{}
	}
	var status SecurityReportScheduleStatus
	if err := client.doRequest(ctx, http.MethodPut, securityReportSchedulePath(clusterID), request, &status, acceptableStatuses...); err != nil {
		return nil, err
	}
	return &status, nil
}
