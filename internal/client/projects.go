// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package client

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"sync"
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

// projectSlugDirectory caches the slug -> ID pairs of the organisation's
// projects. Only a successful listing is cached, so a failed lookup (a
// network error, say) is retried by the next request instead of failing the
// rest of the run.
type projectSlugDirectory struct {
	// listing serialises project listings, so concurrent resources share one.
	listing     sync.Mutex
	identifiers sync.Map
}

func (directory *projectSlugDirectory) lookup(slug string) (string, bool) {
	if directory == nil {
		return "", false
	}
	stored, found := directory.identifiers.Load(slug)
	if !found {
		return "", false
	}
	identifier, isString := stored.(string)
	return identifier, isString
}

// ResolveProject resolves ProjectSlug to the project's ID from the
// organisation's project list. Every request path calls it first, so a slug
// never reaches the platform as the project header. It is a no-op when
// ProjectID is set or no slug is configured.
func (client *Client) ResolveProject(ctx context.Context) error {
	if client.ProjectSlug == "" || client.ProjectID != "" {
		return nil
	}
	_, err := client.projectIDForSlug(ctx, client.ProjectSlug)
	return err
}

// ScopeProjectID is the project ID requests are scoped to: ProjectID, or the
// ID ProjectSlug resolved to. It is "" when no project is set, and while a
// slug is not resolved yet.
func (client *Client) ScopeProjectID() string {
	if client.ProjectID != "" || client.ProjectSlug == "" {
		return client.ProjectID
	}
	identifier, _ := client.projectSlugs.lookup(client.ProjectSlug)
	return identifier
}

// InProject returns a copy of the client scoped to another project, given by
// ID or slug. A slug is resolved here, from the shared cache when it was
// resolved before, so the copy never sends one.
func (client *Client) InProject(ctx context.Context, reference string) (*Client, error) {
	reference = strings.TrimSpace(reference)
	scoped := *client
	scoped.ProjectSlug = ""
	if projectIDPattern.MatchString(reference) {
		scoped.ProjectID = reference
		return &scoped, nil
	}
	projectID, err := client.projectIDForSlug(ctx, reference)
	if err != nil {
		return nil, err
	}
	scoped.ProjectID = projectID
	return &scoped, nil
}

// ProjectIDFor resolves a project reference (an ID or a slug) to its ID.
func (client *Client) ProjectIDFor(ctx context.Context, reference string) (string, error) {
	scoped, err := client.InProject(ctx, reference)
	if err != nil {
		return "", err
	}
	return scoped.ProjectID, nil
}

func (client *Client) projectIDForSlug(ctx context.Context, slug string) (string, error) {
	directory := client.projectSlugs
	if directory == nil {
		directory = &projectSlugDirectory{}
		client.projectSlugs = directory
	}
	if identifier, found := directory.lookup(slug); found {
		return identifier, nil
	}
	directory.listing.Lock()
	defer directory.listing.Unlock()
	if identifier, found := directory.lookup(slug); found {
		return identifier, nil
	}
	projects, err := client.listProjectsForSlug(ctx, slug)
	if err != nil {
		return "", err
	}
	for _, project := range projects {
		directory.identifiers.Store(project.Slug, project.ID)
	}
	if identifier, found := directory.lookup(slug); found {
		return identifier, nil
	}
	return "", fmt.Errorf("no project with slug %q is visible to the token in this organisation", slug)
}

func (client *Client) listProjectsForSlug(ctx context.Context, slug string) ([]Project, error) {
	var envelope projectListEnvelope
	err := client.doRequest(ctx, http.MethodGet, projectsPath, nil, &envelope)
	var apiError *APIError
	switch {
	case errors.As(err, &apiError) && apiError.StatusCode == http.StatusNotFound:
		return nil, fmt.Errorf("project %q cannot be resolved: projects are not enabled for this organisation, so leave project_id unset", slug)
	case errors.As(err, &apiError) && apiError.StatusCode == http.StatusForbidden:
		return nil, fmt.Errorf("project %q cannot be resolved: the token may not list the organisation's projects; set project_id to the project's ID instead of its slug", slug)
	case err != nil:
		return nil, fmt.Errorf("resolving project %q: %w", slug, err)
	}
	return envelope.Projects, nil
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
