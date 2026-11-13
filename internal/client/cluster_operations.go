// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package client

import (
	"context"
	"net/http"
	"net/url"
)

// ProvisionedClusterLanes are the cluster kinds Ankra provisions, whose
// lane-scoped routes (Gateway API, k3s components) answer under
// /api/v1/clusters/{lane}/{cluster_id}.
var ProvisionedClusterLanes = []string{LaneAnkraCloud, LaneAWS, LaneDigitalOcean, LaneHetzner, LaneMorpheus, LaneOVH, LaneProxmox, LaneScaleway, LaneUpCloud}

func clusterLanePath(lane, clusterID, suffix string) string {
	return "/api/v1/clusters/" + url.PathEscape(lane) + "/" + url.PathEscape(clusterID) + suffix
}

func organisationClusterPath(clusterID, suffix string) string {
	return "/api/v1/org/clusters/" + url.PathEscape(clusterID) + suffix
}

// ClusterEnvironment is what a cluster is called and what it is for.
type ClusterEnvironment struct {
	Environment string  `json:"environment"`
	Criticality *string `json:"criticality,omitempty"`
}

// SetClusterEnvironment sets the environment label and, when given, the
// criticality, in one transaction.
func (client *Client) SetClusterEnvironment(ctx context.Context, clusterID string, environment ClusterEnvironment) (*ClusterEnvironment, error) {
	var stored ClusterEnvironment
	requestPath := "/api/v1/org/clusters/imported/" + url.PathEscape(clusterID) + "/environment"
	if requestError := client.doRequest(ctx, http.MethodPatch, requestPath, environment, &stored); requestError != nil {
		return nil, requestError
	}
	return &stored, nil
}

type clusterCriticality struct {
	Criticality string `json:"criticality"`
}

// GetClusterCriticality reads what a cluster is for. A cluster never
// classified reads as production.
func (client *Client) GetClusterCriticality(ctx context.Context, clusterID string) (string, error) {
	var view clusterCriticality
	if requestError := client.doRequest(ctx, http.MethodGet, organisationClusterPath(clusterID, "/criticality"), nil, &view); requestError != nil {
		return "", requestError
	}
	return view.Criticality, nil
}

// StateSnapshotRetention is a cluster's retention window for its scheduled
// etcd backups.
type StateSnapshotRetention struct {
	RetentionDays        int64 `json:"retention_days"`
	IsDefault            bool  `json:"is_default"`
	DefaultRetentionDays int64 `json:"default_retention_days"`
	KeepLatest           int64 `json:"keep_latest"`
}

// GetStateSnapshotRetention reads the retention window.
func (client *Client) GetStateSnapshotRetention(ctx context.Context, clusterID string) (*StateSnapshotRetention, error) {
	var retention StateSnapshotRetention
	if requestError := client.doRequest(ctx, http.MethodGet, organisationClusterPath(clusterID, "/state-snapshot-retention"), nil, &retention); requestError != nil {
		return nil, requestError
	}
	return &retention, nil
}

// SetStateSnapshotRetention sets the window, 1 to 90 days. A shorter window
// deletes the daily backups that fall outside it.
func (client *Client) SetStateSnapshotRetention(ctx context.Context, clusterID string, days int64) (*StateSnapshotRetention, error) {
	var retention StateSnapshotRetention
	if requestError := client.doRequest(ctx, http.MethodPut, organisationClusterPath(clusterID, "/state-snapshot-retention"),
		map[string]int64{"retention_days": days}, &retention); requestError != nil {
		return nil, requestError
	}
	return &retention, nil
}

// ClusterDataSourceCredential is the credential a metrics or log source
// resolves to; never its secret.
type ClusterDataSourceCredential struct {
	Name     string  `json:"name"`
	State    string  `json:"state"`
	Username *string `json:"username"`
}

// ClusterMetricsSource is a cluster's registered metrics source.
type ClusterMetricsSource struct {
	Provider       string                       `json:"provider"`
	Endpoint       string                       `json:"endpoint"`
	State          string                       `json:"state"`
	Flavor         *string                      `json:"flavor"`
	CredentialName *string                      `json:"credential_name"`
	Credential     *ClusterDataSourceCredential `json:"credential"`
}

