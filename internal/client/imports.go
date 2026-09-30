// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package client

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-log/tflog"
)

// importPath is the create-or-update route for imported clusters. wait=true
// asks the platform to run the import synchronously: without it the route
// answers 202 {"status":"accepted"} and imports in the background, so the
// response carries neither the cluster id nor the one-time agent install
// command, and a rejected spec is only ever reported in the platform's logs.
const importPath = "/api/v1/clusters/import?wait=true"

// ImportStatusAccepted is the status the import route reports when it queued
// the import instead of running it (the wait=false contract).
const ImportStatusAccepted = "accepted"

// Parent kinds the platform accepts on a dependency edge.
const (
	ParentKindManifest = "manifest"
	ParentKindAddon    = "addon"
)

// GitRepository is the source-of-truth git repository for an imported cluster.
type GitRepository struct {
	Provider       string `json:"provider"`
	CredentialName string `json:"credential_name"`
	Branch         string `json:"branch"`
	Repository     string `json:"repository"`
}

// Parent is one dependency edge: the named stack member must deploy first.
// The platform requires both keys; a bare string is refused with 422.
type Parent struct {
	Name string `json:"name"`
	Kind string `json:"kind"`
}

// Manifest is a raw Kubernetes manifest deployed as part of a stack.
//
// Parents is a pointer so "not configured" (nil, omitted on the wire) stays
// distinct from "configured empty" (an explicit []): the platform reads an
// absent parents field as "keep the stored edges" and an empty list as
// "remove them all".
type Manifest struct {
	Name           string    `json:"name"`
	Namespace      string    `json:"namespace,omitempty"`
	ManifestBase64 string    `json:"manifest_base64"`
	Parents        *[]Parent `json:"parents,omitempty"`
	FromFile       string    `json:"from_file,omitempty"`
}

// AddonConfiguration is the AddonStandaloneConfiguration object.
type AddonConfiguration struct {
	ValuesBase64 string `json:"values_base64"`
}

// JobConfiguration carries per-addon job timeouts in seconds. Unset members
// take the platform defaults.
type JobConfiguration struct {
	CreateJobTimeout *int64 `json:"create_job_timeout,omitempty"`
	ReadJobTimeout   *int64 `json:"read_job_timeout,omitempty"`
	UpdateJobTimeout *int64 `json:"update_job_timeout,omitempty"`
	DeleteJobTimeout *int64 `json:"delete_job_timeout,omitempty"`
}

