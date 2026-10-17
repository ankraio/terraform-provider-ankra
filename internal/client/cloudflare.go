// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package client

import (
	"context"
	"net/http"
	"net/url"
)

const (
	cloudflareCredentialsPath = "/api/v1/org/cloudflare/credentials"
	cloudflareDomainsPath     = "/api/v1/org/cloudflare/domains"

	// CloudflareTTLAutomatic is the TTL Cloudflare reports for "automatic",
	// which is also what a proxied record always carries.
	CloudflareTTLAutomatic = 1
)

// CloudflareCredential is a connected Cloudflare API token as the platform
// lists it. The token itself is never returned.
type CloudflareCredential struct {
	Name           string `json:"name"`
	AccountID      string `json:"account_id"`
	AccountName    string `json:"account_name"`
	TokenID        string `json:"token_id"`
	TokenKind      string `json:"token_kind"`
	TokenExpiresAt string `json:"token_expires_at"`
	State          string `json:"state"`
	NeedsReconnect bool   `json:"needs_reconnect"`
}

// CloudflareCredentialConnect is the connect body. AccountID is only needed
// for an account-owned token that reaches several accounts.
type CloudflareCredentialConnect struct {
	Name      string `json:"name"`
	APIToken  string `json:"api_token"`
	AccountID string `json:"account_id,omitempty"`
}

type cloudflareCredentialListing struct {
	Items []CloudflareCredential `json:"items"`
}

// ListCloudflareCredentials returns the organisation's Cloudflare credentials.
func (client *Client) ListCloudflareCredentials(ctx context.Context) ([]CloudflareCredential, error) {
	var listing cloudflareCredentialListing
	if err := client.doRequest(ctx, http.MethodGet, cloudflareCredentialsPath, nil, &listing); err != nil {
		return nil, err
	}
	return listing.Items, nil
}

// GetCloudflareCredential finds one credential by name in the listing. A
// missing name is a NotFound APIError.
func (client *Client) GetCloudflareCredential(ctx context.Context, name string) (*CloudflareCredential, error) {
	credentials, err := client.ListCloudflareCredentials(ctx)
	if err != nil {
		return nil, err
	}
	for index := range credentials {
		if credentials[index].Name == name {
			return &credentials[index], nil
		}
	}
	return nil, &APIError{Method: http.MethodGet, Path: cloudflareCredentialsPath, StatusCode: http.StatusNotFound, Body: "Cloudflare credential " + name + " not found"}
}

// ConnectCloudflareCredential verifies and stores a Cloudflare API token and
// returns the credential's id.
func (client *Client) ConnectCloudflareCredential(ctx context.Context, request CloudflareCredentialConnect) (string, error) {
	var created dnsCredentialCreated
	if err := client.doRequest(ctx, http.MethodPost, cloudflareCredentialsPath, request, &created); err != nil {
		return "", err
	}
	return created.ID, nil
}

// CloudflareDomain is one Cloudflare zone a connected credential reaches.
type CloudflareDomain struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	Status         string   `json:"status"`
	Paused         bool     `json:"paused"`
	Type           string   `json:"type"`
	CredentialName string   `json:"credential_name"`
	AccountID      string   `json:"account_id"`
	AccountName    string   `json:"account_name"`
	NameServers    []string `json:"name_servers"`
}

// CloudflareDomainListing is the domains listing. IsTruncated reports that
// the platform stopped walking Cloudflare's pages at its bound.
type CloudflareDomainListing struct {
	Items       []CloudflareDomain `json:"items"`
	IsTruncated bool               `json:"is_truncated"`
}

// ListCloudflareDomains lists the zones one credential (or, with an empty
// name, the organisation's only credential) reaches.
func (client *Client) ListCloudflareDomains(ctx context.Context, credentialName string) (*CloudflareDomainListing, error) {
	var listing CloudflareDomainListing
	if err := client.doRequest(ctx, http.MethodGet, cloudflarePath(cloudflareDomainsPath, credentialName, nil), nil, &listing); err != nil {
		return nil, err
	}
	return &listing, nil
}

// CloudflareRecord is one DNS record in a Cloudflare zone. Name is the FQDN.
type CloudflareRecord struct {
	ID         string `json:"id"`
	ZoneID     string `json:"zone_id"`
	ZoneName   string `json:"zone_name"`
	Name       string `json:"name"`
	RecordType string `json:"record_type"`
	Content    string `json:"content"`
	TTL        int64  `json:"ttl"`
	Proxied    bool   `json:"proxied"`
	Proxiable  bool   `json:"proxiable"`
	Priority   *int64 `json:"priority"`
	Comment    string `json:"comment"`
	ManagedBy  string `json:"managed_by"`
}

// CloudflareRecordCreate is the create body. Name may be a bare label, which
// the platform qualifies with the zone.
type CloudflareRecordCreate struct {
	Name       string `json:"name"`
	RecordType string `json:"record_type"`
	Content    string `json:"content"`
	TTL        int64  `json:"ttl"`
	Proxied    bool   `json:"proxied"`
	Priority   *int64 `json:"priority,omitempty"`
	Comment    string `json:"comment"`
}

