// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package client

import (
	"context"
	"net/http"
	"net/url"
)

const (
	deployEnvironmentsPath = "/api/v1/org/environments"
	deployHostTargetsPath  = "/api/v1/org/host-targets"
)

// DeployEnvironment is one named deploy environment that host targets
// register into.
type DeployEnvironment struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	CreatedAt string `json:"created_at"`
}

// DeployHostJoinTokenCreate is the join token create body. A nil TTLSeconds
// takes the platform's one-hour default.
type DeployHostJoinTokenCreate struct {
	TTLSeconds *int64 `json:"ttl_seconds,omitempty"`
}

// DeployHostJoinToken is a single-use token a host registers with. The
// platform returns JoinToken only in the create answer and stores its hash.
type DeployHostJoinToken struct {
	JoinToken   string `json:"join_token"`
	ExpiresAt   string `json:"expires_at"`
	Environment string `json:"environment"`
}

// DeployHostTarget is a host registered as a deploy target. Host-reported
// fields stay nil until the host's first report.
type DeployHostTarget struct {
	ID              string            `json:"id"`
	Name            string            `json:"name"`
	Environment     string            `json:"environment"`
	EnvironmentID   string            `json:"environment_id"`
	Labels          map[string]string `json:"labels"`
	Status          string            `json:"status"`
	AgentVersion    *string           `json:"agent_version"`
	Hostname        *string           `json:"hostname"`
	OS              *string           `json:"os"`
	Arch            *string           `json:"arch"`
	LastHeartbeatAt *string           `json:"last_heartbeat_at"`
	RegisteredAt    string            `json:"registered_at"`
	RevokedAt       *string           `json:"revoked_at"`
}

type deployEnvironmentListEnvelope struct {
	Environments []DeployEnvironment `json:"environments"`
	Truncated    bool                `json:"truncated"`
}

type deployHostTargetListEnvelope struct {
	HostTargets []DeployHostTarget `json:"host_targets"`
	Truncated   bool               `json:"truncated"`
}

// ListDeployEnvironments returns the deploy environments of the provider's
// project and whether the platform truncated the listing.
func (client *Client) ListDeployEnvironments(ctx context.Context) ([]DeployEnvironment, bool, error) {
	var envelope deployEnvironmentListEnvelope
	if err := client.doRequest(ctx, http.MethodGet, deployEnvironmentsPath, nil, &envelope); err != nil {
		return nil, false, err
	}
	return envelope.Environments, envelope.Truncated, nil
}

// CreateDeployHostJoinToken mints a single-use join token for the
// environment, creating the environment when it does not exist yet.
func (client *Client) CreateDeployHostJoinToken(ctx context.Context, environmentName string, request DeployHostJoinTokenCreate) (*DeployHostJoinToken, error) {
	var token DeployHostJoinToken
	path := deployEnvironmentsPath + "/" + url.PathEscape(environmentName) + "/host-join-tokens"
	if err := client.doRequest(ctx, http.MethodPost, path, request, &token); err != nil {
		return nil, err
	}
	return &token, nil
}

// ListDeployHostTargets returns host targets, narrowed to one environment
// when environmentName is not empty, and whether the listing was truncated.
// An environment filter naming no environment is a NotFound APIError.
func (client *Client) ListDeployHostTargets(ctx context.Context, environmentName string) ([]DeployHostTarget, bool, error) {
	path := deployHostTargetsPath
	if environmentName != "" {
		path += "?" + url.Values{"environment": []string{environmentName}}.Encode()
	}
	var envelope deployHostTargetListEnvelope
	if err := client.doRequest(ctx, http.MethodGet, path, nil, &envelope); err != nil {
		return nil, false, err
	}
	return envelope.HostTargets, envelope.Truncated, nil
}
