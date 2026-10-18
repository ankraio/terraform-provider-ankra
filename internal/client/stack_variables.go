// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package client

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
)

// stackPendingDeletionDetail is the start of the 409 detail a stack being
// deleted answers to a variable write.
const stackPendingDeletionDetail = "Stack is pending deletion"

// StackVariables is the stack-level configuration a live stack runs with, as
// deployed. A pending draft never contributes to it; HasPendingDraft only
// says that one exists.
type StackVariables struct {
	StackName       string            `json:"stack_name"`
	Variables       map[string]string `json:"variables"`
	DeployWave      *int64            `json:"deploy_wave"`
	Description     *string           `json:"description"`
	HasPendingDraft bool              `json:"has_pending_draft"`
	DeletePending   bool              `json:"delete_pending"`
}

// StackVariableWrite is the response of a stack variable write: whether it
// changed the live stack, and the commit and operation it started, which are
// null when nothing changed.
type StackVariableWrite struct {
	StackName   string  `json:"stack_name"`
	Changed     bool    `json:"changed"`
	CommitSHA   *string `json:"commit_sha"`
	OperationID *string `json:"operation_id"`
}

type stackVariableSet struct {
	Value string `json:"value"`
}

func stackVariablesPath(clusterID, stackName string) string {
	return importedClustersPath + url.PathEscape(clusterID) + "/stacks/" + url.PathEscape(stackName) + "/variables"
}

// GetStackVariables reads one stack's deployed variables. A stack the
// cluster does not have is a NotFound APIError; a cluster that is not visible
// is a *ClusterNotVisibleError.
func (client *Client) GetStackVariables(ctx context.Context, clusterID, stackName string) (*StackVariables, error) {
	var deployed StackVariables
	if err := client.doRequest(ctx, http.MethodGet, stackVariablesPath(clusterID, stackName), nil, &deployed); err != nil {
		return nil, clusterScopedError(clusterID, err)
	}
	if deployed.Variables == nil {
		deployed.Variables = map[string]string{}
	}
	return &deployed, nil
}

// GetStackVariable reads one deployed stack variable. A key the stack does
// not hold, like a stack the cluster does not have, is a NotFound APIError.
func (client *Client) GetStackVariable(ctx context.Context, clusterID, stackName, name string) (string, error) {
	deployed, err := client.GetStackVariables(ctx, clusterID, stackName)
	if err != nil {
		return "", err
	}
	value, exists := deployed.Variables[name]
	if !exists {
		return "", &APIError{Method: http.MethodGet, Path: stackVariablesPath(clusterID, stackName), StatusCode: http.StatusNotFound,
			Body: "stack variable " + name + " not found"}
	}
	return value, nil
}

// SetStackVariable sets one key of a stack's deployed variables, leaving the
// other keys as they are.
func (client *Client) SetStackVariable(ctx context.Context, clusterID, stackName, name, value string) (*StackVariableWrite, error) {
	var written StackVariableWrite
	path := stackVariablesPath(clusterID, stackName) + "/" + url.PathEscape(name)
	if err := client.doRequest(ctx, http.MethodPut, path, stackVariableSet{Value: value}, &written); err != nil {
		return nil, clusterScopedError(clusterID, err)
	}
	return &written, nil
}

// DeleteStackVariable removes one key of a stack's deployed variables. A key
// the stack does not hold is not an error. A stack the cluster does not have
// is a NotFound APIError, and a stack pending deletion is treated as gone,
// since its variables go with it; any other 409 is an error. A cluster that is not visible is a
// *ClusterNotVisibleError, because the key may still exist.
func (client *Client) DeleteStackVariable(ctx context.Context, clusterID, stackName, name string) error {
	path := stackVariablesPath(clusterID, stackName) + "/" + url.PathEscape(name)
	err := client.doRequest(ctx, http.MethodDelete, path, nil, nil)
	if isStackPendingDeletion(err) {
		return nil
	}
	return clusterScopedError(clusterID, err)
}

// isStackPendingDeletion reports the 409 a write to a stack being deleted
// answers. Any other conflict stays an error.
func isStackPendingDeletion(err error) bool {
	var apiError *APIError
	if !errors.As(err, &apiError) || apiError.StatusCode != http.StatusConflict {
		return false
	}
	var envelope struct {
		Detail string `json:"detail"`
	}
	return json.Unmarshal([]byte(apiError.Body), &envelope) == nil && strings.Contains(envelope.Detail, stackPendingDeletionDetail)
}
