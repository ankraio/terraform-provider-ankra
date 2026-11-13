// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package client

import (
	"context"
	"net/http"
	"net/url"
)

const addonDefinitionsPath = "/api/v1/org/helm/addon-definitions"

// AddonChartLink is an explicit include or exclude of one chart in a
// registry, overriding a definition's match rules for that chart.
type AddonChartLink struct {
	ChartName   string `json:"chart_name"`
	RegistryURL string `json:"registry_url"`
	LinkType    string `json:"link_type"`
	Source      string `json:"source,omitempty"`
}

// AddonDefinition is an add-on definition with its chart links. Source is
// "global" for Ankra's read-only definitions.
type AddonDefinition struct {
	ID                   string           `json:"id"`
	Name                 string           `json:"name"`
	DisplayName          string           `json:"display_name"`
	Description          *string          `json:"description"`
	WebsiteURL           *string          `json:"website_url"`
	DocumentationURL     *string          `json:"documentation_url"`
	IconURL              *string          `json:"icon_url"`
	Category             *string          `json:"category"`
	MatchChartNames      []string         `json:"match_chart_names"`
	MatchKeywords        []string         `json:"match_keywords"`
	MatchHomeURLPrefixes []string         `json:"match_home_url_prefixes"`
	Source               string           `json:"source"`
	ChartLinks           []AddonChartLink `json:"chart_links"`
}

// AddonDefinitionWrite is the body of a create (with Name) or a full
// replacement (without it).
type AddonDefinitionWrite struct {
	Name                 string   `json:"name,omitempty"`
	DisplayName          string   `json:"display_name"`
	Description          *string  `json:"description"`
	WebsiteURL           *string  `json:"website_url"`
	DocumentationURL     *string  `json:"documentation_url"`
	IconURL              *string  `json:"icon_url"`
	Category             *string  `json:"category"`
	MatchChartNames      []string `json:"match_chart_names"`
	MatchKeywords        []string `json:"match_keywords"`
	MatchHomeURLPrefixes []string `json:"match_home_url_prefixes"`
}

func addonDefinitionPath(name string) string {
	return addonDefinitionsPath + "/" + url.PathEscape(name)
}

// GetAddonDefinition reads one definition with its chart links.
func (client *Client) GetAddonDefinition(ctx context.Context, name string) (*AddonDefinition, error) {
	var definition AddonDefinition
	if requestError := client.doRequest(ctx, http.MethodGet, addonDefinitionPath(name), nil, &definition); requestError != nil {
		return nil, requestError
	}
	return &definition, nil
}

// CreateAddonDefinition creates an organisation definition; a taken name
// answers 409.
func (client *Client) CreateAddonDefinition(ctx context.Context, write AddonDefinitionWrite) (*AddonDefinition, error) {
	var definition AddonDefinition
	if requestError := client.doRequest(ctx, http.MethodPost, addonDefinitionsPath, write, &definition); requestError != nil {
		return nil, requestError
	}
	return &definition, nil
}

// UpdateAddonDefinition replaces an organisation definition's fields.
func (client *Client) UpdateAddonDefinition(ctx context.Context, name string, write AddonDefinitionWrite) (*AddonDefinition, error) {
	write.Name = ""
	var definition AddonDefinition
	if requestError := client.doRequest(ctx, http.MethodPut, addonDefinitionPath(name), write, &definition); requestError != nil {
		return nil, requestError
	}
	return &definition, nil
}

// DeleteAddonDefinition deletes an organisation definition.
func (client *Client) DeleteAddonDefinition(ctx context.Context, name string) error {
	return client.doRequest(ctx, http.MethodDelete, addonDefinitionPath(name), nil, nil)
}

// LinkAddonChart adds an include or exclude chart link.
func (client *Client) LinkAddonChart(ctx context.Context, name string, link AddonChartLink) error {
	link.Source = ""
	return client.doRequest(ctx, http.MethodPost, addonDefinitionPath(name)+"/chart-links", link, nil)
}

// UnlinkAddonChart removes one chart link.
func (client *Client) UnlinkAddonChart(ctx context.Context, name string, link AddonChartLink) error {
	query := url.Values{}
	query.Set("chart_name", link.ChartName)
	query.Set("registry_url", link.RegistryURL)
	query.Set("link_type", link.LinkType)
	return client.doRequest(ctx, http.MethodDelete, addonDefinitionPath(name)+"/chart-links?"+query.Encode(), nil, nil)
}
