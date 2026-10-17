// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package client

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"sync"
	"sync/atomic"
)

const projectsPath = "/api/v1/org/projects"

var projectIDPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// Project is one project the token's user can reach in the organisation.
type Project struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Slug      string `json:"slug"`
	IsDefault bool   `json:"is_default"`
}

type projectListEnvelope struct {
	Projects []Project `json:"projects"`
}

type projectSlugResolution struct {
	once       sync.Once
	projectID  string
	err        error
	isResolved atomic.Bool
}

// ResolveProject resolves ProjectSlug to the project's ID by listing the
// organisation's projects, once per provider instance. Every request path
// calls it first, so a slug never reaches the platform as the project header.
// It is a no-op when ProjectID is set or no slug is configured.
func (client *Client) ResolveProject(ctx context.Context) error {
	if client.ProjectSlug == "" || client.ProjectID != "" {
		return nil
	}
	resolution := client.projectSlug
	if resolution == nil {
		return fmt.Errorf("project %q cannot be resolved: client was not built with NewClient", client.ProjectSlug)
	}
	resolution.once.Do(func() {
		resolution.projectID, resolution.err = client.lookupProjectSlug(ctx, client.ProjectSlug)
		resolution.isResolved.Store(resolution.err == nil)
	})
	return resolution.err
}

// ScopeProjectID is the project ID requests are scoped to: ProjectID, or the
// ID ProjectSlug resolved to. It is "" when no project is set, and while a
// slug is not resolved yet.
func (client *Client) ScopeProjectID() string {
	if client.ProjectID != "" || client.ProjectSlug == "" || client.projectSlug == nil {
		return client.ProjectID
	}
	if !client.projectSlug.isResolved.Load() {
		return ""
	}
	return client.projectSlug.projectID
}

// InProject returns a copy of the client scoped to another project, given by
// ID or slug. A slug is resolved here, so the copy never sends one.
func (client *Client) InProject(ctx context.Context, reference string) (*Client, error) {
	scoped := *client
	scoped.ProjectSlug = ""
	scoped.projectSlug = &projectSlugResolution{}
	if projectIDPattern.MatchString(reference) {
		scoped.ProjectID = reference
		return &scoped, nil
	}
	projectID, err := client.lookupProjectSlug(ctx, reference)
	if err != nil {
		return nil, err
	}
	scoped.ProjectID = projectID
	return &scoped, nil
}

func (client *Client) lookupProjectSlug(ctx context.Context, slug string) (string, error) {
	var envelope projectListEnvelope
	err := client.doRequest(ctx, http.MethodGet, projectsPath, nil, &envelope)
	var apiError *APIError
	switch {
	case errors.As(err, &apiError) && apiError.StatusCode == http.StatusNotFound:
		return "", fmt.Errorf("project %q cannot be resolved: projects are not enabled for this organisation, so leave project_id unset", slug)
	case errors.As(err, &apiError) && apiError.StatusCode == http.StatusForbidden:
		return "", fmt.Errorf("project %q cannot be resolved: the token may not list the organisation's projects; set project_id to the project's ID instead of its slug", slug)
	case err != nil:
		return "", fmt.Errorf("resolving project %q: %w", slug, err)
	}
	for _, project := range envelope.Projects {
		if project.Slug == slug {
			return project.ID, nil
		}
	}
	return "", fmt.Errorf("no project with slug %q is visible to the token in this organisation", slug)
}

type projectScopeCheck struct {
	once  sync.Once
	count int
	err   error
}

// FloatingProjectCount reports how many projects the organisation has when
// requests carry no project_id, so the platform scopes each one to the project
// the user last selected in the portal. It is 0 when project_id is set, when
// the organisation does not use projects (the listing answers 404), or when
// the platform will not say (403, a token without projects.read). The lookup
// runs once per provider instance.
func (client *Client) FloatingProjectCount(ctx context.Context) (int, error) {
	if client.ProjectID != "" || client.ProjectSlug != "" {
		return 0, nil
	}
	check := client.projectScope
	if check == nil {
		check = &projectScopeCheck{}
		client.projectScope = check
	}
	check.once.Do(func() {
		check.count, check.err = client.countProjects(ctx)
	})
	return check.count, check.err
}

func (client *Client) countProjects(ctx context.Context) (int, error) {
	var envelope projectListEnvelope
	err := client.doRequest(ctx, http.MethodGet, projectsPath, nil, &envelope)
	if err == nil {
		return len(envelope.Projects), nil
	}
	var apiError *APIError
	if errors.As(err, &apiError) && (apiError.StatusCode == http.StatusNotFound || apiError.StatusCode == http.StatusForbidden) {
		return 0, nil
	}
	return 0, err
}
