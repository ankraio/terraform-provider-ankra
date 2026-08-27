// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package client

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"time"

	"github.com/hashicorp/terraform-plugin-log/tflog"
)

// Cluster lanes the platform provisions on a cloud account. Each lane is a
// path segment under /api/v1/clusters.
const (
	LaneHetzner      = "hetzner"
	LaneDigitalOcean = "digitalocean"
	LaneOVH          = "ovh"
	LaneScaleway     = "scaleway"
	LaneUpCloud      = "upcloud"
)

// ClusterReadyState is the state a provisioned cluster reaches once its
// control plane and nodes are up. The value comes from the platform's
// ClusterState enum.
const ClusterReadyState = "running"

// ImportedClusterReadyState is the state an imported cluster reaches once its
// agent has checked in, from the platform's ImportedClusterState enum.
const ImportedClusterReadyState = "online"

// provisioningFailureStates are states a cluster should never reach while it
// is being created; seeing one means provisioning will not complete.
var provisioningFailureStates = []string{"deprovisioning", "stopped"}

// defaultPollInterval paces readiness polling. Cluster provisioning takes
// minutes, so a slower poll keeps the platform's rate limits comfortable.
const defaultPollInterval = 15 * time.Second

// CreateProvisionedCluster provisions a cluster on the given cloud lane.
func (client *Client) CreateProvisionedCluster(ctx context.Context, lane string, request any) (ImportClusterResponse, error) {
	var response ImportClusterResponse
	path := "/api/v1/clusters/" + url.PathEscape(lane)
	if err := client.doRequest(ctx, http.MethodPost, path, request, &response); err != nil {
		return ImportClusterResponse{}, err
	}
	return response, nil
}

// DeleteProvisionedCluster deprovisions a cluster on the given cloud lane,
// releasing its cloud resources. A 404 is treated as success so deletes are
// idempotent.
func (client *Client) DeleteProvisionedCluster(ctx context.Context, lane, clusterID string, force bool) error {
	path := "/api/v1/clusters/" + url.PathEscape(lane) + "/" + url.PathEscape(clusterID) +
		"?force=" + strconv.FormatBool(force)
	return client.doRequest(ctx, http.MethodDelete, path, nil, nil, http.StatusNotFound)
}

// WaitOptions configures a readiness poll.
type WaitOptions struct {
	// ReadyStates ends the wait successfully.
	ReadyStates []string
	// FailureStates ends the wait with an error.
	FailureStates []string
	// PollInterval defaults to defaultPollInterval when zero.
	PollInterval time.Duration
}

// WaitForClusterState polls until the cluster reaches one of the ready states,
// hits a failure state, or ctx expires. The caller owns the deadline, which is
// where a resource's configurable timeout is applied.
//
// A cluster that is not listed yet is treated as still provisioning rather
// than as missing: the row can lag the create response.
func (client *Client) WaitForClusterState(ctx context.Context, clusterID string, options WaitOptions) (*Cluster, error) {
	interval := options.PollInterval
	if interval <= 0 {
		interval = defaultPollInterval
	}

	var lastState string
	for {
		cluster, err := client.GetClusterByID(ctx, clusterID)
		if err != nil {
			return nil, err
		}
		if cluster != nil {
			lastState = cluster.State
			if slices.Contains(options.ReadyStates, cluster.State) {
				return cluster, nil
			}
			if slices.Contains(options.FailureStates, cluster.State) {
				return cluster, fmt.Errorf(
					"cluster %s entered state %q while waiting for %v",
					clusterID, cluster.State, options.ReadyStates)
			}
		}

		tflog.Debug(ctx, "waiting for cluster state", map[string]any{
			"cluster_id": clusterID, "state": lastState, "want": options.ReadyStates,
		})

		select {
		case <-ctx.Done():
			return nil, fmt.Errorf(
				"timed out waiting for cluster %s to reach %v (last observed state %q): %w",
				clusterID, options.ReadyStates, lastState, ctx.Err())
		case <-time.After(interval):
		}
	}
}

// WaitForProvisionedCluster waits for a cloud-provisioned cluster to finish
// building.
func (client *Client) WaitForProvisionedCluster(ctx context.Context, clusterID string, pollInterval time.Duration) (*Cluster, error) {
	return client.WaitForClusterState(ctx, clusterID, WaitOptions{
		ReadyStates:   []string{ClusterReadyState},
		FailureStates: provisioningFailureStates,
		PollInterval:  pollInterval,
	})
}

// WaitForImportedCluster waits for an imported cluster's agent to check in.
func (client *Client) WaitForImportedCluster(ctx context.Context, clusterID string, pollInterval time.Duration) (*Cluster, error) {
	return client.WaitForClusterState(ctx, clusterID, WaitOptions{
		ReadyStates:  []string{ImportedClusterReadyState},
		PollInterval: pollInterval,
	})
}
