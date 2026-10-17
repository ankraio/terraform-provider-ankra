// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package client

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
)

// Lanes the platform provisions beyond the original five clouds. Each is a
// path segment under /api/v1/clusters, like the Lane* constants in
// provisioned.go.
const (
	LaneAWS        = "aws"
	LaneProxmox    = "proxmox"
	LaneMorpheus   = "morpheus"
	LaneAnkraCloud = "ankracloud"
)

// The request contracts below mirror the platform's decodeCreate*Request
// readers (cluster go/internal/providerapi). Fields with a server-side
// default are sent whenever the provider plans a value for them; fields the
// platform reads as "absent means something different from empty" are
// pointers or omitempty.

// AWSClusterRequest is the payload for POST /api/v1/clusters/aws.
// external_cloud_provider is not sent: the platform requires it on, and an
// explicit false is refused.
type AWSClusterRequest struct {
	Name                 string   `json:"name"`
	Description          *string  `json:"description,omitempty"`
	CredentialID         string   `json:"credential_id"`
	SSHKeyCredentialID   string   `json:"ssh_key_credential_id"`
	Region               string   `json:"region"`
	VpcID                string   `json:"vpc_id,omitempty"`
	NodeSubnetIDs        []string `json:"node_subnet_ids,omitempty"`
	BastionSubnetID      string   `json:"bastion_subnet_id,omitempty"`
	NetworkIPRange       string   `json:"network_ip_range,omitempty"`
	AvailabilityZones    []string `json:"availability_zones,omitempty"`
	NatGatewaySingleZone bool     `json:"nat_gateway_single_zone,omitempty"`
	EgressMode           string   `json:"egress_mode,omitempty"`
	BastionInstanceType  string   `json:"bastion_instance_type"`
	BastionAllowedIPs    []string `json:"bastion_allowed_ips"`
	ControlPlaneCount    int64    `json:"control_plane_count"`
	ControlPlaneType     string   `json:"control_plane_type"`
	WorkerCount          int64    `json:"worker_count"`
	WorkerType           string   `json:"worker_type"`
	Distribution         string   `json:"distribution"`
	KubernetesVersion    *string  `json:"kubernetes_version,omitempty"`
	EtcdTopology         string   `json:"etcd_topology"`
	EtcdNodeCount        int64    `json:"etcd_node_count"`
	EtcdType             string   `json:"etcd_type"`
	CNI                  string   `json:"cni"`
	UbuntuSeries         string   `json:"ubuntu_series,omitempty"`
	Architecture         string   `json:"architecture"`
	RootVolumeGiB        int64    `json:"root_volume_gib"`
	GitopsCredentialName *string  `json:"gitops_credential_name,omitempty"`
	GitopsRepository     *string  `json:"gitops_repository,omitempty"`
	GitopsBranch         string   `json:"gitops_branch"`
	IncludeNetworking    bool     `json:"include_networking"`
	IncludeDNS           bool     `json:"include_dns"`
	RetentionPolicy      string   `json:"retention_policy"`
}

// ProxmoxClusterRequest is the payload for POST /api/v1/clusters/proxmox.
// Proxmox has no cloud controller and no GitOps source on its create
// contract, so neither is sent.
type ProxmoxClusterRequest struct {
	Name                     string   `json:"name"`
	Description              *string  `json:"description,omitempty"`
	CredentialID             string   `json:"credential_id"`
	SSHKeyCredentialID       string   `json:"ssh_key_credential_id"`
	Node                     string   `json:"node"`
	PlacementNodes           []string `json:"placement_nodes,omitempty"`
	Bridge                   string   `json:"bridge"`
	Storage                  string   `json:"storage"`
	Template                 string   `json:"template,omitempty"`
	BastionInstanceType      string   `json:"bastion_instance_type"`
	ControlPlaneCount        int64    `json:"control_plane_count"`
	ControlPlaneInstanceType string   `json:"control_plane_instance_type"`
	WorkerCount              int64    `json:"worker_count"`
	WorkerInstanceType       string   `json:"worker_instance_type"`
	Distribution             string   `json:"distribution"`
	KubernetesVersion        *string  `json:"kubernetes_version,omitempty"`
	EtcdTopology             string   `json:"etcd_topology"`
	EtcdNodeCount            int64    `json:"etcd_node_count"`
	EtcdInstanceType         string   `json:"etcd_instance_type"`
	CNI                      string   `json:"cni"`
	NetworkMode              string   `json:"network_mode,omitempty"`
	SitePublicIP             string   `json:"site_public_ip,omitempty"`
	IncludeNetworking        bool     `json:"include_networking"`
	IncludeDNS               bool     `json:"include_dns"`
}

