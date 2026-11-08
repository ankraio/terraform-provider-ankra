// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package client

import (
	"context"
	"net/http"
	"net/url"
)

// OSPatchPolicyLanes are the cluster kinds the rolling OS patch lane serves.
// Proxmox and Morpheus clusters reach their nodes through an on-premises jump
// host the lane does not serve.
var OSPatchPolicyLanes = []string{LaneAnkraCloud, LaneAWS, LaneDigitalOcean, LaneHetzner, LaneOVH, LaneScaleway, LaneUpCloud}

// OSPatchWindow is the recurring maintenance window, in UTC, an automatic
// patch run may start in. Days are short lower-case names, Monday first.
type OSPatchWindow struct {
	Days          []string `json:"days"`
	StartHourUTC  int64    `json:"start_hour_utc"`
	DurationHours int64    `json:"duration_hours"`
}

// OSPatchPolicy is a cluster's OS patch policy. A cluster nobody configured
// reports the platform default (disabled) with Stored false.
type OSPatchPolicy struct {
	Enabled              bool          `json:"enabled"`
	Window               OSPatchWindow `json:"window"`
	CadenceDays          int64         `json:"cadence_days"`
	IncludeStatefulNodes bool          `json:"include_stateful_nodes"`
	SecurityLagDays      int64         `json:"security_lag_days"`
	Stored               bool          `json:"stored"`
	WindowDescription    string        `json:"window_description"`
}

// OSPatchPolicyUpdate is a partial write of the policy: a nil member keeps
// the stored value.
type OSPatchPolicyUpdate struct {
	Enabled              *bool    `json:"enabled,omitempty"`
	WindowDays           []string `json:"window_days,omitempty"`
	WindowStartHourUTC   *int64   `json:"window_start_hour_utc,omitempty"`
	WindowDurationHours  *int64   `json:"window_duration_hours,omitempty"`
	CadenceDays          *int64   `json:"cadence_days,omitempty"`
	IncludeStatefulNodes *bool    `json:"include_stateful_nodes,omitempty"`
	SecurityLagDays      *int64   `json:"security_lag_days,omitempty"`
}

type osPatchStatus struct {
	Policy OSPatchPolicy `json:"policy"`
}

func osPatchesPath(lane, clusterID string) string {
	return "/api/v1/clusters/" + url.PathEscape(lane) + "/" + url.PathEscape(clusterID) + "/os-patches"
}

// GetOSPatchPolicy reads the cluster's OS patch policy from the lane's status
// route. A cluster the lane cannot serve answers 409 with the reason.
func (client *Client) GetOSPatchPolicy(ctx context.Context, lane, clusterID string) (*OSPatchPolicy, error) {
	var status osPatchStatus
	if requestError := client.doRequest(ctx, http.MethodGet, osPatchesPath(lane, clusterID), nil, &status); requestError != nil {
		return nil, requestError
	}
	return &status.Policy, nil
}

// UpdateOSPatchPolicy merges update onto the stored policy and answers the
// policy now stored. The platform refuses a value outside its bounds, and
// turning the policy off while a patch run is in flight, with 409.
func (client *Client) UpdateOSPatchPolicy(ctx context.Context, lane, clusterID string, update OSPatchPolicyUpdate) (*OSPatchPolicy, error) {
	var policy OSPatchPolicy
	if requestError := client.doRequest(ctx, http.MethodPatch, osPatchesPath(lane, clusterID)+"/policy", update, &policy); requestError != nil {
		return nil, requestError
	}
	return &policy, nil
}