// ClusterLogSource is a cluster's registered log source.
type ClusterLogSource struct {
	Provider       string                       `json:"provider"`
	Endpoint       string                       `json:"endpoint"`
	State          string                       `json:"state"`
	VerifySSL      bool                         `json:"verify_ssl"`
	IndexPattern   *string                      `json:"index_pattern"`
	CredentialName *string                      `json:"credential_name"`
	Credential     *ClusterDataSourceCredential `json:"credential"`
}

// GetClusterMetricsSource reads the cluster's metrics source. A cluster
// without one answers 404.
func (client *Client) GetClusterMetricsSource(ctx context.Context, clusterID string) (*ClusterMetricsSource, error) {
	var source ClusterMetricsSource
	requestPath := "/api/v1/org/clusters/imported/" + url.PathEscape(clusterID) + "/metrics-source"
	if requestError := client.doRequest(ctx, http.MethodGet, requestPath, nil, &source); requestError != nil {
		return nil, requestError
	}
	return &source, nil
}

// GetClusterLogSource reads the cluster's log source. A cluster without one
// answers 404.
func (client *Client) GetClusterLogSource(ctx context.Context, clusterID string) (*ClusterLogSource, error) {
	var source ClusterLogSource
	requestPath := "/api/v1/org/clusters/imported/" + url.PathEscape(clusterID) + "/log-source"
	if requestError := client.doRequest(ctx, http.MethodGet, requestPath, nil, &source); requestError != nil {
		return nil, requestError
	}
	return &source, nil
}

// ClusterGeneratedDNSZone is a cluster's generated public DNS zone. State
// "none" means the cluster has no zone.
type ClusterGeneratedDNSZone struct {
	FQDN  string `json:"fqdn"`
	State string `json:"state"`
}

func clusterDNSZonePath(clusterID string) string {
	return "/api/v1/clusters/" + url.PathEscape(clusterID) + "/dns-zone"
}

// GetClusterGeneratedDNSZone reads the cluster's zone without creating one.
func (client *Client) GetClusterGeneratedDNSZone(ctx context.Context, clusterID string) (*ClusterGeneratedDNSZone, error) {
	var zone ClusterGeneratedDNSZone
	if requestError := client.doRequest(ctx, http.MethodGet, clusterDNSZonePath(clusterID), nil, &zone); requestError != nil {
		return nil, requestError
	}
	return &zone, nil
}

// EnableClusterGeneratedDNSZone creates the zone, or retries a failed one.
// It is idempotent: an existing zone keeps its FQDN.
func (client *Client) EnableClusterGeneratedDNSZone(ctx context.Context, clusterID string) (*ClusterGeneratedDNSZone, error) {
	var zone ClusterGeneratedDNSZone
	if requestError := client.doRequest(ctx, http.MethodPost, clusterDNSZonePath(clusterID), nil, &zone); requestError != nil {
		return nil, requestError
	}
	return &zone, nil
}

// DisableClusterGeneratedDNSZone hands the zone back for teardown.
func (client *Client) DisableClusterGeneratedDNSZone(ctx context.Context, clusterID string) error {
	return client.doRequest(ctx, http.MethodDelete, clusterDNSZonePath(clusterID), nil, nil)
}

// GatewayAPIStatus is where the Kubernetes Gateway API stands on a cluster.
type GatewayAPIStatus struct {
	Available         bool   `json:"available"`
	UnavailableReason string `json:"unavailable_reason"`
	Enabled           bool   `json:"enabled"`
	Phase             string `json:"phase"`
}

// GetGatewayAPIStatus reads the Gateway API status of a provisioned cluster.
func (client *Client) GetGatewayAPIStatus(ctx context.Context, lane, clusterID string) (*GatewayAPIStatus, error) {
	var status GatewayAPIStatus
	if requestError := client.doRequest(ctx, http.MethodGet, clusterLanePath(lane, clusterID, "/gateway-api"), nil, &status); requestError != nil {
		return nil, requestError
	}
	return &status, nil
}

