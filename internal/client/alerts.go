// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
)

const (
	alertDestinationsPath = "/api/v1/org/alerts/integrations"
	alertRoutesPath       = "/api/v1/org/notifications/routes"

	// alertDestinationsPageSize is the largest page the destinations
	// listing serves.
	alertDestinationsPageSize = 100

	// AlertDestinationMaskedURL is what create and update answer in place of
	// the webhook URL. The get and list reads return the stored URL.
	AlertDestinationMaskedURL = "https://***"
)

// AlertDestination is one alert destination (an "integration" on the wire):
// a webhook URL, or a Slack or Teams channel the Ankra bot posts to.
type AlertDestination struct {
	ID              string  `json:"id"`
	Name            string  `json:"name"`
	URL             *string `json:"url"`
	ChannelID       *string `json:"channel_id"`
	ChannelName     *string `json:"channel_name"`
	IntegrationType string  `json:"integration_type"`
	TeamsTenantID   *string `json:"teams_tenant_id"`
	Description     *string `json:"description"`
	Template        *string `json:"template"`
	Enabled         bool    `json:"enabled"`
}

// AlertDestinationCreate is the create body. The platform defaults
// integration_type to "slack" and enabled to true when they are omitted.
type AlertDestinationCreate struct {
	Name            string  `json:"name"`
	URL             *string `json:"url,omitempty"`
	ChannelID       *string `json:"channel_id,omitempty"`
	ChannelName     *string `json:"channel_name,omitempty"`
	TeamsTenantID   *string `json:"teams_tenant_id,omitempty"`
	IntegrationType *string `json:"integration_type,omitempty"`
	Description     *string `json:"description,omitempty"`
	Template        *string `json:"template,omitempty"`
	Enabled         *bool   `json:"enabled,omitempty"`
}

// AlertDestinationUpdate is the update body. The platform keeps any member
// that is absent or null, so a value is cleared by sending an empty string:
// an empty channel_id switches the destination back to URL delivery, an
// empty teams_tenant_id, description or template clears it.
type AlertDestinationUpdate struct {
	Name          *string `json:"name,omitempty"`
	URL           *string `json:"url,omitempty"`
	ChannelID     *string `json:"channel_id,omitempty"`
	ChannelName   *string `json:"channel_name,omitempty"`
	TeamsTenantID *string `json:"teams_tenant_id,omitempty"`
	Description   *string `json:"description,omitempty"`
	Template      *string `json:"template,omitempty"`
	Enabled       *bool   `json:"enabled,omitempty"`
}

type alertDestinationEnvelope struct {
	Item AlertDestination `json:"item"`
}

type alertDestinationPage struct {
	Items      []AlertDestination `json:"items"`
	Pagination struct {
		Page       int `json:"page"`
		TotalPages int `json:"total_pages"`
	} `json:"pagination"`
}

func alertDestinationPath(destinationID string) string {
	return alertDestinationsPath + "/" + url.PathEscape(destinationID)
}

// ListAlertDestinations walks every page of the destinations listing.
func (client *Client) ListAlertDestinations(ctx context.Context) ([]AlertDestination, error) {
	destinations := []AlertDestination{}
	for page := 1; ; page++ {
		query := url.Values{}
		query.Set("page", strconv.Itoa(page))
		query.Set("page_size", strconv.Itoa(alertDestinationsPageSize))
		var listing alertDestinationPage
		if err := client.doRequest(ctx, http.MethodGet, alertDestinationsPath+"?"+query.Encode(), nil, &listing); err != nil {
			return nil, err
		}
		destinations = append(destinations, listing.Items...)
		if len(listing.Items) == 0 || page >= listing.Pagination.TotalPages {
			return destinations, nil
		}
	}
}

// GetAlertDestination reads one destination. A destination that is gone is a
// NotFound APIError.
func (client *Client) GetAlertDestination(ctx context.Context, destinationID string) (*AlertDestination, error) {
	var envelope alertDestinationEnvelope
	if err := client.doRequest(ctx, http.MethodGet, alertDestinationPath(destinationID), nil, &envelope); err != nil {
		return nil, err
	}
	return &envelope.Item, nil
}

// CreateAlertDestination creates a destination and returns its id. The
// create answer masks the URL, so callers read the destination back for
// anything else.
func (client *Client) CreateAlertDestination(ctx context.Context, request AlertDestinationCreate) (string, error) {
	var envelope alertDestinationEnvelope
	if err := client.doRequest(ctx, http.MethodPost, alertDestinationsPath, request, &envelope); err != nil {
		return "", err
	}
	return envelope.Item.ID, nil
}

// UpdateAlertDestination changes the members set on request.
func (client *Client) UpdateAlertDestination(ctx context.Context, destinationID string, request AlertDestinationUpdate) error {
	return client.doRequest(ctx, http.MethodPut, alertDestinationPath(destinationID), request, nil)
}

// DeleteAlertDestination deletes a destination. One already gone is not an
// error.
func (client *Client) DeleteAlertDestination(ctx context.Context, destinationID string) error {
	return client.doRequest(ctx, http.MethodDelete, alertDestinationPath(destinationID), nil, nil, http.StatusNotFound)
}

