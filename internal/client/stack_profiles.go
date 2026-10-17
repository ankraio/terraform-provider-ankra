// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package client

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"strconv"
	"time"
)

const stackProfilesPath = "/api/v1/org/stack-profiles"

// stackProfileListPageSize is the largest page the listing accepts.
const stackProfileListPageSize = 100

// StackProfile is a stack profile's summary as the platform answers it.
type StackProfile struct {
	ID              string   `json:"id"`
	OrganisationID  string   `json:"organisation_id"`
	Name            string   `json:"name"`
	Description     *string  `json:"description"`
	LogoURL         *string  `json:"logo_url"`
	HasUploadedLogo bool     `json:"has_uploaded_logo"`
	Category        string   `json:"category"`
	Tags            []string `json:"tags"`
	Visibility      string   `json:"visibility"`
	LatestVersion   int64    `json:"latest_version"`
	CurrentVersion  int64    `json:"current_version"`
	SourceClusterID *string  `json:"source_cluster_id"`
	IsOwner         bool     `json:"is_owner"`
	SharedWithYou   bool     `json:"shared_with_you"`
	PublisherName   *string  `json:"publisher_name"`
	PublisherSlug   *string  `json:"publisher_slug"`
}

// StackProfileVersionDeprecation is the withdrawal notice on a published
// version. A version carries at most one.
type StackProfileVersionDeprecation struct {
	Reason       string   `json:"reason"`
	Note         *string  `json:"note"`
	References   []string `json:"references"`
	DeprecatedAt string   `json:"deprecated_at"`
}

// StackProfileVersion is one published, immutable version of a profile.
type StackProfileVersion struct {
	ID          string                          `json:"id"`
	Version     int64                           `json:"version"`
	Channel     string                          `json:"channel"`
	Changelog   *string                         `json:"changelog"`
	CreatedAt   string                          `json:"created_at"`
	Deprecation *StackProfileVersionDeprecation `json:"deprecation"`
	Warnings    []string                        `json:"warnings"`
}

// StackProfileDetail is GET /stack-profiles/{id}: the profile and the
// summaries of all its versions.
type StackProfileDetail struct {
	Profile  *StackProfile         `json:"profile"`
	Versions []StackProfileVersion `json:"versions"`
}

// StackProfileCreate is the create body. The platform snapshots the named
// stack of the source cluster as version 1.
type StackProfileCreate struct {
	Name                       string   `json:"name"`
	Description                *string  `json:"description,omitempty"`
	LogoURL                    *string  `json:"logo_url,omitempty"`
	Category                   *string  `json:"category,omitempty"`
	Tags                       []string `json:"tags"`
	Visibility                 *string  `json:"visibility,omitempty"`
	SourceClusterID            string   `json:"source_cluster_id"`
	StackName                  string   `json:"stack_name"`
	IncludeAddonConfigurations bool     `json:"include_addon_configurations"`
	Changelog                  *string  `json:"changelog,omitempty"`
}

// StackProfileCreateResult is the create answer.
type StackProfileCreateResult struct {
	Profile  *StackProfile        `json:"profile"`
	Version  *StackProfileVersion `json:"version"`
	Warnings []string             `json:"warnings"`
}

// StackProfileUpdate is the PATCH body. An absent key, and equally a null
// one, leaves the field as it is, so a description is cleared with "" (stored
// as an empty string) and a logo URL with "" (stored as NULL). Tags is always
// sent: an empty list removes them all.
type StackProfileUpdate struct {
	Name        *string  `json:"name,omitempty"`
	Description *string  `json:"description,omitempty"`
	LogoURL     *string  `json:"logo_url,omitempty"`
	Category    *string  `json:"category,omitempty"`
	Tags        []string `json:"tags"`
	Visibility  *string  `json:"visibility,omitempty"`
}

// StackProfileVersionSave is the save-version body: the platform snapshots
// the named stack of the source cluster as the next version, which becomes
// the profile's current version.
type StackProfileVersionSave struct {
	SourceClusterID            string  `json:"source_cluster_id"`
	StackName                  string  `json:"stack_name"`
	IncludeAddonConfigurations bool    `json:"include_addon_configurations"`
	Channel                    *string `json:"channel,omitempty"`
	Changelog                  *string `json:"changelog,omitempty"`
}

