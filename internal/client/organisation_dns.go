// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package client

import (
	"context"
	"net/http"
	"net/url"
)

const (
	organisationDNSRecordsPath     = "/api/v1/org/dns/records"
	organisationDNSZonePath        = "/api/v1/org/dns/zone"
	organisationCustomDNSZonesPath = "/api/v1/org/custom-dns-zones"
	dnsRootDomainEnvironmentPath   = "/api/v1/org/ai-environment"
	dnsCredentialsPath             = "/api/v1/credentials/dns"
	dnsOrganisationCredentialsPath = "/api/v1/org/credentials"

	// OrganisationDNSRecordStateDeleting is the state a deleted record holds
	// until the reconciler removes it.
	OrganisationDNSRecordStateDeleting = "deleting"
)

// OrganisationDNSZone is the organisation's own delegated zone: the FQDN
// record names hang off and its provisioning state ("active", "pending",
// or "none" when no zone exists yet).
type OrganisationDNSZone struct {
	FQDN  string `json:"fqdn"`
	State string `json:"state"`
}

// OrganisationDNSRecord is one record in the organisation's zone. Name is
// the full FQDN (label plus zone); a nil TTL is Auto.
type OrganisationDNSRecord struct {
	ID         string  `json:"id"`
	Name       string  `json:"name"`
	RecordType string  `json:"record_type"`
	Content    string  `json:"content"`
	TTL        *int64  `json:"ttl"`
	State      string  `json:"state"`
	LastError  *string `json:"last_error"`
}

// OrganisationDNSRecordCreate is the create body; Name is a label such as
// "logs", which the platform lower-cases and qualifies with the zone.
type OrganisationDNSRecordCreate struct {
	Name       string `json:"name"`
	RecordType string `json:"record_type"`
	Content    string `json:"content"`
	TTL        *int64 `json:"ttl,omitempty"`
}

// OrganisationDNSRecordUpdate is the update body. TTL is always sent: an
// absent ttl keeps the record's current one, an explicit null resets it to
// Auto.
type OrganisationDNSRecordUpdate struct {
	Content string `json:"content"`
	TTL     *int64 `json:"ttl"`
}

type organisationDNSRecordListing struct {
	Items []OrganisationDNSRecord `json:"items"`
}

// GetOrganisationDNSZone reads the organisation's delegated zone.
func (client *Client) GetOrganisationDNSZone(ctx context.Context) (*OrganisationDNSZone, error) {
	var zone OrganisationDNSZone
	if err := client.doRequest(ctx, http.MethodGet, organisationDNSZonePath, nil, &zone); err != nil {
		return nil, err
	}
	return &zone, nil
}

// ListOrganisationDNSRecords returns every record in the organisation's zone.
func (client *Client) ListOrganisationDNSRecords(ctx context.Context) ([]OrganisationDNSRecord, error) {
	var listing organisationDNSRecordListing
	if err := client.doRequest(ctx, http.MethodGet, organisationDNSRecordsPath, nil, &listing); err != nil {
		return nil, err
	}
	return listing.Items, nil
}

// GetOrganisationDNSRecord finds one record in the listing, which is the only
// read the platform offers. A record that is missing or already being deleted
// is a NotFound APIError.
func (client *Client) GetOrganisationDNSRecord(ctx context.Context, recordID string) (*OrganisationDNSRecord, error) {
	records, err := client.ListOrganisationDNSRecords(ctx)
	if err != nil {
		return nil, err
	}
	for index := range records {
		if records[index].ID == recordID && records[index].State != OrganisationDNSRecordStateDeleting {
			return &records[index], nil
		}
	}
	return nil, &APIError{Method: http.MethodGet, Path: organisationDNSRecordsPath, StatusCode: http.StatusNotFound, Body: "DNS record " + recordID + " not found"}
}

