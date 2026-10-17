// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package client

import (
	"context"
	"net/http"
	"net/url"
)

const (
	aiSettingsPath    = "/api/v1/org/ai-settings"
	aiBoardIdentity   = "/api/v1/org/ai-tasks/board-identity"
	aiModelsPath      = aiSettingsPath + "/models"
	aiEndpointsPath   = aiSettingsPath + "/endpoints"
	aiLaneModelsPath  = aiSettingsPath + "/lane-models"
	aiAnthropicPath   = aiSettingsPath + "/anthropic"
	aiOpenRouterPath  = aiSettingsPath + "/openrouter"
	aiOpenAILegacy    = aiSettingsPath + "/openai-compatible"
	aiProviderSetPath = aiSettingsPath + "/provider"
)

// AIKeyStatus is the status of one stored provider key. The platform never
// returns the key itself, only a masked preview.
type AIKeyStatus struct {
	Configured bool    `json:"configured"`
	KeyPreview *string `json:"key_preview"`
}

// AIOpenAICompatibleStatus is the status of the legacy single
// OpenAI-compatible configuration.
type AIOpenAICompatibleStatus struct {
	Configured bool    `json:"configured"`
	BaseURL    *string `json:"base_url"`
	Model      *string `json:"model"`
	KeyPreview *string `json:"key_preview"`
}

// AIProviderStatus is GET /org/ai-settings: the active provider and which
// credentials are stored.
type AIProviderStatus struct {
	Provider         string                   `json:"provider"`
	Anthropic        AIKeyStatus              `json:"anthropic"`
	OpenAICompatible AIOpenAICompatibleStatus `json:"openai_compatible"`
	OpenRouter       AIKeyStatus              `json:"openrouter"`
}

// AIOpenAICompatibleConfiguration is the legacy single-endpoint write body;
// every field is required.
type AIOpenAICompatibleConfiguration struct {
	BaseURL string `json:"base_url"`
	APIKey  string `json:"api_key"`
	Model   string `json:"model"`
}

// AICatalogModel is one model catalogue entry. ID is nil for a built-in
// default the organisation has not customised yet; EndpointID is set for a
// model served by a custom OpenAI-compatible endpoint.
type AICatalogModel struct {
	ID                  *string `json:"id"`
	Key                 string  `json:"key"`
	DisplayName         string  `json:"display_name"`
	Description         string  `json:"description"`
	Provider            string  `json:"provider"`
	EndpointID          *string `json:"endpoint_id"`
	ModelID             string  `json:"model_id"`
	Tier                *string `json:"tier"`
	ContextWindowTokens int64   `json:"context_window_tokens"`
	MaxOutputTokens     int64   `json:"max_output_tokens"`
	SupportsTools       bool    `json:"supports_tools"`
	SupportsThinking    bool    `json:"supports_thinking"`
	SupportsImages      bool    `json:"supports_images"`
	IsEnabled           bool    `json:"is_enabled"`
	SortOrder           int64   `json:"sort_order"`
	IsDefault           bool    `json:"is_default"`
}

// AICatalogModelWrite is the create and update body. The update replaces the
// whole entry, so every field is always sent: a null endpoint_id moves the
// model back to the Ankra-managed route, an empty tier removes it from
// auto-routing, and zero token counts take the platform defaults.
type AICatalogModelWrite struct {
	Key                 string  `json:"key"`
	DisplayName         string  `json:"display_name"`
	Description         string  `json:"description"`
	EndpointID          *string `json:"endpoint_id"`
	ModelID             string  `json:"model_id"`
	Tier                string  `json:"tier"`
	ContextWindowTokens int64   `json:"context_window_tokens"`
	MaxOutputTokens     int64   `json:"max_output_tokens"`
	SupportsTools       bool    `json:"supports_tools"`
	SupportsThinking    bool    `json:"supports_thinking"`
	SupportsImages      bool    `json:"supports_images"`
	IsEnabled           bool    `json:"is_enabled"`
	SortOrder           int64   `json:"sort_order"`
}

