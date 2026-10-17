// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package client

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

const (
	helmRegistriesPath          = "/api/v1/org/helm/registries"
	helmRegistryCredentialsPath = "/api/v1/org/helm/credentials"
	helmChartDetailsPath        = "/api/v1/org/stacks/charts/details"
	helmListPageSize            = 100
	helmListMaximumPages        = 100
)

// HelmRegistry is one Helm chart registry as the platform lists it.
// ExcludeCharts is only present on an HTTP registry.
type HelmRegistry struct {
	Name           string   `json:"name"`
	URL            string   `json:"url"`
	ExcludeCharts  []string `json:"exclude_charts"`
	CredentialName *string  `json:"credential_name"`
	IsGlobal       bool     `json:"is_global"`
	ChartCount     int64    `json:"chart_count"`
	LastSyncError  *string  `json:"last_sync_error"`
}

// HelmRegistryKind is "oci" for an oci:// URL and "http" otherwise, the same
// rule the platform uses to pick the registry kind.
func HelmRegistryKind(registryURL string) string {
	if strings.HasPrefix(strings.ToLower(registryURL), "oci://") {
		return "oci"
	}
	return "http"
}

// HelmRegistryDetail is the registry detail route's answer, reduced to the
// members the provider reads.
type HelmRegistryDetail struct {
	Registry        HelmRegistry `json:"registry"`
	ReadJobInterval *int64       `json:"read_job_interval"`
	LastSyncError   *string      `json:"last_sync_error"`
}

// HelmRegistrySpecification is the registry the create route stores.
// ExcludeCharts is only accepted for an HTTP registry.
type HelmRegistrySpecification struct {
	Name           string   `json:"name"`
	URL            string   `json:"url"`
	ExcludeCharts  []string `json:"exclude_charts,omitempty"`
	CredentialName *string  `json:"credential_name,omitempty"`
}

type helmRegistryCreateSpec struct {
	HelmHTTPRegistry *HelmRegistrySpecification `json:"helm_http_registry,omitempty"`
	HelmOCIRegistry  *HelmRegistrySpecification `json:"helm_oci_registry,omitempty"`
}

type helmRegistryCreateRequest struct {
	Spec helmRegistryCreateSpec `json:"spec"`
}

type helmRegistryCreateResponse struct {
	Errors []ResourceError `json:"errors"`
}

type helmRegistryUpdateRequest struct {
	ReadJobInterval int64 `json:"read_job_interval"`
}

type helmRegistryListResponse struct {
	Result     []HelmRegistry     `json:"result"`
	Pagination helmListPagination `json:"pagination"`
}

type helmListPagination struct {
	TotalPages int `json:"total_pages"`
	TotalCount int `json:"total_count"`
}

// ConfigurationRejectedError reports a Helm registry or registry credential
// write the platform refused with its validation errors inside a 2xx body.
type ConfigurationRejectedError struct {
	Subject string
	Errors  []ResourceError
}

func (rejected *ConfigurationRejectedError) Error() string {
	lines := make([]string, 0, len(rejected.Errors))
	for _, resourceError := range rejected.Errors {
		for _, item := range resourceError.Errors {
			lines = append(lines, fmt.Sprintf("  - %s: %s", item.Key, item.Message))
		}
		if len(resourceError.Errors) == 0 {
			lines = append(lines, fmt.Sprintf("  - %s %q", resourceError.Kind, resourceError.Name))
		}
	}
	return fmt.Sprintf("the platform rejected %s:\n%s", rejected.Subject, strings.Join(lines, "\n"))
}

// CreateHelmRegistry connects a Helm registry. The kind follows the URL
// scheme. Validation failures (a duplicate name or URL, an unknown
// credential) come back as a 200 with an errors list, returned here as a
// *ConfigurationRejectedError.
func (client *Client) CreateHelmRegistry(ctx context.Context, specification HelmRegistrySpecification) error {
	var request helmRegistryCreateRequest
	if HelmRegistryKind(specification.URL) == "oci" {
		request.Spec.HelmOCIRegistry = &specification
	} else {
		request.Spec.HelmHTTPRegistry = &specification
	}
	var response helmRegistryCreateResponse
	if err := client.doRequest(ctx, http.MethodPost, helmRegistriesPath, request, &response); err != nil {
		return err
	}
	if len(response.Errors) > 0 {
		return &ConfigurationRejectedError{Subject: fmt.Sprintf("Helm registry %q", specification.Name), Errors: response.Errors}
	}
	return nil
}

