// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package client

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

const applicationsPath = "/api/v1/org/applications"

// applicationListPageSize is the largest page the application listing serves.
const applicationListPageSize = 100

// applicationNotDeployedMessage is the platform's 422 sentence for removing a
// deployment that is not there, which a delete treats as already done.
const applicationNotDeployedMessage = "is not deployed to that cluster and namespace"

func applicationPath(applicationID string, suffix string) string {
	return applicationsPath + "/" + url.PathEscape(applicationID) + suffix
}

// ApplicationImageRegistry is the image registry an application declares it
// publishes to. The create and image-registry bodies take the same block.
type ApplicationImageRegistry struct {
	URL                   string            `json:"url"`
	CredentialName        string            `json:"credential_name,omitempty"`
	APIURL                string            `json:"api_url,omitempty"`
	PullSecretName        string            `json:"pull_secret_name,omitempty"`
	UsernameSecretName    string            `json:"username_secret_name,omitempty"`
	PasswordSecretName    string            `json:"password_secret_name,omitempty"`
	ManageActionsSecrets  bool              `json:"manage_actions_secrets"`
	AdminCredentialName   string            `json:"admin_credential_name,omitempty"`
	FlatRepositories      bool              `json:"flat_repositories"`
	ComponentRepositories map[string]string `json:"component_repositories,omitempty"`
	ProjectPublic         bool              `json:"project_public"`
}

// ApplicationImageRegistryView is the effective registry the platform reads
// back. Declared is false for an application that publishes to the
// organisation's own registry project. ProjectPublic is not part of it: the
// platform only uses it when it creates the project.
type ApplicationImageRegistryView struct {
	Declared              bool              `json:"declared"`
	URL                   string            `json:"url"`
	CredentialName        string            `json:"credential_name"`
	APIURL                string            `json:"api_url"`
	PullSecretName        string            `json:"pull_secret_name"`
	UsernameSecretName    string            `json:"username_secret_name"`
	PasswordSecretName    string            `json:"password_secret_name"`
	ManageActionsSecrets  bool              `json:"manage_actions_secrets"`
	AdminCredentialName   string            `json:"admin_credential_name"`
	FlatRepositories      bool              `json:"flat_repositories"`
	ComponentRepositories map[string]string `json:"component_repositories"`
	Host                  string            `json:"host"`
	Project               string            `json:"project"`
	ImageRepository       string            `json:"image_repository"`
}

// ApplicationCreate is the create body.
type ApplicationCreate struct {
	Name                  string                    `json:"name"`
	AppRepoCredentialName string                    `json:"app_repo_credential_name"`
	AppRepoOwner          string                    `json:"app_repo_owner"`
	AppRepoName           string                    `json:"app_repo_name"`
	AppRepoBranch         string                    `json:"app_repo_branch"`
	ImageRegistry         *ApplicationImageRegistry `json:"image_registry,omitempty"`
}

// ApplicationResourceError is one refusal the create endpoint reports in a 200
// body, such as a name that is already taken.
type ApplicationResourceError struct {
	Name   string `json:"name"`
	Kind   string `json:"kind"`
	Errors []struct {
		Key     string `json:"key"`
		Message string `json:"message"`
	} `json:"errors"`
}

type applicationCreateResponse struct {
	ID     *string                    `json:"id"`
	Errors []ApplicationResourceError `json:"errors"`
}

// Application is an application as the listing and the detail route return it.
// The repository credential is not part of it; GetApplicationRepositoryCredential
// reads that.
type Application struct {
	ID                string  `json:"id"`
	Name              string  `json:"name"`
	State             string  `json:"state"`
	AnalysisStatus    *string `json:"analysis_status"`
	ErrorMessage      *string `json:"error_message"`
	AppRepoOwner      string  `json:"app_repo_owner"`
	AppRepoName       string  `json:"app_repo_name"`
	AppRepoBranch     string  `json:"app_repo_branch"`
	AppRepoURL        *string `json:"app_repo_url"`
	ContainerImageURL *string `json:"container_image_url"`
	PipelineSource    *string `json:"pipeline_source"`
	DeploymentState   string  `json:"deployment_state"`
	CreatedAt         string  `json:"created_at"`
	UpdatedAt         string  `json:"updated_at"`
}

