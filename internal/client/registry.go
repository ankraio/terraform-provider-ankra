// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package client

import (
	"context"
	"net/http"
	"net/url"
)

const (
	registryProjectsPath  = "/api/v1/org/registry-projects"
	registryRobotsPath    = "/api/v1/org/registry-robots"
	registryRetentionPath = "/api/v1/org/registry-storage/retention"
)

// RegistryProject is one project the organisation holds on the Ankra
// registry: its own, named "default", or an extra one it created.
type RegistryProject struct {
	Name            string `json:"name"`
	Project         string `json:"project"`
	Host            string `json:"host"`
	IsDefault       bool   `json:"is_default"`
	RepositoryCount int64  `json:"repository_count"`
	RobotCount      int64  `json:"robot_count"`
	CreatedAt       string `json:"created_at"`
}

type registryProjectListEnvelope struct {
	Projects []RegistryProject `json:"projects"`
}

// ListRegistryProjects lists the organisation's registry projects.
func (client *Client) ListRegistryProjects(ctx context.Context) ([]RegistryProject, error) {
	var envelope registryProjectListEnvelope
	if err := client.doRequest(ctx, http.MethodGet, registryProjectsPath, nil, &envelope); err != nil {
		return nil, err
	}
	return envelope.Projects, nil
}

// GetRegistryProject finds one project by name in the listing, the only read
// the platform offers. A missing name is a NotFound APIError.
func (client *Client) GetRegistryProject(ctx context.Context, name string) (*RegistryProject, error) {
	projects, err := client.ListRegistryProjects(ctx)
	if err != nil {
		return nil, err
	}
	for index := range projects {
		if projects[index].Name == name {
			return &projects[index], nil
		}
	}
	return nil, &APIError{Method: http.MethodGet, Path: registryProjectsPath, StatusCode: http.StatusNotFound, Body: "registry project " + name + " not found"}
}

// CreateRegistryProject creates an extra registry project.
func (client *Client) CreateRegistryProject(ctx context.Context, name string) (*RegistryProject, error) {
	var project RegistryProject
	if err := client.doRequest(ctx, http.MethodPost, registryProjectsPath, map[string]string{"name": name}, &project); err != nil {
		return nil, err
	}
	return &project, nil
}

// DeleteRegistryProject deletes an extra project. The platform refuses one
// that still holds repositories or has robots bound to it. A project already
// gone is not an error.
func (client *Client) DeleteRegistryProject(ctx context.Context, name string) error {
	return client.doRequest(ctx, http.MethodDelete, registryProjectsPath+"/"+url.PathEscape(name), nil, nil, http.StatusNotFound)
}

// RegistryRobot is a robot account on a registry project. Its secret is
// never part of it: create and rotate answer it once, as
// RegistryRobotWithSecret.
type RegistryRobot struct {
	Name                string   `json:"name"`
	Kind                string   `json:"kind"`
	Managed             bool     `json:"managed"`
	RobotName           string   `json:"robot_name"`
	Host                string   `json:"host"`
	Project             string   `json:"project"`
	ProjectName         string   `json:"project_name"`
	Projects            []string `json:"projects"`
	Scope               string   `json:"scope"`
	Permissions         []string `json:"permissions"`
	Description         string   `json:"description"`
	CredentialName      string   `json:"credential_name"`
	CreatedAt           string   `json:"created_at"`
	RotatedAt           *string  `json:"rotated_at"`
	ExpiresAt           *string  `json:"expires_at"`
	Registry            string   `json:"registry"`
	AdminCredentialName string   `json:"admin_credential_name"`
}

// RegistryRobotWithSecret is a robot with the secret the registry answered
// once and the docker login command that uses it.
type RegistryRobotWithSecret struct {
	RegistryRobot
	Secret      string `json:"secret"`
	DockerLogin string `json:"docker_login"`
	Warning     string `json:"warning"`
}

// RegistryRobotCreate mints a robot: a preset Scope (push or pull) or its
// own Permissions, never both. ExpiresInDays absent is a robot that never
// expires.
type RegistryRobotCreate struct {
	Name                string   `json:"name"`
	Project             *string  `json:"project,omitempty"`
	Scope               *string  `json:"scope,omitempty"`
	Permissions         []string `json:"permissions,omitempty"`
	Description         *string  `json:"description,omitempty"`
	ExpiresInDays       *int64   `json:"expires_in_days,omitempty"`
	Registry            *string  `json:"registry,omitempty"`
	AdminCredentialName *string  `json:"admin_credential_name,omitempty"`
}

type registryRobotListEnvelope struct {
	Robots []RegistryRobot `json:"robots"`
}

func registryRobotPath(name string) string {
	return registryRobotsPath + "/" + url.PathEscape(name)
}

