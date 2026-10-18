// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
)

// Managed Kubernetes providers: the {provider} path segment under
// /api/v1/clusters/managed (cluster go/internal/usecase/managedk8s).
const (
	ManagedProviderDOKS          = "doks"
	ManagedProviderUKS           = "uks"
	ManagedProviderGKE           = "gke"
	ManagedProviderOVHMKS        = "ovh_mks"
	ManagedProviderAKS           = "aks"
	ManagedProviderEKS           = "eks"
	ManagedProviderKapsule       = "kapsule"
	ManagedProviderAnkraCloudK8s = "ankracloud_k8s"
)

// ManagedProviders lists every managed Kubernetes provider the platform
// accepts.
var ManagedProviders = []string{
	ManagedProviderDOKS, ManagedProviderUKS, ManagedProviderGKE, ManagedProviderOVHMKS,
	ManagedProviderAKS, ManagedProviderEKS, ManagedProviderKapsule, ManagedProviderAnkraCloudK8s,
}

// ManagedClusterProviderReadyStates are the cluster states in which a
// managed cluster exists at its provider and its node pools accept writes.
// The platform parks a new cluster in provider_without_access until the
// agent connects, and reports online after.
var ManagedClusterProviderReadyStates = []string{"provider_without_access", "provider_access", "kubernetes_access", "online", "running"}

// ManagedClusterOnlineStates are the states of a managed cluster whose Ankra
// agent has connected.
var ManagedClusterOnlineStates = []string{"online", "running"}

// ManagedNodePoolLabelKeys is the node label each managed provider stamps
// with the name of the pool a node belongs to (cluster enginekit
// clusterengine.managedNodePoolLabelKeys). UKS and OVH MKS have none Ankra
// knows, so their pools' nodes cannot be found.
var ManagedNodePoolLabelKeys = map[string]string{
	ManagedProviderGKE:           "cloud.google.com/gke-nodepool",
	ManagedProviderEKS:           "eks.amazonaws.com/nodegroup",
	ManagedProviderAKS:           "kubernetes.azure.com/agentpool",
	ManagedProviderDOKS:          "doks.digitalocean.com/node-pool",
	ManagedProviderKapsule:       "k8s.scaleway.com/pool-name",
	ManagedProviderAnkraCloudK8s: "kubernetes.ankra.cloud/node-pool",
}

// ManagedClusterFailureStates end a managed cluster wait with an error.
var ManagedClusterFailureStates = []string{"deprovisioning", "stopped"}

// managedProviderDeleteIsExplicit reports whether a provider's bare DELETE is
// only a disconnect. For Kapsule and Ankra Cloud Kubernetes the provider
// cluster is deleted through POST .../delete-provider-cluster instead.
func managedProviderDeleteIsExplicit(provider string) bool {
	return provider == ManagedProviderKapsule || provider == ManagedProviderAnkraCloudK8s
}

// ManagedNodePoolAutoscaling is the nested autoscaling object of a pool.
type ManagedNodePoolAutoscaling struct {
	Enabled  bool  `json:"enabled"`
	MinCount int64 `json:"min_count"`
	MaxCount int64 `json:"max_count"`
}

// ManagedNodePoolSpot asks for spot or preemptible capacity.
type ManagedNodePoolSpot struct {
	Enabled  bool     `json:"enabled"`
	MaxPrice *float64 `json:"max_price,omitempty"`
}

// ManagedNodePoolCreate is one pool of a create body and the body of POST
// .../node-pools.
type ManagedNodePoolCreate struct {
	Name             string                      `json:"name"`
	Size             string                      `json:"size"`
	Count            int64                       `json:"count"`
	Labels           map[string]string           `json:"labels,omitempty"`
	Taints           []NodeGroupTaint            `json:"taints,omitempty"`
	Autoscaling      *ManagedNodePoolAutoscaling `json:"autoscaling,omitempty"`
	Zone             *string                     `json:"zone,omitempty"`
	RootVolumeType   *string                     `json:"root_volume_type,omitempty"`
	RootVolumeSizeGB *int64                      `json:"root_volume_size_gb,omitempty"`
	Spot             *ManagedNodePoolSpot        `json:"spot,omitempty"`
}

// ManagedNodePoolUpdate is the PATCH .../node-pools/{name} body. Every field
// is optional and an absent field is left unchanged.
type ManagedNodePoolUpdate struct {
	Count              *int64 `json:"count,omitempty"`
	AutoscalingEnabled *bool  `json:"autoscaling_enabled,omitempty"`
	AutoscalingMin     *int64 `json:"autoscaling_min,omitempty"`
	AutoscalingMax     *int64 `json:"autoscaling_max,omitempty"`
}

// ManagedNodePoolAutoscalingSummary is the listing's view of a pool's
// autoscaling.
type ManagedNodePoolAutoscalingSummary struct {
	Enabled  bool   `json:"enabled"`
	MinCount *int64 `json:"min_count"`
	MaxCount *int64 `json:"max_count"`
}