// GetHelmRegistry reads one registry. An unknown name is a NotFound APIError.
func (client *Client) GetHelmRegistry(ctx context.Context, name string) (*HelmRegistryDetail, error) {
	var detail HelmRegistryDetail
	path := helmRegistriesPath + "/" + url.PathEscape(name) + "?page=1&page_size=1"
	if err := client.doRequest(ctx, http.MethodGet, path, nil, &detail); err != nil {
		return nil, err
	}
	return &detail, nil
}

// UpdateHelmRegistry sets the registry's background sync interval in seconds,
// the only member the platform can change in place.
func (client *Client) UpdateHelmRegistry(ctx context.Context, name string, readJobInterval int64) error {
	return client.doRequest(ctx, http.MethodPut, helmRegistriesPath+"/"+url.PathEscape(name),
		helmRegistryUpdateRequest{ReadJobInterval: readJobInterval}, nil)
}

// DeleteHelmRegistry removes a registry. A registry already gone is not an
// error.
func (client *Client) DeleteHelmRegistry(ctx context.Context, name string) error {
	return client.doRequest(ctx, http.MethodDelete, helmRegistriesPath+"/"+url.PathEscape(name), nil, nil, http.StatusNotFound)
}

// ListHelmRegistries returns every registry the organisation sees, the
// platform's global registries included.
func (client *Client) ListHelmRegistries(ctx context.Context) ([]HelmRegistry, error) {
	registries := []HelmRegistry{}
	for page := 1; page <= helmListMaximumPages; page++ {
		var response helmRegistryListResponse
		path := helmRegistriesPath + "?page=" + strconv.Itoa(page) + "&page_size=" + strconv.Itoa(helmListPageSize)
		if err := client.doRequest(ctx, http.MethodGet, path, nil, &response); err != nil {
			return nil, err
		}
		registries = append(registries, response.Result...)
		if len(response.Result) == 0 || page >= response.Pagination.TotalPages {
			break
		}
	}
	return registries, nil
}

// HelmRegistryCredential is a registry credential as the platform lists it.
type HelmRegistryCredential struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	CreatedAt string `json:"created_at"`
}

// HelmRegistryCredentialDetail is the credential detail. The password is
// never returned.
type HelmRegistryCredentialDetail struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Username string `json:"username"`
	System   bool   `json:"system"`
}

// HelmRegistryCredentialCreate is the create body.
type HelmRegistryCredentialCreate struct {
	Name     string `json:"name"`
	Username string `json:"username"`
	Password string `json:"password"`
}

// HelmRegistryCredentialUpdate is the update body. The platform requires
// both members on every update.
type HelmRegistryCredentialUpdate struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type helmRegistryCredentialCreateResponse struct {
	Success bool            `json:"success"`
	Errors  []ResourceError `json:"errors"`
}

type helmRegistryCredentialUpdateResponse struct {
	Errors []ResourceError `json:"errors"`
}

type helmRegistryCredentialListResponse struct {
	Credentials []HelmRegistryCredential `json:"credentials"`
	TotalCount  int                      `json:"total_count"`
}

// CreateHelmRegistryCredential stores a registry credential. The token route
// answers a refused create (a duplicate name) with a 200 whose success is
// false, returned here as a *ConfigurationRejectedError.
func (client *Client) CreateHelmRegistryCredential(ctx context.Context, request HelmRegistryCredentialCreate) error {
	var response helmRegistryCredentialCreateResponse
	if err := client.doRequest(ctx, http.MethodPost, helmRegistryCredentialsPath, request, &response); err != nil {
		return err
	}
	if !response.Success || len(response.Errors) > 0 {
		return &ConfigurationRejectedError{Subject: fmt.Sprintf("Helm registry credential %q", request.Name), Errors: response.Errors}
	}
	return nil
}