// CreateOrganisationDNSRecord creates a record.
func (client *Client) CreateOrganisationDNSRecord(ctx context.Context, request OrganisationDNSRecordCreate) (*OrganisationDNSRecord, error) {
	var record OrganisationDNSRecord
	if err := client.doRequest(ctx, http.MethodPost, organisationDNSRecordsPath, request, &record); err != nil {
		return nil, err
	}
	return &record, nil
}

// UpdateOrganisationDNSRecord re-points a record and sets its TTL.
func (client *Client) UpdateOrganisationDNSRecord(ctx context.Context, recordID string, request OrganisationDNSRecordUpdate) (*OrganisationDNSRecord, error) {
	var record OrganisationDNSRecord
	if err := client.doRequest(ctx, http.MethodPatch, organisationDNSRecordsPath+"/"+url.PathEscape(recordID), request, &record); err != nil {
		return nil, err
	}
	return &record, nil
}

// DeleteOrganisationDNSRecord deletes a record. One already gone is not an
// error.
func (client *Client) DeleteOrganisationDNSRecord(ctx context.Context, recordID string) error {
	return client.doRequest(ctx, http.MethodDelete, organisationDNSRecordsPath+"/"+url.PathEscape(recordID), nil, nil, http.StatusNotFound)
}

// OrganisationCustomDNSZone is a zone every cluster in the organisation
// serves with the organisation's own DNS credential. Provider is the
// credential family the platform derived ("cloudflare", "webhook" or
// "avura").
type OrganisationCustomDNSZone struct {
	Zone           string  `json:"zone"`
	CredentialName string  `json:"credential_name"`
	Provider       *string `json:"provider"`
	Policy         *string `json:"policy"`
}

// OrganisationCustomDNSZoneDeclaration is the declare body. Declaring a zone
// that is already declared rebinds it to the given credential.
type OrganisationCustomDNSZoneDeclaration struct {
	Zone           string  `json:"zone"`
	CredentialName string  `json:"credential_name"`
	Provider       *string `json:"provider,omitempty"`
}

type organisationCustomDNSZoneListing struct {
	Zones []OrganisationCustomDNSZone `json:"zones"`
}

// ListOrganisationCustomDNSZones returns the organisation-wide zone
// declarations.
func (client *Client) ListOrganisationCustomDNSZones(ctx context.Context) ([]OrganisationCustomDNSZone, error) {
	var listing organisationCustomDNSZoneListing
	if err := client.doRequest(ctx, http.MethodGet, organisationCustomDNSZonesPath, nil, &listing); err != nil {
		return nil, err
	}
	return listing.Zones, nil
}

// GetOrganisationCustomDNSZone finds one declaration by its normalised zone
// name. A missing zone is a NotFound APIError.
func (client *Client) GetOrganisationCustomDNSZone(ctx context.Context, zone string) (*OrganisationCustomDNSZone, error) {
	zones, err := client.ListOrganisationCustomDNSZones(ctx)
	if err != nil {
		return nil, err
	}
	for index := range zones {
		if zones[index].Zone == zone {
			return &zones[index], nil
		}
	}
	return nil, &APIError{Method: http.MethodGet, Path: organisationCustomDNSZonesPath, StatusCode: http.StatusNotFound, Body: "zone " + zone + " not declared"}
}

// DeclareOrganisationCustomDNSZone declares a zone, or rebinds a declared one.
func (client *Client) DeclareOrganisationCustomDNSZone(ctx context.Context, request OrganisationCustomDNSZoneDeclaration) (*OrganisationCustomDNSZone, error) {
	var declared OrganisationCustomDNSZone
	if err := client.doRequest(ctx, http.MethodPost, organisationCustomDNSZonesPath, request, &declared); err != nil {
		return nil, err
	}
	return &declared, nil
}

// RemoveOrganisationCustomDNSZone withdraws a declaration. One already gone is
// not an error.
func (client *Client) RemoveOrganisationCustomDNSZone(ctx context.Context, zone string) error {
	return client.doRequest(ctx, http.MethodDelete, organisationCustomDNSZonesPath+"/"+url.PathEscape(zone), nil, nil, http.StatusNotFound)
}

