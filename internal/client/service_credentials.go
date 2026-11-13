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
	serviceCredentialsPath      = "/api/v1/org/service-admission/credentials"
	serviceCredentialGrantsPath = "/api/v1/org/service-admission/credential-grants"
	// serviceCredentialPageCap bounds a listing walk: 50 rows per page.
	serviceCredentialPageCap = 40
)

// ServiceCredentialFamily is a managed-service credential family. No secret
// value or store path is ever returned.
type ServiceCredentialFamily struct {
	ID            string  `json:"id"`
	Name          string  `json:"name"`
	CreatedAt     string  `json:"created_at"`
	ActiveValueID *string `json:"active_value_id"`
}

// ServiceCredentialValue is the metadata of one immutable credential value.
type ServiceCredentialValue struct {
	ID    string `json:"id"`
	State string `json:"state"`
}

// ServiceCredentialGrant is a purpose-bound reference to a credential value.
type ServiceCredentialGrant struct {
	ID            string  `json:"id"`
	ValueID       string  `json:"value_id"`
	PackageDigest string  `json:"package_digest"`
	InputName     string  `json:"input_name"`
	Mode          string  `json:"mode"`
	CreatedAt     string  `json:"created_at"`
	RevokedAt     *string `json:"revoked_at"`
}

// ServiceCredentialGrantCreate is the body that issues a grant.
type ServiceCredentialGrantCreate struct {
	FamilyID      string `json:"family_id"`
	ValueID       string `json:"value_id"`
	PackageDigest string `json:"package_digest"`
	InputName     string `json:"input_name"`
	Mode          string `json:"mode"`
}

type serviceCredentialValueWrite struct {
	Value           string  `json:"value"`
	ExpectedValueID *string `json:"expected_value_id"`
}

// ServiceCredentialRotation is the answer to a value write: the new value,
// the grants it revoked and the grants it reissued on the new value.
type ServiceCredentialRotation struct {
	Value    ServiceCredentialValue   `json:"value"`
	Replaced []ServiceCredentialGrant `json:"replaced"`
	Granted  []ServiceCredentialGrant `json:"granted"`
}

type serviceCredentialGrantPage struct {
	Items      []ServiceCredentialGrant `json:"items"`
	NextCursor *string                  `json:"next_cursor"`
}

func serviceCredentialFamilyPath(familyID string) string {
	return serviceCredentialsPath + "/" + url.PathEscape(familyID)
}

// GetServiceCredentialFamily reads a family's metadata.
func (client *Client) GetServiceCredentialFamily(ctx context.Context, familyID string) (*ServiceCredentialFamily, error) {
	var family ServiceCredentialFamily
	if requestError := client.doRequest(ctx, http.MethodGet, serviceCredentialFamilyPath(familyID), nil, &family); requestError != nil {
		return nil, requestError
	}
	return &family, nil
}

// CreateServiceCredentialFamily creates an empty family.
func (client *Client) CreateServiceCredentialFamily(ctx context.Context, name string) (*ServiceCredentialFamily, error) {
	var family ServiceCredentialFamily
	if requestError := client.doRequest(ctx, http.MethodPost, serviceCredentialsPath, map[string]string{"name": name}, &family); requestError != nil {
		return nil, requestError
	}
	return &family, nil
}

// WriteServiceCredentialValue creates the family's first value or rotates
// it. expectedValueID must be the active value (nil for none): a stale one
// is refused, so a concurrent write cannot be replaced.
func (client *Client) WriteServiceCredentialValue(ctx context.Context, familyID, value string, expectedValueID *string) (*ServiceCredentialRotation, error) {
	var rotation ServiceCredentialRotation
	body := serviceCredentialValueWrite{Value: value, ExpectedValueID: expectedValueID}
	if requestError := client.doRequest(ctx, http.MethodPost, serviceCredentialFamilyPath(familyID)+"/values", body, &rotation); requestError != nil {
		return nil, requestError
	}
	return &rotation, nil
}

// ListServiceCredentialGrants walks every page of a family's grants.
func (client *Client) ListServiceCredentialGrants(ctx context.Context, familyID string) ([]ServiceCredentialGrant, error) {
	var grants []ServiceCredentialGrant
	query := url.Values{}
	query.Set("limit", "50")
	for page := 0; page < serviceCredentialPageCap; page++ {
		var listing serviceCredentialGrantPage
		if requestError := client.doRequest(ctx, http.MethodGet, serviceCredentialFamilyPath(familyID)+"/grants?"+query.Encode(), nil, &listing); requestError != nil {
			return nil, requestError
		}
		grants = append(grants, listing.Items...)
		if listing.NextCursor == nil || *listing.NextCursor == "" {
			return grants, nil
		}
		query.Set("after", *listing.NextCursor)
	}
	return nil, fmt.Errorf("credential family %s lists more than %d pages of grants", familyID, serviceCredentialPageCap)
}

// IssueServiceCredentialGrant issues a purpose-bound reference.
func (client *Client) IssueServiceCredentialGrant(ctx context.Context, create ServiceCredentialGrantCreate) (*ServiceCredentialGrant, error) {
	var grant ServiceCredentialGrant
	if requestError := client.doRequest(ctx, http.MethodPost, serviceCredentialGrantsPath, create, &grant); requestError != nil {
		return nil, requestError
	}
	return &grant, nil
}

// RevokeServiceCredentialGrant revokes a grant.
func (client *Client) RevokeServiceCredentialGrant(ctx context.Context, grantID string) error {
	return client.doRequest(ctx, http.MethodDelete, serviceCredentialGrantsPath+"/"+url.PathEscape(grantID), nil, nil)
}

type servicePackageShares struct {
	OrganisationIDs []string `json:"organisation_ids"`
}

func servicePackageSharesPath(versionID string) string {
	return servicePackagesPath + "/" + url.PathEscape(versionID) + "/shares"
}

// ListServicePackageShares lists the organisations a package version is
// shared with, as its owner sees them.
func (client *Client) ListServicePackageShares(ctx context.Context, versionID string) ([]string, error) {
	var shares servicePackageShares
	if requestError := client.doRequest(ctx, http.MethodGet, servicePackageSharesPath(versionID), nil, &shares); requestError != nil {
		return nil, requestError
	}
	return shares.OrganisationIDs, nil
}

// ShareServicePackage shares a package version with an organisation.
func (client *Client) ShareServicePackage(ctx context.Context, versionID, organisationID string) error {
	return client.doRequest(ctx, http.MethodPut, servicePackageSharesPath(versionID)+"/"+url.PathEscape(organisationID), nil, nil)
}

// RevokeServicePackageShare removes a recipient organisation.
func (client *Client) RevokeServicePackageShare(ctx context.Context, versionID, organisationID string) error {
	return client.doRequest(ctx, http.MethodDelete, servicePackageSharesPath(versionID)+"/"+url.PathEscape(organisationID), nil, nil)
}
