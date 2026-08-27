// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package client

import "context"

// The request contracts below mirror the platform's Create*ClusterRequest
// schemas. Fields carrying a server-side default are always sent so the
// provider's plan and the platform's state agree; nullable fields are omitted
// when empty.
//
// The lanes share most of their shape and differ mainly in how each cloud
// names capacity and placement - server_type / size / plan / flavor_id, and
// location / region / zone. Those names are kept verbatim per lane rather
// than normalised, so a practitioner reads the same word here as in the
// cloud's own console.

// DigitalOceanClusterRequest is the payload for POST /api/v1/clusters/digitalocean.
type DigitalOceanClusterRequest struct {
	Name                  string  `json:"name"`
	CredentialID          string  `json:"credential_id"`
	Region                string  `json:"region"`
	SSHKeyCredentialID    string  `json:"ssh_key_credential_id"`
	Distribution          string  `json:"distribution"`
	CNI                   string  `json:"cni"`
	ControlPlaneCount     int64   `json:"control_plane_count"`
	ControlPlaneSize      string  `json:"control_plane_size"`
	WorkerCount           int64   `json:"worker_count"`
	WorkerSize            string  `json:"worker_size"`
	BastionSize           string  `json:"bastion_size"`
	ExternalCloudProvider bool    `json:"external_cloud_provider"`
	IncludeNetworking     bool    `json:"include_networking"`
	EtcdTopology          string  `json:"etcd_topology"`
	EtcdNodeCount         int64   `json:"etcd_node_count"`
	EtcdSize              string  `json:"etcd_size"`
	GitopsBranch          string  `json:"gitops_branch"`
	KubernetesVersion     *string `json:"kubernetes_version,omitempty"`
	NetworkIPRange        *string `json:"network_ip_range,omitempty"`
	Description           *string `json:"description,omitempty"`
	GitopsCredentialName  *string `json:"gitops_credential_name,omitempty"`
	GitopsRepository      *string `json:"gitops_repository,omitempty"`
}

// UpCloudClusterRequest is the payload for POST /api/v1/clusters/upcloud.
type UpCloudClusterRequest struct {
	Name                  string  `json:"name"`
	CredentialID          string  `json:"credential_id"`
	Zone                  string  `json:"zone"`
	SSHKeyCredentialID    string  `json:"ssh_key_credential_id"`
	Distribution          string  `json:"distribution"`
	CNI                   string  `json:"cni"`
	ControlPlaneCount     int64   `json:"control_plane_count"`
	ControlPlanePlan      string  `json:"control_plane_plan"`
	WorkerCount           int64   `json:"worker_count"`
	WorkerPlan            string  `json:"worker_plan"`
	BastionPlan           string  `json:"bastion_plan"`
	ExternalCloudProvider bool    `json:"external_cloud_provider"`
	IncludeNetworking     bool    `json:"include_networking"`
	EtcdTopology          string  `json:"etcd_topology"`
	EtcdNodeCount         int64   `json:"etcd_node_count"`
	EtcdPlan              string  `json:"etcd_plan"`
	GitopsBranch          string  `json:"gitops_branch"`
	KubernetesVersion     *string `json:"kubernetes_version,omitempty"`
	NetworkIPRange        *string `json:"network_ip_range,omitempty"`
	Description           *string `json:"description,omitempty"`
	GitopsCredentialName  *string `json:"gitops_credential_name,omitempty"`
	GitopsRepository      *string `json:"gitops_repository,omitempty"`
}