// OrganisationDNSRootDomain is the organisation's Ankra root domain. DNSRootDomain
// is nil while the organisation uses the platform default.
type OrganisationDNSRootDomain struct {
	DNSRootDomain        *string `json:"dns_root_domain"`
	DNSRootDomainDefault string  `json:"dns_root_domain_default"`
}

type organisationDNSRootDomainUpdate struct {
	DNSRootDomain *string `json:"dns_root_domain"`
}

// GetOrganisationDNSRootDomain reads the organisation's root domain from its
// environment settings.
func (client *Client) GetOrganisationDNSRootDomain(ctx context.Context) (*OrganisationDNSRootDomain, error) {
	var domain OrganisationDNSRootDomain
	if err := client.doRequest(ctx, http.MethodGet, dnsRootDomainEnvironmentPath, nil, &domain); err != nil {
		return nil, err
	}
	return &domain, nil
}

// SetOrganisationDNSRootDomain sets the root domain; nil restores the platform
// default. Only dns_root_domain is sent, which the platform applies without
// touching the environment's other settings.
func (client *Client) SetOrganisationDNSRootDomain(ctx context.Context, rootDomain *string) (*OrganisationDNSRootDomain, error) {
	var domain OrganisationDNSRootDomain
	if err := client.doRequest(ctx, http.MethodPut, dnsRootDomainEnvironmentPath, organisationDNSRootDomainUpdate{DNSRootDomain: rootDomain}, &domain); err != nil {
		return nil, err
	}
	return &domain, nil
}

// DNSCredential is an external-dns webhook credential. The webhook URL is
// never returned.
type DNSCredential struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Provider  string `json:"provider"`
	CreatedAt string `json:"created_at"`
}

// DNSCredentialCreate is the create body.
type DNSCredentialCreate struct {
	Name               string `json:"name"`
	WebhookProviderURL string `json:"webhook_provider_url"`
}

type dnsCredentialCreated struct {
	ID string `json:"id"`
}

// ListDNSCredentials returns the organisation's DNS webhook credentials.
func (client *Client) ListDNSCredentials(ctx context.Context) ([]DNSCredential, error) {
	var credentials []DNSCredential
	if err := client.doRequest(ctx, http.MethodGet, dnsCredentialsPath, nil, &credentials); err != nil {
		return nil, err
	}
	return credentials, nil
}

// CreateDNSCredential stores a DNS webhook credential and returns its id.
func (client *Client) CreateDNSCredential(ctx context.Context, request DNSCredentialCreate) (string, error) {
	var created dnsCredentialCreated
	if err := client.doRequest(ctx, http.MethodPost, dnsCredentialsPath, request, &created); err != nil {
		return "", err
	}
	return created.ID, nil
}

// DNSOrganisationCredential is a credential as the organisation-wide listing
// shows it, across every provider.
type DNSOrganisationCredential struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Provider string `json:"provider"`
}

// ListDNSOrganisationCredentials lists the organisation's credentials of one
// provider ("dns", "cloudflare", ...).
func (client *Client) ListDNSOrganisationCredentials(ctx context.Context, provider string) ([]DNSOrganisationCredential, error) {
	var credentials []DNSOrganisationCredential
	query := url.Values{}
	query.Set("provider", provider)
	if err := client.doRequest(ctx, http.MethodGet, dnsOrganisationCredentialsPath+"?"+query.Encode(), nil, &credentials); err != nil {
		return nil, err
	}
	return credentials, nil
}

// DeleteDNSOrganisationCredential deletes a credential of any provider by id.
// One already gone is not an error; one still in use answers 409.
func (client *Client) DeleteDNSOrganisationCredential(ctx context.Context, credentialID string) error {
	return client.doRequest(ctx, http.MethodDelete, dnsOrganisationCredentialsPath+"/"+url.PathEscape(credentialID), nil, nil, http.StatusNotFound)
}
