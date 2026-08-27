// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package client

import "context"

// HetznerClusterRequest is the payload for POST /api/v1/clusters/hetzner.
// Fields with a server-side default are always sent; nullable fields are
// omitted when empty.
type HetznerClusterRequest struct {
	Name                   string  `json:"name"`
	CredentialID           string  `json:"credential_id"`
	Location               string  `json:"location"`
	Distribution           string  `json:"distribution"`
	CNI                    string  `json:"cni"`
	ControlPlaneCount      int64   `json:"control_plane_count"`
	ControlPlaneServerType string  `json:"control_plane_server_type"`
	WorkerCount            int64   `json:"worker_count"`
	WorkerServerType       string  `json:"worker_server_type"`
	BastionServerType      string  `json:"bastion_server_type"`
	NetworkIPRange         string  `json:"network_ip_range"`
	SubnetRange            string  `json:"subnet_range"`
	ExternalCloudProvider  bool    `json:"external_cloud_provider"`
	IncludeNetworking      bool    `json:"include_networking"`
	EtcdTopology           string  `json:"etcd_topology"`
	EtcdNodeCount          int64   `json:"etcd_node_count"`
	EtcdServerType         string  `json:"etcd_server_type"`
	GitopsBranch           string  `json:"gitops_branch"`
	KubernetesVersion      *string `json:"kubernetes_version,omitempty"`
	Description            *string `json:"description,omitempty"`
	SSHKeyCredentialID     *string `json:"ssh_key_credential_id,omitempty"`
	GitopsCredentialName   *string `json:"gitops_credential_name,omitempty"`
	GitopsRepository       *string `json:"gitops_repository,omitempty"`
}

// CreateHetznerCluster provisions a new Hetzner-backed cluster.
func (client *Client) CreateHetznerCluster(ctx context.Context, request HetznerClusterRequest) (ImportClusterResponse, error) {
	return client.CreateProvisionedCluster(ctx, LaneHetzner, request)
}

// DeleteHetznerCluster deprovisions a Hetzner cluster by id, releasing its
// cloud resources. A 404 is treated as success so deletes are idempotent.
func (client *Client) DeleteHetznerCluster(ctx context.Context, clusterID string, force bool) error {
	return client.DeleteProvisionedCluster(ctx, LaneHetzner, clusterID, force)
}
