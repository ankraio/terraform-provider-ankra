// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package client

import (
	"context"
	"net/http"
	"net/url"
)

const (
	apiTokensPath     = "/api/v1/org/account/tokens"
	serviceTokensPath = "/api/v1/org/organisation/service-tokens"
)

// OrganisationAPIToken is one personal API token as the platform lists it. The secret is
// never part of the listing, and ExpiresAt is a naive UTC timestamp.
type OrganisationAPIToken struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	ExpiresAt   string   `json:"expires_at"`
	CreatedAt   string   `json:"created_at"`
	RevokedAt   *string  `json:"revoked_at"`
	LastUsedAt  *string  `json:"last_used_at"`
	Revoked     bool     `json:"revoked"`
	Scopes      []string `json:"scopes"`
	ProjectID   *string  `json:"project_id"`
	ProjectName *string  `json:"project_name"`
}

// OrganisationAPITokenCreate is the create body. A nil ExpiresAt takes the platform's
// 90-day default; an explicit null would be a server error, so it is left
// out. Nil Scopes keeps the token at its user's full authority.
type OrganisationAPITokenCreate struct {
	Name      string   `json:"name"`
	ExpiresAt *string  `json:"expires_at,omitempty"`
	Scopes    []string `json:"scopes,omitempty"`
}

// OrganisationAPITokenCreated carries the token value, which the platform reveals only
// in this answer.
type OrganisationAPITokenCreated struct {
	ID        string `json:"id"`
	Token     string `json:"token"`
	ExpiresAt string `json:"expires_at"`
}

// OrganisationServiceToken is one organisation service token as the platform lists it.
type OrganisationServiceToken struct {
	ID          string   `json:"id"`
	AccountID   string   `json:"account_id"`
	AccountName string   `json:"account_name"`
	Label       string   `json:"label"`
	RoleSlug    string   `json:"role_slug"`
	ExpiresAt   string   `json:"expires_at"`
	CreatedAt   string   `json:"created_at"`
	Revoked     bool     `json:"revoked"`
	Scopes      []string `json:"scopes"`
	ProjectID   *string  `json:"project_id"`
}

// OrganisationServiceTokenCreate mints a token for the service account Name, creating
// the account with RoleSlug when the organisation has none of that name.
type OrganisationServiceTokenCreate struct {
	Name      string   `json:"name"`
	Label     string   `json:"label,omitempty"`
	RoleSlug  string   `json:"role_slug"`
	ExpiresAt *string  `json:"expires_at,omitempty"`
	Scopes    []string `json:"scopes,omitempty"`
}

// OrganisationServiceTokenCreated carries the token value, revealed only here.
type OrganisationServiceTokenCreated struct {
	ID          string `json:"id"`
	Token       string `json:"token"`
	AccountID   string `json:"account_id"`
	AccountName string `json:"account_name"`
	Label       string `json:"label"`
	RoleSlug    string `json:"role_slug"`
	ExpiresAt   string `json:"expires_at"`
}

type serviceTokenListEnvelope struct {
	Tokens []OrganisationServiceToken `json:"tokens"`
}

// ListAPITokens returns the token user's personal tokens in the current
// organisation, revoked ones included.
func (client *Client) ListAPITokens(ctx context.Context) ([]OrganisationAPIToken, error) {
	var tokens []OrganisationAPIToken
	if err := client.doRequest(ctx, http.MethodGet, apiTokensPath, nil, &tokens); err != nil {
		return nil, err
	}
	return tokens, nil
}

// GetAPIToken finds one personal token by id in the listing, the only read
// the platform offers. A missing id is a NotFound APIError.
func (client *Client) GetAPIToken(ctx context.Context, tokenID string) (*OrganisationAPIToken, error) {
	tokens, err := client.ListAPITokens(ctx)
	if err != nil {
		return nil, err
	}
	for index := range tokens {
		if tokens[index].ID == tokenID {
			return &tokens[index], nil
		}
	}
	return nil, &APIError{Method: http.MethodGet, Path: apiTokensPath, StatusCode: http.StatusNotFound, Body: "token " + tokenID + " not found"}
}

// CreateAPIToken mints a personal token.
func (client *Client) CreateAPIToken(ctx context.Context, request OrganisationAPITokenCreate) (*OrganisationAPITokenCreated, error) {
	var created OrganisationAPITokenCreated
	if err := client.doRequest(ctx, http.MethodPost, apiTokensPath, request, &created); err != nil {
		return nil, err
	}
	return &created, nil
}

// RevokeAPIToken revokes a personal token. The platform answers 409 for a
// token already revoked or gone, which is accepted: either way it no longer
// authenticates.
func (client *Client) RevokeAPIToken(ctx context.Context, tokenID string) error {
	return client.doRequest(ctx, http.MethodPost, apiTokensPath+"/"+url.PathEscape(tokenID)+"/revoke", nil, nil, http.StatusConflict)
}

// DeleteAPIToken deletes a revoked personal token. A token already gone is
// not an error.
func (client *Client) DeleteAPIToken(ctx context.Context, tokenID string) error {
	return client.doRequest(ctx, http.MethodDelete, apiTokensPath+"/"+url.PathEscape(tokenID), nil, nil, http.StatusNotFound)
}

// ListServiceTokens returns the organisation's service tokens, revoked ones
// included. It needs the members.manage permission.
func (client *Client) ListServiceTokens(ctx context.Context) ([]OrganisationServiceToken, error) {
	var envelope serviceTokenListEnvelope
	if err := client.doRequest(ctx, http.MethodGet, serviceTokensPath, nil, &envelope); err != nil {
		return nil, err
	}
	return envelope.Tokens, nil
}

// GetServiceToken finds one service token by id in the listing. A missing id
// is a NotFound APIError.
func (client *Client) GetServiceToken(ctx context.Context, tokenID string) (*OrganisationServiceToken, error) {
	tokens, err := client.ListServiceTokens(ctx)
	if err != nil {
		return nil, err
	}
	for index := range tokens {
		if tokens[index].ID == tokenID {
			return &tokens[index], nil
		}
	}
	return nil, &APIError{Method: http.MethodGet, Path: serviceTokensPath, StatusCode: http.StatusNotFound, Body: "service token " + tokenID + " not found"}
}

// CreateServiceToken mints an organisation service token.
func (client *Client) CreateServiceToken(ctx context.Context, request OrganisationServiceTokenCreate) (*OrganisationServiceTokenCreated, error) {
	var created OrganisationServiceTokenCreated
	if err := client.doRequest(ctx, http.MethodPost, serviceTokensPath, request, &created); err != nil {
		return nil, err
	}
	return &created, nil
}

// RevokeServiceToken revokes a service token. Already revoked (409) and gone
// (404) are both accepted.
func (client *Client) RevokeServiceToken(ctx context.Context, tokenID string) error {
	return client.doRequest(ctx, http.MethodPost, serviceTokensPath+"/"+url.PathEscape(tokenID)+"/revoke", nil, nil, http.StatusConflict, http.StatusNotFound)
}

// DeleteServiceToken deletes a revoked service token. A token already gone
// is not an error.
func (client *Client) DeleteServiceToken(ctx context.Context, tokenID string) error {
	return client.doRequest(ctx, http.MethodDelete, serviceTokensPath+"/"+url.PathEscape(tokenID), nil, nil, http.StatusNotFound)
}
