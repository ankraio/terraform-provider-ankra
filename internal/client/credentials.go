// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package client

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
)

const (
	credentialsPath = "/api/v1/org/credentials"
	// credentialCreatePath is the token surface the provider credential
	// creates are mounted under, one path segment per provider.
	credentialCreatePath = "/api/v1/credentials"
	// CredentialSSHKeyProvider is the listing filter (and the provider every
	// SSH key credential is listed under) for organisation-wide SSH keys.
	CredentialSSHKeyProvider = "ssh-key"
	// CredentialAwsProvider is the listing filter for AWS credentials.
	CredentialAwsProvider = "aws"
	// credentialSSHKeyCreatePath is where SSH key credentials are created. The
	// platform mounts the same handler under every provider segment and stores
	// one organisation-wide credential whatever the segment says; hetzner is
	// the one segment no dark-rollout flag gates.
	credentialSSHKeyCreatePath = credentialCreatePath + "/hetzner/ssh-key"
)

// Credential is one credential as the platform lists it. The listing is
// metadata only: it never carries a token, key or password, which is why the
// provider reads credentials from it instead of from the detail route (the
// detail decrypts the secret, needs credentials.reveal and writes a reveal
// audit row per read).
type Credential struct {
	ID             string  `json:"id"`
	Name           string  `json:"name"`
	DisplayName    *string `json:"display_name"`
	Provider       string  `json:"provider"`
	OrganisationID string  `json:"organisation_id"`
	System         bool    `json:"system"`
	Available      bool    `json:"available"`
	State          *string `json:"state"`
	CreatedAt      string  `json:"created_at"`
	UpdatedAt      *string `json:"updated_at"`
	// Scope and AuthMethod are set on AWS credentials only.
	Scope      *string `json:"scope,omitempty"`
	AuthMethod *string `json:"auth_method,omitempty"`
	// Managed is set on Ankra Cloud credentials only: true for the built-in
	// credential the platform mints and renews.
	Managed *bool `json:"managed,omitempty"`
	// SSHPublicKey and the key identity are set on SSH key credentials only.
	SSHPublicKey *string `json:"ssh_public_key,omitempty"`
	Fingerprint  *string `json:"fingerprint,omitempty"`
	KeyType      *string `json:"key_type,omitempty"`
	Comment      *string `json:"comment,omitempty"`
}

// CredentialSSHKeyCreate is the SSH key credential create body: either a
// public key or generate.
type CredentialSSHKeyCreate struct {
	Name         string  `json:"name"`
	SSHPublicKey *string `json:"ssh_public_key,omitempty"`
	Generate     bool    `json:"generate"`
}

// CredentialSSHKeyCreated is the platform's answer to an SSH key create.
// PrivateKey is set only when the platform generated the keypair, and is
// never returned again.
type CredentialSSHKeyCreated struct {
	ID         string  `json:"id"`
	Name       string  `json:"name"`
	PrivateKey *string `json:"private_key"`
}

// CredentialAwsCreated is the platform's answer to an AWS credential create.
type CredentialAwsCreated struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Provider string `json:"provider"`
}

// CredentialProxmoxTailscale is the Tailscale join a Proxmox VE credential
// carries.
type CredentialProxmoxTailscale struct {
	LoginServer     string `json:"login_server"`
	AuthKey         string `json:"auth_key"`
	AcceptRoutes    bool   `json:"accept_routes"`
	AdvertiseRoutes string `json:"advertise_routes,omitempty"`
}

// credentialProxmoxTailscaleUpdate always carries the tailscale key: an
// explicit null clears the stored join.
type credentialProxmoxTailscaleUpdate struct {
	Tailscale *CredentialProxmoxTailscale `json:"tailscale"`
}

// ListCredentials lists the organisation's credentials, filtered to one
// provider unless provider is empty.
func (client *Client) ListCredentials(ctx context.Context, provider string) ([]Credential, error) {
	requestPath := credentialsPath
	if provider != "" {
		requestPath += "?provider=" + url.QueryEscape(provider)
	}
	var credentials []Credential
	if err := client.doRequest(ctx, http.MethodGet, requestPath, nil, &credentials); err != nil {
		return nil, err
	}
	return credentials, nil
}

// GetCredential finds one credential by id in the provider's listing. A
// credential the listing does not hold is a NotFound APIError.
func (client *Client) GetCredential(ctx context.Context, provider, credentialID string) (*Credential, error) {
	return client.findCredential(ctx, provider, func(credential Credential) bool {
		return credential.ID == credentialID
	}, "credential "+credentialID+" not found")
}

