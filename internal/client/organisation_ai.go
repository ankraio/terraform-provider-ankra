// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package client

import (
	"context"
	"net/http"
	"net/url"
)

const (
	scmBindingsPath   = "/api/v1/org/ai-gateway/scm-bindings"
	relatedReposPath  = "/api/v1/org/ai-gateway/related-repos"
	aiSpendCapPath    = "/api/v1/org/billing/ai-spend-cap"
	aiEnvironmentPath = "/api/v1/org/ai-environment"
)

// AIReviewRepositoryRule is one repository's explicit AI review rule on a
// source-control binding. RepoFullName is stored lowercased.
type AIReviewRepositoryRule struct {
	RepoFullName    string `json:"repo_full_name"`
	AIReview        bool   `json:"ai_review"`
	MentionReplies  bool   `json:"mention_replies"`
	PRPreviews      bool   `json:"pr_previews"`
	ReviewModel     string `json:"review_model"`
	ReviewDrafts    bool   `json:"review_drafts"`
	MaxReviewsPerPR int64  `json:"max_reviews_per_pr"`
}

// AIReviewBinding is one source-control binding (a GitHub App installation,
// a GitLab or Bitbucket connection) with its repository rules.
type AIReviewBinding struct {
	Provider          string                   `json:"provider"`
	BindingExternalID string                   `json:"binding_external_id"`
	DisplayName       string                   `json:"display_name"`
	RepoOverrides     []AIReviewRepositoryRule `json:"repo_overrides"`
}

// AIReviewRepositoryRuleWrite is the rule upsert body. A nil ReviewModel,
// ReviewDrafts or MaxReviewsPerPR keeps the stored value (or the binding's,
// for a new rule); an empty ReviewModel inherits the binding's model.
type AIReviewRepositoryRuleWrite struct {
	RepoFullName    string  `json:"repo_full_name"`
	AIReview        bool    `json:"ai_review"`
	MentionReplies  bool    `json:"mention_replies"`
	PRPreviews      bool    `json:"pr_previews"`
	ReviewModel     *string `json:"review_model,omitempty"`
	ReviewDrafts    *bool   `json:"review_drafts,omitempty"`
	MaxReviewsPerPR *int64  `json:"max_reviews_per_pr,omitempty"`
}

// AIReviewRelatedRepository relates two repositories so an AI review of one
// may read the other. The pair is stored lowercased and ordered.
type AIReviewRelatedRepository struct {
	ID                  string `json:"id"`
	Provider            string `json:"provider"`
	RepoFullName        string `json:"repo_full_name"`
	RelatedRepoFullName string `json:"related_repo_full_name"`
}

// AIReviewRelatedRepositoryCreate is the create body.
type AIReviewRelatedRepositoryCreate struct {
	Provider            string `json:"provider"`
	BindingExternalID   string `json:"binding_external_id"`
	RepoFullName        string `json:"repo_full_name"`
	RelatedRepoFullName string `json:"related_repo_full_name"`
}

// OrganisationAISpendCap is the organisation's daily AI caps. A nil cap is no
// override: the money cap then falls back to the plan default.
type OrganisationAISpendCap struct {
	DailyChatUSDCap   *float64 `json:"daily_chat_usd_cap"`
	PlanDefaultUSD    *float64 `json:"plan_default_usd"`
	EffectiveUSD      *float64 `json:"effective_usd"`
	DailyTokenSoftCap *int64   `json:"daily_token_soft_cap"`
	DailyTokenHardCap *int64   `json:"daily_token_hard_cap"`
}

// OrganisationAISpendCapUpdate is the full cap write: every key is sent, and a null
// clears that cap.
type OrganisationAISpendCapUpdate struct {
	DailyChatUSDCap   *float64 `json:"daily_chat_usd_cap"`
	DailyTokenSoftCap *int64   `json:"daily_token_soft_cap"`
	DailyTokenHardCap *int64   `json:"daily_token_hard_cap"`
}

// OrganisationAIEnvironment is the organisation's AI workspace and preview environment.
type OrganisationAIEnvironment struct {
	StagingClusterID     *string `json:"staging_cluster_id"`
	StagingClusterName   *string `json:"staging_cluster_name"`
	WorkspaceTTLHours    int64   `json:"workspace_ttl_hours"`
	DemoTTLHours         int64   `json:"demo_ttl_hours"`
	DefaultGatewayMode   string  `json:"default_gateway_mode"`
	DemoBaseDomain       *string `json:"demo_base_domain"`
	DemoIngressClassName *string `json:"demo_ingress_class_name"`
	DemoTLSSecretName    *string `json:"demo_tls_secret_name"`
	DemoCertIssuerName   *string `json:"demo_cert_issuer_name"`
}

// OrganisationAIEnvironmentUpdate writes the environment settings this provider owns.
// Every key is sent, so a null staging cluster or demo setting clears it.
// dns_root_domain is deliberately absent: it moves the organisation's DNS
// and is managed separately.
type OrganisationAIEnvironmentUpdate struct {
	StagingClusterID     *string `json:"staging_cluster_id"`
	WorkspaceTTLHours    int64   `json:"workspace_ttl_hours"`
	DemoTTLHours         int64   `json:"demo_ttl_hours"`
	DefaultGatewayMode   string  `json:"default_gateway_mode"`
	DemoBaseDomain       *string `json:"demo_base_domain"`
	DemoIngressClassName *string `json:"demo_ingress_class_name"`
	DemoTLSSecretName    *string `json:"demo_tls_secret_name"`
	DemoCertIssuerName   *string `json:"demo_cert_issuer_name"`
}

