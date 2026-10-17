// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// routeFake stands in for the platform API in resource tests. Register
// handlers with ServeMux patterns ("POST /api/v1/things", "GET
// /api/v1/things/{id}"); a request no route matches fails the test, so a
// provider calling an endpoint the test did not expect cannot pass silently.
type routeFake struct {
	t      *testing.T
	server *httptest.Server
	mux    *http.ServeMux
	mutex  sync.Mutex
	// serial runs every handler one at a time, across all routes, so handlers
	// may share state without locking even when Terraform applies resources
	// in parallel.
	serial sync.Mutex
	// projectsRegistered records that a test registered its own
	// GET /api/v1/org/projects, replacing the default.
	projectsRegistered bool
	// requests records "METHOD path" for every request served, in order.
	requests []string
	// organisationHeaders records the organisation override each request carried.
	organisationHeaders []string
}

func newRouteFake(t *testing.T) *routeFake {
	t.Helper()
	fake := &routeFake{t: t, mux: http.NewServeMux()}
	fake.mux.HandleFunc("/", func(writer http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodGet && request.URL.Path == "/api/v1/org/projects" && !fake.projectsRegistered {
			// The default: an organisation without projects, which is what the
			// platform answers (404) when the projects flag is off.
			writeJSON(t, writer, http.StatusNotFound, map[string]any{"detail": "Projects are not enabled for this organisation"})
			return
		}
		t.Errorf("unexpected request: %s %s", request.Method, request.URL.String())
		writer.WriteHeader(http.StatusNotFound)
	})
	fake.server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		fake.mutex.Lock()
		fake.requests = append(fake.requests, request.Method+" "+request.URL.Path)
		fake.organisationHeaders = append(fake.organisationHeaders, request.Header.Get("X-Ankra-Organisation-Id"))
		fake.mutex.Unlock()
		writer.Header().Set("Content-Type", "application/json")
		fake.mux.ServeHTTP(writer, request)
	}))
	t.Cleanup(fake.server.Close)
	return fake
}

// handle registers a handler for a ServeMux pattern. All handlers of a fake
// run one at a time, across routes, so they may share state without locking.
func (fake *routeFake) handle(pattern string, handler http.HandlerFunc) {
	if pattern == "GET /api/v1/org/projects" {
		fake.projectsRegistered = true
	}
	fake.mux.HandleFunc(pattern, func(writer http.ResponseWriter, request *http.Request) {
		fake.serial.Lock()
		defer fake.serial.Unlock()
		handler(writer, request)
	})
}

// providerBlock is a provider configuration pointing at the fake.
func (fake *routeFake) providerBlock() string {
	return fmt.Sprintf(`
provider "ankra" {
  token    = "test-token"
  base_url = %q
}
`, fake.server.URL)
}

// served returns a copy of the requests served so far.
func (fake *routeFake) served() []string {
	fake.mutex.Lock()
	defer fake.mutex.Unlock()
	return append([]string(nil), fake.requests...)
}

// writeJSON encodes body as the response.
func writeJSON(t *testing.T, writer http.ResponseWriter, status int, body any) {
	t.Helper()
	writer.WriteHeader(status)
	if err := json.NewEncoder(writer).Encode(body); err != nil {
		t.Errorf("encoding fake response: %v", err)
	}
}

// decodeJSON decodes the request body into target, failing the test on error.
func decodeJSON(t *testing.T, request *http.Request, target any) {
	t.Helper()
	body, err := io.ReadAll(request.Body)
	if err != nil {
		t.Errorf("reading fake request body: %v", err)
		return
	}
	if err := json.Unmarshal(body, target); err != nil {
		t.Errorf("decoding fake request body %q: %v", body, err)
	}
}