type applicationListResponse struct {
	Result     []Application `json:"result"`
	Pagination struct {
		Page       int `json:"page"`
		TotalPages int `json:"total_pages"`
	} `json:"pagination"`
}

type applicationDeleteResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

// ApplicationRepositoryCredential is the GitHub credential an application's
// repository calls ride on, and whether it resolves to an installation.
type ApplicationRepositoryCredential struct {
	CredentialName string `json:"credential_name"`
	Resolved       bool   `json:"resolved"`
	Message        string `json:"message"`
}

// CreateApplication registers an application and returns its id. The platform
// answers a refused create, such as a duplicate name, with a 200 carrying
// errors and no id; that is returned as an error.
func (client *Client) CreateApplication(ctx context.Context, request ApplicationCreate) (string, error) {
	var response applicationCreateResponse
	if err := client.doRequest(ctx, http.MethodPost, applicationsPath, request, &response); err != nil {
		return "", err
	}
	if response.ID == nil || len(response.Errors) > 0 {
		messages := []string{}
		for _, resourceError := range response.Errors {
			for _, item := range resourceError.Errors {
				messages = append(messages, item.Message)
			}
		}
		if len(messages) == 0 {
			messages = append(messages, "the platform returned no application id")
		}
		return "", errors.New(strings.Join(messages, "; "))
	}
	return *response.ID, nil
}

// GetApplication reads one application; an unknown id is a NotFound APIError.
func (client *Client) GetApplication(ctx context.Context, applicationID string) (*Application, error) {
	var application Application
	if err := client.doRequest(ctx, http.MethodGet, applicationPath(applicationID, ""), nil, &application); err != nil {
		return nil, err
	}
	return &application, nil
}

// ListApplications returns every application, optionally filtered by the
// platform's name search, walking every page.
func (client *Client) ListApplications(ctx context.Context, search string) ([]Application, error) {
	applications := []Application{}
	for page := 1; ; page++ {
		query := url.Values{}
		query.Set("page", strconv.Itoa(page))
		query.Set("page_size", strconv.Itoa(applicationListPageSize))
		if search != "" {
			query.Set("search", search)
		}
		var response applicationListResponse
		if err := client.doRequest(ctx, http.MethodGet, applicationsPath+"?"+query.Encode(), nil, &response); err != nil {
			return nil, err
		}
		applications = append(applications, response.Result...)
		if len(response.Result) == 0 || page >= response.Pagination.TotalPages {
			return applications, nil
		}
	}
}

// DeleteApplication deletes an application and tears down every deployment of
// it. The platform answers an id it cannot see, whether deleted or outside the
// caller's project, with a 200 reporting failure; that is returned as a
// NotFound APIError, so the caller can tell "gone" from "out of scope".
func (client *Client) DeleteApplication(ctx context.Context, applicationID string) error {
	path := applicationPath(applicationID, "")
	var response applicationDeleteResponse
	if err := client.doRequest(ctx, http.MethodDelete, path, nil, &response); err != nil {
		return err
	}
	if response.Success {
		return nil
	}
	if strings.Contains(strings.ToLower(response.Message), "not found") {
		return &APIError{Method: http.MethodDelete, Path: path, StatusCode: http.StatusNotFound, Body: response.Message}
	}
	if response.Message == "" {
		return errors.New("the platform reported the application was not deleted")
	}
	return errors.New(response.Message)
}

// GetApplicationRepositoryCredential reads the GitHub credential binding.
func (client *Client) GetApplicationRepositoryCredential(ctx context.Context, applicationID string) (*ApplicationRepositoryCredential, error) {
	var credential ApplicationRepositoryCredential
	if err := client.doRequest(ctx, http.MethodGet, applicationPath(applicationID, "/repository-credential"), nil, &credential); err != nil {
		return nil, err
	}
	return &credential, nil
}