// CloudflareRecordUpdate is the update body. A member that is absent keeps
// the record's current value; TTL, Proxied and Comment are always sent so the
// record matches configuration. Priority can only be set, never cleared.
type CloudflareRecordUpdate struct {
	Content  string `json:"content"`
	TTL      int64  `json:"ttl"`
	Proxied  bool   `json:"proxied"`
	Priority *int64 `json:"priority,omitempty"`
	Comment  string `json:"comment"`
}

type cloudflareRecordListing struct {
	Items []CloudflareRecord `json:"items"`
}

// CloudflareRecordFilter narrows a zone's record listing; an empty member
// does not filter.
type CloudflareRecordFilter struct {
	Name       string
	RecordType string
	Search     string
}

// CloudflareRecordListing is a zone's record listing. The platform walks
// Cloudflare's pages up to its own bound; IsTruncated reports that it
// stopped before the end.
type CloudflareRecordListing struct {
	Items       []CloudflareRecord `json:"items"`
	IsTruncated bool               `json:"is_truncated"`
}

// ListCloudflareRecords lists the records in a zone, narrowed by filter. A
// zone the credential cannot reach is a NotFound APIError.
func (client *Client) ListCloudflareRecords(ctx context.Context, credentialName, zoneID string, filter CloudflareRecordFilter) (*CloudflareRecordListing, error) {
	query := url.Values{}
	if filter.Name != "" {
		query.Set("name", filter.Name)
	}
	if filter.RecordType != "" {
		query.Set("record_type", filter.RecordType)
	}
	if filter.Search != "" {
		query.Set("search", filter.Search)
	}
	var listing CloudflareRecordListing
	if err := client.doRequest(ctx, http.MethodGet, cloudflarePath(cloudflareRecordsPath(zoneID), credentialName, query), nil, &listing); err != nil {
		return nil, err
	}
	return &listing, nil
}

func cloudflarePath(path string, credentialName string, query url.Values) string {
	if query == nil {
		query = url.Values{}
	}
	if credentialName != "" {
		query.Set("credential_name", credentialName)
	}
	if len(query) == 0 {
		return path
	}
	return path + "?" + query.Encode()
}

func cloudflareRecordsPath(zoneID string) string {
	return cloudflareDomainsPath + "/" + url.PathEscape(zoneID) + "/records"
}

// GetCloudflareRecord finds one record in its zone's listing, narrowed by the
// record's FQDN and type when they are known: the platform offers no read by
// id. A missing record is a NotFound APIError.
func (client *Client) GetCloudflareRecord(ctx context.Context, credentialName, zoneID, recordID, name, recordType string) (*CloudflareRecord, error) {
	query := url.Values{}
	if name != "" {
		query.Set("name", name)
	}
	if recordType != "" {
		query.Set("record_type", recordType)
	}
	path := cloudflareRecordsPath(zoneID)
	var listing cloudflareRecordListing
	if err := client.doRequest(ctx, http.MethodGet, cloudflarePath(path, credentialName, query), nil, &listing); err != nil {
		return nil, err
	}
	for index := range listing.Items {
		if listing.Items[index].ID == recordID {
			return &listing.Items[index], nil
		}
	}
	return nil, &APIError{Method: http.MethodGet, Path: path, StatusCode: http.StatusNotFound, Body: "Cloudflare record " + recordID + " not found"}
}

// CreateCloudflareRecord creates a record in a zone.
func (client *Client) CreateCloudflareRecord(ctx context.Context, credentialName, zoneID string, request CloudflareRecordCreate) (*CloudflareRecord, error) {
	var record CloudflareRecord
	if err := client.doRequest(ctx, http.MethodPost, cloudflarePath(cloudflareRecordsPath(zoneID), credentialName, nil), request, &record); err != nil {
		return nil, err
	}
	return &record, nil
}

// UpdateCloudflareRecord replaces a record's content and settings; its name
// and type are fixed.
func (client *Client) UpdateCloudflareRecord(ctx context.Context, credentialName, zoneID, recordID string, request CloudflareRecordUpdate) (*CloudflareRecord, error) {
	var record CloudflareRecord
	path := cloudflareRecordsPath(zoneID) + "/" + url.PathEscape(recordID)
	if err := client.doRequest(ctx, http.MethodPatch, cloudflarePath(path, credentialName, nil), request, &record); err != nil {
		return nil, err
	}
	return &record, nil
}

// DeleteCloudflareRecord deletes a record. The credential it is written with
// is project-scoped, so a 404 is returned for the caller to tell "gone" from
// "out of scope".
func (client *Client) DeleteCloudflareRecord(ctx context.Context, credentialName, zoneID, recordID string) error {
	path := cloudflareRecordsPath(zoneID) + "/" + url.PathEscape(recordID)
	return client.doRequest(ctx, http.MethodDelete, cloudflarePath(path, credentialName, nil), nil, nil)
}
