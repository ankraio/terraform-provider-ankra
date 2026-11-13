// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package client

import (
	"context"
	"net/http"
)

const (
	pipelinePolicyPath   = "/api/v1/org/pipeline-policy"
	provenancePolicyPath = "/api/v1/org/provenance/policy"
)

// PipelinePolicyToleration is one toleration pipelines may use.
type PipelinePolicyToleration struct {
	Key      string `json:"key"`
	Operator string `json:"operator"`
	Value    string `json:"value"`
	Effect   string `json:"effect"`
}

// PipelinePolicy is the organisation's pipeline authority ceiling.
// AllowedImagePrefixes is owned by the CI settings and read-only here.
type PipelinePolicy struct {
	AllowedImagePrefixes    []string                   `json:"allowed_image_prefixes"`
	AllowedNetworkTiers     []string                   `json:"allowed_network_tiers"`
	AllowedCredentials      []string                   `json:"allowed_credentials"`
	AllowedNodeSelectors    map[string][]string        `json:"allowed_node_selectors"`
	AllowedTolerations      []PipelinePolicyToleration `json:"allowed_tolerations"`
	AllowedRuntimeClasses   []string                   `json:"allowed_runtime_classes"`
	MaxCPU                  string                     `json:"max_cpu"`
	MaxMemory               string                     `json:"max_memory"`
	MaxCacheSize            string                     `json:"max_cache_size"`
	MaxGPU                  int64                      `json:"max_gpu"`
	MaxAgentToolCallsPerRun int64                      `json:"max_agent_tool_calls_per_run"`
	WebhookAllowedHosts     []string                   `json:"webhook_allowed_hosts"`
	ExternalProviders       []string                   `json:"external_providers"`
	UpdatedBy               string                     `json:"updated_by"`
	UpdatedAt               *string                    `json:"updated_at"`
}

// PipelinePolicyUpdate is a partial write: the platform changes the
// settings the body names, at least one.
type PipelinePolicyUpdate map[string]any

// GetPipelinePolicy reads the pipeline policy. It needs pipelines.manage,
// the read included.
func (client *Client) GetPipelinePolicy(ctx context.Context) (*PipelinePolicy, error) {
	var policy PipelinePolicy
	if requestError := client.doRequest(ctx, http.MethodGet, pipelinePolicyPath, nil, &policy); requestError != nil {
		return nil, requestError
	}
	return &policy, nil
}

// UpdatePipelinePolicy changes the settings update names.
func (client *Client) UpdatePipelinePolicy(ctx context.Context, update PipelinePolicyUpdate) (*PipelinePolicy, error) {
	var policy PipelinePolicy
	if requestError := client.doRequest(ctx, http.MethodPut, pipelinePolicyPath, update, &policy); requestError != nil {
		return nil, requestError
	}
	return &policy, nil
}

// ProvenancePolicy is a deploy provenance mode: off, warn, enforce, or nil
// when no choice is stored.
type ProvenancePolicy struct {
	Mode *string `json:"mode"`
}

// GetOrganisationProvenancePolicy reads the organisation's default deploy
// provenance mode. It needs pipelines.read.
func (client *Client) GetOrganisationProvenancePolicy(ctx context.Context) (*ProvenancePolicy, error) {
	var policy ProvenancePolicy
	if requestError := client.doRequest(ctx, http.MethodGet, provenancePolicyPath, nil, &policy); requestError != nil {
		return nil, requestError
	}
	return &policy, nil
}

// SetOrganisationProvenancePolicy stores the organisation's default; nil
// clears it. It needs security.manage.
func (client *Client) SetOrganisationProvenancePolicy(ctx context.Context, mode *string) (*ProvenancePolicy, error) {
	var policy ProvenancePolicy
	if requestError := client.doRequest(ctx, http.MethodPut, provenancePolicyPath, ProvenancePolicy{Mode: mode}, &policy); requestError != nil {
		return nil, requestError
	}
	return &policy, nil
}
