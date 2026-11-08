// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package client

import (
	"context"
	"net/http"
	"net/url"
)

const oidcTrustPoliciesPath = "/api/v1/org/organisation/oidc-trust-policies"

// OIDCTrustPolicy is one GitHub Actions OIDC trust policy: which workflow runs
// may exchange their GitHub ID token for a short-lived Ankra token, and what
// that token may do.
type OIDCTrustPolicy struct {
	ID                string   `json:"id"`
	Name              string   `json:"name"`
	RepositoryOwner   string   `json:"repository_owner"`
	RepositoryName    string   `json:"repository_name"`
	RepositoryOwnerID int64    `json:"repository_owner_id"`
	RepositoryID      int64    `json:"repository_id"`
	Ref               *string  `json:"ref"`
	Environment       *string  `json:"environment"`
	JobWorkflowRef    *string  `json:"job_workflow_ref"`
	AllowPullRequest  bool     `json:"allow_pull_request"`
	RoleSlug          string   `json:"role_slug"`
	Scopes            []string `json:"scopes"`
	TTLSeconds        int64    `json:"ttl_seconds"`
	ServiceAccountID  string   `json:"service_account_id"`
	CreatedAt         string   `json:"created_at"`
}

// OIDCTrustPolicyCreate is the create body. The platform has no update: a
// policy is replaced by deleting it and creating another.
type OIDCTrustPolicyCreate struct {
	Name              string   `json:"name"`
	RepositoryOwner   string   `json:"repository_owner"`
	RepositoryName    string   `json:"repository_name"`
	RepositoryOwnerID int64    `json:"repository_owner_id"`
	RepositoryID      int64    `json:"repository_id"`
	Ref               *string  `json:"ref"`
	Environment       *string  `json:"environment"`
	JobWorkflowRef    *string  `json:"job_workflow_ref"`
	AllowPullRequest  bool     `json:"allow_pull_request"`
	RoleSlug          string   `json:"role_slug"`
	Scopes            []string `json:"scopes"`
	TTLSeconds        int64    `json:"ttl_seconds"`
}

type oidcTrustPolicyListEnvelope struct {
	Policies []OIDCTrustPolicy `json:"policies"`
}

// ListOIDCTrustPolicies returns the organisation's live trust policies. It
// needs members.manage on a person's own token: a service account and a token
// pinned to a project are refused.
func (client *Client) ListOIDCTrustPolicies(ctx context.Context) ([]OIDCTrustPolicy, error) {
	var envelope oidcTrustPolicyListEnvelope
	if requestError := client.doRequest(ctx, http.MethodGet, oidcTrustPoliciesPath, nil, &envelope); requestError != nil {
		return nil, requestError
	}
	if envelope.Policies == nil {
		return []OIDCTrustPolicy{}, nil
	}
	return envelope.Policies, nil
}

// GetOIDCTrustPolicy finds one policy by id in the listing, the only read the
// platform offers. A missing id is a NotFound APIError.
func (client *Client) GetOIDCTrustPolicy(ctx context.Context, policyID string) (*OIDCTrustPolicy, error) {
	policies, listError := client.ListOIDCTrustPolicies(ctx)
	if listError != nil {
		return nil, listError
	}
	for index := range policies {
		if policies[index].ID == policyID {
			return &policies[index], nil
		}
	}
	return nil, &APIError{Method: http.MethodGet, Path: oidcTrustPoliciesPath, StatusCode: http.StatusNotFound, Body: "trust policy " + policyID + " not found"}
}

// CreateOIDCTrustPolicy creates a policy and the service account its tokens
// belong to.
func (client *Client) CreateOIDCTrustPolicy(ctx context.Context, request OIDCTrustPolicyCreate) (*OIDCTrustPolicy, error) {
	var policy OIDCTrustPolicy
	if requestError := client.doRequest(ctx, http.MethodPost, oidcTrustPoliciesPath, request, &policy); requestError != nil {
		return nil, requestError
	}
	return &policy, nil
}

// DeleteOIDCTrustPolicy deletes a policy and revokes every token it minted
// that is still live. A policy already gone is a NotFound APIError.
func (client *Client) DeleteOIDCTrustPolicy(ctx context.Context, policyID string) error {
	return client.doRequest(ctx, http.MethodDelete, oidcTrustPoliciesPath+"/"+url.PathEscape(policyID), nil, nil)
}