// AIEndpoint is one custom OpenAI-compatible endpoint. Its key never leaves
// the platform; KeyPreview is masked.
type AIEndpoint struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Provider   string `json:"provider"`
	BaseURL    string `json:"base_url"`
	KeyPreview string `json:"key_preview"`
}

// AIEndpointWrite is the endpoint create and update body. On update an
// absent api_key keeps the stored key.
type AIEndpointWrite struct {
	Name    string  `json:"name"`
	BaseURL string  `json:"base_url"`
	APIKey  *string `json:"api_key,omitempty"`
}

// AILaneModel is one AI lane with the organisation's model selection. An
// empty SelectedModelKey means the lane follows its default tier.
type AILaneModel struct {
	Lane               string `json:"lane"`
	DisplayName        string `json:"display_name"`
	Description        string `json:"description"`
	DefaultTier        string `json:"default_tier"`
	SelectedModelKey   string `json:"selected_model_key"`
	EffectiveModelID   string `json:"effective_model_id"`
	EffectiveModelName string `json:"effective_model_name"`
	IsSelectionStale   bool   `json:"is_selection_stale"`
}

// AIBoardIdentity is the service principal the AI board's agents act as.
// Provisioned is false when none exists or its grant was revoked.
type AIBoardIdentity struct {
	Provisioned bool    `json:"provisioned"`
	Subject     string  `json:"subject"`
	RoleSlug    string  `json:"role_slug"`
	AnkraUserID *string `json:"ankra_user_id"`
}

type aiModelListEnvelope struct {
	Models []AICatalogModel `json:"models"`
}

type aiEndpointListEnvelope struct {
	Endpoints []AIEndpoint `json:"endpoints"`
}

type aiLaneModelListEnvelope struct {
	Lanes []AILaneModel `json:"lanes"`
}

func aiNotFound(path, detail string) error {
	return &APIError{Method: http.MethodGet, Path: path, StatusCode: http.StatusNotFound, Body: detail}
}

// GetAIProviderStatus returns the active provider and the stored credentials.
func (client *Client) GetAIProviderStatus(ctx context.Context) (*AIProviderStatus, error) {
	var status AIProviderStatus
	if err := client.doRequest(ctx, http.MethodGet, aiSettingsPath, nil, &status); err != nil {
		return nil, err
	}
	return &status, nil
}

// SetAIProvider switches the active provider. The platform refuses a
// provider whose credential has not been stored.
func (client *Client) SetAIProvider(ctx context.Context, provider string) (*AIProviderStatus, error) {
	var status AIProviderStatus
	if err := client.doRequest(ctx, http.MethodPut, aiProviderSetPath, map[string]string{"provider": provider}, &status); err != nil {
		return nil, err
	}
	return &status, nil
}

// SaveAIAnthropicKey validates and stores the organisation's Anthropic key.
func (client *Client) SaveAIAnthropicKey(ctx context.Context, apiKey string) (*AIKeyStatus, error) {
	var status AIKeyStatus
	if err := client.doRequest(ctx, http.MethodPost, aiAnthropicPath, map[string]string{"api_key": apiKey}, &status); err != nil {
		return nil, err
	}
	return &status, nil
}

// DeleteAIAnthropicKey removes the Anthropic key; the platform falls back to
// the Ankra provider when Anthropic was active.
func (client *Client) DeleteAIAnthropicKey(ctx context.Context) error {
	return client.doRequest(ctx, http.MethodDelete, aiAnthropicPath, nil, nil)
}

// SaveAIOpenRouterKey validates and stores the organisation's OpenRouter key.
func (client *Client) SaveAIOpenRouterKey(ctx context.Context, apiKey string) (*AIKeyStatus, error) {
	var status AIKeyStatus
	if err := client.doRequest(ctx, http.MethodPost, aiOpenRouterPath, map[string]string{"api_key": apiKey}, &status); err != nil {
		return nil, err
	}
	return &status, nil
}

// DeleteAIOpenRouterKey removes the OpenRouter key; the platform falls back to
// the Ankra provider when OpenRouter was active.
func (client *Client) DeleteAIOpenRouterKey(ctx context.Context) error {
	return client.doRequest(ctx, http.MethodDelete, aiOpenRouterPath, nil, nil)
}

