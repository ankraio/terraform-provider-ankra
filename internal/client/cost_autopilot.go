// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package client

import (
	"context"
	"net/http"
	"net/url"
)

const costAutopilotPath = "/api/v1/org/cloud-cost/autopilot"

// CostAutopilotTierHandsOff is the tier every environment kind has until an
// organisation sets a policy: nothing happens unasked.
const CostAutopilotTierHandsOff = "hands-off"

// CostAutopilotQuietHours is a daily window, in an IANA timezone, when the
// autopilot starts no change of its own. Start and End are 24-hour HH:MM.
type CostAutopilotQuietHours struct {
	Start    string `json:"start"`
	End      string `json:"end"`
	Timezone string `json:"timezone"`
}

// CostAutopilotOverride is the tier a person set on one cluster. ExpiresAt is
// nil for an override that never expires.
type CostAutopilotOverride struct {
	Tier      string  `json:"tier"`
	Reason    string  `json:"reason"`
	SetBy     string  `json:"set_by"`
	SetAt     string  `json:"set_at"`
	ExpiresAt *string `json:"expires_at"`
}

// CostAutopilotCluster is one live cluster's tier. Override is nil when no
// active override applies: an expired override is not returned.
type CostAutopilotCluster struct {
	ClusterID   string                 `json:"cluster_id"`
	ClusterName string                 `json:"cluster_name"`
	Override    *CostAutopilotOverride `json:"override"`
	Tier        string                 `json:"tier"`
	Source      string                 `json:"source"`
}

// CostAutopilotPolicy is the organisation's autopilot policy. Defaults always
// holds every environment kind; a kind never set is hands-off.
type CostAutopilotPolicy struct {
	IsSet               bool                     `json:"is_set"`
	Defaults            map[string]string        `json:"defaults"`
	QuietHours          *CostAutopilotQuietHours `json:"quiet_hours"`
	NotificationRouteID *string                  `json:"notification_route_id"`
	EnvironmentKinds    []string                 `json:"environment_kinds"`
	Clusters            []CostAutopilotCluster   `json:"clusters"`
}

// CostAutopilotPolicyUpdate is the policy write. Defaults names the kinds it
// changes (a kind left out keeps its tier); QuietHours and
// NotificationRouteID are always sent, so null clears them.
type CostAutopilotPolicyUpdate struct {
	Defaults            map[string]string        `json:"defaults"`
	QuietHours          *CostAutopilotQuietHours `json:"quiet_hours"`
	NotificationRouteID *string                  `json:"notification_route_id"`
}

// CostAutopilotOverrideRequest puts one cluster in a tier. ExpiresAt is
// omitted for an override that never expires.
type CostAutopilotOverrideRequest struct {
	Tier      string  `json:"tier"`
	Reason    string  `json:"reason"`
	ExpiresAt *string `json:"expires_at,omitempty"`
}

// GetCostAutopilot returns the policy with every live cluster's tier.
func (client *Client) GetCostAutopilot(ctx context.Context) (*CostAutopilotPolicy, error) {
	var policy CostAutopilotPolicy
	if err := client.doRequest(ctx, http.MethodGet, costAutopilotPath, nil, &policy); err != nil {
		return nil, err
	}
	return &policy, nil
}

// UpdateCostAutopilot writes the policy and returns it as it now stands.
func (client *Client) UpdateCostAutopilot(ctx context.Context, request CostAutopilotPolicyUpdate) (*CostAutopilotPolicy, error) {
	var policy CostAutopilotPolicy
	if err := client.doRequest(ctx, http.MethodPut, costAutopilotPath, request, &policy); err != nil {
		return nil, err
	}
	return &policy, nil
}

// GetCostAutopilotCluster finds one cluster in the policy read, which is the
// only read the platform offers. A cluster that is not a live one of the
// organisation is a NotFound APIError.
func (client *Client) GetCostAutopilotCluster(ctx context.Context, clusterID string) (*CostAutopilotCluster, error) {
	policy, err := client.GetCostAutopilot(ctx)
	if err != nil {
		return nil, err
	}
	for index := range policy.Clusters {
		if policy.Clusters[index].ClusterID == clusterID {
			return &policy.Clusters[index], nil
		}
	}
	return nil, &APIError{Method: http.MethodGet, Path: costAutopilotPath, StatusCode: http.StatusNotFound, Body: "cluster " + clusterID + " not found"}
}

// SetCostAutopilotOverride puts one cluster in a tier, replacing its previous
// override.
func (client *Client) SetCostAutopilotOverride(ctx context.Context, clusterID string, request CostAutopilotOverrideRequest) (*CostAutopilotCluster, error) {
	var cluster CostAutopilotCluster
	if err := client.doRequest(ctx, http.MethodPut, costAutopilotPath+"/clusters/"+url.PathEscape(clusterID), request, &cluster); err != nil {
		return nil, err
	}
	return &cluster, nil
}

// ClearCostAutopilotOverride returns one cluster to its environment's default
// tier. Clearing a cluster with no override, or one already gone, is not an
// error.
func (client *Client) ClearCostAutopilotOverride(ctx context.Context, clusterID string) error {
	return client.doRequest(ctx, http.MethodDelete, costAutopilotPath+"/clusters/"+url.PathEscape(clusterID), nil, nil, http.StatusNotFound)
}
