// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package client

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestDeleteOrganisationVariableReportsNotFound pins that a 404 reaches the
// caller as NotFound (which decides, knowing the scope, whether it means
// deleted), while a refusal stays a different error.
func TestDeleteOrganisationVariableReportsNotFound(t *testing.T) {
	statuses := map[string]int{"gone": http.StatusNotFound, "refused": http.StatusForbidden}
	mux := http.NewServeMux()
	mux.HandleFunc("DELETE /api/v1/org/variables/{name}", func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(statuses[request.PathValue("name")])
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	apiClient := NewClient(server.URL, "test-token", "test")
	if err := apiClient.DeleteOrganisationVariable(context.Background(), "gone"); !IsNotFound(err) {
		t.Errorf("deleting a variable already gone returned %v, want NotFound", err)
	}
	if err := apiClient.DeleteOrganisationVariable(context.Background(), "refused"); err == nil || IsNotFound(err) {
		t.Errorf("a refused delete returned %v, want a non-NotFound error", err)
	}
}

func TestScopeHeadersAreSent(t *testing.T) {
	var organisation, project string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		organisation = request.Header.Get(OrganisationHeader)
		project = request.Header.Get(ProjectHeader)
		_, _ = writer.Write([]byte(`{"variables":[]}`))
	}))
	defer server.Close()

	apiClient := NewClient(server.URL, "test-token", "test")
	apiClient.OrganisationID = "org-1"
	apiClient.ProjectID = "project-1"
	if _, err := apiClient.ListOrganisationVariables(context.Background()); err != nil {
		t.Fatalf("listing failed: %v", err)
	}
	if organisation != "org-1" || project != "project-1" {
		t.Errorf("sent organisation %q and project %q", organisation, project)
	}
}