type aiReviewBindingListEnvelope struct {
	Bindings []AIReviewBinding `json:"bindings"`
}

type aiReviewRelatedRepositoryListEnvelope struct {
	RelatedRepos []AIReviewRelatedRepository `json:"related_repos"`
}

func scmBindingPath(provider, bindingExternalID string) string {
	return scmBindingsPath + "/" + url.PathEscape(provider) + "/" + url.PathEscape(bindingExternalID)
}

// ListAIReviewBindings lists the organisation's source-control bindings
// with their repository rules.
func (client *Client) ListAIReviewBindings(ctx context.Context) ([]AIReviewBinding, error) {
	var envelope aiReviewBindingListEnvelope
	if err := client.doRequest(ctx, http.MethodGet, scmBindingsPath, nil, &envelope); err != nil {
		return nil, err
	}
	return envelope.Bindings, nil
}

// PutAIReviewRepositoryRule creates or replaces one repository's rule. An
// unknown binding is a NotFound APIError.
func (client *Client) PutAIReviewRepositoryRule(ctx context.Context, provider, bindingExternalID string, request AIReviewRepositoryRuleWrite) error {
	return client.doRequest(ctx, http.MethodPut, scmBindingPath(provider, bindingExternalID)+"/repo-overrides", request, nil)
}

// DeleteAIReviewRepositoryRule reverts a repository to the binding's
// settings. A binding already gone is not an error.
func (client *Client) DeleteAIReviewRepositoryRule(ctx context.Context, provider, bindingExternalID, repoFullName string) error {
	query := url.Values{}
	query.Set("repo_full_name", repoFullName)
	rulePath := scmBindingPath(provider, bindingExternalID) + "/repo-overrides?" + query.Encode()
	return client.doRequest(ctx, http.MethodDelete, rulePath, nil, nil, http.StatusNotFound)
}

// ListAIReviewRelatedRepositories lists the related-repository pairs of one
// binding.
func (client *Client) ListAIReviewRelatedRepositories(ctx context.Context, provider, bindingExternalID string) ([]AIReviewRelatedRepository, error) {
	query := url.Values{}
	query.Set("provider", provider)
	query.Set("binding_external_id", bindingExternalID)
	var envelope aiReviewRelatedRepositoryListEnvelope
	if err := client.doRequest(ctx, http.MethodGet, relatedReposPath+"?"+query.Encode(), nil, &envelope); err != nil {
		return nil, err
	}
	return envelope.RelatedRepos, nil
}

// CreateAIReviewRelatedRepository relates two repositories. Relating a pair
// that is already related answers the existing row.
func (client *Client) CreateAIReviewRelatedRepository(ctx context.Context, request AIReviewRelatedRepositoryCreate) (*AIReviewRelatedRepository, error) {
	var created AIReviewRelatedRepository
	if err := client.doRequest(ctx, http.MethodPost, relatedReposPath, request, &created); err != nil {
		return nil, err
	}
	return &created, nil
}

// DeleteAIReviewRelatedRepository removes a pair. A pair already gone is not
// an error.
func (client *Client) DeleteAIReviewRelatedRepository(ctx context.Context, relatedRepositoryID string) error {
	return client.doRequest(ctx, http.MethodDelete, relatedReposPath+"/"+url.PathEscape(relatedRepositoryID), nil, nil, http.StatusNotFound)
}

// GetAISpendCap reads the organisation's daily AI caps.
func (client *Client) GetAISpendCap(ctx context.Context) (*OrganisationAISpendCap, error) {
	var spendCap OrganisationAISpendCap
	if err := client.doRequest(ctx, http.MethodGet, aiSpendCapPath, nil, &spendCap); err != nil {
		return nil, err
	}
	return &spendCap, nil
}

// UpdateAISpendCap writes the organisation's daily AI caps.
func (client *Client) UpdateAISpendCap(ctx context.Context, request OrganisationAISpendCapUpdate) (*OrganisationAISpendCap, error) {
	var spendCap OrganisationAISpendCap
	if err := client.doRequest(ctx, http.MethodPut, aiSpendCapPath, request, &spendCap); err != nil {
		return nil, err
	}
	return &spendCap, nil
}

// GetAIEnvironment reads the organisation's AI environment settings.
func (client *Client) GetAIEnvironment(ctx context.Context) (*OrganisationAIEnvironment, error) {
	var environment OrganisationAIEnvironment
	if err := client.doRequest(ctx, http.MethodGet, aiEnvironmentPath, nil, &environment); err != nil {
		return nil, err
	}
	return &environment, nil
}

// UpdateAIEnvironment writes the organisation's AI environment settings.
func (client *Client) UpdateAIEnvironment(ctx context.Context, request OrganisationAIEnvironmentUpdate) (*OrganisationAIEnvironment, error) {
	var environment OrganisationAIEnvironment
	if err := client.doRequest(ctx, http.MethodPut, aiEnvironmentPath, request, &environment); err != nil {
		return nil, err
	}
	return &environment, nil
}
