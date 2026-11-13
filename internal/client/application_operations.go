// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
)

// AutonomyLayerLimits are the limits a "who runs it" layer sets; a nil
// member is not set by the layer.
type AutonomyLayerLimits struct {
	MonthlyCostIncreaseCents     *int64 `json:"monthly_cost_increase_cents,omitempty"`
	ProductionDowntimeSeconds    *int64 `json:"production_downtime_seconds,omitempty"`
	NonProductionDowntimeSeconds *int64 `json:"non_production_downtime_seconds,omitempty"`
	MayChangeData                *bool  `json:"may_change_data,omitempty"`
}

// AutonomyLayer is one stored "who runs it" layer: a level for every area,
// per-area levels and limits.
type AutonomyLayer struct {
	Level          *string             `json:"level"`
	Areas          map[string]string   `json:"areas"`
	Limits         AutonomyLayerLimits `json:"limits"`
	WidenedByAdmin bool                `json:"widened_by_admin"`
}

// AutonomyLayerWrite replaces a stored layer. Expected is the layer the
// write starts from, so a layer changed meanwhile is refused instead of
// overwritten.
type AutonomyLayerWrite struct {
	Level    *string             `json:"level"`
	Areas    map[string]string   `json:"areas"`
	Limits   AutonomyLayerLimits `json:"limits"`
	Expected *AutonomyLayer      `json:"expected"`
}

// ApplicationAutonomyLayers are the layers stored for an application: its
// own and those of its environments, keyed by environment id.
type ApplicationAutonomyLayers struct {
	Application  *AutonomyLayer           `json:"application"`
	Environments map[string]AutonomyLayer `json:"environments"`
}

type applicationAutonomyView struct {
	Layers *ApplicationAutonomyLayers `json:"layers"`
}

// GetApplicationAutonomyLayers reads the stored autonomy layers of an
// application. Nil means the platform could not read them, and no write may
// be built from them.
func (client *Client) GetApplicationAutonomyLayers(ctx context.Context, applicationID string) (*ApplicationAutonomyLayers, error) {
	var view applicationAutonomyView
	if requestError := client.doRequest(ctx, http.MethodGet, applicationPath(applicationID, "/autonomy"), nil, &view); requestError != nil {
		return nil, requestError
	}
	return view.Layers, nil
}

// SetApplicationAutonomyLayer replaces the application's own layer.
func (client *Client) SetApplicationAutonomyLayer(ctx context.Context, applicationID string, write AutonomyLayerWrite) error {
	return client.doRequest(ctx, http.MethodPut, applicationPath(applicationID, "/autonomy"), write, nil)
}

// SetApplicationEnvironmentAutonomyLayer replaces one environment's layer.
func (client *Client) SetApplicationEnvironmentAutonomyLayer(ctx context.Context, applicationID, environmentID string, write AutonomyLayerWrite) error {
	return client.doRequest(ctx, http.MethodPut, applicationPath(applicationID, "/environments/"+url.PathEscape(environmentID)+"/autonomy"), write, nil)
}

type applicationEnabledFlag struct {
	Enabled bool `json:"enabled"`
}

// GetApplicationAutoFixBuilds reads whether Ankra fixes failed builds of the
// application on its own.
func (client *Client) GetApplicationAutoFixBuilds(ctx context.Context, applicationID string) (bool, error) {
	var flag applicationEnabledFlag
	if requestError := client.doRequest(ctx, http.MethodGet, applicationPath(applicationID, "/auto-fix-builds"), nil, &flag); requestError != nil {
		return false, requestError
	}
	return flag.Enabled, nil
}

// SetApplicationAutoFixBuilds turns automatic build fixing on or off.
func (client *Client) SetApplicationAutoFixBuilds(ctx context.Context, applicationID string, enabled bool) error {
	return client.doRequest(ctx, http.MethodPut, applicationPath(applicationID, "/auto-fix-builds"), applicationEnabledFlag{Enabled: enabled}, nil)
}

// ApplicationSetupAutoMerge is whether Ankra may merge the application's
// setup pull requests: Enabled is the application's own answer, nil when it
// follows its organisation.
type ApplicationSetupAutoMerge struct {
	Enabled             *bool `json:"enabled"`
	OrganisationEnabled bool  `json:"organisation_enabled"`
	Effective           bool  `json:"effective"`
}

type applicationSetupAutoMergeWrite struct {
	Enabled *bool `json:"enabled"`
}

