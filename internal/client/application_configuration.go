// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package client

import (
	"context"
	"net/http"
)

// ApplicationAILaneSettings is one AI lane's fully resolved settings, as the
// platform answers them: what the lane actually runs with after inheritance.
type ApplicationAILaneSettings struct {
	Model                    string   `json:"model"`
	Autonomy                 string   `json:"autonomy"`
	Branches                 []string `json:"branches"`
	IgnorePaths              []string `json:"ignore_paths"`
	SkipDrafts               bool     `json:"skip_drafts"`
	SkillIDs                 []string `json:"skill_ids"`
	CustomInstructions       string   `json:"custom_instructions"`
	MaxRunsPerDay            int64    `json:"max_runs_per_day"`
	MaxInputCharacters       int64    `json:"max_input_characters"`
	MaxReviewsPerPullRequest int64    `json:"max_reviews_per_pull_request"`
}

// ApplicationAILane is one lane's state for an application.
type ApplicationAILane struct {
	Lane                     string                    `json:"lane"`
	Enabled                  bool                      `json:"enabled"`
	OrganisationEnabled      bool                      `json:"organisation_enabled"`
	Source                   string                    `json:"source"`
	Effective                ApplicationAILaneSettings `json:"effective"`
	OverriddenForApplication bool                      `json:"overridden_for_application"`
}

// ApplicationAIConfig is an application's AI configuration. Lanes is empty
// when Binding.Connected is false, and Binding.Reason then says why.
type ApplicationAIConfig struct {
	Binding struct {
		Connected    bool   `json:"connected"`
		RepoFullName string `json:"repo_full_name"`
		Reason       string `json:"reason"`
	} `json:"binding"`
	Lanes []ApplicationAILane `json:"lanes"`
}

// ApplicationAILaneBudget is the sparse spend cap of a lane override.
type ApplicationAILaneBudget struct {
	MaxRunsPerDay            *int64 `json:"max_runs_per_day,omitempty"`
	MaxInputCharacters       *int64 `json:"max_input_characters,omitempty"`
	MaxReviewsPerPullRequest *int64 `json:"max_reviews_per_pull_request,omitempty"`
}

// ApplicationAILaneSettingsPatch is a sparse lane override: a nil field
// inherits from the organisation. The platform stores it wholesale, replacing
// whatever override the lane had.
type ApplicationAILaneSettingsPatch struct {
	Model              *string                  `json:"model,omitempty"`
	Autonomy           *string                  `json:"autonomy,omitempty"`
	Branches           *[]string                `json:"branches,omitempty"`
	IgnorePaths        *[]string                `json:"ignore_paths,omitempty"`
	SkipDrafts         *bool                    `json:"skip_drafts,omitempty"`
	SkillIDs           *[]string                `json:"skill_ids,omitempty"`
	CustomInstructions *string                  `json:"custom_instructions,omitempty"`
	Budget             *ApplicationAILaneBudget `json:"budget,omitempty"`
}

// ApplicationAILanePatch changes one lane. Override false returns the lane to
// the organisation default; Override true stores Enabled and Settings.
type ApplicationAILanePatch struct {
	Lane     string                          `json:"lane"`
	Override bool                            `json:"override"`
	Enabled  *bool                           `json:"enabled,omitempty"`
	Settings *ApplicationAILaneSettingsPatch `json:"settings,omitempty"`
}

// ApplicationAIConfigUpdate patches the lanes it names; other lanes keep what
// they had.
type ApplicationAIConfigUpdate struct {
	Lanes []ApplicationAILanePatch `json:"lanes"`
}

// GetApplicationAIConfig reads the application's AI configuration.
func (client *Client) GetApplicationAIConfig(ctx context.Context, applicationID string) (*ApplicationAIConfig, error) {
	var configuration ApplicationAIConfig
	if err := client.doRequest(ctx, http.MethodGet, applicationPath(applicationID, "/ai-config"), nil, &configuration); err != nil {
		return nil, err
	}
	return &configuration, nil
}

// UpdateApplicationAIConfig applies a lane patch.
func (client *Client) UpdateApplicationAIConfig(ctx context.Context, applicationID string, request ApplicationAIConfigUpdate) (*ApplicationAIConfig, error) {
	var configuration ApplicationAIConfig
	if err := client.doRequest(ctx, http.MethodPut, applicationPath(applicationID, "/ai-config"), request, &configuration); err != nil {
		return nil, err
	}
	return &configuration, nil
}

// ApplicationDemoEnvironmentVariable is one variable every demo of the
// application starts with. A secret one carries ${SECRET_SLOT:<uuid>}
// references, never a plaintext value.
type ApplicationDemoEnvironmentVariable struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Secret bool   `json:"secret"`
}

// ApplicationDemoBaseStack references the shared base stack demos depend on.
type ApplicationDemoBaseStack struct {
	ID string `json:"id"`
}

// ApplicationDemoConfiguration is the demo configuration as the platform
// reads it back. GeneratedPassword is only set on the answer to the save that
// turned protection on.
type ApplicationDemoConfiguration struct {
	Env                []ApplicationDemoEnvironmentVariable `json:"env"`
	Database           bool                                 `json:"database"`
	BaseStack          *ApplicationDemoBaseStack            `json:"base_stack"`
	MigrateCommand     *string                              `json:"migrate_command"`
	DatabaseExtensions *[]string                            `json:"database_extensions"`
	Protected          *bool                                `json:"protected"`
	ProtectedUsername  string                               `json:"protected_username"`
	GeneratedPassword  string                               `json:"generated_password"`
}

// ApplicationDemoConfigurationUpdate is the demo configuration body. Every
// key is sent, so each one is written: an explicit null base stack, empty
// migrate command and empty extension list clear them. The stack profile and
// routing keys are left out, which leaves them as they are.
type ApplicationDemoConfigurationUpdate struct {
	Env                []ApplicationDemoEnvironmentVariable `json:"env"`
	Database           bool                                 `json:"database"`
	BaseStack          *ApplicationDemoBaseStack            `json:"base_stack"`
	MigrateCommand     string                               `json:"migrate_command"`
	DatabaseExtensions []string                             `json:"database_extensions"`
	Protected          bool                                 `json:"protected"`
}

// GetApplicationDemoConfiguration reads the application's demo configuration.
func (client *Client) GetApplicationDemoConfiguration(ctx context.Context, applicationID string) (*ApplicationDemoConfiguration, error) {
	var configuration ApplicationDemoConfiguration
	if err := client.doRequest(ctx, http.MethodGet, applicationPath(applicationID, "/demo-config"), nil, &configuration); err != nil {
		return nil, err
	}
	return &configuration, nil
}

// UpdateApplicationDemoConfiguration writes the application's demo
// configuration.
func (client *Client) UpdateApplicationDemoConfiguration(ctx context.Context, applicationID string, request ApplicationDemoConfigurationUpdate) (*ApplicationDemoConfiguration, error) {
	var configuration ApplicationDemoConfiguration
	if err := client.doRequest(ctx, http.MethodPut, applicationPath(applicationID, "/demo-config"), request, &configuration); err != nil {
		return nil, err
	}
	return &configuration, nil
}
