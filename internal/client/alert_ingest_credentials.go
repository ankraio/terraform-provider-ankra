// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package client

import (
	"context"
	"net/http"
	"net/url"
)

// AlertIngestCredentialCreate is the body that mints an ingest credential.
type AlertIngestCredentialCreate struct {
	Name      string  `json:"name"`
	ClusterID *string `json:"cluster_id"`
	Enabled   bool    `json:"enabled"`
}

// AlertIngestCredentialMinted is the answer to a create: the only time the
// platform returns the token.
type AlertIngestCredentialMinted struct {
	Item        AlertIngestCredential `json:"item"`
	Token       string                `json:"token"`
	WebhookPath string                `json:"webhook_path"`
}

// AlertIngestCredentialRebind always carries cluster_id (null unpins) and
// the scope.
type AlertIngestCredentialRebind struct {
	ClusterID *string `json:"cluster_id"`
	Scope     string  `json:"scope,omitempty"`
}

type alertIngestCredentialRebound struct {
	Item AlertIngestCredential `json:"item"`
}

// CreateAlertIngestCredential mints an alert ingest credential.
func (client *Client) CreateAlertIngestCredential(ctx context.Context, create AlertIngestCredentialCreate) (*AlertIngestCredentialMinted, error) {
	var minted AlertIngestCredentialMinted
	if requestError := client.doRequest(ctx, http.MethodPost, alertIngestCredentialsPath, create, &minted); requestError != nil {
		return nil, requestError
	}
	return &minted, nil
}

// RebindAlertIngestCredential pins a credential to a cluster, unpins it, or
// changes its scope.
func (client *Client) RebindAlertIngestCredential(ctx context.Context, credentialID string, rebind AlertIngestCredentialRebind) (*AlertIngestCredential, error) {
	var rebound alertIngestCredentialRebound
	if requestError := client.doRequest(ctx, http.MethodPatch, alertIngestCredentialsPath+"/"+url.PathEscape(credentialID), rebind, &rebound); requestError != nil {
		return nil, requestError
	}
	return &rebound.Item, nil
}

// DeleteAlertIngestCredential revokes an ingest credential.
func (client *Client) DeleteAlertIngestCredential(ctx context.Context, credentialID string) error {
	return client.doRequest(ctx, http.MethodDelete, alertIngestCredentialsPath+"/"+url.PathEscape(credentialID), nil, nil)
}