// GetApplicationSetupAutoMerge reads the setup pull request auto-merge
// setting.
func (client *Client) GetApplicationSetupAutoMerge(ctx context.Context, applicationID string) (*ApplicationSetupAutoMerge, error) {
	var setting ApplicationSetupAutoMerge
	if requestError := client.doRequest(ctx, http.MethodGet, applicationPath(applicationID, "/auto-merge-setup-pull-requests"), nil, &setting); requestError != nil {
		return nil, requestError
	}
	return &setting, nil
}

// SetApplicationSetupAutoMerge stores the application's own answer, or nil
// to follow the organisation. Organisation administrators only.
func (client *Client) SetApplicationSetupAutoMerge(ctx context.Context, applicationID string, enabled *bool) (*ApplicationSetupAutoMerge, error) {
	var setting ApplicationSetupAutoMerge
	if requestError := client.doRequest(ctx, http.MethodPut, applicationPath(applicationID, "/auto-merge-setup-pull-requests"),
		applicationSetupAutoMergeWrite{Enabled: enabled}, &setting); requestError != nil {
		return nil, requestError
	}
	return &setting, nil
}

// ApplicationEnvironmentSummary is one environment of an application.
type ApplicationEnvironmentSummary struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Role string `json:"role"`
}

type applicationEnvironmentsView struct {
	ReadState    string                          `json:"read_state"`
	ReadReason   string                          `json:"read_reason"`
	Environments []ApplicationEnvironmentSummary `json:"environments"`
}

// ListApplicationEnvironments lists the application's environments. An
// unknown read state is an error: an empty answer then means could not
// check, never no environments.
func (client *Client) ListApplicationEnvironments(ctx context.Context, applicationID string) ([]ApplicationEnvironmentSummary, error) {
	var view applicationEnvironmentsView
	if requestError := client.doRequest(ctx, http.MethodGet, applicationPath(applicationID, "/environments"), nil, &view); requestError != nil {
		return nil, requestError
	}
	if view.ReadState == "unknown" {
		return nil, &APIError{Method: http.MethodGet, Path: applicationPath(applicationID, "/environments"), StatusCode: http.StatusServiceUnavailable,
			Body: "the platform could not read the application's environments: " + view.ReadReason}
	}
	return view.Environments, nil
}

// RenameApplicationEnvironment renames one environment of the application.
func (client *Client) RenameApplicationEnvironment(ctx context.Context, applicationID, environmentID, name string) error {
	return client.doRequest(ctx, http.MethodPatch, applicationPath(applicationID, "/environments/"+url.PathEscape(environmentID)),
		map[string]string{"name": name}, nil)
}

// OperatingExpectations is an environment's operating intent.
type OperatingExpectations struct {
	AvailabilityPercent float64 `json:"availability_percent"`
	DataRegion          string  `json:"data_region"`
	RecoveryMinutes     int64   `json:"recovery_minutes"`
	DataLossMinutes     int64   `json:"data_loss_minutes"`
	RetentionDays       int64   `json:"retention_days"`
	MonthlyBudgetCents  int64   `json:"monthly_budget_cents"`
	RoutineCare         string  `json:"routine_care"`
}

// OperatingExpectationsRevision is one adopted revision.
type OperatingExpectationsRevision struct {
	Revision     int64                 `json:"revision"`
	Expectations OperatingExpectations `json:"expectations"`
}

type operatingExpectationsView struct {
	Current *OperatingExpectationsRevision `json:"current"`
}

type operatingExpectationsAdopt struct {
	OperatingExpectations
	ExpectedRevision int64 `json:"expected_revision"`
}

func operatingExpectationsPath(applicationID, environmentID string) string {
	return applicationPath(applicationID, "/environments/"+url.PathEscape(environmentID)+"/operating-expectations")
}

// GetOperatingExpectations reads the current revision; nil when none was
// ever adopted.
func (client *Client) GetOperatingExpectations(ctx context.Context, applicationID, environmentID string) (*OperatingExpectationsRevision, error) {
	var view operatingExpectationsView
	if requestError := client.doRequest(ctx, http.MethodGet, operatingExpectationsPath(applicationID, environmentID), nil, &view); requestError != nil {
		return nil, requestError
	}
	return view.Current, nil
}

// AdoptOperatingExpectations adopts expectations over expectedRevision (0
// when none was adopted), so a revision adopted meanwhile is refused.
func (client *Client) AdoptOperatingExpectations(ctx context.Context, applicationID, environmentID string, expectations OperatingExpectations,
	expectedRevision int64) (*OperatingExpectationsRevision, error) {
	var view operatingExpectationsView
	body := operatingExpectationsAdopt{OperatingExpectations: expectations, ExpectedRevision: expectedRevision}
	if requestError := client.doRequest(ctx, http.MethodPut, operatingExpectationsPath(applicationID, environmentID), body, &view); requestError != nil {
		return nil, requestError
	}
	return view.Current, nil
}

