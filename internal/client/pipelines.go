// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package client

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
)

const pipelineRepositoriesPath = "/api/v1/org/pipelines/repositories"

// pipelineRepositoryPageLimit is the largest page the repository listing
// serves.
const pipelineRepositoryPageLimit = 100

// PipelineSelector addresses one repository's pipeline: through the
// repository directly, or through the application it is linked to. Exactly
// one of the two is set.
type PipelineSelector struct {
	ApplicationID string
	RepositoryID  string
}

// errPipelineSelectorRequired refuses a call whose selector names neither.
var errPipelineSelectorRequired = errors.New("a pipeline needs an application id or a repository id")

func (selector PipelineSelector) basePath() (string, error) {
	switch {
	case selector.RepositoryID != "":
		return "/api/v1/org/pipeline-repositories/" + url.PathEscape(selector.RepositoryID), nil
	case selector.ApplicationID != "":
		return applicationPath(selector.ApplicationID, ""), nil
	default:
		return "", errPipelineSelectorRequired
	}
}

// PipelineRepository is a repository connected to Ankra Pipelines.
type PipelineRepository struct {
	ID                string  `json:"id"`
	Provider          string  `json:"provider"`
	Owner             string  `json:"owner"`
	Name              string  `json:"name"`
	CredentialName    string  `json:"credential_name"`
	DefaultBranch     string  `json:"default_branch"`
	ApplicationID     *string `json:"application_id"`
	ClusterID         *string `json:"cluster_id"`
	MergeTrainEnabled bool    `json:"merge_train_enabled"`
	MergeTrainMaxCars int64   `json:"merge_train_max_cars"`
	WorkspacesOnly    bool    `json:"workspaces_only"`
}

// PipelineRepositoryConnect is the connect body.
type PipelineRepositoryConnect struct {
	Provider       string `json:"provider"`
	Owner          string `json:"owner"`
	Name           string `json:"name"`
	CredentialName string `json:"credential_name,omitempty"`
	DefaultBranch  string `json:"default_branch,omitempty"`
	ApplicationID  string `json:"application_id,omitempty"`
	ClusterID      string `json:"cluster_id,omitempty"`
	WorkspacesOnly bool   `json:"workspaces_only"`
}

// PipelineRepositoryUpdate is the PATCH body. ClusterID is always sent: an
// explicit null clears the override and returns the repository to the
// organisation's CI cluster.
type PipelineRepositoryUpdate struct {
	ClusterID      *string `json:"cluster_id"`
	WorkspacesOnly bool    `json:"workspaces_only"`
}

// PipelineRepositoryFilter narrows the repository listing.
type PipelineRepositoryFilter struct {
	Provider string
	Owner    string
	Name     string
}

type pipelineRepositoryPage struct {
	Repositories []PipelineRepository `json:"repositories"`
	NextCursor   *string              `json:"next_cursor"`
}

func pipelineRepositoryPath(repositoryID string) string {
	return pipelineRepositoriesPath + "/" + url.PathEscape(repositoryID)
}

// ConnectPipelineRepository connects a repository. One the organisation has
// already connected is a 409 APIError naming the existing id.
func (client *Client) ConnectPipelineRepository(ctx context.Context, request PipelineRepositoryConnect) (*PipelineRepository, error) {
	var repository PipelineRepository
	if err := client.doRequest(ctx, http.MethodPost, pipelineRepositoriesPath, request, &repository); err != nil {
		return nil, err
	}
	return &repository, nil
}

// GetPipelineRepository reads one connected repository; a disconnected one is
// a NotFound APIError.
func (client *Client) GetPipelineRepository(ctx context.Context, repositoryID string) (*PipelineRepository, error) {
	var repository PipelineRepository
	if err := client.doRequest(ctx, http.MethodGet, pipelineRepositoryPath(repositoryID), nil, &repository); err != nil {
		return nil, err
	}
	return &repository, nil
}

// UpdatePipelineRepository changes a repository's CI cluster override and
// Workspaces-only mode.
func (client *Client) UpdatePipelineRepository(ctx context.Context, repositoryID string, request PipelineRepositoryUpdate) (*PipelineRepository, error) {
	var repository PipelineRepository
	if err := client.doRequest(ctx, http.MethodPatch, pipelineRepositoryPath(repositoryID), request, &repository); err != nil {
		return nil, err
	}
	return &repository, nil
}

