// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package client

import (
	"context"
	"net/http"
	"net/url"
)

// ClusterSecurityPolicyMode is the stored security-baseline policy mode of a
// cluster, read from the platform's database, so it answers for an offline
// cluster too.
type ClusterSecurityPolicyMode struct {
	ClusterID string `json:"cluster_id"`
	// PolicyMode is what the cluster stores now: "enforce", "audit", or
	// "off" when no policy engine is installed. It is nil when the cluster
	// carries no security baseline.
	PolicyMode *string `json:"policy_mode"`
	// RequestedMode is the mode the last policy-mode write asked for; nil
	// when none was ever set.
	RequestedMode         *string `json:"requested_mode"`
	PolicyEngineInstalled bool    `json:"policy_engine_installed"`
}

// ClusterSecurityPolicyModeChange is the platform's answer to a policy-mode
// write: the mode set and the GitOps commit that carries it, when the
// cluster has a GitOps repository.
type ClusterSecurityPolicyModeChange struct {
	Valid     bool    `json:"valid"`
	Mode      string  `json:"mode"`
	CommitSHA *string `json:"commit_sha"`
	CommitURL *string `json:"commit_url"`
}

func clusterSecurityPolicyModePath(clusterID string) string {
	return "/api/v1/org/clusters/imported/" + url.PathEscape(clusterID) + "/security/policy-mode"
}

// GetClusterSecurityPolicyMode reads a cluster's stored security-baseline
// policy mode.
func (client *Client) GetClusterSecurityPolicyMode(ctx context.Context, clusterID string) (*ClusterSecurityPolicyMode, error) {
	var view ClusterSecurityPolicyMode
	if requestError := client.doRequest(ctx, http.MethodGet, clusterSecurityPolicyModePath(clusterID), nil, &view); requestError != nil {
		return nil, requestError
	}
	return &view, nil
}

// SetClusterSecurityPolicyMode switches a cluster's security baseline to
// audit, enforce or off (`ankra security policy-mode`). A cluster without
// the baseline answers 404.
func (client *Client) SetClusterSecurityPolicyMode(ctx context.Context, clusterID, mode string) (*ClusterSecurityPolicyModeChange, error) {
	var change ClusterSecurityPolicyModeChange
	body := map[string]string{"mode": mode}
	if requestError := client.doRequest(ctx, http.MethodPost, clusterSecurityPolicyModePath(clusterID), body, &change); requestError != nil {
		return nil, requestError
	}
	return &change, nil
}
