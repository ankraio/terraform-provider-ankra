// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package client

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClusterVariableNotFoundIsToldApartFromClusterNotFound(t *testing.T) {
	cases := []struct {
		detail          string
		isDeleteSuccess bool
	}{
		{detail: `{"detail":"Variable not found"}`, isDeleteSuccess: true},
		{detail: `{"detail":"Cluster not found"}`, isDeleteSuccess: false},
	}
	for _, testCase := range cases {
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			writer.WriteHeader(http.StatusNotFound)
			_, _ = writer.Write([]byte(testCase.detail))
		}))
		apiClient := NewClient(server.URL, "token", "test-agent")

		deleteError := apiClient.DeleteClusterVariable(context.Background(), "cluster-1", "domain")
		if testCase.isDeleteSuccess && deleteError != nil {
			t.Errorf("%s: delete of a variable already gone failed: %v", testCase.detail, deleteError)
		}
		if !testCase.isDeleteSuccess && !IsClusterNotVisible(deleteError) {
			t.Errorf("%s: delete reported %v, want a cluster-not-visible error rather than success", testCase.detail, deleteError)
		}

		_, listError := apiClient.ListClusterVariables(context.Background(), "cluster-1")
		if IsNotFound(listError) == !testCase.isDeleteSuccess {
			t.Errorf("%s: listing error %v must be NotFound only for a missing variable route", testCase.detail, listError)
		}
		if IsClusterNotVisible(listError) == testCase.isDeleteSuccess {
			t.Errorf("%s: listing error %v misclassified", testCase.detail, listError)
		}
		server.Close()
	}
}