// SaveAIOpenAICompatible validates and stores the legacy single
// OpenAI-compatible configuration.
func (client *Client) SaveAIOpenAICompatible(ctx context.Context, configuration AIOpenAICompatibleConfiguration) (*AIOpenAICompatibleStatus, error) {
	var status AIOpenAICompatibleStatus
	if err := client.doRequest(ctx, http.MethodPost, aiOpenAILegacy, configuration, &status); err != nil {
		return nil, err
	}
	return &status, nil
}

// DeleteAIOpenAICompatible removes the legacy OpenAI-compatible configuration.
func (client *Client) DeleteAIOpenAICompatible(ctx context.Context) error {
	return client.doRequest(ctx, http.MethodDelete, aiOpenAILegacy, nil, nil)
}

// ListAICatalogModels returns the organisation's model catalogue: its own
// entries once customised, otherwise the built-in defaults.
func (client *Client) ListAICatalogModels(ctx context.Context) ([]AICatalogModel, error) {
	var envelope aiModelListEnvelope
	if err := client.doRequest(ctx, http.MethodGet, aiModelsPath, nil, &envelope); err != nil {
		return nil, err
	}
	return envelope.Models, nil
}

// GetAICatalogModel finds one catalogue entry by row id or by key in the
// listing, the only read the platform offers. A miss is a NotFound APIError.
func (client *Client) GetAICatalogModel(ctx context.Context, reference string) (*AICatalogModel, error) {
	models, err := client.ListAICatalogModels(ctx)
	if err != nil {
		return nil, err
	}
	for index := range models {
		model := &models[index]
		if (model.ID != nil && *model.ID == reference) || model.Key == reference {
			return model, nil
		}
	}
	return nil, aiNotFound(aiModelsPath, "model "+reference+" not found")
}

// CreateAICatalogModel adds a catalogue entry. The first change to an
// uncustomised catalogue also stores the built-in defaults as entries.
func (client *Client) CreateAICatalogModel(ctx context.Context, write AICatalogModelWrite) (*AICatalogModel, error) {
	var model AICatalogModel
	if err := client.doRequest(ctx, http.MethodPost, aiModelsPath, write, &model); err != nil {
		return nil, err
	}
	return &model, nil
}

// UpdateAICatalogModel replaces a catalogue entry addressed by row id or key.
func (client *Client) UpdateAICatalogModel(ctx context.Context, reference string, write AICatalogModelWrite) (*AICatalogModel, error) {
	var model AICatalogModel
	if err := client.doRequest(ctx, http.MethodPut, aiModelsPath+"/"+url.PathEscape(reference), write, &model); err != nil {
		return nil, err
	}
	return &model, nil
}

// DeleteAICatalogModel deletes a catalogue entry addressed by row id or key.
// An entry already gone answers a NotFound APIError.
func (client *Client) DeleteAICatalogModel(ctx context.Context, reference string) error {
	return client.doRequest(ctx, http.MethodDelete, aiModelsPath+"/"+url.PathEscape(reference), nil, nil)
}

// ListAIEndpoints returns the organisation's custom endpoints.
func (client *Client) ListAIEndpoints(ctx context.Context) ([]AIEndpoint, error) {
	var envelope aiEndpointListEnvelope
	if err := client.doRequest(ctx, http.MethodGet, aiEndpointsPath, nil, &envelope); err != nil {
		return nil, err
	}
	return envelope.Endpoints, nil
}

// GetAIEndpoint finds one endpoint by id in the listing, the only read the
// platform offers. A miss is a NotFound APIError.
func (client *Client) GetAIEndpoint(ctx context.Context, endpointID string) (*AIEndpoint, error) {
	endpoints, err := client.ListAIEndpoints(ctx)
	if err != nil {
		return nil, err
	}
	for index := range endpoints {
		if endpoints[index].ID == endpointID {
			return &endpoints[index], nil
		}
	}
	return nil, aiNotFound(aiEndpointsPath, "endpoint "+endpointID+" not found")
}

