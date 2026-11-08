// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package client

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
)

// projectsDisabledDetail is the platform's answer on every project route while
// the organisation has projects off.
const projectsDisabledDetail = "Projects are not enabled for this organisation"

// ProjectCreate is the create body. The slug never changes once created.
type ProjectCreate struct {
	Name        string `json:"name"`
	Slug        string `json:"slug"`
	Description string `json:"description"`
}

// ProjectUpdate is the patch body; a nil field keeps the stored value.
type ProjectUpdate struct {
	Name        *string `json:"name,omitempty"`
	Description *string `json:"description,omitempty"`
}

// IsProjectsDisabled reports whether the error is the 404 every project route
// answers while the organisation has projects off. It is not a missing
// project: a resource must not plan its recreation on it.
func IsProjectsDisabled(requestError error) bool {
	var apiError *APIError
	if !errors.As(requestError, &apiError) {
		return false
	}
	return apiError.StatusCode == http.StatusNotFound && strings.Contains(apiError.Body, projectsDisabledDetail)
}

func projectPath(projectID string) string {
	return projectsPath + "/" + url.PathEscape(projectID)
}

// ListProjects returns the organisation's projects the token's user can reach.
func (client *Client) ListProjects(ctx context.Context) ([]Project, error) {
	var envelope projectListEnvelope
	if requestError := client.doRequest(ctx, http.MethodGet, projectsPath, nil, &envelope); requestError != nil {
		return nil, requestError
	}
	if envelope.Projects == nil {
		return []Project{}, nil
	}
	return envelope.Projects, nil
}

// GetProject returns one project by id. A project the organisation does not
// have is a NotFound APIError; so is an organisation with projects off, which
// IsProjectsDisabled tells apart.
func (client *Client) GetProject(ctx context.Context, projectID string) (*Project, error) {
	var project Project
	if requestError := client.doRequest(ctx, http.MethodGet, projectPath(projectID), nil, &project); requestError != nil {
		return nil, requestError
	}
	return &project, nil
}

// CreateProject creates a project. It needs the projects.manage permission.
func (client *Client) CreateProject(ctx context.Context, request ProjectCreate) (*Project, error) {
	var project Project
	if requestError := client.doRequest(ctx, http.MethodPost, projectsPath, request, &project); requestError != nil {
		return nil, requestError
	}
	return &project, nil
}

// UpdateProject renames a project or changes its description.
func (client *Client) UpdateProject(ctx context.Context, projectID string, request ProjectUpdate) (*Project, error) {
	var project Project
	if requestError := client.doRequest(ctx, http.MethodPatch, projectPath(projectID), request, &project); requestError != nil {
		return nil, requestError
	}
	return &project, nil
}

// DeleteProject deletes an empty project that is not the organisation's
// default. A project that still holds resources is refused with a 409.
func (client *Client) DeleteProject(ctx context.Context, projectID string) error {
	return client.doRequest(ctx, http.MethodDelete, projectPath(projectID), nil, nil)
}
