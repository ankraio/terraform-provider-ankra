// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package client

import (
	"context"
	"net/http"
)

const aiRemediationPolicyPath = "/api/v1/org/ai-remediation/policy"

// AIRemediationPolicy is the organisation's AI remediation policy. An
// organisation that never set one reads as the platform default.
type AIRemediationPolicy struct {
	Enabled               bool              `json:"enabled"`
	AutonomyLevel         string            `json:"autonomy_level"`
	TierOverrides         map[string]string `json:"tier_overrides"`
	SlackWebhookID        *string           `json:"slack_webhook_id"`
	ApproverUserIDs       []string          `json:"approver_user_ids"`
	MaxActionsPerIncident int64             `json:"max_actions_per_incident"`
	CooldownMinutes       int64             `json:"cooldown_minutes"`
	ClusterAllowList      []string          `json:"cluster_allow_list"`
	UpdatedAt             *string           `json:"updated_at"`
}

// GetAIRemediationPolicy reads the organisation's AI remediation policy.
func (client *Client) GetAIRemediationPolicy(ctx context.Context) (*AIRemediationPolicy, error) {
	var policy AIRemediationPolicy
	if err := client.doRequest(ctx, http.MethodGet, aiRemediationPolicyPath, nil, &policy); err != nil {
		return nil, err
	}
	return &policy, nil
}