// StackProfileVersionDeprecate is the deprecation body. Reason is one of
// incompatibility, critical_bug or cve.
type StackProfileVersionDeprecate struct {
	Reason     string   `json:"reason"`
	Note       *string  `json:"note,omitempty"`
	References []string `json:"references"`
}

// StackProfileShare is one grant exposing a profile to another
// organisation. OrganisationSlug is nil once that organisation is deleted.
type StackProfileShare struct {
	ID               string  `json:"id"`
	OrganisationID   string  `json:"organisation_id"`
	OrganisationName *string `json:"organisation_name"`
	OrganisationSlug *string `json:"organisation_slug"`
	CreatedAt        string  `json:"created_at"`
}

// StackProfileLogo is an uploaded logo's bytes and media type.
type StackProfileLogo struct {
	Content     []byte
	ContentType string
}

type stackProfileListEnvelope struct {
	Result     []StackProfile `json:"result"`
	TotalCount int            `json:"total_count"`
}

type stackProfileShareListEnvelope struct {
	Result []StackProfileShare `json:"result"`
}

func stackProfilePath(profileID string) string {
	return stackProfilesPath + "/" + url.PathEscape(profileID)
}

func stackProfileVersionPath(profileID string, version int64) string {
	return stackProfilePath(profileID) + "/versions/" + strconv.FormatInt(version, 10)
}

// ListStackProfiles returns every profile the organisation can see: its own,
// public ones, and ones shared with it.
func (client *Client) ListStackProfiles(ctx context.Context) ([]StackProfile, error) {
	var profiles []StackProfile
	for page := 1; ; page++ {
		query := url.Values{}
		query.Set("page", strconv.Itoa(page))
		query.Set("page_size", strconv.Itoa(stackProfileListPageSize))
		var envelope stackProfileListEnvelope
		if err := client.doRequest(ctx, http.MethodGet, stackProfilesPath+"?"+query.Encode(), nil, &envelope); err != nil {
			return nil, err
		}
		profiles = append(profiles, envelope.Result...)
		if len(envelope.Result) < stackProfileListPageSize || len(profiles) >= envelope.TotalCount {
			return profiles, nil
		}
	}
}

// CreateStackProfile creates a profile and its first version.
func (client *Client) CreateStackProfile(ctx context.Context, request StackProfileCreate) (*StackProfileCreateResult, error) {
	var result StackProfileCreateResult
	if err := client.doRequest(ctx, http.MethodPost, stackProfilesPath, request, &result); err != nil {
		return nil, err
	}
	if result.Profile == nil {
		return nil, fmt.Errorf("creating stack profile: the platform answered no profile")
	}
	return &result, nil
}

// GetStackProfile reads a profile and its version summaries. A deleted
// profile is a NotFound APIError.
func (client *Client) GetStackProfile(ctx context.Context, profileID string) (*StackProfileDetail, error) {
	var detail StackProfileDetail
	if err := client.doRequest(ctx, http.MethodGet, stackProfilePath(profileID), nil, &detail); err != nil {
		return nil, err
	}
	if detail.Profile == nil {
		return nil, &APIError{Method: http.MethodGet, Path: stackProfilePath(profileID), StatusCode: http.StatusNotFound, Body: "stack profile " + profileID + " not found"}
	}
	return &detail, nil
}

// UpdateStackProfile patches a profile's metadata.
func (client *Client) UpdateStackProfile(ctx context.Context, profileID string, request StackProfileUpdate) error {
	return client.doRequest(ctx, http.MethodPatch, stackProfilePath(profileID), request, nil)
}

// SetStackProfileCurrentVersion makes a published version the one new
// deployments get. The platform refuses a deprecated version.
func (client *Client) SetStackProfileCurrentVersion(ctx context.Context, profileID string, version int64) error {
	return client.doRequest(ctx, http.MethodPost, stackProfilePath(profileID)+"/current-version", map[string]int64{"version": version}, nil)
}