// OVHClusterRequest is the payload for POST /api/v1/clusters/ovh.
type OVHClusterRequest struct {
	Name                  string   `json:"name"`
	CredentialID          string   `json:"credential_id"`
	Region                string   `json:"region"`
	SSHKeyCredentialID    string   `json:"ssh_key_credential_id"`
	Distribution          string   `json:"distribution"`
	CNI                   string   `json:"cni"`
	ControlPlaneCount     int64    `json:"control_plane_count"`
	ControlPlaneFlavorID  string   `json:"control_plane_flavor_id"`
	WorkerCount           int64    `json:"worker_count"`
	WorkerFlavorID        string   `json:"worker_flavor_id"`
	GatewayFlavorID       string   `json:"gateway_flavor_id"`
	ExternalCloudProvider bool     `json:"external_cloud_provider"`
	IncludeNetworking     bool     `json:"include_networking"`
	EtcdTopology          string   `json:"etcd_topology"`
	EtcdNodeCount         int64    `json:"etcd_node_count"`
	EtcdFlavorID          string   `json:"etcd_flavor_id"`
	NetworkVLANID         int64    `json:"network_vlan_id"`
	SubnetCIDR            string   `json:"subnet_cidr"`
	DHCPStart             string   `json:"dhcp_start"`
	DHCPEnd               string   `json:"dhcp_end"`
	AvailabilityZones     []string `json:"availability_zones,omitempty"`
	GitopsBranch          string   `json:"gitops_branch"`
	KubernetesVersion     *string  `json:"kubernetes_version,omitempty"`
	Description           *string  `json:"description,omitempty"`
	GitopsCredentialName  *string  `json:"gitops_credential_name,omitempty"`
	GitopsRepository      *string  `json:"gitops_repository,omitempty"`
}

// ScalewayClusterRequest is the payload for POST /api/v1/clusters/scaleway.
// Scaleway's gitops fields are plain strings on the contract rather than
// nullable, so they are always sent.
type ScalewayClusterRequest struct {
	Name                  string   `json:"name"`
	CredentialID          string   `json:"credential_id"`
	Region                string   `json:"region"`
	Zone                  string   `json:"zone"`
	SSHKeyCredentialID    string   `json:"ssh_key_credential_id"`
	Distribution          string   `json:"distribution"`
	CNI                   string   `json:"cni"`
	ControlPlaneCount     int64    `json:"control_plane_count"`
	ControlPlaneType      string   `json:"control_plane_type"`
	WorkerCount           int64    `json:"worker_count"`
	WorkerType            string   `json:"worker_type"`
	GatewayType           string   `json:"gateway_type"`
	GatewayAllowedIPs     []string `json:"gateway_allowed_ips,omitempty"`
	BastionPort           int64    `json:"bastion_port"`
	ExternalCloudProvider bool     `json:"external_cloud_provider"`
	IncludeNetworking     bool     `json:"include_networking"`
	IncludeDNS            bool     `json:"include_dns"`
	EtcdTopology          string   `json:"etcd_topology"`
	EtcdNodeCount         int64    `json:"etcd_node_count"`
	EtcdType              string   `json:"etcd_type"`
	RetentionPolicy       string   `json:"retention_policy"`
	GitopsBranch          string   `json:"gitops_branch"`
	GitopsCredentialName  string   `json:"gitops_credential_name"`
	GitopsRepository      string   `json:"gitops_repository"`
	KubernetesVersion     *string  `json:"kubernetes_version,omitempty"`
	NetworkIPRange        *string  `json:"network_ip_range,omitempty"`
	PrivateNetworkID      *string  `json:"private_network_id,omitempty"`
	RuntimeCredentialID   *string  `json:"runtime_credential_id,omitempty"`
	Description           *string  `json:"description,omitempty"`
}

// CreateDigitalOceanCluster provisions a new DigitalOcean-backed cluster.
func (client *Client) CreateDigitalOceanCluster(ctx context.Context, request DigitalOceanClusterRequest) (ImportClusterResponse, error) {
	return client.CreateProvisionedCluster(ctx, LaneDigitalOcean, request)
}

// CreateUpCloudCluster provisions a new UpCloud-backed cluster.
func (client *Client) CreateUpCloudCluster(ctx context.Context, request UpCloudClusterRequest) (ImportClusterResponse, error) {
	return client.CreateProvisionedCluster(ctx, LaneUpCloud, request)
}

// CreateOVHCluster provisions a new OVH-backed cluster.
func (client *Client) CreateOVHCluster(ctx context.Context, request OVHClusterRequest) (ImportClusterResponse, error) {
	return client.CreateProvisionedCluster(ctx, LaneOVH, request)
}

// CreateScalewayCluster provisions a new Scaleway-backed cluster.
func (client *Client) CreateScalewayCluster(ctx context.Context, request ScalewayClusterRequest) (ImportClusterResponse, error) {
	return client.CreateProvisionedCluster(ctx, LaneScaleway, request)
}