// GetCredentialByName finds one credential by name in the provider's
// listing. Names are unique per provider within an organisation.
func (client *Client) GetCredentialByName(ctx context.Context, provider, name string) (*Credential, error) {
	return client.findCredential(ctx, provider, func(credential Credential) bool {
		return credential.Name == name
	}, "credential "+name+" not found")
}

func (client *Client) findCredential(ctx context.Context, provider string, matches func(Credential) bool, missing string) (*Credential, error) {
	credentials, err := client.ListCredentials(ctx, provider)
	if err != nil {
		return nil, err
	}
	for index := range credentials {
		if matches(credentials[index]) {
			return &credentials[index], nil
		}
	}
	return nil, &APIError{Method: http.MethodGet, Path: credentialsPath, StatusCode: http.StatusNotFound, Body: missing}
}

// CreateProviderCredential creates a cloud provider credential from the
// provider's create body. The platform answers {"success": true} without the
// new credential's id, so it is looked up by name afterwards.
func (client *Client) CreateProviderCredential(ctx context.Context, provider string, body map[string]any) (*Credential, error) {
	var outcome struct {
		Success bool `json:"success"`
	}
	createPath := credentialCreatePath + "/" + url.PathEscape(provider)
	if err := client.doRequest(ctx, http.MethodPost, createPath, body, &outcome); err != nil {
		return nil, err
	}
	if !outcome.Success {
		return nil, fmt.Errorf("the platform did not report the %s credential as created", provider)
	}
	name, _ := body["name"].(string)
	created, err := client.GetCredentialByName(ctx, provider, name)
	if err != nil {
		return nil, &CredentialReadBackError{Provider: provider, Name: name, Err: err}
	}
	return created, nil
}

// CredentialReadBackError reports a credential the platform created but the
// listing did not return afterwards, for example because the token lacks
// credentials.read. The credential exists, so a retried create fails on its
// name; the caller should point at an import instead.
type CredentialReadBackError struct {
	Provider string
	Name     string
	Err      error
}

func (readBackError *CredentialReadBackError) Error() string {
	return fmt.Sprintf("the %s credential %q was created but could not be read back: %s", readBackError.Provider, readBackError.Name, readBackError.Err)
}

func (readBackError *CredentialReadBackError) Unwrap() error {
	return readBackError.Err
}

// CreateSSHKeyCredential creates an organisation-wide SSH key credential.
func (client *Client) CreateSSHKeyCredential(ctx context.Context, request CredentialSSHKeyCreate) (*CredentialSSHKeyCreated, error) {
	var created CredentialSSHKeyCreated
	if err := client.doRequest(ctx, http.MethodPost, credentialSSHKeyCreatePath, request, &created); err != nil {
		return nil, err
	}
	if created.ID == "" {
		return nil, fmt.Errorf("the platform did not return the SSH key credential's id")
	}
	return &created, nil
}

// CreateAwsCredential creates an AWS credential through method, "role" or
// "keys".
func (client *Client) CreateAwsCredential(ctx context.Context, method string, body map[string]any) (*CredentialAwsCreated, error) {
	var created CredentialAwsCreated
	if err := client.doRequest(ctx, http.MethodPost, credentialCreatePath+"/aws/"+url.PathEscape(method), body, &created); err != nil {
		return nil, err
	}
	if created.ID == "" {
		return nil, fmt.Errorf("the platform did not return the AWS credential's id")
	}
	return &created, nil
}

// UpdateProxmoxCredentialTailscale sets the Tailscale join on a Proxmox VE
// credential, or clears it when tailscale is nil.
func (client *Client) UpdateProxmoxCredentialTailscale(ctx context.Context, credentialID string, tailscale *CredentialProxmoxTailscale) error {
	updatePath := credentialCreatePath + "/proxmox/" + url.PathEscape(credentialID) + "/tailscale"
	return client.doRequest(ctx, http.MethodPut, updatePath, credentialProxmoxTailscaleUpdate{Tailscale: tailscale}, nil)
}

// DeleteCredential deletes a credential. A 404 is returned as an error: the
// credential may be gone, or only outside the provider's project scope, and
// only the caller can tell which. One still used by a cluster is refused with
// 409.
func (client *Client) DeleteCredential(ctx context.Context, credentialID string) error {
	return client.doRequest(ctx, http.MethodDelete, credentialsPath+"/"+url.PathEscape(credentialID), nil, nil)
}