type applicationBackupsView struct {
	DatabaseBackup bool `json:"database_backup"`
	Database       struct {
		Status string `json:"status"`
	} `json:"database"`
}

// ApplicationDatabaseBackup is the application's database backup default
// and whether it ships a database.
type ApplicationDatabaseBackup struct {
	Enabled        bool
	DatabaseStatus string
}

// GetApplicationDatabaseBackup reads the database backup default. It needs
// backups.read.
func (client *Client) GetApplicationDatabaseBackup(ctx context.Context, applicationID string) (*ApplicationDatabaseBackup, error) {
	var view applicationBackupsView
	if requestError := client.doRequest(ctx, http.MethodGet, applicationPath(applicationID, "/backups"), nil, &view); requestError != nil {
		return nil, requestError
	}
	return &ApplicationDatabaseBackup{Enabled: view.DatabaseBackup, DatabaseStatus: view.Database.Status}, nil
}

// SetApplicationDatabaseBackup turns the database backup default on or off.
// It needs backups.manage.
func (client *Client) SetApplicationDatabaseBackup(ctx context.Context, applicationID string, enabled bool) (*ApplicationDatabaseBackup, error) {
	var view applicationBackupsView
	if requestError := client.doRequest(ctx, http.MethodPut, applicationPath(applicationID, "/backups/database-backup"),
		applicationEnabledFlag{Enabled: enabled}, &view); requestError != nil {
		return nil, requestError
	}
	return &ApplicationDatabaseBackup{Enabled: view.DatabaseBackup, DatabaseStatus: view.Database.Status}, nil
}

// ApplicationProvenancePolicy is an application's deploy provenance mode and
// where the effective mode came from.
type ApplicationProvenancePolicy struct {
	Mode             string  `json:"mode"`
	Source           string  `json:"source"`
	ApplicationMode  *string `json:"application_mode"`
	OrganisationMode *string `json:"organisation_mode"`
}

// GetApplicationProvenancePolicy reads the application's provenance mode.
func (client *Client) GetApplicationProvenancePolicy(ctx context.Context, applicationID string) (*ApplicationProvenancePolicy, error) {
	var policy ApplicationProvenancePolicy
	if requestError := client.doRequest(ctx, http.MethodGet, applicationPath(applicationID, "/provenance-policy"), nil, &policy); requestError != nil {
		return nil, requestError
	}
	return &policy, nil
}

// SetApplicationProvenancePolicy stores the application's own mode; nil
// clears it so the application follows its organisation. It needs
// security.manage.
func (client *Client) SetApplicationProvenancePolicy(ctx context.Context, applicationID string, mode *string) (*ApplicationProvenancePolicy, error) {
	var policy ApplicationProvenancePolicy
	if requestError := client.doRequest(ctx, http.MethodPut, applicationPath(applicationID, "/provenance-policy"),
		map[string]*string{"mode": mode}, &policy); requestError != nil {
		return nil, requestError
	}
	return &policy, nil
}

type workspaceProfilesView struct {
	Profiles json.RawMessage `json:"profiles"`
}

func workspaceProfilesPath(repositoryID string) string {
	return "/api/v1/org/pipeline-repositories/" + url.PathEscape(repositoryID) + "/workspace-profiles"
}

// GetWorkspaceProfiles reads a pipeline repository's workspace profiles as
// JSON, keyed by workspace kind. It needs workspaces.manage to read them
// whole.
func (client *Client) GetWorkspaceProfiles(ctx context.Context, repositoryID string) (json.RawMessage, error) {
	var view workspaceProfilesView
	if requestError := client.doRequest(ctx, http.MethodGet, workspaceProfilesPath(repositoryID), nil, &view); requestError != nil {
		return nil, requestError
	}
	return view.Profiles, nil
}

// SetWorkspaceProfiles replaces a repository's workspace profiles; an empty
// object clears them.
func (client *Client) SetWorkspaceProfiles(ctx context.Context, repositoryID string, profiles json.RawMessage) (json.RawMessage, error) {
	var view workspaceProfilesView
	if requestError := client.doRequest(ctx, http.MethodPut, workspaceProfilesPath(repositoryID), workspaceProfilesView{Profiles: profiles}, &view); requestError != nil {
		return nil, requestError
	}
	return view.Profiles, nil
}
