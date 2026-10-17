// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package client

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
)

// ClusterNodeUpState is the state of a node with no write in flight. A node
// group write (an instance-type resize among them) moves the group's workers
// to "updating" in the same transaction that accepts it, and the scheduler
// returns each one to "up" once its part of the roll is done.
const ClusterNodeUpState = "up"

// ClusterNode is one row of GET /api/v1/clusters/{lane}/{cluster_id}/nodes.
type ClusterNode struct {
	ID           string  `json:"id"`
	Kind         string  `json:"kind"`
	Name         string  `json:"name"`
	NodeGroup    *string `json:"node_group"`
	InstanceType string  `json:"instance_type"`
	State        string  `json:"state"`
	IsDeleted    bool    `json:"is_deleted"`
}

type clusterNodeListEnvelope struct {
	Nodes []ClusterNode `json:"nodes"`
}

// ListClusterNodes returns the machines of a provisioned cluster.
func (client *Client) ListClusterNodes(ctx context.Context, lane, clusterID string) ([]ClusterNode, error) {
	var envelope clusterNodeListEnvelope
	path := "/api/v1/clusters/" + url.PathEscape(lane) + "/" + url.PathEscape(clusterID) + "/nodes"
	if err := client.doRequest(ctx, http.MethodGet, path, nil, &envelope); err != nil {
		return nil, err
	}
	return envelope.Nodes, nil
}

// kubernetesResourceRequest is one read of POST .../kubernetes/resources/get.
type kubernetesResourceRequest struct {
	Kind          string `json:"kind"`
	Version       string `json:"version"`
	LabelSelector string `json:"label_selector,omitempty"`
}

type kubernetesResourcesRequest struct {
	ResourceRequests []kubernetesResourceRequest `json:"resource_requests"`
	SkipCache        bool                        `json:"skip_cache"`
}

type kubernetesNodeCondition struct {
	Type   string `json:"type"`
	Status string `json:"status"`
}

type kubernetesNode struct {
	Status struct {
		Conditions []kubernetesNodeCondition `json:"conditions"`
	} `json:"status"`
}

type kubernetesResourcesResponse struct {
	ResourceResponses []struct {
		Status        string           `json:"status"`
		Items         []kubernetesNode `json:"items"`
		ErrorResponse *string          `json:"error_response"`
	} `json:"resource_responses"`
}

// CountReadyKubernetesNodes counts the cluster's Kubernetes nodes that carry
// the label labelKey=labelValue and report Ready. It reads the live cluster
// through the Ankra agent, so it fails while the agent is not connected.
func (client *Client) CountReadyKubernetesNodes(ctx context.Context, clusterID, labelKey, labelValue string) (int64, error) {
	request := kubernetesResourcesRequest{
		ResourceRequests: []kubernetesResourceRequest{{Kind: "Node", Version: "v1", LabelSelector: labelKey + "=" + labelValue}},
		SkipCache:        true,
	}
	var response kubernetesResourcesResponse
	path := "/api/v1/clusters/" + url.PathEscape(clusterID) + "/kubernetes/resources/get"
	if err := client.doRequest(ctx, http.MethodPost, path, request, &response); err != nil {
		return 0, err
	}
	var ready int64
	for _, item := range response.ResourceResponses {
		if item.Status == "error" {
			detail := ""
			if item.ErrorResponse != nil {
				detail = *item.ErrorResponse
			}
			return 0, fmt.Errorf("the cluster agent could not list nodes: %s", detail)
		}
		for _, node := range item.Items {
			for _, condition := range node.Status.Conditions {
				if condition.Type == "Ready" && condition.Status == "True" {
					ready++
				}
			}
		}
	}
	return ready, nil
}
