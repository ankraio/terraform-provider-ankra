// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package client

import (
	"context"
	"net/http"
	"net/url"
)

// BastionAllowlistLanes are the cluster kinds whose bastion enforces a
// per-cluster SSH source allowlist.
var BastionAllowlistLanes = []string{LaneHetzner, LaneOVH, LaneUpCloud, LaneDigitalOcean}

// BastionAllowedIPs is a cluster's bastion SSH source allowlist. Configured
// tells a list that was cleared (true, empty) from one never set (false,
// empty); both leave SSH open to every source.
type BastionAllowedIPs struct {
	NodeID            string   `json:"node_id"`
	Kind              string   `json:"kind"`
	Name              string   `json:"name"`
	BastionAllowedIPs []string `json:"bastion_allowed_ips"`
	Configured        bool     `json:"configured"`
}

type bastionAllowedIPsUpdate struct {
	BastionAllowedIPs []string `json:"bastion_allowed_ips"`
}

func bastionAllowedIPsPath(lane, clusterID string) string {
	return "/api/v1/clusters/" + url.PathEscape(lane) + "/" + url.PathEscape(clusterID) + "/bastion/allowed-ips"
}

// GetBastionAllowedIPs reads the allowlist stored on the cluster's bastion.
// An unknown or out-of-scope cluster is a 404; a cluster of another kind, or
// one without a bastion, is a 409.
func (client *Client) GetBastionAllowedIPs(ctx context.Context, lane, clusterID string) (*BastionAllowedIPs, error) {
	var allowedIPs BastionAllowedIPs
	if err := client.doRequest(ctx, http.MethodGet, bastionAllowedIPsPath(lane, clusterID), nil, &allowedIPs); err != nil {
		return nil, err
	}
	if allowedIPs.BastionAllowedIPs == nil {
		allowedIPs.BastionAllowedIPs = []string{}
	}
	return &allowedIPs, nil
}

// BastionAllowedIPsWrite is the platform's answer to an allowlist write: the
// stored, normalised list and the operation that applies it to the bastion.
// OperationID is nil when the write scheduled no work (the list already
// matched, the cluster is stopped, or a running operation covers it).
type BastionAllowedIPsWrite struct {
	BastionAllowedIPs []string `json:"bastion_allowed_ips"`
	OperationID       *string  `json:"operation_id"`
}

// SetBastionAllowedIPs replaces the allowlist. An empty list clears it,
// opening SSH to every source. The platform applies the change to the
// bastion in the background, as the answered operation.
func (client *Client) SetBastionAllowedIPs(ctx context.Context, lane, clusterID string, allowedIPs []string) (*BastionAllowedIPsWrite, error) {
	if allowedIPs == nil {
		allowedIPs = []string{}
	}
	var written BastionAllowedIPsWrite
	if err := client.doRequest(ctx, http.MethodPut, bastionAllowedIPsPath(lane, clusterID), bastionAllowedIPsUpdate{BastionAllowedIPs: allowedIPs}, &written); err != nil {
		return nil, err
	}
	return &written, nil
}
