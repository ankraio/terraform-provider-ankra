// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package client

import (
	"context"
	"net/http"
)

const clusterAccessPolicyPath = "/api/v1/org/cluster-access-policy"

// ClusterAccessPolicy is the organisation's cluster access policy: the limits
// every kubectl access grant is held to. A nil ElevatedMaxTTLSeconds sets no
// lifetime bound and a nil RequireReasonFromRole asks a reason only for
// break-glass. IsConfigured is false while the platform defaults apply.
type ClusterAccessPolicy struct {
	CreatorGrantRole      string  `json:"creator_grant_role"`
	MaxGrantRole          string  `json:"max_grant_role"`
	ElevatedMaxTTLSeconds *int64  `json:"elevated_max_ttl_seconds"`
	RequireReasonFromRole *string `json:"require_reason_from_role"`
	IsConfigured          bool    `json:"is_configured"`
}

// ClusterAccessPolicyRead is the policy and the organisation it belongs to.
type ClusterAccessPolicyRead struct {
	Policy           ClusterAccessPolicy `json:"policy"`
	OrganisationID   string              `json:"organisation_id"`
	OrganisationName *string             `json:"organisation_name"`
}

// GetClusterAccessPolicy reads the organisation's cluster access policy. It
// needs kube_access.manage, kube_access.policy or kube_access.elevate. An
// organisation without the cluster gateway answers 404.
//
// There is deliberately no write here: kube_access.policy is human-only, and
// the token API mints no token that names it, so the platform refuses every
// policy write from a provider token. A change is proposed with
// `ankra org access-policy set` and applied by an owner or admin, signed in.
func (client *Client) GetClusterAccessPolicy(ctx context.Context) (*ClusterAccessPolicyRead, error) {
	var read ClusterAccessPolicyRead
	if requestError := client.doRequest(ctx, http.MethodGet, clusterAccessPolicyPath, nil, &read); requestError != nil {
		return nil, requestError
	}
	return &read, nil
}
