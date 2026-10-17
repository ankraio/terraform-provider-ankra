// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package client

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/hashicorp/terraform-plugin-log/tflog"
)

// NodeGroupLanes are the cluster kinds whose clusters carry provider node
// groups (cluster go/internal/providerapi mountClusterRoutes).
var NodeGroupLanes = []string{
	LaneHetzner, LaneOVH, LaneUpCloud, LaneDigitalOcean, LaneScaleway,
	LaneAWS, LaneProxmox, LaneMorpheus, LaneAnkraCloud,
}

// NodeGroupTaint is one Kubernetes taint on a node group.
type NodeGroupTaint struct {
	Key    string `json:"key"`
	Value  string `json:"value"`
	Effect string `json:"effect"`
}

// NodeGroup is one row of GET .../node-groups. The platform derives a group
// from its worker rows, so a group is listed once its first worker exists.
type NodeGroup struct {
	Name               string           `json:"name"`
	InstanceType       string           `json:"instance_type"`
	Count              int64            `json:"count"`
	Min                int64            `json:"min"`
	Max                int64            `json:"max"`
	AutoscalingEnabled bool             `json:"autoscaling_enabled"`
	Labels             map[string]any   `json:"labels"`
	Taints             []NodeGroupTaint `json:"taints"`
	AvailabilityZones  []string         `json:"availability_zones,omitempty"`
	Zones              []string         `json:"zones,omitempty"`
}

// NodeGroupAutoscaling is the Cluster Autoscaler setting of a group, the body
// of PUT .../node-groups/{group}/autoscaling and the nested "autoscaling"
// object of the add body.
type NodeGroupAutoscaling struct {
	Enabled  bool  `json:"enabled"`
	MinCount int64 `json:"min_count"`
	MaxCount int64 `json:"max_count"`
}

// NodeGroupCreate is the body of POST .../node-groups.
type NodeGroupCreate struct {
	Name             string                `json:"name"`
	InstanceType     string                `json:"instance_type"`
	Count            *int64                `json:"count,omitempty"`
	Labels           map[string]string     `json:"labels,omitempty"`
	Taints           []NodeGroupTaint      `json:"taints,omitempty"`
	Autoscaling      *NodeGroupAutoscaling `json:"autoscaling,omitempty"`
	AvailabilityZone string                `json:"availability_zone,omitempty"`
	Zone             string                `json:"zone,omitempty"`
	UserData         string                `json:"user_data,omitempty"`
}

type nodeGroupListEnvelope struct {
	NodeGroups []NodeGroup `json:"node_groups"`
}

// nodeGroupsPath is the node-group collection of one cluster. Every write
// carries wait=false: a synchronous write holds the response for as long as
// the nodes take to build, far beyond a single request's timeout, so the
// provider submits, takes the platform's early verdict (a refusal decided
// within the platform's verdict window is answered as a 409, not lost), and
// then polls the listing until the change is visible.
func nodeGroupsPath(lane, clusterID string) string {
	return "/api/v1/clusters/" + url.PathEscape(lane) + "/" + url.PathEscape(clusterID) + "/node-groups"
}

func nodeGroupPath(lane, clusterID, groupName, suffix string) string {
	path := nodeGroupsPath(lane, clusterID) + "/" + url.PathEscape(groupName)
	if suffix != "" {
		path += "/" + suffix
	}
	return path + "?wait=false"
}

// ListNodeGroups returns the node groups of a cluster. A cluster that no
// longer exists is a NotFound APIError.
func (client *Client) ListNodeGroups(ctx context.Context, lane, clusterID string) ([]NodeGroup, error) {
	var envelope nodeGroupListEnvelope
	if err := client.doRequest(ctx, http.MethodGet, nodeGroupsPath(lane, clusterID), nil, &envelope); err != nil {
		return nil, err
	}
	return envelope.NodeGroups, nil
}

// GetNodeGroup finds one group in the listing, the only read the platform
// offers. A group that is not listed is a NotFound APIError.
func (client *Client) GetNodeGroup(ctx context.Context, lane, clusterID, groupName string) (*NodeGroup, error) {
	groups, err := client.ListNodeGroups(ctx, lane, clusterID)
	if err != nil {
		return nil, err
	}
	for index := range groups {
		if groups[index].Name == groupName {
			return &groups[index], nil
		}
	}
	return nil, &APIError{
		Method: http.MethodGet, Path: nodeGroupsPath(lane, clusterID), StatusCode: http.StatusNotFound,
		Body: "node group " + groupName + " not found",
	}
}