// ManagedNodePool is one row of GET .../node-pools.
type ManagedNodePool struct {
	Name              string                             `json:"name"`
	Size              string                             `json:"size"`
	Count             int64                              `json:"count"`
	Status            *string                            `json:"status"`
	ProviderID        *string                            `json:"provider_id"`
	Zone              *string                            `json:"zone"`
	RootVolumeType    *string                            `json:"root_volume_type"`
	RootVolumeSizeGB  *int64                             `json:"root_volume_size_gb"`
	Labels            map[string]string                  `json:"labels"`
	Taints            []map[string]string                `json:"taints"`
	Autoscaling       *ManagedNodePoolAutoscalingSummary `json:"autoscaling"`
	Spot              *ManagedNodePoolSpot               `json:"spot"`
	ExternallyManaged bool                               `json:"externally_managed"`
	ObservedCount     *int64                             `json:"observed_count"`
}

// ManagedClusterCreate is the POST /api/v1/clusters/managed/{provider} body.
// ProviderOptions is the provider's own option object (aks, eks, gke,
// ovh_mks, kapsule, ankracloud_k8s, uks); it is sent under the key named
// after the provider, which is how the platform reads it.
type ManagedClusterCreate struct {
	Name                 string                  `json:"name"`
	Description          *string                 `json:"description,omitempty"`
	CredentialID         *string                 `json:"credential_id,omitempty"`
	Location             string                  `json:"location"`
	KubernetesVersion    *string                 `json:"kubernetes_version,omitempty"`
	CNI                  *string                 `json:"cni,omitempty"`
	HA                   *bool                   `json:"ha,omitempty"`
	ClusterPlan          *string                 `json:"cluster_plan,omitempty"`
	NodePools            []ManagedNodePoolCreate `json:"node_pools"`
	GitopsCredentialName *string                 `json:"gitops_credential_name,omitempty"`
	GitopsRepository     *string                 `json:"gitops_repository,omitempty"`
	GitopsBranch         string                  `json:"gitops_branch"`
	Provider             string                  `json:"-"`
	ProviderOptions      json.RawMessage         `json:"-"`
}

// MarshalJSON places ProviderOptions under the provider's key.
func (request ManagedClusterCreate) MarshalJSON() ([]byte, error) {
	type plain ManagedClusterCreate
	encoded, err := json.Marshal(plain(request))
	if err != nil {
		return nil, err
	}
	if len(request.ProviderOptions) == 0 {
		return encoded, nil
	}
	var members map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &members); err != nil {
		return nil, err
	}
	members[request.Provider] = request.ProviderOptions
	return json.Marshal(members)
}

type managedClusterCreated struct {
	ClusterID string `json:"cluster_id"`
	Name      string `json:"name"`
}

type managedNodePoolListEnvelope struct {
	NodePools []ManagedNodePool `json:"node_pools"`
}

func managedClusterPath(provider, clusterID string) string {
	path := "/api/v1/clusters/managed/" + url.PathEscape(provider)
	if clusterID != "" {
		path += "/" + url.PathEscape(clusterID)
	}
	return path
}

// CreateManagedCluster creates a managed Kubernetes cluster at the provider
// and returns its Ankra cluster id.
func (client *Client) CreateManagedCluster(ctx context.Context, request ManagedClusterCreate) (string, error) {
	var created managedClusterCreated
	if err := client.doRequest(ctx, http.MethodPost, managedClusterPath(request.Provider, ""), request, &created); err != nil {
		return "", err
	}
	if created.ClusterID == "" {
		return "", fmt.Errorf("the platform did not return a cluster_id for the %s cluster", request.Provider)
	}
	return created.ClusterID, nil
}

// Managed cluster destroy modes.
const (
	// ManagedDestroyModeDisconnect removes the cluster from Ankra and leaves
	// the provider cluster running.
	ManagedDestroyModeDisconnect = "disconnect"
	// ManagedDestroyModeDeleteProviderCluster deletes the provider cluster
	// too.
	ManagedDestroyModeDeleteProviderCluster = "delete_provider_cluster"
)

// ManagedClusterDeleteResult is the deprovision answer. The platform answers
// 200 with success=true and nothing marked when it did not recognise the
// cluster as one of this provider's (its VerifyCloudCluster check failed), so
// a caller must not read success alone as "deleted".
type ManagedClusterDeleteResult struct {
	Success         bool     `json:"success"`
	ClusterID       string   `json:"cluster_id"`
	OperationID     *string  `json:"operation_id"`
	ResourcesMarked int64    `json:"resources_marked"`
	Errors          []string `json:"errors"`
}