// DisconnectPipelineRepository disconnects a repository. One already
// disconnected is not an error.
func (client *Client) DisconnectPipelineRepository(ctx context.Context, repositoryID string) error {
	return client.doRequest(ctx, http.MethodDelete, pipelineRepositoryPath(repositoryID), nil, nil, http.StatusNotFound)
}

// ListPipelineRepositories returns every connected repository the filter
// matches, walking every page.
func (client *Client) ListPipelineRepositories(ctx context.Context, filter PipelineRepositoryFilter) ([]PipelineRepository, error) {
	repositories := []PipelineRepository{}
	cursor := ""
	for {
		query := url.Values{}
		query.Set("limit", strconv.Itoa(pipelineRepositoryPageLimit))
		if filter.Provider != "" {
			query.Set("provider", filter.Provider)
		}
		if filter.Owner != "" {
			query.Set("owner", filter.Owner)
		}
		if filter.Name != "" {
			query.Set("name", filter.Name)
		}
		if cursor != "" {
			query.Set("cursor", cursor)
		}
		var page pipelineRepositoryPage
		if err := client.doRequest(ctx, http.MethodGet, pipelineRepositoriesPath+"?"+query.Encode(), nil, &page); err != nil {
			return nil, err
		}
		repositories = append(repositories, page.Repositories...)
		if page.NextCursor == nil || *page.NextCursor == "" || len(page.Repositories) == 0 {
			return repositories, nil
		}
		cursor = *page.NextCursor
	}
}

// PipelineDefinition is a repository's definition of record. SpecYAML is the
// platform's canonical render of the spec, not the text that was stored.
type PipelineDefinition struct {
	Source        string   `json:"source"`
	SpecYAML      string   `json:"spec_yaml"`
	SpecHash      string   `json:"spec_hash"`
	ProtectedHash *string  `json:"protected_hash"`
	Violations    []string `json:"violations"`
}

// GetPipelineDefinition reads the definition of record. A repository with none
// is a NotFound APIError.
func (client *Client) GetPipelineDefinition(ctx context.Context, selector PipelineSelector) (*PipelineDefinition, error) {
	base, err := selector.basePath()
	if err != nil {
		return nil, err
	}
	var definition PipelineDefinition
	if err := client.doRequest(ctx, http.MethodGet, base+"/pipeline", nil, &definition); err != nil {
		return nil, err
	}
	return &definition, nil
}

// PutPipelineDefinition stores a spec as the definition of record.
func (client *Client) PutPipelineDefinition(ctx context.Context, selector PipelineSelector, specYAML string) (*PipelineDefinition, error) {
	base, err := selector.basePath()
	if err != nil {
		return nil, err
	}
	var definition PipelineDefinition
	body := map[string]string{"spec_yaml": specYAML}
	if err := client.doRequest(ctx, http.MethodPut, base+"/pipeline", body, &definition); err != nil {
		return nil, err
	}
	return &definition, nil
}

// PipelineSchedule is one cron entry of a repository's pipeline.
type PipelineSchedule struct {
	ID           string            `json:"id"`
	RepositoryID string            `json:"repository_id"`
	Cron         string            `json:"cron"`
	Timezone     string            `json:"timezone"`
	Ref          string            `json:"ref"`
	Inputs       map[string]string `json:"inputs"`
	Enabled      bool              `json:"enabled"`
	NextFireAt   *string           `json:"next_fire_at"`
}

// PipelineScheduleCreate is the create body; an empty timezone is UTC.
type PipelineScheduleCreate struct {
	Cron     string            `json:"cron"`
	Timezone string            `json:"timezone,omitempty"`
	Ref      string            `json:"ref"`
	Inputs   map[string]string `json:"inputs"`
	Enabled  bool              `json:"enabled"`
}

// PipelineScheduleUpdate is the update body. Every field is sent, so the
// update replaces the schedule rather than patching it.
type PipelineScheduleUpdate struct {
	Cron     string            `json:"cron"`
	Timezone string            `json:"timezone"`
	Ref      string            `json:"ref"`
	Inputs   map[string]string `json:"inputs"`
	Enabled  bool              `json:"enabled"`
}

