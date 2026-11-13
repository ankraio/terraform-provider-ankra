// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package client

import (
	"context"
	"net/http"
	"net/url"
)

const (
	aiTicketSettingsPath        = "/api/v1/org/ai-tickets/settings"
	aiTicketProjectChannelsPath = aiTicketSettingsPath + "/projects"
	aiTicketSyncConnectionsPath = "/api/v1/org/ai-tickets/sync/connections"
)

// AITicketDeskSettings are the organisation's ticket desk settings. An
// organisation that never saved any reads the defaults.
type AITicketDeskSettings struct {
	SlackChannelID            *string  `json:"slack_channel_id"`
	TeamsConversationID       *string  `json:"teams_conversation_id"`
	ReviewerAgentTaskID       *string  `json:"reviewer_agent_task_id"`
	RequireAgentReview        bool     `json:"require_agent_review"`
	AutoFileInsightSeverities []string `json:"auto_file_insight_severities"`
	NotifyMode                string   `json:"notify_mode"`
	NotifyMinPriority         string   `json:"notify_min_priority"`
	NotifySourceKinds         []string `json:"notify_source_kinds"`
	NotifyEventScope          string   `json:"notify_event_scope"`
	NotifyClusterEnvironments []string `json:"notify_cluster_environments"`
}

// AITicketDeskSettingsUpdate is a partial write: a key left out keeps its
// stored value.
type AITicketDeskSettingsUpdate map[string]any

// GetAITicketDeskSettings reads the ticket desk settings.
func (client *Client) GetAITicketDeskSettings(ctx context.Context) (*AITicketDeskSettings, error) {
	var settings AITicketDeskSettings
	if requestError := client.doRequest(ctx, http.MethodGet, aiTicketSettingsPath, nil, &settings); requestError != nil {
		return nil, requestError
	}
	return &settings, nil
}

// UpdateAITicketDeskSettings writes the keys update names. Organisation
// admins only.
func (client *Client) UpdateAITicketDeskSettings(ctx context.Context, update AITicketDeskSettingsUpdate) (*AITicketDeskSettings, error) {
	var settings AITicketDeskSettings
	if requestError := client.doRequest(ctx, http.MethodPut, aiTicketSettingsPath, update, &settings); requestError != nil {
		return nil, requestError
	}
	return &settings, nil
}

// AITicketProjectChannels are the Slack channel and Teams conversation one
// project's board threads go to instead of the organisation's.
type AITicketProjectChannels struct {
	ProjectID           string  `json:"project_id"`
	ProjectName         string  `json:"project_name"`
	SlackChannelID      *string `json:"slack_channel_id"`
	TeamsConversationID *string `json:"teams_conversation_id"`
}

type aiTicketProjectChannelsListing struct {
	Projects []AITicketProjectChannels `json:"projects"`
}

// AITicketProjectChannelsUpdate always carries both keys: null clears one.
type AITicketProjectChannelsUpdate struct {
	SlackChannelID      *string `json:"slack_channel_id"`
	TeamsConversationID *string `json:"teams_conversation_id"`
}

// ListAITicketProjectChannels lists the projects that post to channels of
// their own.
func (client *Client) ListAITicketProjectChannels(ctx context.Context) ([]AITicketProjectChannels, error) {
	var listing aiTicketProjectChannelsListing
	if requestError := client.doRequest(ctx, http.MethodGet, aiTicketProjectChannelsPath, nil, &listing); requestError != nil {
		return nil, requestError
	}
	return listing.Projects, nil
}

// SetAITicketProjectChannels sets a project's channels. Organisation admins
// only.
func (client *Client) SetAITicketProjectChannels(ctx context.Context, projectID string, update AITicketProjectChannelsUpdate) error {
	return client.doRequest(ctx, http.MethodPut, aiTicketProjectChannelsPath+"/"+url.PathEscape(projectID), update, nil)
}

// RemoveAITicketProjectChannels sends a project's board threads back to the
// organisation's channels.
func (client *Client) RemoveAITicketProjectChannels(ctx context.Context, projectID string) error {
	return client.doRequest(ctx, http.MethodDelete, aiTicketProjectChannelsPath+"/"+url.PathEscape(projectID), nil, nil)
}

// AITicketSyncConnection is an outbound ticket sync connection. Its stored
// credential is never returned; CredentialConfigured says whether one is held.
type AITicketSyncConnection struct {
	ID                   string         `json:"id"`
	Name                 string         `json:"name"`
	Provider             string         `json:"provider"`
	Config               map[string]any `json:"config"`
	Enabled              bool           `json:"enabled"`
	CredentialConfigured bool           `json:"credential_configured"`
	SyncKinds            []string       `json:"sync_kinds"`
	SyncMinPriority      string         `json:"sync_min_priority"`
	SyncEventScope       string         `json:"sync_event_scope"`
	LastSyncedAt         *string        `json:"last_synced_at"`
	LastError            *string        `json:"last_error"`
}

// AITicketSyncConnectionWrite is the body of a create or update. On update
// Provider is left out, and a nil member keeps its stored value; a non-empty
// Credential replaces the stored secret.
type AITicketSyncConnectionWrite struct {
	Name            *string           `json:"name,omitempty"`
	Provider        string            `json:"provider,omitempty"`
	Config          map[string]string `json:"config,omitempty"`
	Credential      map[string]string `json:"credential,omitempty"`
	Enabled         *bool             `json:"enabled,omitempty"`
	SyncKinds       []string          `json:"sync_kinds,omitempty"`
	SyncMinPriority *string           `json:"sync_min_priority,omitempty"`
	SyncEventScope  *string           `json:"sync_event_scope,omitempty"`
}

// GetAITicketSyncConnection reads one sync connection.
func (client *Client) GetAITicketSyncConnection(ctx context.Context, connectionID string) (*AITicketSyncConnection, error) {
	var connection AITicketSyncConnection
	if requestError := client.doRequest(ctx, http.MethodGet, aiTicketSyncConnectionsPath+"/"+url.PathEscape(connectionID), nil, &connection); requestError != nil {
		return nil, requestError
	}
	return &connection, nil
}

// CreateAITicketSyncConnection creates a sync connection. The platform
// answers 409 for a taken name and 502 when the tracker rejects the setup.
func (client *Client) CreateAITicketSyncConnection(ctx context.Context, write AITicketSyncConnectionWrite) (*AITicketSyncConnection, error) {
	var connection AITicketSyncConnection
	if requestError := client.doRequest(ctx, http.MethodPost, aiTicketSyncConnectionsPath, write, &connection); requestError != nil {
		return nil, requestError
	}
	return &connection, nil
}

// UpdateAITicketSyncConnection updates the members write names.
func (client *Client) UpdateAITicketSyncConnection(ctx context.Context, connectionID string, write AITicketSyncConnectionWrite) (*AITicketSyncConnection, error) {
	write.Provider = ""
	var connection AITicketSyncConnection
	if requestError := client.doRequest(ctx, http.MethodPut, aiTicketSyncConnectionsPath+"/"+url.PathEscape(connectionID), write, &connection); requestError != nil {
		return nil, requestError
	}
	return &connection, nil
}

// DeleteAITicketSyncConnection deletes a sync connection and its stored
// credential.
func (client *Client) DeleteAITicketSyncConnection(ctx context.Context, connectionID string) error {
	return client.doRequest(ctx, http.MethodDelete, aiTicketSyncConnectionsPath+"/"+url.PathEscape(connectionID), nil, nil)
}