// EnableGatewayAPI turns the Gateway API on. It is idempotent and
// enable-only: the platform has no route that turns it off.
func (client *Client) EnableGatewayAPI(ctx context.Context, lane, clusterID string) (*GatewayAPIStatus, error) {
	var status GatewayAPIStatus
	if requestError := client.doRequest(ctx, http.MethodPut, clusterLanePath(lane, clusterID, "/gateway-api"), nil, &status); requestError != nil {
		return nil, requestError
	}
	return &status, nil
}

type k3sDisabledComponents struct {
	Components []string `json:"k3s_disabled_components"`
}

// GetK3sDisabledComponents reads the k3s components the platform keeps
// disabled. A kubeadm or Scaleway cluster answers 409.
func (client *Client) GetK3sDisabledComponents(ctx context.Context, lane, clusterID string) ([]string, error) {
	var view k3sDisabledComponents
	if requestError := client.doRequest(ctx, http.MethodGet, clusterLanePath(lane, clusterID, "/k3s-disabled-components"), nil, &view); requestError != nil {
		return nil, requestError
	}
	return view.Components, nil
}

// SetK3sDisabledComponents replaces the list; the platform restarts k3s on
// one control plane at a time to apply it.
func (client *Client) SetK3sDisabledComponents(ctx context.Context, lane, clusterID string, components []string) ([]string, error) {
	var view k3sDisabledComponents
	if requestError := client.doRequest(ctx, http.MethodPut, clusterLanePath(lane, clusterID, "/k3s-disabled-components"),
		k3sDisabledComponents{Components: components}, &view); requestError != nil {
		return nil, requestError
	}
	return view.Components, nil
}

type upcloudLoadBalancerPlan struct {
	LoadBalancerPlan *string `json:"load_balancer_plan"`
}

// GetUpcloudLoadBalancerPlan reads the plan recorded for an UpCloud
// cluster's ingress load balancer; nil when none was ever recorded.
func (client *Client) GetUpcloudLoadBalancerPlan(ctx context.Context, clusterID string) (*string, error) {
	var view upcloudLoadBalancerPlan
	if requestError := client.doRequest(ctx, http.MethodGet, clusterLanePath(LaneUpCloud, clusterID, "/load-balancer-plan"), nil, &view); requestError != nil {
		return nil, requestError
	}
	return view.LoadBalancerPlan, nil
}

// SetUpcloudLoadBalancerPlan records a new ingress load balancer plan.
func (client *Client) SetUpcloudLoadBalancerPlan(ctx context.Context, clusterID, plan string) error {
	return client.doRequest(ctx, http.MethodPut, clusterLanePath(LaneUpCloud, clusterID, "/load-balancer-plan"),
		map[string]string{"load_balancer_plan": plan}, nil)
}

// SetUpcloudGatewayPlan changes the plan of an UpCloud cluster's NAT
// gateways. It answers once the change is recorded; outbound access is
// interrupted while UpCloud rebuilds the gateway.
func (client *Client) SetUpcloudGatewayPlan(ctx context.Context, clusterID, plan string) error {
	return client.doRequest(ctx, http.MethodPut, clusterLanePath(LaneUpCloud, clusterID, "/gateway-plan"),
		map[string]string{"gateway_plan": plan}, nil)
}

type upcloudZonePool struct {
	Zones []string `json:"zones"`
}

// ExtendUpcloudZonePool adds zones to an UpCloud cluster's zone pool and
// answers the whole pool. Zones can only be added.
func (client *Client) ExtendUpcloudZonePool(ctx context.Context, clusterID string, zones []string) ([]string, error) {
	var pool upcloudZonePool
	if requestError := client.doRequest(ctx, http.MethodPut, clusterLanePath(LaneUpCloud, clusterID, "/zones"), upcloudZonePool{Zones: zones}, &pool); requestError != nil {
		return nil, requestError
	}
	return pool.Zones, nil
}

// SetOVHGatewayModel changes the model of an OVH cluster's managed gateway.
func (client *Client) SetOVHGatewayModel(ctx context.Context, clusterID, model string) error {
	return client.doRequest(ctx, http.MethodPut, clusterLanePath(LaneOVH, clusterID, "/gateway-model"),
		map[string]string{"gateway_model": model}, nil)
}
