// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package client

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

const securityDispositionsPath = "/api/v1/org/security/vulnerability-dispositions"

// securityDispositionPageSize is the platform's largest policy page.
const securityDispositionPageSize = 100

// SecurityDispositionStatusRevoked is the lifecycle status of a withdrawn
// policy. Revocation is the platform's only removal, and it is soft: a
// revoked policy stays in the listing.
const SecurityDispositionStatusRevoked = "revoked"

// SecurityDispositionSelector is what a disposition policy matches: the
// organisation plus the members the policy pins. Which members are set
// follows from the scope the policy was created with.
type SecurityDispositionSelector struct {
	OrganisationID    string `json:"organisation_id"`
	AddonSlug         string `json:"addon_slug,omitempty"`
	CVEID             string `json:"cve_id,omitempty"`
	PackageType       string `json:"package_type,omitempty"`
	PackageName       string `json:"package_name,omitempty"`
	ClusterID         string `json:"cluster_id,omitempty"`
	WorkloadKind      string `json:"workload_kind,omitempty"`
	WorkloadNamespace string `json:"workload_namespace,omitempty"`
	WorkloadName      string `json:"workload_name,omitempty"`
	ImageDigest       string `json:"image_digest,omitempty"`
	ExplicitBroad     bool   `json:"explicit_broad,omitempty"`
}

// SecurityDisposition is one vulnerability-disposition policy: an
// acknowledgement or an accepted risk over the occurrences its selector
// matches.
type SecurityDisposition struct {
	ID                     string                      `json:"id"`
	Selector               SecurityDispositionSelector `json:"selector"`
	SelectorHash           string                      `json:"selector_hash"`
	Disposition            string                      `json:"disposition"`
	Reason                 string                      `json:"reason"`
	ExpiresAt              *time.Time                  `json:"expires_at"`
	ExpireWhenFixAvailable bool                        `json:"expire_when_fix_available"`
	CreatedBy              string                      `json:"created_by"`
	Status                 string                      `json:"status"`
	MatchHealth            string                      `json:"match_health"`
	MatchedOccurrenceCount int64                       `json:"matched_occurrence_count"`
	RevokedAt              *time.Time                  `json:"revoked_at"`
	CreatedAt              time.Time                   `json:"created_at"`
}

// SecurityDispositionCreate is the create body. The policy's selector is
// derived from the anchoring occurrence and the scope, and never changes.
type SecurityDispositionCreate struct {
	OccurrenceID           string     `json:"occurrence_id"`
	Scope                  string     `json:"scope,omitempty"`
	Disposition            string     `json:"disposition"`
	ExpireWhenFixAvailable bool       `json:"expire_when_fix_available"`
	Reason                 string     `json:"reason"`
	ExpiresAt              *time.Time `json:"expires_at,omitempty"`
	AllowUnmatched         bool       `json:"allow_unmatched"`
}

// SecurityDispositionUpdate is the PATCH body: an absent member is left
// unchanged, and a review deadline can be moved but never cleared.
type SecurityDispositionUpdate struct {
	Reason                 *string    `json:"reason,omitempty"`
	ExpiresAt              *time.Time `json:"expires_at,omitempty"`
	ExpireWhenFixAvailable *bool      `json:"expire_when_fix_available,omitempty"`
}

type securityDispositionMutation struct {
	Policy SecurityDisposition `json:"policy"`
}

type securityDispositionPage struct {
	Result     []SecurityDisposition `json:"result"`
	Pagination struct {
		Page       int `json:"page"`
		TotalPages int `json:"total_pages"`
	} `json:"pagination"`
}

// ListSecurityDispositions returns every disposition policy of the
// organisation, revoked ones included, walking the pages.
func (client *Client) ListSecurityDispositions(ctx context.Context) ([]SecurityDisposition, error) {
	return client.ListSecurityDispositionsFiltered(ctx, SecurityDispositionFilter{})
}

// SecurityDispositionFilter narrows the policy listing. Statuses match any
// of the named lifecycle statuses; empty fields do not filter.
type SecurityDispositionFilter struct {
	Statuses    []string
	Disposition string
}