// CreateAIEndpoint probes and stores an endpoint.
func (client *Client) CreateAIEndpoint(ctx context.Context, write AIEndpointWrite) (*AIEndpoint, error) {
	var endpoint AIEndpoint
	if err := client.doRequest(ctx, http.MethodPost, aiEndpointsPath, write, &endpoint); err != nil {
		return nil, err
	}
	return &endpoint, nil
}

// UpdateAIEndpoint renames, re-points or rotates the key of an endpoint.
func (client *Client) UpdateAIEndpoint(ctx context.Context, endpointID string, write AIEndpointWrite) (*AIEndpoint, error) {
	var endpoint AIEndpoint
	if err := client.doRequest(ctx, http.MethodPut, aiEndpointsPath+"/"+url.PathEscape(endpointID), write, &endpoint); err != nil {
		return nil, err
	}
	return &endpoint, nil
}

// DeleteAIEndpoint deletes an endpoint, its key and every catalogue model on
// it. An endpoint already gone answers a NotFound APIError.
func (client *Client) DeleteAIEndpoint(ctx context.Context, endpointID string) error {
	return client.doRequest(ctx, http.MethodDelete, aiEndpointsPath+"/"+url.PathEscape(endpointID), nil, nil)
}

// ListAILaneModels returns every AI lane with its model selection.
func (client *Client) ListAILaneModels(ctx context.Context) ([]AILaneModel, error) {
	var envelope aiLaneModelListEnvelope
	if err := client.doRequest(ctx, http.MethodGet, aiLaneModelsPath, nil, &envelope); err != nil {
		return nil, err
	}
	return envelope.Lanes, nil
}

// GetAILaneModel finds one lane in the listing. An unknown lane is a NotFound
// APIError.
func (client *Client) GetAILaneModel(ctx context.Context, lane string) (*AILaneModel, error) {
	lanes, err := client.ListAILaneModels(ctx)
	if err != nil {
		return nil, err
	}
	for index := range lanes {
		if lanes[index].Lane == lane {
			return &lanes[index], nil
		}
	}
	return nil, aiNotFound(aiLaneModelsPath, "lane "+lane+" not found")
}

// SetAILaneModel runs a lane on a catalogue key or an exact OpenRouter model
// id. A nil modelKey sends an explicit null, returning the lane to its
// default tier.
func (client *Client) SetAILaneModel(ctx context.Context, lane string, modelKey *string) ([]AILaneModel, error) {
	var envelope aiLaneModelListEnvelope
	body := map[string]*string{"model_key": modelKey}
	if err := client.doRequest(ctx, http.MethodPut, aiLaneModelsPath+"/"+url.PathEscape(lane), body, &envelope); err != nil {
		return nil, err
	}
	return envelope.Lanes, nil
}

// GetAIBoardIdentity reads the AI board's identity.
func (client *Client) GetAIBoardIdentity(ctx context.Context) (*AIBoardIdentity, error) {
	var identity AIBoardIdentity
	if err := client.doRequest(ctx, http.MethodGet, aiBoardIdentity, nil, &identity); err != nil {
		return nil, err
	}
	return &identity, nil
}

// ProvisionAIBoardIdentity provisions the board identity with a built-in
// role. An identity that already exists keeps its role: the answer carries
// the role it actually has.
func (client *Client) ProvisionAIBoardIdentity(ctx context.Context, roleSlug string) (*AIBoardIdentity, error) {
	var identity AIBoardIdentity
	if err := client.doRequest(ctx, http.MethodPost, aiBoardIdentity, map[string]string{"role_slug": roleSlug}, &identity); err != nil {
		return nil, err
	}
	return &identity, nil
}

// RevokeAIBoardIdentity stands the board identity down: the principal stays
// for attribution and loses every grant above the viewer floor.
func (client *Client) RevokeAIBoardIdentity(ctx context.Context) error {
	return client.doRequest(ctx, http.MethodDelete, aiBoardIdentity, nil, nil)
}
