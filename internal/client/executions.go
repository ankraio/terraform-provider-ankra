// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package client

import (
	"context"
	"net/http"
	"net/url"
)

// Execution statuses that end an execution. "success" is the only good one.
const (
	ExecutionStatusSuccess   = "success"
	ExecutionStatusFailed    = "failed"
	ExecutionStatusCritical  = "critical"
	ExecutionStatusCancelled = "cancelled"
)

// ExecutionSummary is the execution object of GET
// /api/v1/org/executions/{execution_id}.
type ExecutionSummary struct {
	ID           string  `json:"id"`
	Name         string  `json:"name"`
	Status       string  `json:"status"`
	ErrorExcerpt *string `json:"error_excerpt"`
}

type executionDetailEnvelope struct {
	Execution ExecutionSummary `json:"execution"`
}

// IsExecutionFailed reports whether status ends an execution without success.
func IsExecutionFailed(status string) bool {
	return status == ExecutionStatusFailed || status == ExecutionStatusCritical || status == ExecutionStatusCancelled
}

// GetExecution reads one execution of the organisation. An execution of
// another organisation is a NotFound APIError.
func (client *Client) GetExecution(ctx context.Context, executionID string) (*ExecutionSummary, error) {
	var envelope executionDetailEnvelope
	if err := client.doRequest(ctx, http.MethodGet, "/api/v1/org/executions/"+url.PathEscape(executionID), nil, &envelope); err != nil {
		return nil, err
	}
	return &envelope.Execution, nil
}
