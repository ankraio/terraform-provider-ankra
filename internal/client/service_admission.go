// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package client

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
)

const serviceAdmissionPath = "/api/v1/org/service-admission"

// ServicePlacementPolicy is a cluster's declared service placement policy.
type ServicePlacementPolicy struct {
	ClusterID    string  `json:"cluster_id"`
	Region       string  `json:"region"`
	DataBoundary string  `json:"data_boundary"`
	LocalOnly    bool    `json:"local_only"`
	Revision     int64   `json:"revision"`
	Source       string  `json:"source"`
	UpdatedAt    *string `json:"updated_at"`
}

// ServicePlacementPolicyWrite declares a policy. ExpectedRevision is the
// revision being replaced, zero to create one; any other value is refused
// with a 409.
type ServicePlacementPolicyWrite struct {
	ExpectedRevision int64  `json:"expected_revision"`
	Region           string `json:"region"`
	DataBoundary     string `json:"data_boundary"`
	LocalOnly        bool   `json:"local_only"`
}

// ServiceConsumer is a binding that lets one application namespace on one
// cluster consume managed services, resolved against the cluster's policy.
type ServiceConsumer struct {
	ID              string  `json:"id"`
	ApplicationID   string  `json:"application_id"`
	ClusterID       string  `json:"cluster_id"`
	Namespace       string  `json:"namespace"`
	Region          string  `json:"region"`
	DataBoundary    string  `json:"data_boundary"`
	PolicyRevision  int64   `json:"policy_revision"`
	BindingRevision int64   `json:"binding_revision"`
	BindingKind     string  `json:"binding_kind"`
	InstallationID  *string `json:"installation_id"`
	LocalOnly       bool    `json:"local_only"`
}

// ServiceConsumerWrite creates or updates a binding, identified by
// application, cluster and namespace. ExpectedRevision is zero to create and
// the binding's current revision to update.
type ServiceConsumerWrite struct {
	ApplicationID    string `json:"application_id"`
	ClusterID        string `json:"cluster_id"`
	Namespace        string `json:"namespace"`
	AllowPlanned     bool   `json:"allow_planned"`
	LocalOnly        bool   `json:"local_only"`
	ExpectedRevision int64  `json:"expected_revision"`
}

// GetServicePlacementPolicy reads a cluster's declared policy. A cluster
// with no declaration answers 409, which this reports as a NotFound APIError
// so callers treat an undeclared policy as absent.
func (client *Client) GetServicePlacementPolicy(ctx context.Context, clusterID string) (*ServicePlacementPolicy, error) {
	var policy ServicePlacementPolicy
	path := serviceAdmissionPath + "/cluster-policies/" + url.PathEscape(clusterID)
	err := client.doRequest(ctx, http.MethodGet, path, nil, &policy)
	var apiError *APIError
	if errors.As(err, &apiError) && apiError.StatusCode == http.StatusConflict {
		return nil, &APIError{Method: http.MethodGet, Path: path, StatusCode: http.StatusNotFound, Body: apiError.Body}
	}
	if err != nil {
		return nil, err
	}
	return &policy, nil
}

// SetServicePlacementPolicy declares or replaces a cluster's policy.
func (client *Client) SetServicePlacementPolicy(ctx context.Context, clusterID string, write ServicePlacementPolicyWrite) (*ServicePlacementPolicy, error) {
	var policy ServicePlacementPolicy
	if err := client.doRequest(ctx, http.MethodPut, serviceAdmissionPath+"/cluster-policies/"+url.PathEscape(clusterID), write, &policy); err != nil {
		return nil, err
	}
	return &policy, nil
}

// GetServiceConsumer reads one binding by id.
func (client *Client) GetServiceConsumer(ctx context.Context, consumerID string) (*ServiceConsumer, error) {
	var consumer ServiceConsumer
	if err := client.doRequest(ctx, http.MethodGet, serviceAdmissionPath+"/consumers/"+url.PathEscape(consumerID), nil, &consumer); err != nil {
		return nil, err
	}
	return &consumer, nil
}

// BindServiceConsumer creates or updates a binding.
func (client *Client) BindServiceConsumer(ctx context.Context, write ServiceConsumerWrite) (*ServiceConsumer, error) {
	var consumer ServiceConsumer
	if err := client.doRequest(ctx, http.MethodPost, serviceAdmissionPath+"/consumers", write, &consumer); err != nil {
		return nil, err
	}
	return &consumer, nil
}

// UnbindServiceConsumer removes a binding at the revision last read. One a
// service still uses is a 409. A 404 is returned as an error: it may mean
// the binding is gone, or that service admission is not enabled for the
// organisation, which the caller tells apart.
func (client *Client) UnbindServiceConsumer(ctx context.Context, consumerID string, expectedRevision int64) error {
	path := serviceAdmissionPath + "/consumers/" + url.PathEscape(consumerID) +
		"?" + url.Values{"expected_revision": []string{strconv.FormatInt(expectedRevision, 10)}}.Encode()
	return client.doRequest(ctx, http.MethodDelete, path, nil, nil)
}