type pipelineScheduleList struct {
	Schedules []PipelineSchedule `json:"schedules"`
}

// GetPipelineSchedule finds one schedule in the repository's listing, the only
// read the platform offers. A missing one is a NotFound APIError.
func (client *Client) GetPipelineSchedule(ctx context.Context, selector PipelineSelector, scheduleID string) (*PipelineSchedule, error) {
	base, err := selector.basePath()
	if err != nil {
		return nil, err
	}
	path := base + "/pipeline/schedules"
	var listing pipelineScheduleList
	if err := client.doRequest(ctx, http.MethodGet, path, nil, &listing); err != nil {
		return nil, err
	}
	for index := range listing.Schedules {
		if listing.Schedules[index].ID == scheduleID {
			return &listing.Schedules[index], nil
		}
	}
	return nil, &APIError{Method: http.MethodGet, Path: path, StatusCode: http.StatusNotFound, Body: "schedule " + scheduleID + " not found"}
}

// CreatePipelineSchedule adds a schedule.
func (client *Client) CreatePipelineSchedule(ctx context.Context, selector PipelineSelector, request PipelineScheduleCreate) (*PipelineSchedule, error) {
	base, err := selector.basePath()
	if err != nil {
		return nil, err
	}
	var schedule PipelineSchedule
	if err := client.doRequest(ctx, http.MethodPost, base+"/pipeline/schedules", request, &schedule); err != nil {
		return nil, err
	}
	return &schedule, nil
}

// UpdatePipelineSchedule replaces a schedule's settings.
func (client *Client) UpdatePipelineSchedule(ctx context.Context, selector PipelineSelector, scheduleID string, request PipelineScheduleUpdate) (*PipelineSchedule, error) {
	base, err := selector.basePath()
	if err != nil {
		return nil, err
	}
	var schedule PipelineSchedule
	if err := client.doRequest(ctx, http.MethodPut, base+"/pipeline/schedules/"+url.PathEscape(scheduleID), request, &schedule); err != nil {
		return nil, err
	}
	return &schedule, nil
}

// DeletePipelineSchedule removes a schedule. One the caller cannot see is a
// NotFound APIError.
func (client *Client) DeletePipelineSchedule(ctx context.Context, selector PipelineSelector, scheduleID string) error {
	base, err := selector.basePath()
	if err != nil {
		return err
	}
	return client.doRequest(ctx, http.MethodDelete, base+"/pipeline/schedules/"+url.PathEscape(scheduleID), nil, nil)
}

// PipelineMergeTrainSettings is a repository's merge train switch and how
// many pull requests it tests at once.
type PipelineMergeTrainSettings struct {
	Enabled bool  `json:"enabled"`
	MaxCars int64 `json:"max_cars"`
}

// PipelineMergeTrainSettingsUpdate is the settings body; a nil MaxCars keeps
// the stored value.
type PipelineMergeTrainSettingsUpdate struct {
	Enabled bool   `json:"enabled"`
	MaxCars *int64 `json:"max_cars,omitempty"`
}

type pipelineMergeTrain struct {
	Settings PipelineMergeTrainSettings `json:"settings"`
}

// GetPipelineMergeTrainSettings reads a repository's merge train settings.
func (client *Client) GetPipelineMergeTrainSettings(ctx context.Context, selector PipelineSelector) (*PipelineMergeTrainSettings, error) {
	base, err := selector.basePath()
	if err != nil {
		return nil, err
	}
	var train pipelineMergeTrain
	if err := client.doRequest(ctx, http.MethodGet, base+"/merge-train", nil, &train); err != nil {
		return nil, err
	}
	return &train.Settings, nil
}

// SetPipelineMergeTrainSettings turns a repository's merge train on or off.
func (client *Client) SetPipelineMergeTrainSettings(ctx context.Context, selector PipelineSelector, request PipelineMergeTrainSettingsUpdate) (*PipelineMergeTrainSettings, error) {
	base, err := selector.basePath()
	if err != nil {
		return nil, err
	}
	var settings PipelineMergeTrainSettings
	if err := client.doRequest(ctx, http.MethodPut, base+"/merge-train/settings", request, &settings); err != nil {
		return nil, err
	}
	return &settings, nil
}