// ListSecurityDispositionsFiltered returns the policies the filter matches,
// walking the pages.
func (client *Client) ListSecurityDispositionsFiltered(ctx context.Context, filter SecurityDispositionFilter) ([]SecurityDisposition, error) {
	policies := []SecurityDisposition{}
	for page := 1; ; page++ {
		query := url.Values{}
		for _, status := range filter.Statuses {
			query.Add("status", status)
		}
		if filter.Disposition != "" {
			query.Set("disposition", filter.Disposition)
		}
		query.Set("page", strconv.Itoa(page))
		query.Set("page_size", strconv.Itoa(securityDispositionPageSize))
		var response securityDispositionPage
		if err := client.doRequest(ctx, http.MethodGet, securityDispositionsPath+"?"+query.Encode(), nil, &response); err != nil {
			return nil, err
		}
		policies = append(policies, response.Result...)
		if len(response.Result) == 0 || page >= response.Pagination.TotalPages {
			return policies, nil
		}
	}
}

// GetSecurityDisposition finds one policy by id in the listing, the only
// read the platform offers. A missing id is a NotFound APIError.
func (client *Client) GetSecurityDisposition(ctx context.Context, policyID string) (*SecurityDisposition, error) {
	policies, err := client.ListSecurityDispositions(ctx)
	if err != nil {
		return nil, err
	}
	for index := range policies {
		if policies[index].ID == policyID {
			return &policies[index], nil
		}
	}
	return nil, &APIError{Method: http.MethodGet, Path: securityDispositionsPath, StatusCode: http.StatusNotFound, Body: "disposition " + policyID + " not found"}
}

// CreateSecurityDisposition writes a policy anchored on one occurrence. The
// platform answers the policy together with a preview of what it covers.
func (client *Client) CreateSecurityDisposition(ctx context.Context, request SecurityDispositionCreate) (*SecurityDisposition, error) {
	var response securityDispositionMutation
	if err := client.doRequest(ctx, http.MethodPost, securityDispositionsPath, request, &response); err != nil {
		return nil, err
	}
	return &response.Policy, nil
}

// UpdateSecurityDisposition edits a policy's reason and lifecycle. Unlike
// create, the platform answers the bare policy, not a policy and preview
// envelope.
func (client *Client) UpdateSecurityDisposition(ctx context.Context, policyID string, request SecurityDispositionUpdate) (*SecurityDisposition, error) {
	var policy SecurityDisposition
	if err := client.doRequest(ctx, http.MethodPatch, securityDispositionsPath+"/"+url.PathEscape(policyID), request, &policy); err != nil {
		return nil, err
	}
	return &policy, nil
}

// RevokeSecurityDisposition withdraws a policy. A policy already revoked
// (409) is not an error; one already gone answers a NotFound APIError.
func (client *Client) RevokeSecurityDisposition(ctx context.Context, policyID string, reason string) error {
	body := map[string]string{"reason": reason}
	return client.doRequest(ctx, http.MethodPost, securityDispositionsPath+"/"+url.PathEscape(policyID)+"/revoke", body, nil, http.StatusConflict)
}

// SecurityDispositionPreviewRequest asks what a policy anchored on one
// occurrence with a scope would match, without writing anything.
type SecurityDispositionPreviewRequest struct {
	OccurrenceID string `json:"occurrence_id"`
	Scope        string `json:"scope,omitempty"`
	Disposition  string `json:"disposition"`
}

// SecurityDispositionPreview is the part of a preview the provider reads:
// the selector the occurrence and scope derive, and its hash.
type SecurityDispositionPreview struct {
	Selector     SecurityDispositionSelector `json:"selector"`
	SelectorHash string                      `json:"selector_hash"`
}

// PreviewSecurityDisposition derives the selector an occurrence and a scope
// would give a policy.
func (client *Client) PreviewSecurityDisposition(ctx context.Context, request SecurityDispositionPreviewRequest) (*SecurityDispositionPreview, error) {
	var preview SecurityDispositionPreview
	if err := client.doRequest(ctx, http.MethodPost, securityDispositionsPath+"/preview", request, &preview); err != nil {
		return nil, err
	}
	return &preview, nil
}