// AddNodeGroup submits a new node group.
func (client *Client) AddNodeGroup(ctx context.Context, lane, clusterID string, request NodeGroupCreate) error {
	return client.doRequest(ctx, http.MethodPost, nodeGroupsPath(lane, clusterID)+"?wait=false", request, nil)
}

// ScaleNodeGroup submits a new node count for a group.
func (client *Client) ScaleNodeGroup(ctx context.Context, lane, clusterID, groupName string, count int64, forceDrain bool) error {
	body := map[string]any{"count": count}
	if forceDrain {
		body["force_drain"] = true
	}
	return client.doRequest(ctx, http.MethodPut, nodeGroupPath(lane, clusterID, groupName, "scale"), body, nil)
}

// UpdateNodeGroupInstanceType submits an in-place resize of every node in a
// group. The platform drains and power-cycles each node.
func (client *Client) UpdateNodeGroupInstanceType(ctx context.Context, lane, clusterID, groupName, instanceType string, forceDrain bool) error {
	body := map[string]any{"instance_type": instanceType}
	if forceDrain {
		body["force_drain"] = true
	}
	return client.doRequest(ctx, http.MethodPut, nodeGroupPath(lane, clusterID, groupName, "instance-type"), body, nil)
}

// UpdateNodeGroupLabels replaces a group's labels. The body always carries
// the labels key: an empty map clears them.
func (client *Client) UpdateNodeGroupLabels(ctx context.Context, lane, clusterID, groupName string, labels map[string]string) error {
	if labels == nil {
		labels = map[string]string{}
	}
	body := map[string]any{"labels": labels}
	return client.doRequest(ctx, http.MethodPut, nodeGroupPath(lane, clusterID, groupName, "labels"), body, nil)
}

// UpdateNodeGroupTaints replaces a group's taints. An empty list clears them.
func (client *Client) UpdateNodeGroupTaints(ctx context.Context, lane, clusterID, groupName string, taints []NodeGroupTaint) error {
	if taints == nil {
		taints = []NodeGroupTaint{}
	}
	body := map[string]any{"taints": taints}
	return client.doRequest(ctx, http.MethodPut, nodeGroupPath(lane, clusterID, groupName, "taints"), body, nil)
}

// UpdateNodeGroupAutoscaling replaces a group's autoscaling setting.
func (client *Client) UpdateNodeGroupAutoscaling(ctx context.Context, lane, clusterID, groupName string, autoscaling NodeGroupAutoscaling) error {
	return client.doRequest(ctx, http.MethodPut, nodeGroupPath(lane, clusterID, groupName, "autoscaling"), autoscaling, nil)
}

// DeleteNodeGroup submits the removal of a group and its nodes. A group or
// cluster already gone is not an error.
func (client *Client) DeleteNodeGroup(ctx context.Context, lane, clusterID, groupName string, forceDrain bool) error {
	path := nodeGroupPath(lane, clusterID, groupName, "")
	if forceDrain {
		path += "&force_drain=true"
	}
	return client.doRequest(ctx, http.MethodDelete, path, nil, nil, http.StatusNotFound)
}

// NodeGroupCondition decides whether a polled group has reached the state a
// write asked for. group is nil when the group is not listed.
type NodeGroupCondition func(group *NodeGroup) bool

// WaitForNodeGroup polls the listing until condition holds or ctx expires. A
// cluster that disappears while waiting ends the wait with its NotFound error.
func (client *Client) WaitForNodeGroup(ctx context.Context, lane, clusterID, groupName string, pollInterval time.Duration, condition NodeGroupCondition) (*NodeGroup, error) {
	if pollInterval <= 0 {
		pollInterval = defaultPollInterval
	}
	for {
		groups, err := client.ListNodeGroups(ctx, lane, clusterID)
		if err != nil {
			if ctx.Err() != nil {
				return nil, fmt.Errorf("timed out waiting for node group %s on cluster %s: %w", groupName, clusterID, ctx.Err())
			}
			return nil, err
		}
		var found *NodeGroup
		for index := range groups {
			if groups[index].Name == groupName {
				found = &groups[index]
				break
			}
		}
		if condition(found) {
			return found, nil
		}
		tflog.Debug(ctx, "waiting for node group", map[string]any{"cluster_id": clusterID, "group": groupName})
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("timed out waiting for node group %s on cluster %s: %w", groupName, clusterID, ctx.Err())
		case <-time.After(pollInterval):
		}
	}
}