// MorpheusClusterRequest is the payload for POST /api/v1/clusters/morpheus.
// HPE Morpheus sizes nodes by numeric plan id rather than an instance-type
// name.
type MorpheusClusterRequest struct {
	Name               string  `json:"name"`
	Description        *string `json:"description,omitempty"`
	CredentialID       string  `json:"credential_id"`
	SSHKeyCredentialID string  `json:"ssh_key_credential_id"`
	GroupID            int64   `json:"group_id"`
	CloudID            int64   `json:"cloud_id"`
	NetworkID          int64   `json:"network_id"`
	LayoutID           int64   `json:"layout_id"`
	VirtualImageID     *int64  `json:"virtual_image_id,omitempty"`
	BastionPlanID      int64   `json:"bastion_plan_id"`
	ControlPlaneCount  int64   `json:"control_plane_count"`
	ControlPlanePlanID int64   `json:"control_plane_plan_id"`
	WorkerCount        int64   `json:"worker_count"`
	WorkerPlanID       int64   `json:"worker_plan_id"`
	Distribution       string  `json:"distribution"`
	KubernetesVersion  *string `json:"kubernetes_version,omitempty"`
	EtcdTopology       string  `json:"etcd_topology"`
	EtcdNodeCount      int64   `json:"etcd_node_count"`
	EtcdPlanID         *int64  `json:"etcd_plan_id,omitempty"`
	CNI                string  `json:"cni"`
	IncludeNetworking  bool    `json:"include_networking"`
	IncludeDNS         bool    `json:"include_dns"`
}

// AnkraCloudClusterRequest is the payload for POST /api/v1/clusters/ankracloud.
// credential_id and ssh_key_credential_id are optional: without a credential
// the platform uses the organisation's built-in Ankra Cloud credential.
type AnkraCloudClusterRequest struct {
	Name                 string   `json:"name"`
	Description          *string  `json:"description,omitempty"`
	CredentialID         *string  `json:"credential_id,omitempty"`
	RuntimeCredentialID  *string  `json:"runtime_credential_id,omitempty"`
	SSHKeyCredentialID   *string  `json:"ssh_key_credential_id,omitempty"`
	Zone                 string   `json:"zone"`
	Template             string   `json:"template,omitempty"`
	PrivateNetworkID     *string  `json:"private_network_id,omitempty"`
	NetworkIPRange       *string  `json:"network_ip_range,omitempty"`
	NetworkMode          string   `json:"network_mode"`
	BastionPlan          string   `json:"bastion_plan"`
	BastionAllowedIPs    []string `json:"bastion_allowed_ips,omitempty"`
	ControlPlaneCount    int64    `json:"control_plane_count"`
	ControlPlanePlan     string   `json:"control_plane_plan"`
	WorkerCount          int64    `json:"worker_count"`
	WorkerPlan           string   `json:"worker_plan,omitempty"`
	Distribution         string   `json:"distribution"`
	KubernetesVersion    *string  `json:"kubernetes_version,omitempty"`
	EtcdTopology         string   `json:"etcd_topology"`
	EtcdNodeCount        int64    `json:"etcd_node_count"`
	EtcdPlan             string   `json:"etcd_plan,omitempty"`
	CNI                  string   `json:"cni"`
	GitopsCredentialName *string  `json:"gitops_credential_name,omitempty"`
	GitopsRepository     *string  `json:"gitops_repository,omitempty"`
	GitopsBranch         string   `json:"gitops_branch"`
	IncludeNetworking    bool     `json:"include_networking"`
	IncludeDNS           bool     `json:"include_dns"`
	RetentionPolicy      string   `json:"retention_policy"`
}

// LaneDeprovisionOptions are the query flags of DELETE
// /api/v1/clusters/{lane}/{cluster_id}.
type LaneDeprovisionOptions struct {
	// Force skips the platform's usual teardown guards.
	Force bool
	// AcceptVolumeDataLoss consents to deleting the cluster's persistent
	// volumes. Without it the platform refuses with 409 when the cluster
	// still holds volumes it would delete.
	AcceptVolumeDataLoss bool
}

// CreateLaneCluster provisions a cluster on one of the lanes in this file.
// It is CreateProvisionedCluster under a name that keeps the lane family
// together.
func (client *Client) CreateLaneCluster(ctx context.Context, lane string, request any) (ImportClusterResponse, error) {
	return client.CreateProvisionedCluster(ctx, lane, request)
}

// DeprovisionLaneCluster deprovisions a cluster with the full set of
// deprovision flags, including the volume-loss consent the AWS and Ankra Cloud
// lanes ask for. A 404 is treated as success so deletes are idempotent.
func (client *Client) DeprovisionLaneCluster(ctx context.Context, lane, clusterID string, options LaneDeprovisionOptions) error {
	query := url.Values{}
	query.Set("force", strconv.FormatBool(options.Force))
	if options.AcceptVolumeDataLoss {
		query.Set("accept_volume_data_loss", "true")
	}
	path := "/api/v1/clusters/" + url.PathEscape(lane) + "/" + url.PathEscape(clusterID) + "?" + query.Encode()
	return client.doRequest(ctx, http.MethodDelete, path, nil, nil, http.StatusNotFound)
}
