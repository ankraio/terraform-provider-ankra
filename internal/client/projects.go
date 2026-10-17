// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package client

import (
	"context"
	"errors"
	"net/http"
	"sync"
)

const projectsPath = "/api/v1/org/projects"

type projectListEnvelope struct {
	Projects []struct {
		ID string `json:"id"`
	} `json:"projects"`
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
	if client.ProjectID != "" {
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
