// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
)

const (
	importedClustersPath = "/api/v1/org/clusters/imported/"
	// clusterNotFoundDetail is the 404 detail the cluster-scoped routes answer
	// when the cluster is not visible: deleted, in another organisation, or
	// outside the request's project scope.
	clusterNotFoundDetail = "Cluster not found"
)

// ClusterVariable is one cluster-scoped variable. Unlike an organisation
// variable it has no secret mode: the platform always returns the value.
type ClusterVariable struct {
	ID          string  `json:"id"`
	ClusterID   string  `json:"cluster_id"`
	Name        string  `json:"name"`
	Value       string  `json:"value"`
	Description *string `json:"description"`
}

// ClusterVariableCreate is the create body.
type ClusterVariableCreate struct {
	Name        string  `json:"name"`
	Value       string  `json:"value"`
	Description *string `json:"description,omitempty"`
}

// ClusterVariableUpdate is the update body. The platform writes the
// description it is given, so a nil description clears the stored one.
type ClusterVariableUpdate struct {
	Value       string  `json:"value"`
	Description *string `json:"description"`
}

// ClusterNotVisibleError reports a cluster-scoped route answering 404
// "Cluster not found". It is deliberately not a NotFound APIError: the
// cluster may only be outside the request's project scope, so the variables
// under it must not be treated as deleted.
type ClusterNotVisibleError struct {
	ClusterID string
}

func (notVisible *ClusterNotVisibleError) Error() string {
	return fmt.Sprintf("cluster %s is not visible to this token: it was deleted, or it is outside the provider's "+
		"organisation or project scope", notVisible.ClusterID)
}

// IsClusterNotVisible reports whether err is a *ClusterNotVisibleError.
func IsClusterNotVisible(err error) bool {
	var notVisible *ClusterNotVisibleError
	return errors.As(err, &notVisible)
}

type clusterVariableEnvelope struct {
	Variable ClusterVariable `json:"variable"`
}

type clusterVariableListEnvelope struct {
	Variables []ClusterVariable `json:"variables"`
}

func clusterVariablesPath(clusterID string) string {
	return importedClustersPath + url.PathEscape(clusterID) + "/variables"
}

// clusterScopedError maps a 404 "Cluster not found" to a
// *ClusterNotVisibleError and leaves every other error, a 404 "Variable not
// found" included, as it is.
func clusterScopedError(clusterID string, err error) error {
	var apiError *APIError
	if !errors.As(err, &apiError) || apiError.StatusCode != http.StatusNotFound {
		return err
	}
	var envelope struct {
		Detail string `json:"detail"`
	}
	if json.Unmarshal([]byte(apiError.Body), &envelope) == nil && envelope.Detail == clusterNotFoundDetail {
		return &ClusterNotVisibleError{ClusterID: clusterID}
	}
	return err
}

// ListClusterVariables returns every variable on a cluster. A cluster that
// is not visible is a *ClusterNotVisibleError.
func (client *Client) ListClusterVariables(ctx context.Context, clusterID string) ([]ClusterVariable, error) {
	var envelope clusterVariableListEnvelope
	if err := client.doRequest(ctx, http.MethodGet, clusterVariablesPath(clusterID), nil, &envelope); err != nil {
		return nil, clusterScopedError(clusterID, err)
	}
	return envelope.Variables, nil
}

// GetClusterVariable finds one variable by name in the listing, which is the
// only read the platform offers. A missing variable is a NotFound APIError;
// a cluster that is not visible is a *ClusterNotVisibleError.
func (client *Client) GetClusterVariable(ctx context.Context, clusterID, name string) (*ClusterVariable, error) {
	variables, err := client.ListClusterVariables(ctx, clusterID)
	if err != nil {
		return nil, err
	}
	for index := range variables {
		if variables[index].Name == name {
			return &variables[index], nil
		}
	}
	return nil, &APIError{Method: http.MethodGet, Path: clusterVariablesPath(clusterID), StatusCode: http.StatusNotFound, Body: "variable " + name + " not found"}
}

// CreateClusterVariable creates a cluster variable.
func (client *Client) CreateClusterVariable(ctx context.Context, clusterID string, request ClusterVariableCreate) (*ClusterVariable, error) {
	var envelope clusterVariableEnvelope
	if err := client.doRequest(ctx, http.MethodPost, clusterVariablesPath(clusterID), request, &envelope); err != nil {
		return nil, clusterScopedError(clusterID, err)
	}
	return &envelope.Variable, nil
}

// UpdateClusterVariable replaces a cluster variable's value and description.
func (client *Client) UpdateClusterVariable(ctx context.Context, clusterID, name string, request ClusterVariableUpdate) (*ClusterVariable, error) {
	var envelope clusterVariableEnvelope
	if err := client.doRequest(ctx, http.MethodPut, clusterVariablesPath(clusterID)+"/"+url.PathEscape(name), request, &envelope); err != nil {
		return nil, clusterScopedError(clusterID, err)
	}
	return &envelope.Variable, nil
}

// DeleteClusterVariable deletes a cluster variable. A variable already gone
// is not an error; a cluster that is not visible is a
// *ClusterNotVisibleError, because the variable may still exist.
func (client *Client) DeleteClusterVariable(ctx context.Context, clusterID, name string) error {
	err := client.doRequest(ctx, http.MethodDelete, clusterVariablesPath(clusterID)+"/"+url.PathEscape(name), nil, nil)
	if err == nil {
		return nil
	}
	err = clusterScopedError(clusterID, err)
	if IsNotFound(err) {
		return nil
	}
	return err
}
