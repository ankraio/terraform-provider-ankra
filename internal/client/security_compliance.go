// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package client

import (
	"context"
	"net/http"
	"net/url"
)

const securityComplianceFrameworksPath = "/api/v1/org/security/compliance/frameworks"

// SecurityComplianceFramework is one framework of the compliance catalogue
// and whether the organisation tracks it.
type SecurityComplianceFramework struct {
	Key                   string  `json:"key"`
	Title                 string  `json:"title"`
	ShortTitle            string  `json:"short_title"`
	Version               string  `json:"version"`
	Description           string  `json:"description"`
	ControlCount          int64   `json:"control_count"`
	AutomatedControlCount int64   `json:"automated_control_count"`
	Enabled               bool    `json:"enabled"`
	EnabledAt             *string `json:"enabled_at"`
}

type securityComplianceFrameworkList struct {
	Frameworks []SecurityComplianceFramework `json:"frameworks"`
}

// ListSecurityComplianceFrameworks returns the framework catalogue.
func (client *Client) ListSecurityComplianceFrameworks(ctx context.Context) ([]SecurityComplianceFramework, error) {
	var list securityComplianceFrameworkList
	if err := client.doRequest(ctx, http.MethodGet, securityComplianceFrameworksPath, nil, &list); err != nil {
		return nil, err
	}
	return list.Frameworks, nil
}

// GetSecurityComplianceFramework finds one framework by key in the
// catalogue. A key the catalogue does not carry is a NotFound APIError.
func (client *Client) GetSecurityComplianceFramework(ctx context.Context, key string) (*SecurityComplianceFramework, error) {
	frameworks, err := client.ListSecurityComplianceFrameworks(ctx)
	if err != nil {
		return nil, err
	}
	for index := range frameworks {
		if frameworks[index].Key == key {
			return &frameworks[index], nil
		}
	}
	return nil, &APIError{Method: http.MethodGet, Path: securityComplianceFrameworksPath, StatusCode: http.StatusNotFound, Body: "framework " + key + " not found"}
}

// SetSecurityComplianceFrameworkEnabled switches one framework on or off
// and returns its card.
func (client *Client) SetSecurityComplianceFrameworkEnabled(ctx context.Context, key string, enabled bool, acceptableStatuses ...int) (*SecurityComplianceFramework, error) {
	var framework SecurityComplianceFramework
	body := map[string]bool{"enabled": enabled}
	if err := client.doRequest(ctx, http.MethodPatch, securityComplianceFrameworksPath+"/"+url.PathEscape(key), body, &framework, acceptableStatuses...); err != nil {
		return nil, err
	}
	return &framework, nil
}
