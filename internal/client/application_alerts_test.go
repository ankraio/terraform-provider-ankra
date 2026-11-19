// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package client

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

const (
	applicationAlertTestApplicationID = "5b0c4a9e-2f43-4b8e-9a51-1f0d8e7c2a10"
	applicationAlertTestAlertID       = "4c2a9e1f-2222-4333-8444-955556666777"
)

// TestGetApplicationAlertReportsGoneOnlyWhenThePlatformSaysSo pins the three
// outcomes of reading one alert out of the application's list: found, gone
// (a NotFound the provider may drop from state), and unreadable (any other
// error, which must never be taken for gone).
func TestGetApplicationAlertReportsGoneOnlyWhenThePlatformSaysSo(t *testing.T) {
	ownAlert := `{"alert_id":"` + applicationAlertTestAlertID + `","rule_id":"r1","name":"Failed requests","own":true,` +
		`"environment_id":"e1","condition":{"condition_type":"app_metric","metric":"p95_ms","operator":"gt","threshold":800,"hold_minutes":5},` +
		`"severity":"warning","send_to":[{"integration_id":"d1","type":"slack","label":"ops"}],"enabled":true}`
	organisationRule := `{"alert_id":"` + applicationAlertTestAlertID + `","rule_id":"r1","name":"Cluster down","own":false,` +
		`"environment_id":null,"condition":null,"severity":"critical","send_to":[],"enabled":true}`
	otherAlert := `{"alert_id":"9d2f6a3b-7c1e-4f0a-b5d8-2e4c6a8b0f13","rule_id":"r2","name":"Restarts","own":true,` +
		`"environment_id":"e1","condition":{"condition_type":"app_metric","metric":"restarts","operator":"gt","threshold":3,"hold_minutes":1},` +
		`"severity":"warning","send_to":[],"enabled":true}`
	cases := []struct {
		name       string
		status     int
		body       string
		isFound    bool
		isNotFound bool
	}{
		{name: "listed", status: http.StatusOK, body: `{"alerts":[` + ownAlert + `],"truncated":false,"limit":100}`, isFound: true},
		{name: "a complete list without it", status: http.StatusOK, body: `{"alerts":[` + otherAlert + `],"truncated":false,"limit":100}`, isNotFound: true},
		{name: "application not found", status: http.StatusNotFound, body: `{"detail":"Application not found"}`, isNotFound: true},
		{name: "a list cut short without it", status: http.StatusOK, body: `{"alerts":[` + otherAlert + `],"truncated":true,"limit":100}`},
		{name: "an organisation rule with its id", status: http.StatusOK, body: `{"alerts":[` + organisationRule + `],"truncated":false,"limit":100}`},
		{name: "permission denied", status: http.StatusForbidden, body: `{"detail":"permission_denied: alerts.read"}`},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				if request.URL.Path != "/api/v1/org/applications/"+applicationAlertTestApplicationID+"/alerts" {
					writer.WriteHeader(http.StatusNotFound)
					_, _ = writer.Write([]byte(`{"detail":"Projects are not enabled for this organisation"}`))
					return
				}
				writer.WriteHeader(testCase.status)
				_, _ = writer.Write([]byte(testCase.body))
			}))
			defer server.Close()
			apiClient := NewClient(server.URL, "token", "test-agent")
			apiClient.MaxRetries = 1

			alert, readError := apiClient.GetApplicationAlert(context.Background(), applicationAlertTestApplicationID, applicationAlertTestAlertID)
			switch {
			case testCase.isFound:
				if readError != nil || alert == nil || alert.Condition == nil || alert.Condition.Metric != "p95_ms" || alert.SendTo[0].IntegrationID != "d1" {
					t.Fatalf("want the listed alert, got %+v, %v", alert, readError)
				}
			case testCase.isNotFound:
				if !IsNotFound(readError) {
					t.Fatalf("want a NotFound error, got %v", readError)
				}
			default:
				if readError == nil || IsNotFound(readError) {
					t.Fatalf("want an error that is not NotFound, got %v", readError)
				}
			}
		})
	}
}

// TestApplicationAlertWriteLeavesAnUnsetSeverityOut pins the body an update
// sends without a severity: the key is absent, so the platform keeps the
// stored one, rather than present and empty.
func TestApplicationAlertWriteLeavesAnUnsetSeverityOut(t *testing.T) {
	var received string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPut {
			writer.WriteHeader(http.StatusNotFound)
			_, _ = writer.Write([]byte(`{"detail":"Projects are not enabled for this organisation"}`))
			return
		}
		body, _ := io.ReadAll(request.Body)
		received = string(body)
		writer.WriteHeader(http.StatusOK)
		_, _ = writer.Write([]byte(`{"alert_id":"` + applicationAlertTestAlertID + `","own":true,"severity":"critical"}`))
	}))
	defer server.Close()
	apiClient := NewClient(server.URL, "token", "test-agent")

	_, updateError := apiClient.UpdateApplicationAlert(context.Background(), applicationAlertTestApplicationID, applicationAlertTestAlertID, ApplicationAlertWrite{
		Name:          "Slow responses",
		EnvironmentID: "e1",
		Condition:     ApplicationAlertCondition{ConditionType: "app_metric", Metric: "p95_ms", Operator: "gt", Threshold: 0, HoldMinutes: 5},
		SendTo:        []string{"d1"},
	})
	if updateError != nil {
		t.Fatalf("update: %v", updateError)
	}
	want := `{"name":"Slow responses","environment_id":"e1","condition":{"condition_type":"app_metric","metric":"p95_ms","operator":"gt","threshold":0,"hold_minutes":5},"send_to":["d1"]}`
	if received != want {
		t.Fatalf("body\n got %s\nwant %s", received, want)
	}
}