// GetHelmRegistryCredential reads one credential with its username. The
// route needs the credentials.reveal permission; a token without it gets a
// 403 APIError.
func (client *Client) GetHelmRegistryCredential(ctx context.Context, name string) (*HelmRegistryCredentialDetail, error) {
	var detail HelmRegistryCredentialDetail
	if err := client.doRequest(ctx, http.MethodGet, helmRegistryCredentialsPath+"/"+url.PathEscape(name), nil, &detail); err != nil {
		return nil, err
	}
	return &detail, nil
}

// FindHelmRegistryCredential finds one credential by name in the listing,
// which needs no reveal permission. A missing name is a NotFound APIError.
func (client *Client) FindHelmRegistryCredential(ctx context.Context, name string) (*HelmRegistryCredential, error) {
	credentials, err := client.ListHelmRegistryCredentials(ctx)
	if err != nil {
		return nil, err
	}
	for index := range credentials {
		if credentials[index].Name == name {
			return &credentials[index], nil
		}
	}
	return nil, &APIError{Method: http.MethodGet, Path: helmRegistryCredentialsPath, StatusCode: http.StatusNotFound, Body: "credential " + name + " not found"}
}

// UpdateHelmRegistryCredential replaces a credential's username and password.
func (client *Client) UpdateHelmRegistryCredential(ctx context.Context, name string, request HelmRegistryCredentialUpdate) error {
	var response helmRegistryCredentialUpdateResponse
	if err := client.doRequest(ctx, http.MethodPut, helmRegistryCredentialsPath+"/"+url.PathEscape(name), request, &response); err != nil {
		return err
	}
	if len(response.Errors) > 0 {
		return &ConfigurationRejectedError{Subject: fmt.Sprintf("Helm registry credential %q", name), Errors: response.Errors}
	}
	return nil
}

// DeleteHelmRegistryCredential removes a credential. A credential already
// gone is not an error.
func (client *Client) DeleteHelmRegistryCredential(ctx context.Context, name string) error {
	return client.doRequest(ctx, http.MethodDelete, helmRegistryCredentialsPath+"/"+url.PathEscape(name), nil, nil, http.StatusNotFound)
}

// ListHelmRegistryCredentials returns every registry credential in the
// organisation.
func (client *Client) ListHelmRegistryCredentials(ctx context.Context) ([]HelmRegistryCredential, error) {
	credentials := []HelmRegistryCredential{}
	for page := 1; page <= helmListMaximumPages; page++ {
		var response helmRegistryCredentialListResponse
		path := helmRegistryCredentialsPath + "?page=" + strconv.Itoa(page) + "&page_size=" + strconv.Itoa(helmListPageSize)
		if err := client.doRequest(ctx, http.MethodGet, path, nil, &response); err != nil {
			return nil, err
		}
		credentials = append(credentials, response.Credentials...)
		if len(response.Credentials) == 0 || len(credentials) >= response.TotalCount {
			break
		}
	}
	return credentials, nil
}

// HelmChart is a chart in the organisation's catalogue. Versions are newest
// first, with prereleases after every release.
type HelmChart struct {
	Name           string   `json:"name"`
	RepositoryName string   `json:"repository_name"`
	RepositoryURL  string   `json:"repository_url"`
	Versions       []string `json:"versions"`
}

type helmChartDetailsRequest struct {
	ChartName     string `json:"chart_name"`
	RepositoryURL string `json:"repository_url"`
}

// GetHelmChart looks a chart up by name and registry URL. The route is a
// read shaped as a POST. An unknown chart is a NotFound APIError.
func (client *Client) GetHelmChart(ctx context.Context, chartName, registryURL string) (*HelmChart, error) {
	var chart HelmChart
	if err := client.doRequest(ctx, http.MethodPost, helmChartDetailsPath,
		helmChartDetailsRequest{ChartName: chartName, RepositoryURL: registryURL}, &chart); err != nil {
		return nil, err
	}
	return &chart, nil
}
