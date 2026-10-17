// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package client

import (
	"context"
	"net/http"
	"net/url"
)

const backupVaultsPath = "/api/v1/org/backup-vaults"

// BackupVaultStatusProvisioning is the status of a vault, or a bucket, the
// platform is still creating.
const BackupVaultStatusProvisioning = "provisioning"

// BackupVaultStatusError is the status of a vault, or a bucket, whose
// provisioning or last verification failed; ErrorExcerpt then says why.
const BackupVaultStatusError = "error"

// BackupVault is one organisation backup vault: the object-storage bucket
// cluster backups and migration data move through. Credential material is
// never returned.
type BackupVault struct {
	ID                         string  `json:"id"`
	Name                       string  `json:"name"`
	Kind                       string  `json:"kind"`
	Provider                   string  `json:"provider"`
	Endpoint                   string  `json:"endpoint"`
	Region                     string  `json:"region"`
	Bucket                     string  `json:"bucket"`
	PathStyle                  bool    `json:"path_style"`
	Status                     string  `json:"status"`
	ErrorExcerpt               *string `json:"error_excerpt"`
	LastVerifiedAt             *string `json:"last_verified_at"`
	ProvisionedViaCredentialID *string `json:"provisioned_via_credential_id"`
	CreatedAt                  string  `json:"created_at"`
	UpdatedAt                  string  `json:"updated_at"`
}

// BackupVaultCreate registers a bucket the organisation already has. The
// platform verifies the keys against it synchronously.
type BackupVaultCreate struct {
	Name            string  `json:"name"`
	Provider        *string `json:"provider,omitempty"`
	Endpoint        string  `json:"endpoint"`
	Region          *string `json:"region,omitempty"`
	Bucket          string  `json:"bucket"`
	PathStyle       *bool   `json:"path_style,omitempty"`
	AccessKeyID     string  `json:"access_key_id"`
	SecretAccessKey string  `json:"secret_access_key"`
}

// BackupVaultProvision asks the platform to create the bucket itself on one
// of the organisation's provider credentials. The key pair is for Hetzner
// only, whose API cannot mint Object Storage keys.
type BackupVaultProvision struct {
	Name            string  `json:"name"`
	CredentialID    string  `json:"credential_id"`
	Region          string  `json:"region"`
	Bucket          *string `json:"bucket,omitempty"`
	AccessKeyID     *string `json:"access_key_id,omitempty"`
	SecretAccessKey *string `json:"secret_access_key,omitempty"`
}

type backupVaultListEnvelope struct {
	Items []BackupVault `json:"items"`
}

// ListBackupVaults returns the organisation's backup vaults.
func (client *Client) ListBackupVaults(ctx context.Context) ([]BackupVault, error) {
	var envelope backupVaultListEnvelope
	if err := client.doRequest(ctx, http.MethodGet, backupVaultsPath, nil, &envelope); err != nil {
		return nil, err
	}
	return envelope.Items, nil
}

// GetBackupVault reads one vault. A deleted vault is a NotFound APIError.
func (client *Client) GetBackupVault(ctx context.Context, vaultID string) (*BackupVault, error) {
	var vault BackupVault
	if err := client.doRequest(ctx, http.MethodGet, backupVaultsPath+"/"+url.PathEscape(vaultID), nil, &vault); err != nil {
		return nil, err
	}
	return &vault, nil
}

// CreateBackupVault registers a bring-your-own-bucket vault.
func (client *Client) CreateBackupVault(ctx context.Context, request BackupVaultCreate) (*BackupVault, error) {
	var vault BackupVault
	if err := client.doRequest(ctx, http.MethodPost, backupVaultsPath, request, &vault); err != nil {
		return nil, err
	}
	return &vault, nil
}

// ProvisionBackupVault records an Ankra-provisioned vault and starts the job
// that creates its bucket. The vault answers in provisioning.
func (client *Client) ProvisionBackupVault(ctx context.Context, request BackupVaultProvision) (*BackupVault, error) {
	var vault BackupVault
	if err := client.doRequest(ctx, http.MethodPost, backupVaultsPath+"/provision", request, &vault); err != nil {
		return nil, err
	}
	return &vault, nil
}

// DeleteBackupVault forgets a vault. With destroyProviderResources the
// platform also empties and deletes the bucket it provisioned, which it
// refuses for a bring-your-own vault. A vault the scope cannot see is a NotFound
// APIError: the caller decides whether that means gone.
func (client *Client) DeleteBackupVault(ctx context.Context, vaultID string, destroyProviderResources bool) error {
	path := backupVaultsPath + "/" + url.PathEscape(vaultID)
	if destroyProviderResources {
		path += "?destroy_provider_resources=true"
	}
	return client.doRequest(ctx, http.MethodDelete, path, nil, nil)
}
