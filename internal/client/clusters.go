// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package client

import (
	"context"
	"net/http"
	"net/url"
)

// GitRepository is the source-of-truth git repository for an imported cluster.
type GitRepository struct {
	Provider       string `json:"provider"`
	CredentialName string `json:"credential_name"`
	Branch         string `json:"branch"`
	Repository     string `json:"repository"`
}

// Manifest is a raw Kubernetes manifest deployed as part of a stack.
type Manifest struct {
	Name           string   `json:"name"`
	Namespace      string   `json:"namespace,omitempty"`
	ManifestBase64 string   `json:"manifest_base64"`
	Parents        []string `json:"parents,omitempty"`
	FromFile       string   `json:"from_file,omitempty"`
}

// Addon is a Helm-chart addon deployed as part of a stack.
type Addon struct {
	Name              string   `json:"name"`
	ChartName         string   `json:"chart_name"`
	ChartVersion      string   `json:"chart_version"`
	RepositoryURL     string   `json:"repository_url"`
	Namespace         string   `json:"namespace"`
	ConfigurationType string   `json:"configuration_type,omitempty"`
	Configuration     string   `json:"configuration,omitempty"`
	Parents           []string `json:"parents,omitempty"`
	JobConfiguration  string   `json:"job_configuration,omitempty"`
}

// Stack groups manifests and addons applied to a cluster.
type Stack struct {
	Name        string     `json:"name"`
	Description string     `json:"description,omitempty"`
	Manifests   []Manifest `json:"manifests,omitempty"`
	Addons      []Addon    `json:"addons,omitempty"`
}

// ImportClusterSpec is the desired state sent to the import endpoint.
type ImportClusterSpec struct {
	GitRepository GitRepository `json:"git_repository"`
	Stacks        []Stack       `json:"stacks"`
}

// ImportClusterRequest is the payload for POST /api/v1/clusters/import.
type ImportClusterRequest struct {
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Spec        ImportClusterSpec `json:"spec"`
}

// ImportClusterResponse is returned by the import endpoint.
type ImportClusterResponse struct {
	ClusterID     string `json:"cluster_id"`
	ImportCommand string `json:"import_command"`
}

// Cluster is a cluster as returned by the list endpoint.
type Cluster struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// ImportCluster creates or updates a cluster via the import endpoint.
func (client *Client) ImportCluster(ctx context.Context, request ImportClusterRequest) (ImportClusterResponse, error) {
	var response ImportClusterResponse
	if err := client.doRequest(ctx, http.MethodPost, "/api/v1/clusters/import", request, &response); err != nil {
		return ImportClusterResponse{}, err
	}
	return response, nil
}

// ListClusters returns all clusters visible to the authenticated token.
func (client *Client) ListClusters(ctx context.Context) ([]Cluster, error) {
	var response struct {
		Clusters []Cluster `json:"clusters"`
	}
	if err := client.doRequest(ctx, http.MethodGet, "/api/v1/clusters", nil, &response); err != nil {
		return nil, err
	}
	return response.Clusters, nil
}

// GetClusterByID returns the cluster with the given id, or (nil, nil) when it
// no longer exists.
func (client *Client) GetClusterByID(ctx context.Context, clusterID string) (*Cluster, error) {
	clusters, err := client.ListClusters(ctx)
	if err != nil {
		return nil, err
	}
	for index := range clusters {
		if clusters[index].ID == clusterID {
			return &clusters[index], nil
		}
	}
	return nil, nil
}

// DeleteCluster deletes a cluster by name. A 404 is treated as success so
// deletes are idempotent.
func (client *Client) DeleteCluster(ctx context.Context, clusterName string) error {
	path := "/api/v1/clusters/" + url.PathEscape(clusterName)
	return client.doRequest(ctx, http.MethodDelete, path, nil, nil, http.StatusNotFound)
}
