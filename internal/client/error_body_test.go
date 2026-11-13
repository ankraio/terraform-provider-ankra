// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package client

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestValidationErrorDropsTheRequestEcho(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = writer.Write([]byte(`{"detail":[{"type":"string_too_short","loc":["body","password"],"msg":"too short",` +
			`"input":"hunter2-secret","ctx":{"min_length":12}}]}`))
	}))
	defer server.Close()
	requestError := NewClient(server.URL, "token", "test").doRequest(context.Background(), http.MethodPost, "/api/v1/credentials/datasource", map[string]string{}, nil)
	var apiError *APIError
	if !errors.As(requestError, &apiError) {
		t.Fatalf("expected an APIError, got %v", requestError)
	}
	if strings.Contains(apiError.Body, "hunter2-secret") || strings.Contains(apiError.Body, "min_length") {
		t.Errorf("the error body must not echo the request: %s", apiError.Body)
	}
	if !strings.Contains(apiError.Body, "too short") || !strings.Contains(apiError.Body, "password") {
		t.Errorf("the error body must keep the message and location: %s", apiError.Body)
	}
}

func TestErrorBodyWithoutDetailListIsKept(t *testing.T) {
	for _, body := range []string{`{"detail":"Cluster not found"}`, `not json`, `{"detail":[1,2]}`} {
		if redacted := redactErrorBody(http.StatusNotFound, []byte(body)); redacted != body {
			t.Errorf("redactErrorBody(%q) = %q, want it unchanged", body, redacted)
		}
	}
	if redacted := redactErrorBody(http.StatusInternalServerError, []byte(`{"detail":[{"input":"x"}]}`)); !strings.Contains(redacted, "input") {
		t.Errorf("a 5xx body is kept as it came, got %q", redacted)
	}
}