// SetApplicationRepositoryCredential rebinds the application to another GitHub
// credential of the organisation.
func (client *Client) SetApplicationRepositoryCredential(ctx context.Context, applicationID string, credentialName string) (*ApplicationRepositoryCredential, error) {
	var credential ApplicationRepositoryCredential
	body := map[string]string{"credential_name": credentialName}
	if err := client.doRequest(ctx, http.MethodPut, applicationPath(applicationID, "/repository-credential"), body, &credential); err != nil {
		return nil, err
	}
	return &credential, nil
}

// GetApplicationImageRegistry reads the application's effective image registry.
func (client *Client) GetApplicationImageRegistry(ctx context.Context, applicationID string) (*ApplicationImageRegistryView, error) {
	var view ApplicationImageRegistryView
	if err := client.doRequest(ctx, http.MethodGet, applicationPath(applicationID, "/image-registry"), nil, &view); err != nil {
		return nil, err
	}
	return &view, nil
}

// applicationImageRegistryUpdate always carries the image_registry key: an
// explicit null clears the declaration, while the platform refuses a body
// without the key.
type applicationImageRegistryUpdate struct {
	ImageRegistry *ApplicationImageRegistry `json:"image_registry"`
}

// SetApplicationImageRegistry replaces the registry declaration; nil clears it
// and hands the application back to the organisation's own registry project.
func (client *Client) SetApplicationImageRegistry(ctx context.Context, applicationID string, registry *ApplicationImageRegistry) (*ApplicationImageRegistryView, error) {
	var view ApplicationImageRegistryView
	body := applicationImageRegistryUpdate{ImageRegistry: registry}
	if err := client.doRequest(ctx, http.MethodPut, applicationPath(applicationID, "/image-registry"), body, &view); err != nil {
		return nil, err
	}
	return &view, nil
}

// ApplicationAutoDeploy is the push-to-deploy switch and the branch it watches.
type ApplicationAutoDeploy struct {
	Enabled bool   `json:"enabled"`
	Branch  string `json:"branch"`
}

// GetApplicationAutoDeploy reads the push-to-deploy switch.
func (client *Client) GetApplicationAutoDeploy(ctx context.Context, applicationID string) (*ApplicationAutoDeploy, error) {
	var autoDeploy ApplicationAutoDeploy
	if err := client.doRequest(ctx, http.MethodGet, applicationPath(applicationID, "/auto-deploy"), nil, &autoDeploy); err != nil {
		return nil, err
	}
	return &autoDeploy, nil
}

// SetApplicationAutoDeploy turns push-to-deploy on or off for the application
// as a whole.
func (client *Client) SetApplicationAutoDeploy(ctx context.Context, applicationID string, enabled bool) (*ApplicationAutoDeploy, error) {
	var autoDeploy ApplicationAutoDeploy
	body := map[string]bool{"enabled": enabled}
	if err := client.doRequest(ctx, http.MethodPut, applicationPath(applicationID, "/auto-deploy"), body, &autoDeploy); err != nil {
		return nil, err
	}
	return &autoDeploy, nil
}

// ApplicationSettings is the organisation-wide application CI settings. The
// label reads as the platform default when IsDefault is set.
type ApplicationSettings struct {
	CIRunnerLabel              string `json:"ci_runner_label"`
	IsDefault                  bool   `json:"is_default"`
	AutoMergeSetupPullRequests bool   `json:"auto_merge_setup_pull_requests"`
}

// ApplicationSettingsUpdate is the settings body. CIRunnerLabel is always
// sent: an explicit null returns the organisation to the default runner.
type ApplicationSettingsUpdate struct {
	CIRunnerLabel              *string `json:"ci_runner_label"`
	AutoMergeSetupPullRequests bool    `json:"auto_merge_setup_pull_requests"`
}