// ListRegistryRobots lists every robot the organisation holds, including the
// ones Ankra manages itself.
func (client *Client) ListRegistryRobots(ctx context.Context) ([]RegistryRobot, error) {
	var envelope registryRobotListEnvelope
	if err := client.doRequest(ctx, http.MethodGet, registryRobotsPath, nil, &envelope); err != nil {
		return nil, err
	}
	return envelope.Robots, nil
}

// GetRegistryRobot reads one robot. An unknown robot is a NotFound APIError.
func (client *Client) GetRegistryRobot(ctx context.Context, name string) (*RegistryRobot, error) {
	var robot RegistryRobot
	if err := client.doRequest(ctx, http.MethodGet, registryRobotPath(name), nil, &robot); err != nil {
		return nil, err
	}
	return &robot, nil
}

// CreateRegistryRobot mints a robot and answers its secret, the one time it
// is answered.
func (client *Client) CreateRegistryRobot(ctx context.Context, request RegistryRobotCreate) (*RegistryRobotWithSecret, error) {
	var robot RegistryRobotWithSecret
	if err := client.doRequest(ctx, http.MethodPost, registryRobotsPath, request, &robot); err != nil {
		return nil, err
	}
	return &robot, nil
}

// RotateRegistryRobotSecret mints a new secret for a robot and answers it
// once; the previous secret stops working.
func (client *Client) RotateRegistryRobotSecret(ctx context.Context, name string) (*RegistryRobotWithSecret, error) {
	var robot RegistryRobotWithSecret
	if err := client.doRequest(ctx, http.MethodPost, registryRobotPath(name)+"/rotate", nil, &robot); err != nil {
		return nil, err
	}
	return &robot, nil
}

// DeleteRegistryRobot deletes a robot and the credential holding its login.
// A robot already gone is not an error.
func (client *Client) DeleteRegistryRobot(ctx context.Context, name string) error {
	return client.doRequest(ctx, http.MethodDelete, registryRobotPath(name), nil, nil, http.StatusNotFound)
}

// RegistryRetentionPolicy keeps every image pushed within KeepDays days (0
// turns that rule off) and the KeepLatest newest images of each repository.
type RegistryRetentionPolicy struct {
	KeepDays   int64 `json:"keep_days"`
	KeepLatest int64 `json:"keep_latest"`
}

// RegistryRetentionRule is a policy for the repositories its pattern
// matches, replacing the project-wide policy for them.
type RegistryRetentionRule struct {
	RepositoryPattern string  `json:"repository_pattern"`
	ApplicationID     *string `json:"application_id"`
	KeepDays          int64   `json:"keep_days"`
	KeepLatest        int64   `json:"keep_latest"`
}

// RegistryRetention is the organisation's retention. Source is "default"
// while the platform default applies and "organisation" once the
// organisation set its own policy; Default is the policy in force either way.
type RegistryRetention struct {
	Default         RegistryRetentionPolicy `json:"default"`
	PlatformDefault RegistryRetentionPolicy `json:"platform_default"`
	Source          string                  `json:"source"`
	Rules           []RegistryRetentionRule `json:"rules"`
	Version         string                  `json:"version"`
}

// RegistryRetentionSourceOrganisation is the source of a policy the
// organisation set itself.
const RegistryRetentionSourceOrganisation = "organisation"

// RegistryRetentionSet replaces the project-wide policy and the rule list.
// A nil Default is sent as null, which restores the platform default; Rules
// is always sent, so an empty list removes every rule. ExpectedVersion makes
// the save refuse (409) when someone changed retention since it was read.
type RegistryRetentionSet struct {
	Default         *RegistryRetentionPolicy `json:"default"`
	Rules           []RegistryRetentionRule  `json:"rules"`
	ExpectedVersion *string                  `json:"expected_version,omitempty"`
}

type registryRetentionSetEnvelope struct {
	Retention        *RegistryRetention `json:"retention"`
	HarborApplied    bool               `json:"harbor_applied"`
	HarborApplyError *string            `json:"harbor_apply_error"`
}

// GetRegistryRetention reads the organisation's retention.
func (client *Client) GetRegistryRetention(ctx context.Context) (*RegistryRetention, error) {
	var retention RegistryRetention
	if err := client.doRequest(ctx, http.MethodGet, registryRetentionPath, nil, &retention); err != nil {
		return nil, err
	}
	return &retention, nil
}

// SetRegistryRetention saves the policy and rules and answers the stored
// retention, and why the registry did not apply it at once when it did not.
func (client *Client) SetRegistryRetention(ctx context.Context, request RegistryRetentionSet) (*RegistryRetention, *string, error) {
	if request.Rules == nil {
		request.Rules = []RegistryRetentionRule{}
	}
	var envelope registryRetentionSetEnvelope
	if err := client.doRequest(ctx, http.MethodPut, registryRetentionPath, request, &envelope); err != nil {
		return nil, nil, err
	}
	if envelope.Retention == nil {
		retention, err := client.GetRegistryRetention(ctx)
		return retention, envelope.HarborApplyError, err
	}
	return envelope.Retention, envelope.HarborApplyError, nil
}
