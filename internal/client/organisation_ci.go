// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package client

import (
	"context"
	"net/http"
	"net/url"
)

const (
	ciSettingsPath = "/api/v1/org/ci-settings"
	ciPoolPath     = "/api/v1/org/ci-settings/pool"
)

// OrganisationCISettings is the organisation's Ankra Pipelines settings.
// The platform answers its defaults for an organisation that never chose.
type OrganisationCISettings struct {
	ClusterID              *string  `json:"ci_cluster_id"`
	ClusterName            *string  `json:"ci_cluster_name"`
	BuildFallback          string   `json:"ci_build_fallback"`
	MaxParallelRuns        int64    `json:"ci_max_parallel_runs"`
	MaxParallelSteps       int64    `json:"ci_max_parallel_steps"`
	AllowedImagePrefixes   []string `json:"ci_allowed_image_prefixes"`
	ArtifactRetentionDays  int64    `json:"ci_artifact_retention_days"`
	CacheRetentionDays     int64    `json:"ci_cache_retention_days"`
	RunRetentionDays       int64    `json:"ci_run_retention_days"`
	ImageGate              string   `json:"ci_image_gate"`
	IgnoreUnfixed          bool     `json:"ci_ignore_unfixed"`
	EgressAllowedCIDRs     []string `json:"ci_egress_allowed_cidrs"`
	AlwaysRunStages        []string `json:"ci_always_run_stages"`
	FullRunOnDefaultBranch bool     `json:"ci_full_run_on_default_branch"`
	SkipMarkers            string   `json:"ci_skip_markers"`
	PlatformBuildsEnabled  bool     `json:"platform_builds_enabled"`
	IsDefault              bool     `json:"is_default"`
}

// OrganisationCIPoolMember is one cluster of the organisation's CI pool.
// IsListed is false for the pipeline cluster when it is in the pool only by
// being the primary.
type OrganisationCIPoolMember struct {
	ClusterID   string `json:"cluster_id"`
	ClusterName string `json:"cluster_name"`
	Weight      int64  `json:"weight"`
	IsPrimary   bool   `json:"is_primary"`
	IsListed    bool   `json:"is_listed"`
}

// OrganisationCIPool is the organisation's CI cluster pool.
type OrganisationCIPool struct {
	IsPooled bool                       `json:"is_pooled"`
	Members  []OrganisationCIPoolMember `json:"members"`
}

type organisationCIPoolWeight struct {
	Weight int64 `json:"weight"`
}

// GetOrganisationCISettings reads the CI settings.
func (client *Client) GetOrganisationCISettings(ctx context.Context) (*OrganisationCISettings, error) {
	var settings OrganisationCISettings
	if err := client.doRequest(ctx, http.MethodGet, ciSettingsPath, nil, &settings); err != nil {
		return nil, err
	}
	return &settings, nil
}

// UpdateOrganisationCISettings writes the named CI settings and returns what
// was stored. Every member is tri-state: an absent key keeps the stored value
// and a null ci_cluster_id clears the pipeline cluster; the platform refuses
// a null for any other key and a body naming nothing.
func (client *Client) UpdateOrganisationCISettings(ctx context.Context, request map[string]any) (*OrganisationCISettings, error) {
	var settings OrganisationCISettings
	if err := client.doRequest(ctx, http.MethodPut, ciSettingsPath, request, &settings); err != nil {
		return nil, err
	}
	return &settings, nil
}

// GetOrganisationCIPool reads the CI cluster pool.
func (client *Client) GetOrganisationCIPool(ctx context.Context) (*OrganisationCIPool, error) {
	var pool OrganisationCIPool
	if err := client.doRequest(ctx, http.MethodGet, ciPoolPath, nil, &pool); err != nil {
		return nil, err
	}
	return &pool, nil
}

// PutOrganisationCIPoolMember lists a cluster in the pool, or re-weights a
// listed one.
func (client *Client) PutOrganisationCIPoolMember(ctx context.Context, clusterID string, weight int64) (*OrganisationCIPool, error) {
	var pool OrganisationCIPool
	if err := client.doRequest(ctx, http.MethodPut, ciPoolPath+"/"+url.PathEscape(clusterID), organisationCIPoolWeight{Weight: weight}, &pool); err != nil {
		return nil, err
	}
	return &pool, nil
}

// RemoveOrganisationCIPoolMember unlists a cluster. A cluster not listed is
// a NotFound APIError.
func (client *Client) RemoveOrganisationCIPoolMember(ctx context.Context, clusterID string) error {
	return client.doRequest(ctx, http.MethodDelete, ciPoolPath+"/"+url.PathEscape(clusterID), nil, nil)
}