// AlertRoute is one notification routing rule. A notification matching every
// non-null filter is delivered to (mode "include") or withheld from (mode
// "exclude") the destination; routes are walked in ascending priority and
// stop_on_match ends the walk. Kind is the legacy single-kind spelling the
// platform derives from Kinds; KindsNegated turns Kinds into "every kind
// except these".
type AlertRoute struct {
	ID               string   `json:"id"`
	Kind             *string  `json:"kind"`
	Kinds            []string `json:"kinds"`
	KindsNegated     bool     `json:"kinds_negated"`
	Severity         *string  `json:"severity"`
	ClusterID        *string  `json:"cluster_id"`
	SourceID         *string  `json:"source_id"`
	ApplicationID    *string  `json:"application_id"`
	ReferencePattern *string  `json:"reference_pattern"`
	DestinationID    string   `json:"destination_id"`
	DestinationType  string   `json:"destination_type"`
	Priority         int64    `json:"priority"`
	StopOnMatch      bool     `json:"stop_on_match"`
	Mode             string   `json:"mode"`
	Enabled          bool     `json:"enabled"`
}

// AlertRouteWrite is both the create and the update body. Every member is
// sent: on update the platform clears a filter sent as null and an empty
// kinds list is refused, so a nil Kinds is sent as null ("any kind"). The
// legacy kind member is never sent; Kinds supersedes it.
type AlertRouteWrite struct {
	DestinationID    string   `json:"destination_id"`
	Kinds            []string `json:"kinds"`
	KindsNegated     bool     `json:"kinds_negated"`
	Severity         *string  `json:"severity"`
	ClusterID        *string  `json:"cluster_id"`
	SourceID         *string  `json:"source_id"`
	ApplicationID    *string  `json:"application_id"`
	ReferencePattern *string  `json:"reference_pattern"`
	Priority         int64    `json:"priority"`
	StopOnMatch      bool     `json:"stop_on_match"`
	Mode             string   `json:"mode"`
	Enabled          bool     `json:"enabled"`
}

type alertRouteListing struct {
	Items []AlertRoute `json:"items"`
}

// ListAlertRoutes returns every routing rule, in evaluation order.
func (client *Client) ListAlertRoutes(ctx context.Context) ([]AlertRoute, error) {
	var listing alertRouteListing
	if err := client.doRequest(ctx, http.MethodGet, alertRoutesPath, nil, &listing); err != nil {
		return nil, err
	}
	return listing.Items, nil
}

// GetAlertRoute finds one route in the listing, which is the only read the
// platform offers. A missing route is a NotFound APIError.
func (client *Client) GetAlertRoute(ctx context.Context, routeID string) (*AlertRoute, error) {
	routes, err := client.ListAlertRoutes(ctx)
	if err != nil {
		return nil, err
	}
	for index := range routes {
		if routes[index].ID == routeID {
			return &routes[index], nil
		}
	}
	return nil, &APIError{Method: http.MethodGet, Path: alertRoutesPath, StatusCode: http.StatusNotFound, Body: "route " + routeID + " not found"}
}

// AlertRouteProject is the project a route create stamps. With IsSet false
// project_id is left out and the platform stamps the request's project; with
// IsSet true it is sent, and a nil ProjectID makes the route
// organisation-wide.
type AlertRouteProject struct {
	IsSet     bool
	ProjectID *string
}

// alertRouteCreate is the create body: the write body plus project_id when
// the caller chose one.
type alertRouteCreate struct {
	AlertRouteWrite
	project AlertRouteProject
}

func (request alertRouteCreate) MarshalJSON() ([]byte, error) {
	encoded, err := json.Marshal(request.AlertRouteWrite)
	if err != nil || !request.project.IsSet {
		return encoded, err
	}
	var members map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &members); err != nil {
		return nil, err
	}
	projectMember, err := json.Marshal(request.project.ProjectID)
	if err != nil {
		return nil, err
	}
	members["project_id"] = projectMember
	return json.Marshal(members)
}

// CreateAlertRoute creates a routing rule in the project project names. The
// platform has no way to move a route to another project afterwards.
func (client *Client) CreateAlertRoute(ctx context.Context, request AlertRouteWrite, project AlertRouteProject) (*AlertRoute, error) {
	var route AlertRoute
	if err := client.doRequest(ctx, http.MethodPost, alertRoutesPath, alertRouteCreate{AlertRouteWrite: request, project: project}, &route); err != nil {
		return nil, err
	}
	return &route, nil
}

// UpdateAlertRoute replaces a routing rule's filters and delivery settings.
func (client *Client) UpdateAlertRoute(ctx context.Context, routeID string, request AlertRouteWrite) (*AlertRoute, error) {
	var route AlertRoute
	if err := client.doRequest(ctx, http.MethodPatch, alertRoutesPath+"/"+url.PathEscape(routeID), request, &route); err != nil {
		return nil, err
	}
	return &route, nil
}

// DeleteAlertRoute deletes a routing rule. Routes are project-scoped, so a
// 404 is returned for the caller to tell "gone" from "out of scope".
func (client *Client) DeleteAlertRoute(ctx context.Context, routeID string) error {
	return client.doRequest(ctx, http.MethodDelete, alertRoutesPath+"/"+url.PathEscape(routeID), nil, nil)
}