// DeleteStackProfile soft-deletes a profile. A profile the scope cannot see
// is a NotFound APIError.
func (client *Client) DeleteStackProfile(ctx context.Context, profileID string) error {
	return client.doRequest(ctx, http.MethodDelete, stackProfilePath(profileID), nil, nil)
}

// SaveStackProfileVersion publishes the next version of a profile.
func (client *Client) SaveStackProfileVersion(ctx context.Context, profileID string, request StackProfileVersionSave) (*StackProfileVersion, error) {
	var version StackProfileVersion
	if err := client.doRequest(ctx, http.MethodPost, stackProfilePath(profileID)+"/versions", request, &version); err != nil {
		return nil, err
	}
	return &version, nil
}

// GetStackProfileVersion reads one published version. An unknown version,
// or a version of a deleted profile, is a NotFound APIError.
func (client *Client) GetStackProfileVersion(ctx context.Context, profileID string, version int64) (*StackProfileVersion, error) {
	var detail StackProfileVersion
	if err := client.doRequest(ctx, http.MethodGet, stackProfileVersionPath(profileID, version), nil, &detail); err != nil {
		return nil, err
	}
	return &detail, nil
}

// DeprecateStackProfileVersion sets, or replaces, a version's withdrawal
// notice. The platform refuses to deprecate the current version.
func (client *Client) DeprecateStackProfileVersion(ctx context.Context, profileID string, version int64, request StackProfileVersionDeprecate) error {
	return client.doRequest(ctx, http.MethodPost, stackProfileVersionPath(profileID, version)+"/deprecation", request, nil)
}

// UndeprecateStackProfileVersion clears a version's withdrawal notice.
func (client *Client) UndeprecateStackProfileVersion(ctx context.Context, profileID string, version int64) error {
	return client.doRequest(ctx, http.MethodDelete, stackProfileVersionPath(profileID, version)+"/deprecation", nil, nil)
}

// ListStackProfileShares lists a profile's share grants.
func (client *Client) ListStackProfileShares(ctx context.Context, profileID string) ([]StackProfileShare, error) {
	var envelope stackProfileShareListEnvelope
	if err := client.doRequest(ctx, http.MethodGet, stackProfilePath(profileID)+"/shares", nil, &envelope); err != nil {
		return nil, err
	}
	return envelope.Result, nil
}

// CreateStackProfileShare shares a profile with another organisation, named
// by its slug.
func (client *Client) CreateStackProfileShare(ctx context.Context, profileID, organisationSlug string) (*StackProfileShare, error) {
	var share StackProfileShare
	if err := client.doRequest(ctx, http.MethodPost, stackProfilePath(profileID)+"/shares", map[string]string{"organisation_slug": organisationSlug}, &share); err != nil {
		return nil, err
	}
	return &share, nil
}

// DeleteStackProfileShare revokes a share grant. A grant, or a profile, the
// scope cannot see is a NotFound APIError.
func (client *Client) DeleteStackProfileShare(ctx context.Context, profileID, shareID string) error {
	return client.doRequest(ctx, http.MethodDelete, stackProfilePath(profileID)+"/shares/"+url.PathEscape(shareID), nil, nil)
}

// GetStackProfileLogo downloads a profile's uploaded logo. A profile without
// one is a NotFound APIError.
func (client *Client) GetStackProfileLogo(ctx context.Context, profileID string) (*StackProfileLogo, error) {
	path := stackProfilePath(profileID) + "/logo"
	response, err := client.stackProfileLogoRequest(ctx, http.MethodGet, path, nil, "")
	if err != nil {
		return nil, err
	}
	return &StackProfileLogo{Content: response.body, ContentType: response.contentType}, nil
}

