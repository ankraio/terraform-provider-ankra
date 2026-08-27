// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// Package client is a small typed HTTP client for the Ankra platform API used
// by the Terraform provider.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-log/tflog"
)

// DefaultBaseURL is the public Ankra platform API endpoint.
const DefaultBaseURL = "https://platform.ankra.app"

// DefaultTimeout bounds a single HTTP attempt.
const DefaultTimeout = 60 * time.Second

// DefaultMaxRetries is how many times a retryable attempt is repeated before
// the error is surfaced to Terraform.
const DefaultMaxRetries = 4

// baseRetryDelay is the first backoff step; each further attempt doubles it.
const baseRetryDelay = 500 * time.Millisecond

// maxRetryDelay caps the backoff so a long Retry-After cannot stall an apply
// indefinitely.
const maxRetryDelay = 30 * time.Second

// maxResponseBodySize bounds how much of a response is buffered, so a
// misbehaving endpoint cannot exhaust memory inside a Terraform run.
const maxResponseBodySize = 32 << 20

// Client talks to the Ankra platform REST API.
type Client struct {
	BaseURL    string
	Token      string
	UserAgent  string
	MaxRetries int
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
		MaxRetries: DefaultMaxRetries,
		HTTPClient: &http.Client{Timeout: DefaultTimeout},
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

// IsNotFound reports whether the error is a 404 from the platform API.
func IsNotFound(err error) bool {
	var apiError *APIError
	if errors.As(err, &apiError) {
		return apiError.StatusCode == http.StatusNotFound
	}
	return false
}

// doRequest issues a request against the platform API and decodes a 2xx JSON
// body into responseTarget when it is non-nil. acceptableStatuses lists extra
// status codes (beyond 2xx) that should not be treated as errors. Retryable
// failures are repeated with exponential backoff.
func (client *Client) doRequest(ctx context.Context, method, path string, requestBody any, responseTarget any, acceptableStatuses ...int) error {
	var encodedBody []byte
	if requestBody != nil {
		encoded, err := json.Marshal(requestBody)
		if err != nil {
			return fmt.Errorf("encoding request body: %w", err)
		}
		encodedBody = encoded
	}

	attempts := client.MaxRetries
	if attempts < 1 {
		attempts = 1
	}

	var lastError error
	for attempt := 1; attempt <= attempts; attempt++ {
		responseBytes, statusCode, err := client.attempt(ctx, method, path, encodedBody)
		if err == nil && isAcceptableStatus(statusCode, acceptableStatuses) {
			if responseTarget != nil && len(responseBytes) > 0 {
				if unmarshalError := json.Unmarshal(responseBytes, responseTarget); unmarshalError != nil {
					return fmt.Errorf("decoding response body: %w", unmarshalError)
				}
			}
			return nil
		}

		if err != nil {
			lastError = err
		} else {
			lastError = &APIError{Method: method, Path: path, StatusCode: statusCode, Body: string(responseBytes)}
		}

		if attempt == attempts || !isRetryable(method, statusCode, err) {
			return lastError
		}

		delay := retryDelay(attempt, responseBytes, statusCode)
		tflog.Debug(ctx, "ankra API retrying", map[string]any{
			"method": method, "path": path, "status": statusCode,
			"attempt": attempt, "delay_ms": delay.Milliseconds(),
		})
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
	}
	return lastError
}

// attempt performs one HTTP round trip and returns the buffered body and
// status. A non-nil error means the request never produced a response.
func (client *Client) attempt(ctx context.Context, method, path string, encodedBody []byte) ([]byte, int, error) {
	var bodyReader io.Reader
	if encodedBody != nil {
		bodyReader = bytes.NewReader(encodedBody)
	}

	url := client.BaseURL + path
	request, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
	if err != nil {
		return nil, 0, fmt.Errorf("building request: %w", err)
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
		return nil, 0, fmt.Errorf("performing request: %w", err)
	}
	defer response.Body.Close()

	responseBytes, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBodySize))
	if err != nil {
		return nil, response.StatusCode, fmt.Errorf("reading response body: %w", err)
	}

	tflog.Debug(ctx, "ankra API response", map[string]any{"method": method, "path": path, "status": response.StatusCode})
	return responseBytes, response.StatusCode, nil
}

// isRetryable reports whether an attempt is worth repeating.
//
// Only idempotent methods are retried on server-side and transport failures: a
// POST that provisions a cluster may well have been executed before the
// failure surfaced, and repeating it would create a second cluster. A 429 is
// safe for any method, because it means the request was rejected unprocessed.
func isRetryable(method string, statusCode int, transportError error) bool {
	if statusCode == http.StatusTooManyRequests {
		return true
	}
	if !isIdempotentMethod(method) {
		return false
	}
	if transportError != nil {
		return true
	}
	switch statusCode {
	case http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	}
	return false
}

func isIdempotentMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodPut, http.MethodDelete:
		return true
	}
	return false
}

// retryDelay honours a Retry-After hint carried in the response body when the
// platform sends one, and otherwise backs off exponentially.
func retryDelay(attempt int, responseBytes []byte, statusCode int) time.Duration {
	if statusCode == http.StatusTooManyRequests {
		if hinted := retryAfterFromBody(responseBytes); hinted > 0 {
			return capDelay(hinted)
		}
	}
	delay := baseRetryDelay * time.Duration(1<<(attempt-1))
	return capDelay(delay)
}

// retryAfterFromBody reads the retry_after field the platform includes in its
// rate-limit envelope.
func retryAfterFromBody(responseBytes []byte) time.Duration {
	var envelope struct {
		RetryAfter json.Number `json:"retry_after"`
	}
	if err := json.Unmarshal(responseBytes, &envelope); err != nil {
		return 0
	}
	seconds, err := strconv.ParseFloat(envelope.RetryAfter.String(), 64)
	if err != nil || seconds <= 0 {
		return 0
	}
	return time.Duration(seconds * float64(time.Second))
}

func capDelay(delay time.Duration) time.Duration {
	if delay > maxRetryDelay {
		return maxRetryDelay
	}
	if delay < 0 {
		return 0
	}
	return delay
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
