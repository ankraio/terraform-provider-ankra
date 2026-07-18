// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// Package client is a small typed HTTP client for the Ankra platform API used
// by the Terraform provider.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-log/tflog"
)

// DefaultBaseURL is the public Ankra platform API endpoint.
const DefaultBaseURL = "https://platform.ankra.app"

// Client talks to the Ankra platform REST API.
type Client struct {
	BaseURL    string
	Token      string
	UserAgent  string
	HTTPClient *http.Client
}

// NewClient returns a Client. An empty baseURL falls back to DefaultBaseURL.
func NewClient(baseURL, token, userAgent string) *Client {
	if strings.TrimSpace(baseURL) == "" {
		baseURL = DefaultBaseURL
	}
	return &Client{
		BaseURL:    strings.TrimRight(baseURL, "/"),
		Token:      token,
		UserAgent:  userAgent,
		HTTPClient: &http.Client{Timeout: 60 * time.Second},
	}
}

// APIError describes a non-2xx response from the platform API.
type APIError struct {
	Method     string
	Path       string
	StatusCode int
	Body       string
}

func (apiError *APIError) Error() string {
	return fmt.Sprintf("ankra API %s %s returned %d: %s", apiError.Method, apiError.Path, apiError.StatusCode, apiError.Body)
}

// doRequest issues a request against the platform API and decodes a 2xx JSON
// body into responseTarget when it is non-nil. acceptableStatuses lists extra
// status codes (beyond 2xx) that should not be treated as errors.
func (client *Client) doRequest(ctx context.Context, method, path string, requestBody any, responseTarget any, acceptableStatuses ...int) error {
	var bodyReader io.Reader
	if requestBody != nil {
		encoded, err := json.Marshal(requestBody)
		if err != nil {
			return fmt.Errorf("encoding request body: %w", err)
		}
		bodyReader = bytes.NewReader(encoded)
	}

	url := client.BaseURL + path
	request, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
	if err != nil {
		return fmt.Errorf("building request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+client.Token)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	if client.UserAgent != "" {
		request.Header.Set("User-Agent", client.UserAgent)
	}

	tflog.Debug(ctx, "ankra API request", map[string]any{"method": method, "path": path})

	response, err := client.HTTPClient.Do(request)
	if err != nil {
		return fmt.Errorf("performing request: %w", err)
	}
	defer response.Body.Close()

	responseBytes, err := io.ReadAll(response.Body)
	if err != nil {
		return fmt.Errorf("reading response body: %w", err)
	}

	tflog.Debug(ctx, "ankra API response", map[string]any{"method": method, "path": path, "status": response.StatusCode})

	if !isAcceptableStatus(response.StatusCode, acceptableStatuses) {
		return &APIError{Method: method, Path: path, StatusCode: response.StatusCode, Body: string(responseBytes)}
	}

	if responseTarget != nil && len(responseBytes) > 0 {
		if err := json.Unmarshal(responseBytes, responseTarget); err != nil {
			return fmt.Errorf("decoding response body: %w", err)
		}
	}
	return nil
}

func isAcceptableStatus(statusCode int, extra []int) bool {
	if statusCode >= 200 && statusCode < 300 {
		return true
	}
	for _, candidate := range extra {
		if statusCode == candidate {
			return true
		}
	}
	return false
}