// DeleteManagedCluster disconnects or deletes a managed cluster. For
// disconnect it calls POST .../disconnect; for a provider delete it calls
// DELETE, except for Kapsule and Ankra Cloud Kubernetes, whose bare DELETE is
// only a disconnect and whose provider delete is POST
// .../delete-provider-cluster. force overrides the platform's ownership
// check: it deletes an imported cluster's provider cluster too.
// retentionPolicy is "delete" or "retain". A 404 is returned to the caller.
func (client *Client) DeleteManagedCluster(ctx context.Context, provider, clusterID, mode string, force bool, retentionPolicy string) (*ManagedClusterDeleteResult, error) {
	query := url.Values{}
	if force {
		query.Set("force", strconv.FormatBool(force))
	}
	if retentionPolicy != "" && mode == ManagedDestroyModeDeleteProviderCluster {
		query.Set("retention_policy", retentionPolicy)
	}
	path := managedClusterPath(provider, clusterID)
	method := http.MethodDelete
	switch {
	case mode == ManagedDestroyModeDisconnect:
		path += "/disconnect"
		method = http.MethodPost
	case managedProviderDeleteIsExplicit(provider):
		path += "/delete-provider-cluster"
		method = http.MethodPost
	}
	if encoded := query.Encode(); encoded != "" {
		path += "?" + encoded
	}
	var result ManagedClusterDeleteResult
	if err := client.doRequest(ctx, method, path, nil, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// ListManagedNodePools returns the node pools of a managed cluster. A cluster
// that no longer exists is a NotFound APIError.
func (client *Client) ListManagedNodePools(ctx context.Context, provider, clusterID string) ([]ManagedNodePool, error) {
	var envelope managedNodePoolListEnvelope
	if err := client.doRequest(ctx, http.MethodGet, managedClusterPath(provider, clusterID)+"/node-pools", nil, &envelope); err != nil {
		return nil, err
	}
	return envelope.NodePools, nil
}

// GetManagedNodePool finds one pool in the listing. A pool that is not listed
// is a NotFound APIError.
func (client *Client) GetManagedNodePool(ctx context.Context, provider, clusterID, poolName string) (*ManagedNodePool, error) {
	pools, err := client.ListManagedNodePools(ctx, provider, clusterID)
	if err != nil {
		return nil, err
	}
	for index := range pools {
		if pools[index].Name == poolName {
			return &pools[index], nil
		}
	}
	return nil, &APIError{
		Method: http.MethodGet, Path: managedClusterPath(provider, clusterID) + "/node-pools",
		StatusCode: http.StatusNotFound, Body: "node pool " + poolName + " not found",
	}
}

// AddManagedNodePool adds a pool to a managed cluster.
func (client *Client) AddManagedNodePool(ctx context.Context, provider, clusterID string, request ManagedNodePoolCreate) error {
	return client.doRequest(ctx, http.MethodPost, managedClusterPath(provider, clusterID)+"/node-pools", request, nil)
}

// UpdateManagedNodePool changes a pool's count or autoscaling in place.
func (client *Client) UpdateManagedNodePool(ctx context.Context, provider, clusterID, poolName string, request ManagedNodePoolUpdate) error {
	path := managedClusterPath(provider, clusterID) + "/node-pools/" + url.PathEscape(poolName)
	return client.doRequest(ctx, http.MethodPatch, path, request, nil)
}

// ManagedNodePoolReplace is the POST .../node-pools/{name}/replace body: a
// blue/green replacement of the pool with another size. ReplacementPoolName
// names the new pool; absent, the platform derives one.
type ManagedNodePoolReplace struct {
	Size                string  `json:"size"`
	ReplacementPoolName *string `json:"replacement_pool_name,omitempty"`
}

// ManagedNodePoolReplacement is the platform's acceptance of a pool
// replacement. OperationID is the execution that creates the new pool, waits
// for it to be ready, and deletes the replaced one.
type ManagedNodePoolReplacement struct {
	OperationID         string `json:"operation_id"`
	NodePoolName        string `json:"node_pool_name"`
	ReplacementPoolName string `json:"replacement_pool_name"`
	OldSize             string `json:"old_size"`
	NewSize             string `json:"new_size"`
}

// ReplaceManagedNodePool asks for a blue/green replacement of a pool with a
// new size. The platform answers 202 once it has accepted the intent; the
// replacement itself runs as the returned operation.
func (client *Client) ReplaceManagedNodePool(ctx context.Context, provider, clusterID, poolName string, request ManagedNodePoolReplace) (*ManagedNodePoolReplacement, error) {
	path := managedClusterPath(provider, clusterID) + "/node-pools/" + url.PathEscape(poolName) + "/replace"
	var accepted ManagedNodePoolReplacement
	if err := client.doRequest(ctx, http.MethodPost, path, request, &accepted); err != nil {
		return nil, err
	}
	return &accepted, nil
}

// DeleteManagedNodePool removes a pool. A 404 is returned to the caller.
func (client *Client) DeleteManagedNodePool(ctx context.Context, provider, clusterID, poolName string) error {
	path := managedClusterPath(provider, clusterID) + "/node-pools/" + url.PathEscape(poolName)
	return client.doRequest(ctx, http.MethodDelete, path, nil, nil)
}
