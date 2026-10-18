// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package client

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestListServicePackagesFollowsCursor(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Query().Get("after") == "" {
			_, _ = writer.Write([]byte(`{"items":[{"id":"p-1"}],"next_cursor":"p-1"}`))
			return
		}
		_, _ = writer.Write([]byte(`{"items":[{"id":"p-2"}],"next_cursor":null}`))
	}))
	defer server.Close()

	packages, err := NewClient(server.URL, "token", "test").ListServicePackages(context.Background())
	if err != nil {
		t.Fatalf("ListServicePackages: %v", err)
	}
	if len(packages) != 2 || packages[1].ID != "p-2" {
		t.Errorf("packages = %v, want both pages", packages)
	}
}

// A listing that never reaches its end fails instead of silently returning
// a truncated list.
func TestListServicePackagesFailsAtThePageCap(t *testing.T) {
	served := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		served++
		_, _ = fmt.Fprintf(writer, `{"items":[{"id":"p-%d"}],"next_cursor":"p-%d"}`, served, served)
	}))
	defer server.Close()

	packages, err := NewClient(server.URL, "token", "test").ListServicePackages(context.Background())
	if err == nil || !strings.Contains(err.Error(), "stopped after") {
		t.Fatalf("err = %v, packages = %d; want the page cap reported", err, len(packages))
	}
	if served != maxServicePages {
		t.Errorf("served %d pages, want %d", served, maxServicePages)
	}
}
