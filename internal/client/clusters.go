// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package client

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
)

// maxListPages bounds the paging loop so a platform that keeps reporting more
// pages can never spin forever inside a Terraform read.
const maxListPages = 100

// listPageSize is the largest page the platform accepts, so a full listing
// costs as few round trips as possible.
const listPageSize = 100

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

// ImportClusterResponse is returned by the import endpoint. ImportCommand
// embeds a live cluster agent token, so callers must treat it as a secret.
type ImportClusterResponse struct {
	ClusterID     string `json:"cluster_id"`
	ImportCommand string `json:"import_command"`
}

// Cluster is one row of a cluster listing. The fields mirror
// ClusterListItemContract in the platform OpenAPI contract.
type Cluster struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Kind  string `json:"kind"`
	State string `json:"state"`
}

// Pagination is the paging envelope the platform returns alongside a listing.
type Pagination struct {
	TotalCount int `json:"total_count"`
	TotalPages int `json:"total_pages"`
	Page       int `json:"page"`
	PageSize   int `json:"page_size"`
}

// ClusterListResponse mirrors ClusterListResponseContract. The rows arrive
// under "result" - not "clusters"; decoding the wrong key yields an empty
// listing, which reads to Terraform as "every cluster was deleted".
type ClusterListResponse struct {
	Result     []Cluster  `json:"result"`
	Pagination Pagination `json:"pagination"`
}

// ImportCluster creates or updates a cluster via the import endpoint.
func (client *Client) ImportCluster(ctx context.Context, request ImportClusterRequest) (ImportClusterResponse, error) {
	var response ImportClusterResponse
	if err := client.doRequest(ctx, http.MethodPost, "/api/v1/clusters/import", request, &response); err != nil {
		return ImportClusterResponse{}, err
	}
	return response, nil
}

// listClusterPage fetches a single page of the cluster listing, applying the
// caller's server-side filters.
func (client *Client) listClusterPage(ctx context.Context, query url.Values) (ClusterListResponse, error) {
	var response ClusterListResponse
	path := "/api/v1/clusters?" + query.Encode()
	if err := client.doRequest(ctx, http.MethodGet, path, nil, &response); err != nil {
		return ClusterListResponse{}, err
	}
	return response, nil
}

// ListClusters returns every cluster visible to the authenticated token,
// following the platform's pagination to the end. A single unpaged request
// would stop at the default page size and silently hide the rest.
func (client *Client) ListClusters(ctx context.Context) ([]Cluster, error) {
	clusters := make([]Cluster, 0)
	for page := 1; page <= maxListPages; page++ {
		query := url.Values{}
		query.Set("page", fmt.Sprintf("%d", page))
		query.Set("page_size", fmt.Sprintf("%d", listPageSize))

		response, err := client.listClusterPage(ctx, query)
		if err != nil {
			return nil, err
		}
		clusters = append(clusters, response.Result...)

		if isLastClusterPage(page, len(response.Result), response.Pagination) {
			return clusters, nil
		}
	}
	return clusters, nil
}

// isLastClusterPage decides whether paging can stop. It trusts total_pages
// when the platform reports it and otherwise falls back to a short page, so
// the loop terminates even if the pagination envelope is absent.
func isLastClusterPage(page int, rowsOnPage int, pagination Pagination) bool {
	if pagination.TotalPages > 0 {
		return page >= pagination.TotalPages
	}
	return rowsOnPage < listPageSize
}

// GetClusterByID returns the cluster with the given id, or (nil, nil) when it
// no longer exists. The platform filters server-side, so this costs one row
// rather than a full listing of the organisation's estate.
func (client *Client) GetClusterByID(ctx context.Context, clusterID string) (*Cluster, error) {
	if clusterID == "" {
		return nil, nil
	}
	query := url.Values{}
	query.Set("cluster_id", clusterID)
	query.Set("page", "1")
	query.Set("page_size", "1")

	response, err := client.listClusterPage(ctx, query)
	if err != nil {
		return nil, err
	}
	for index := range response.Result {
		if response.Result[index].ID == clusterID {
			return &response.Result[index], nil
		}
	}
	return nil, nil
}

// GetClusterByName returns the cluster with the given exact name, or
// (nil, nil) when no cluster matches.
func (client *Client) GetClusterByName(ctx context.Context, clusterName string) (*Cluster, error) {
	if clusterName == "" {
		return nil, nil
	}
	query := url.Values{}
	query.Set("cluster_name", clusterName)
	query.Set("page", "1")
	query.Set("page_size", fmt.Sprintf("%d", listPageSize))

	response, err := client.listClusterPage(ctx, query)
	if err != nil {
		return nil, err
	}
	for index := range response.Result {
		if response.Result[index].Name == clusterName {
			return &response.Result[index], nil
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