// GetApplicationSettings reads the organisation's application settings.
func (client *Client) GetApplicationSettings(ctx context.Context) (*ApplicationSettings, error) {
	var settings ApplicationSettings
	if err := client.doRequest(ctx, http.MethodGet, applicationsPath+"/settings", nil, &settings); err != nil {
		return nil, err
	}
	return &settings, nil
}

// UpdateApplicationSettings replaces the organisation's application settings.
func (client *Client) UpdateApplicationSettings(ctx context.Context, request ApplicationSettingsUpdate) (*ApplicationSettings, error) {
	var settings ApplicationSettings
	if err := client.doRequest(ctx, http.MethodPut, applicationsPath+"/settings", request, &settings); err != nil {
		return nil, err
	}
	return &settings, nil
}

// ApplicationEnvSecret is one catalogued key of an application's env Secret.
// The platform never returns the value.
type ApplicationEnvSecret struct {
	Key       string `json:"key"`
	Status    string `json:"status"`
	UpdatedAt string `json:"updated_at"`
}

// ApplicationEnvSecretStatusSet marks a key whose value is in the secret store.
const ApplicationEnvSecretStatusSet = "set"

type applicationEnvSecretList struct {
	Secrets []ApplicationEnvSecret `json:"secrets"`
}

// GetApplicationEnvSecret finds one key in the application's env-secret
// listing, which is the only read the platform offers. A missing key is a
// NotFound APIError.
func (client *Client) GetApplicationEnvSecret(ctx context.Context, applicationID string, key string) (*ApplicationEnvSecret, error) {
	path := applicationPath(applicationID, "/env-secrets")
	var listing applicationEnvSecretList
	if err := client.doRequest(ctx, http.MethodGet, path, nil, &listing); err != nil {
		return nil, err
	}
	for index := range listing.Secrets {
		if listing.Secrets[index].Key == key {
			return &listing.Secrets[index], nil
		}
	}
	return nil, &APIError{Method: http.MethodGet, Path: path, StatusCode: http.StatusNotFound, Body: "env secret " + key + " not found"}
}

// SetApplicationEnvSecret stores a value for one key.
func (client *Client) SetApplicationEnvSecret(ctx context.Context, applicationID string, key string, value string) (*ApplicationEnvSecret, error) {
	var secret ApplicationEnvSecret
	body := map[string]string{"value": value}
	if err := client.doRequest(ctx, http.MethodPut, applicationPath(applicationID, "/env-secrets/"+url.PathEscape(key)), body, &secret); err != nil {
		return nil, err
	}
	return &secret, nil
}

// DeleteApplicationEnvSecret removes one key. A key or application the caller
// cannot see is a NotFound APIError.
func (client *Client) DeleteApplicationEnvSecret(ctx context.Context, applicationID string, key string) error {
	return client.doRequest(ctx, http.MethodDelete, applicationPath(applicationID, "/env-secrets/"+url.PathEscape(key)), nil, nil)
}

// ApplicationInstallation is one deployment of an application: a cluster and
// namespace the platform keeps it running in.
type ApplicationInstallation struct {
	ID           string  `json:"id"`
	ClusterID    string  `json:"cluster_id"`
	Namespace    string  `json:"namespace"`
	DeployMode   string  `json:"deploy_mode"`
	StackName    *string `json:"stack_name"`
	Status       string  `json:"status"`
	ErrorMessage *string `json:"error_message"`
}

// ApplicationInstallationStatusRemoving is a deployment whose teardown has
// been accepted.
const ApplicationInstallationStatusRemoving = "removing"

type applicationInstallationList struct {
	Installations []ApplicationInstallation `json:"installations"`
}

// GetApplicationInstallation finds one deployment by id in the application's
// installation listing. A missing one is a NotFound APIError.
func (client *Client) GetApplicationInstallation(ctx context.Context, applicationID string, installationID string) (*ApplicationInstallation, error) {
	path := applicationPath(applicationID, "/installations")
	var listing applicationInstallationList
	if err := client.doRequest(ctx, http.MethodGet, path, nil, &listing); err != nil {
		return nil, err
	}
	for index := range listing.Installations {
		if listing.Installations[index].ID == installationID {
			return &listing.Installations[index], nil
		}
	}
	return nil, &APIError{Method: http.MethodGet, Path: path, StatusCode: http.StatusNotFound, Body: "installation " + installationID + " not found"}
}

