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
	"strconv"
	"strings"
)

const (
	importedClustersPath        = "/api/v1/org/clusters/imported/"
	clusterStackListPageSize    = 100
	clusterStackListMaxPages    = 100
	clusterStackGitPushDeferred = "GIT_PUSH_DEFERRED"
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

type clusterVariableEnvelope struct {
	Variable ClusterVariable `json:"variable"`
}

type clusterVariableListEnvelope struct {
	Variables []ClusterVariable `json:"variables"`
}

func clusterVariablesPath(clusterID string) string {
	return importedClustersPath + url.PathEscape(clusterID) + "/variables"
}

// ListClusterVariables returns every variable on a cluster. A cluster that
// does not exist is a NotFound APIError.
func (client *Client) ListClusterVariables(ctx context.Context, clusterID string) ([]ClusterVariable, error) {
	var envelope clusterVariableListEnvelope
	if err := client.doRequest(ctx, http.MethodGet, clusterVariablesPath(clusterID), nil, &envelope); err != nil {
		return nil, err
	}
	return envelope.Variables, nil
}

// GetClusterVariable finds one variable by name in the listing, which is the
// only read the platform offers. A missing name is a NotFound APIError.
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
		return nil, err
	}
	return &envelope.Variable, nil
}

// UpdateClusterVariable replaces a cluster variable's value and description.
func (client *Client) UpdateClusterVariable(ctx context.Context, clusterID, name string, request ClusterVariableUpdate) (*ClusterVariable, error) {
	var envelope clusterVariableEnvelope
	if err := client.doRequest(ctx, http.MethodPut, clusterVariablesPath(clusterID)+"/"+url.PathEscape(name), request, &envelope); err != nil {
		return nil, err
	}
	return &envelope.Variable, nil
}

// DeleteClusterVariable deletes a cluster variable. A variable or cluster
// already gone is not an error.
func (client *Client) DeleteClusterVariable(ctx context.Context, clusterID, name string) error {
	return client.doRequest(ctx, http.MethodDelete, clusterVariablesPath(clusterID)+"/"+url.PathEscape(name), nil, nil, http.StatusNotFound)
}

// ClusterStackVariableSet is the part of a stack the stack-variable routes
// read and write: its identity, the metadata a partial update carries back
// unchanged, and its variables map.
type ClusterStackVariableSet struct {
	Name        string            `json:"name"`
	Description *string           `json:"description"`
	DeployWave  *int64            `json:"deploy_wave"`
	Variables   map[string]string `json:"variables"`
}

type clusterStackListResponse struct {
	Stacks     []ClusterStackVariableSet `json:"stacks"`
	Pagination helmListPagination        `json:"pagination"`
}

// clusterStackPatchStack is the stack a partial update sends. Manifests and
// addons are empty: with partial_stack the platform keeps the members it
// already has, and replaces the variables map with the one sent.
type clusterStackPatchStack struct {
	Name        string            `json:"name"`
	Description *string           `json:"description,omitempty"`
	DeployWave  *int64            `json:"deploy_wave,omitempty"`
	Variables   map[string]string `json:"variables"`
	Manifests   []any             `json:"manifests"`
	Addons      []any             `json:"addons"`
}

type clusterStackPatchRequest struct {
	Spec struct {
		Stacks []clusterStackPatchStack `json:"stacks"`
	} `json:"spec"`
	PartialStack bool `json:"partial_stack"`
}

type clusterStackPatchResponse struct {
	Errors []ResourceError `json:"errors"`
}

func clusterStacksPath(clusterID string) string {
	return importedClustersPath + url.PathEscape(clusterID) + "/stacks"
}

// GetClusterStackVariables finds a stack by name through the stack listing,
// the only stack read the token API offers. A missing stack is a NotFound
// APIError.
func (client *Client) GetClusterStackVariables(ctx context.Context, clusterID, stackName string) (*ClusterStackVariableSet, error) {
	for page := 1; page <= clusterStackListMaxPages; page++ {
		var response clusterStackListResponse
		path := clusterStacksPath(clusterID) + "?page=" + strconv.Itoa(page) +
			"&page_size=" + strconv.Itoa(clusterStackListPageSize) + "&search=" + url.QueryEscape(stackName)
		if err := client.doRequest(ctx, http.MethodGet, path, nil, &response); err != nil {
			return nil, err
		}
		for index := range response.Stacks {
			if response.Stacks[index].Name == stackName {
				stack := response.Stacks[index]
				if stack.Variables == nil {
					stack.Variables = map[string]string{}
				}
				return &stack, nil
			}
		}
		if len(response.Stacks) == 0 || page >= response.Pagination.TotalPages {
			break
		}
	}
	return nil, &APIError{Method: http.MethodGet, Path: clusterStacksPath(clusterID), StatusCode: http.StatusNotFound, Body: "stack " + stackName + " not found"}
}

// UpdateClusterStackVariables replaces a stack's variables map through the
// partial stack update, carrying the stack's description and deploy wave
// back unchanged. The platform applies the change and commits it to the
// cluster's GitOps repository before it answers, so the call runs without the
// per-attempt timeout; it is never retried. A GIT_PUSH_DEFERRED refusal means
// the change is live and only the commit is pending, so it is a success.
func (client *Client) UpdateClusterStackVariables(ctx context.Context, clusterID string, stack ClusterStackVariableSet) error {
	variables := stack.Variables
	if variables == nil {
		variables = map[string]string{}
	}
	var request clusterStackPatchRequest
	request.PartialStack = true
	request.Spec.Stacks = []clusterStackPatchStack{{
		Name:        stack.Name,
		Description: stack.Description,
		DeployWave:  stack.DeployWave,
		Variables:   variables,
		Manifests:   []any{},
		Addons:      []any{},
	}}
	var response clusterStackPatchResponse
	err := client.withoutAttemptTimeout().doRequest(ctx, http.MethodPatch,
		clusterStacksPath(clusterID)+"/"+url.PathEscape(stack.Name), request, &response)
	if isGitPushDeferred(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if len(response.Errors) > 0 {
		return &ConfigurationRejectedError{Subject: fmt.Sprintf("the variables of stack %q", stack.Name), Errors: response.Errors}
	}
	return nil
}

func isGitPushDeferred(err error) bool {
	var apiError *APIError
	if !errors.As(err, &apiError) || apiError.StatusCode != http.StatusUnprocessableEntity {
		return false
	}
	var envelope struct {
		ErrorCode string `json:"error_code"`
	}
	if json.Unmarshal([]byte(apiError.Body), &envelope) != nil {
		return false
	}
	return strings.EqualFold(envelope.ErrorCode, clusterStackGitPushDeferred)
}
