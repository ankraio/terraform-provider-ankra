// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package client

import (
	"context"
	"net/http"
	"net/url"
)

const cloudDNSZonesPath = "/api/v1/org/cloud-dns/zones"

// CloudDNSZone is a hosted zone in the organisation's Ankra Cloud account.
type CloudDNSZone struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	ParentZoneID string `json:"parent_zone_id"`
}

// CloudDNSZoneAccess is the access gateway policy of a Cloud DNS zone: who
// may reach the hostnames the Ankra Cloud proxy serves in it.
type CloudDNSZoneAccess struct {
	// Mode is "public" (anyone) or "organisation" (signed-in members only).
	Mode           string   `json:"mode"`
	Roles          []string `json:"roles"`
	OrganisationID string   `json:"organisation_id,omitempty"`
	ProjectID      string   `json:"project_id,omitempty"`
	// InheritedFrom names the zone the policy is inherited from, when the
	// zone has no policy of its own.
	InheritedFrom string `json:"inherited_from,omitempty"`
}

// CloudDNSZoneAccessWrite is the body of a zone access write. ProjectID nil
// keeps the zone's project limit, "" removes it.
type CloudDNSZoneAccessWrite struct {
	Mode      string   `json:"mode"`
	Roles     []string `json:"roles"`
	ProjectID *string  `json:"project_id,omitempty"`
}

type cloudDNSZoneListEnvelope struct {
	Items []CloudDNSZone `json:"items"`
}

type cloudDNSZoneAccessEnvelope struct {
	Access CloudDNSZoneAccess `json:"access"`
}

func cloudDNSZoneAccessPath(zoneID string) string {
	return cloudDNSZonesPath + "/" + url.PathEscape(zoneID) + "/access"
}

// ListCloudDNSZones lists the zones in the organisation's Ankra Cloud
// account.
func (client *Client) ListCloudDNSZones(ctx context.Context) ([]CloudDNSZone, error) {
	var envelope cloudDNSZoneListEnvelope
	if requestError := client.doRequest(ctx, http.MethodGet, cloudDNSZonesPath, nil, &envelope); requestError != nil {
		return nil, requestError
	}
	return envelope.Items, nil
}

// GetCloudDNSZoneAccess reads the effective access policy of a zone. A zone
// outside the organisation is a NotFound APIError.
func (client *Client) GetCloudDNSZoneAccess(ctx context.Context, zoneID string) (*CloudDNSZoneAccess, error) {
	var envelope cloudDNSZoneAccessEnvelope
	if requestError := client.doRequest(ctx, http.MethodGet, cloudDNSZoneAccessPath(zoneID), nil, &envelope); requestError != nil {
		return nil, requestError
	}
	return &envelope.Access, nil
}

// SetCloudDNSZoneAccess sets a zone's own access policy. Ankra Cloud binds
// the organisation from the request.
func (client *Client) SetCloudDNSZoneAccess(ctx context.Context, zoneID string, request CloudDNSZoneAccessWrite) (*CloudDNSZoneAccess, error) {
	if request.Roles == nil {
		request.Roles = []string{}
	}
	var envelope cloudDNSZoneAccessEnvelope
	if requestError := client.doRequest(ctx, http.MethodPut, cloudDNSZoneAccessPath(zoneID), request, &envelope); requestError != nil {
		return nil, requestError
	}
	return &envelope.Access, nil
}
