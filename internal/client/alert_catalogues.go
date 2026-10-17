// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package client

import (
	"context"
	"net/http"
)

const (
	alertSlackChannelsPath       = "/api/v1/org/alerts/integrations/slack/channels"
	alertTeamsChannelsPath       = "/api/v1/org/alerts/integrations/teams/channels"
	alertIngestCredentialsPath   = "/api/v1/org/alerts/ingest-credentials"
	organisationClusterZonesPath = "/api/v1/org/dns/cluster-zones"

	// AlertIngestWebhookPathPrefix is the path an ingest credential's
	// notifier posts to, followed by the credential id.
	AlertIngestWebhookPathPrefix = "/webhooks/alerts/"
)

// SlackChannel is one channel the Ankra bot can post to.
type SlackChannel struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	IsPrivate bool   `json:"is_private"`
}

// SlackChannelListing is the connected Slack workspace and its channels.
type SlackChannelListing struct {
	TeamID   string         `json:"team_id"`
	TeamName *string        `json:"team_name"`
	Channels []SlackChannel `json:"channels"`
}

// ListSlackChannels lists the channels of the organisation's connected Slack
// workspace. No connected workspace is a NotFound APIError.
func (client *Client) ListSlackChannels(ctx context.Context) (*SlackChannelListing, error) {
	var listing SlackChannelListing
	if err := client.doRequest(ctx, http.MethodGet, alertSlackChannelsPath, nil, &listing); err != nil {
		return nil, err
	}
	return &listing, nil
}

// TeamsChannel is one channel of a team the Ankra bot is installed in.
type TeamsChannel struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	TeamID   string `json:"team_id"`
	TeamName string `json:"team_name"`
	TenantID string `json:"tenant_id"`
}

type teamsChannelListing struct {
	Channels []TeamsChannel `json:"channels"`
}

// ListTeamsChannels lists the channels across the organisation's connected
// Microsoft Teams tenants. No connected tenant is a NotFound APIError.
func (client *Client) ListTeamsChannels(ctx context.Context) ([]TeamsChannel, error) {
	var listing teamsChannelListing
	if err := client.doRequest(ctx, http.MethodGet, alertTeamsChannelsPath, nil, &listing); err != nil {
		return nil, err
	}
	return listing.Channels, nil
}

// AlertIngestCredential is an alert ingest credential as listed. The token
// is only returned when the credential is minted, never here.
type AlertIngestCredential struct {
	ID         string  `json:"id"`
	Name       string  `json:"name"`
	ClusterID  *string `json:"cluster_id"`
	Scope      *string `json:"scope"`
	Enabled    bool    `json:"enabled"`
	CreatedAt  string  `json:"created_at"`
	UpdatedAt  string  `json:"updated_at"`
	LastUsedAt *string `json:"last_used_at"`
}

type alertIngestCredentialListing struct {
	Items []AlertIngestCredential `json:"items"`
}

// ListAlertIngestCredentials lists the organisation's alert ingest
// credentials.
func (client *Client) ListAlertIngestCredentials(ctx context.Context) ([]AlertIngestCredential, error) {
	var listing alertIngestCredentialListing
	if err := client.doRequest(ctx, http.MethodGet, alertIngestCredentialsPath, nil, &listing); err != nil {
		return nil, err
	}
	return listing.Items, nil
}

// ClusterDNSZone is one cluster's generated DNS domain and its
// reconciliation state.
type ClusterDNSZone struct {
	ClusterID   string  `json:"cluster_id"`
	ClusterName string  `json:"cluster_name"`
	FQDN        string  `json:"fqdn"`
	State       string  `json:"state"`
	LastError   *string `json:"last_error"`
}

type clusterDNSZoneListing struct {
	Items []ClusterDNSZone `json:"items"`
}

// ListClusterDNSZones lists every cluster DNS domain in the organisation.
func (client *Client) ListClusterDNSZones(ctx context.Context) ([]ClusterDNSZone, error) {
	var listing clusterDNSZoneListing
	if err := client.doRequest(ctx, http.MethodGet, organisationClusterZonesPath, nil, &listing); err != nil {
		return nil, err
	}
	return listing.Items, nil
}