// FindApplicationInstallation finds the deployment on one cluster and
// namespace. A missing one is a NotFound APIError.
func (client *Client) FindApplicationInstallation(ctx context.Context, applicationID string, clusterID string, namespace string) (*ApplicationInstallation, error) {
	path := applicationPath(applicationID, "/installations")
	var listing applicationInstallationList
	if err := client.doRequest(ctx, http.MethodGet, path, nil, &listing); err != nil {
		return nil, err
	}
	for index := range listing.Installations {
		installation := listing.Installations[index]
		if installation.ClusterID == clusterID && installation.Namespace == namespace {
			return &installation, nil
		}
	}
	return nil, &APIError{Method: http.MethodGet, Path: path, StatusCode: http.StatusNotFound,
		Body: fmt.Sprintf("no deployment on cluster %s in namespace %s", clusterID, namespace)}
}

// ApplicationDeployRequest is the deploy body. Inputs only names the options
// it sets; ResetInputs returns options to the application's defaults.
type ApplicationDeployRequest struct {
	ClusterID   string            `json:"cluster_id"`
	Namespace   string            `json:"namespace,omitempty"`
	DeployMode  string            `json:"deploy_mode,omitempty"`
	Inputs      map[string]string `json:"inputs,omitempty"`
	ResetInputs []string          `json:"reset_inputs,omitempty"`
}

// ApplicationDeployResult is the accepted deploy.
type ApplicationDeployResult struct {
	InstallationID string `json:"installation_id"`
	OperationID    string `json:"operation_id"`
	Status         string `json:"status"`
	Message        string `json:"message"`
}

// DeployApplication deploys the application to one cluster and namespace,
// creating that deployment or updating it in place.
func (client *Client) DeployApplication(ctx context.Context, applicationID string, request ApplicationDeployRequest) (*ApplicationDeployResult, error) {
	var result ApplicationDeployResult
	if err := client.doRequest(ctx, http.MethodPost, applicationPath(applicationID, "/deploy"), request, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// applicationNotReadyMessages are the platform's 409 sentences for a deploy
// refused only because setup has not finished: the setup pull request is not
// merged, no build has published an image, or the installation changed under
// the deploy. Each clears on its own, so a caller may wait and retry.
var applicationNotReadyMessages = []string{
	"setup is not complete",
	"No image has been published",
	"no generated manifest to deploy",
	"changed while this deploy was being saved",
}

// IsApplicationNotReady reports whether err is a deploy refused only until the
// application's setup completes. A deployment being removed is not one of
// them: that needs the removal to finish, not the application.
func IsApplicationNotReady(err error) bool {
	var apiError *APIError
	if !errors.As(err, &apiError) || apiError.StatusCode != http.StatusConflict {
		return false
	}
	for _, message := range applicationNotReadyMessages {
		if strings.Contains(apiError.Body, message) {
			return true
		}
	}
	return false
}

// RemoveApplicationDeployment takes the application off one cluster and
// namespace, leaving its other deployments standing. A deployment already being
// removed, or not deployed there, is not an error; an application or cluster
// the caller cannot see is a NotFound APIError.
func (client *Client) RemoveApplicationDeployment(ctx context.Context, applicationID string, clusterID string, namespace string) error {
	body := map[string]string{"cluster_id": clusterID, "namespace": namespace}
	err := client.doRequest(ctx, http.MethodPost, applicationPath(applicationID, "/deployments/remove"), body, nil,
		http.StatusConflict)
	var apiError *APIError
	if errors.As(err, &apiError) && apiError.StatusCode == http.StatusUnprocessableEntity &&
		strings.Contains(apiError.Body, applicationNotDeployedMessage) {
		return nil
	}
	return err
}