// Addon is a Helm-chart addon deployed as part of a stack. RegistryName and
// RegistryURL are both required by the platform.
type Addon struct {
	Name                   string              `json:"name"`
	ChartName              string              `json:"chart_name"`
	ChartVersion           string              `json:"chart_version"`
	RegistryName           string              `json:"registry_name"`
	RegistryURL            string              `json:"registry_url"`
	RegistryCredentialName string              `json:"registry_credential_name,omitempty"`
	Namespace              string              `json:"namespace"`
	Configuration          *AddonConfiguration `json:"configuration,omitempty"`
	Parents                *[]Parent           `json:"parents,omitempty"`
	JobConfiguration       *JobConfiguration   `json:"job_configuration,omitempty"`
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

// ResourceErrorItem is one field-level problem the platform found.
type ResourceErrorItem struct {
	Key     string `json:"key"`
	Message string `json:"message"`
}

// ResourceError groups the problems found on one stack member.
type ResourceError struct {
	Name   string              `json:"name"`
	Kind   string              `json:"kind"`
	Errors []ResourceErrorItem `json:"errors"`
}

// ValidationWarning is an advisory finding on the state the import stored.
type ValidationWarning struct {
	Kind     string `json:"kind"`
	Name     string `json:"name"`
	Key      string `json:"key"`
	Message  string `json:"message"`
	Category string `json:"category"`
}

// ImportClusterResponse is the CreateOrUpdateImportClusterResult the import
// route returns with wait=true. ImportCommand embeds a live cluster agent
// token, so callers must treat it as a secret; the platform only issues it
// when the import registers a new cluster and returns "" on a re-import.
//
// Status is set only by the asynchronous contract (202 {"status":"accepted"}),
// in which case every other member is empty.
type ImportClusterResponse struct {
	Status         string              `json:"status,omitempty"`
	Name           string              `json:"name"`
	ClusterID      string              `json:"cluster_id"`
	OrganisationID string              `json:"organisation_id"`
	ImportCommand  string              `json:"import_command"`
	Errors         []ResourceError     `json:"errors"`
	Warnings       []ValidationWarning `json:"warnings"`
	CommitSHA      *string             `json:"commit_sha"`
	CommitURL      *string             `json:"commit_url"`
}

// ImportRejectedError reports a spec the platform refused. The platform rolls
// the whole import back in this case, so nothing was registered.
type ImportRejectedError struct {
	ClusterName string
	Errors      []ResourceError
}

func (rejected *ImportRejectedError) Error() string {
	lines := make([]string, 0, len(rejected.Errors))
	for _, resourceError := range rejected.Errors {
		for _, item := range resourceError.Errors {
			lines = append(lines, fmt.Sprintf("  - %s %q: %s: %s",
				resourceError.Kind, resourceError.Name, item.Key, item.Message))
		}
		if len(resourceError.Errors) == 0 {
			lines = append(lines, fmt.Sprintf("  - %s %q", resourceError.Kind, resourceError.Name))
		}
	}
	return fmt.Sprintf("the platform rejected the specification for cluster %q; nothing was applied:\n%s",
		rejected.ClusterName, strings.Join(lines, "\n"))
}

// ImportCluster creates or updates a cluster via the import endpoint and waits
// for the platform to finish the import.
//
// The call runs without the client's per-attempt timeout: a synchronous import
// commits the spec and pushes it to the GitOps repository before it answers,
// which can outlast a normal API round trip. ctx owns the deadline, which is
// where the resource's create/update timeout applies. The request is never
// retried on a transport or 5xx failure, because it may already have run.
//
// A 2xx whose body is the asynchronous {"status":"accepted"} (a platform that
// ignored wait) is returned as-is with Status set and no ClusterID; the
// caller resolves the cluster by name.
func (client *Client) ImportCluster(ctx context.Context, request ImportClusterRequest) (ImportClusterResponse, error) {
	var response ImportClusterResponse
	if err := client.withoutAttemptTimeout().doRequest(ctx, http.MethodPost, importPath, request, &response); err != nil {
		return ImportClusterResponse{}, err
	}
	if len(response.Errors) > 0 {
		return response, &ImportRejectedError{ClusterName: request.Name, Errors: response.Errors}
	}
	return response, nil
}

// withoutAttemptTimeout returns a shallow copy of the client whose HTTP client
// has no per-attempt timeout, leaving the deadline to the caller's context.
func (client *Client) withoutAttemptTimeout() *Client {
	copied := *client
	httpClient := http.Client{}
	if client.HTTPClient != nil {
		httpClient = *client.HTTPClient
	}
	httpClient.Timeout = 0
	copied.HTTPClient = &httpClient
	return &copied
}

// WaitForClusterByName polls the cluster listing until a cluster with the
// given name appears or ctx expires. It resolves the cluster an asynchronous
// import registered, whose id the platform never returned.
func (client *Client) WaitForClusterByName(ctx context.Context, clusterName string, pollInterval time.Duration) (*Cluster, error) {
	if pollInterval <= 0 {
		pollInterval = importPollInterval
	}
	for {
		cluster, err := client.GetClusterByName(ctx, clusterName)
		if err != nil {
			if ctx.Err() != nil {
				return nil, fmt.Errorf("timed out waiting for cluster %q to be registered: %w", clusterName, ctx.Err())
			}
			return nil, err
		}
		if cluster != nil {
			return cluster, nil
		}
		tflog.Debug(ctx, "waiting for imported cluster to be registered", map[string]any{"cluster_name": clusterName})
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("timed out waiting for cluster %q to be registered: %w", clusterName, ctx.Err())
		case <-time.After(pollInterval):
		}
	}
}

// importPollInterval paces the by-name lookup after an asynchronous import;
// a background import normally lands within seconds.
const importPollInterval = 3 * time.Second
