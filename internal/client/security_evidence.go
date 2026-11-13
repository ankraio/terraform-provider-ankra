// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package client

import (
	"context"
	"net/http"
	"net/url"
)

const securityEvidenceDestinationsPath = "/api/v1/org/security/evidence/destinations"

// SecurityEvidenceDestinationConfig is the non-secret part of a
// destination: a Vanta document, a Wazuh relay URL or a webhook URL.
type SecurityEvidenceDestinationConfig struct {
	APIBaseURL string `json:"api_base_url,omitempty"`
	DocumentID string `json:"document_id,omitempty"`
	URL        string `json:"url,omitempty"`
}

// SecurityEvidenceDestination is a destination as listed; its credential is
// never returned. A revoked destination keeps its row with RevokedAt set.
type SecurityEvidenceDestination struct {
	ID                   string                            `json:"id"`
	Name                 string                            `json:"name"`
	Kind                 string                            `json:"kind"`
	Config               SecurityEvidenceDestinationConfig `json:"config"`
	Enabled              bool                              `json:"enabled"`
	CredentialConfigured bool                              `json:"credential_configured"`
	RevokedAt            *string                           `json:"revoked_at"`
}

// SecurityEvidenceDestinationCreate is the body of a create.
type SecurityEvidenceDestinationCreate struct {
	Name       string                            `json:"name"`
	Kind       string                            `json:"kind"`
	Config     SecurityEvidenceDestinationConfig `json:"config"`
	Credential map[string]string                 `json:"credential"`
}

type securityEvidenceDestinationListing struct {
	Result []SecurityEvidenceDestination `json:"result"`
}

// ListSecurityEvidenceDestinations lists the organisation's evidence
// destinations, revoked ones included.
func (client *Client) ListSecurityEvidenceDestinations(ctx context.Context) ([]SecurityEvidenceDestination, error) {
	var listing securityEvidenceDestinationListing
	if requestError := client.doRequest(ctx, http.MethodGet, securityEvidenceDestinationsPath, nil, &listing); requestError != nil {
		return nil, requestError
	}
	return listing.Result, nil
}

// CreateSecurityEvidenceDestination stores the credential and records the
// destination.
func (client *Client) CreateSecurityEvidenceDestination(ctx context.Context, create SecurityEvidenceDestinationCreate) (*SecurityEvidenceDestination, error) {
	var destination SecurityEvidenceDestination
	if requestError := client.doRequest(ctx, http.MethodPost, securityEvidenceDestinationsPath, create, &destination); requestError != nil {
		return nil, requestError
	}
	return &destination, nil
}

// RevokeSecurityEvidenceDestination disables a destination, deletes its
// credential and cancels its undelivered deliveries.
func (client *Client) RevokeSecurityEvidenceDestination(ctx context.Context, destinationID string) error {
	return client.doRequest(ctx, http.MethodPost, securityEvidenceDestinationsPath+"/"+url.PathEscape(destinationID)+"/revoke", nil, nil)
}