// PutStackProfileLogo uploads a PNG, JPEG or WebP logo for a profile. An
// upload clears the profile's logo_url: a profile carries one or the other.
func (client *Client) PutStackProfileLogo(ctx context.Context, profileID string, content []byte, contentType string) error {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	header := textproto.MIMEHeader{}
	header.Set("Content-Disposition", `form-data; name="file"; filename="logo"`)
	header.Set("Content-Type", contentType)
	part, err := writer.CreatePart(header)
	if err != nil {
		return fmt.Errorf("encoding logo upload: %w", err)
	}
	if _, err := part.Write(content); err != nil {
		return fmt.Errorf("encoding logo upload: %w", err)
	}
	if err := writer.Close(); err != nil {
		return fmt.Errorf("encoding logo upload: %w", err)
	}
	_, err = client.stackProfileLogoRequest(ctx, http.MethodPut, stackProfilePath(profileID)+"/logo", body.Bytes(), writer.FormDataContentType())
	return err
}

// DeleteStackProfileLogo removes a profile's uploaded logo. The platform
// answers success when there is none, and NotFound for a profile the scope
// cannot see.
func (client *Client) DeleteStackProfileLogo(ctx context.Context, profileID string) error {
	return client.doRequest(ctx, http.MethodDelete, stackProfilePath(profileID)+"/logo", nil, nil)
}

type stackProfileLogoResponse struct {
	body        []byte
	contentType string
}

// stackProfileLogoRequest is doRequest for the logo route, which takes a
// multipart upload and answers raw image bytes rather than JSON. It keeps the
// shared request contract: it refuses while a scope argument is unresolved,
// sends the organisation and project scope headers, and retries exactly what
// doRequest retries (never a non-idempotent method on a server error).
func (client *Client) stackProfileLogoRequest(ctx context.Context, method, path string, body []byte, contentType string) (*stackProfileLogoResponse, error) {
	if client.UnresolvedScope != "" {
		return nil, fmt.Errorf("%s is not known until apply; refusing to call %s %s without it", client.UnresolvedScope, method, path)
	}
	if err := client.ResolveProject(ctx); err != nil {
		return nil, err
	}
	attempts := client.MaxRetries
	if attempts < 1 {
		attempts = 1
	}
	var lastError error
	for attempt := 1; attempt <= attempts; attempt++ {
		response, statusCode, err := client.stackProfileLogoAttempt(ctx, method, path, body, contentType)
		if err == nil && isAcceptableStatus(statusCode, nil) {
			return response, nil
		}
		var responseBytes []byte
		if err != nil {
			lastError = err
		} else {
			responseBytes = response.body
			lastError = &APIError{Method: method, Path: path, StatusCode: statusCode, Body: string(responseBytes)}
		}
		if attempt == attempts || !isRetryable(method, statusCode, err) {
			return nil, lastError
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(retryDelay(attempt, responseBytes, statusCode)):
		}
	}
	return nil, lastError
}

func (client *Client) stackProfileLogoAttempt(ctx context.Context, method, path string, body []byte, contentType string) (*stackProfileLogoResponse, int, error) {
	var bodyReader io.Reader
	if body != nil {
		bodyReader = bytes.NewReader(body)
	}
	request, err := http.NewRequestWithContext(ctx, method, client.BaseURL+path, bodyReader)
	if err != nil {
		return nil, 0, fmt.Errorf("building request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+client.Token)
	request.Header.Set("Accept", "image/png, image/jpeg, image/webp, application/json")
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	if client.UserAgent != "" {
		request.Header.Set("User-Agent", client.UserAgent)
	}
	if client.OrganisationID != "" {
		request.Header.Set(OrganisationHeader, client.OrganisationID)
	}
	if projectID := client.ScopeProjectID(); projectID != "" {
		request.Header.Set(ProjectHeader, projectID)
	}
	response, err := client.HTTPClient.Do(request)
	if err != nil {
		return nil, 0, fmt.Errorf("performing request: %w", err)
	}
	defer response.Body.Close()
	responseBytes, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBodySize))
	if err != nil {
		return nil, response.StatusCode, fmt.Errorf("reading response body: %w", err)
	}
	return &stackProfileLogoResponse{body: responseBytes, contentType: response.Header.Get("Content-Type")}, response.StatusCode, nil
}
