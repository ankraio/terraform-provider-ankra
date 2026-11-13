// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package client

import (
	"context"
	"net/http"
)

const datasourceCredentialsPath = credentialCreatePath + "/datasource"

// DatasourceCredentialCreate is the body that stores a metrics or log
// backend credential. The password and token go to the secret store and are
// never returned.
type DatasourceCredentialCreate struct {
	Name       string `json:"name"`
	AuthScheme string `json:"auth_scheme,omitempty"`
	Username   string `json:"username,omitempty"`
	Password   string `json:"password,omitempty"`
	Token      string `json:"token,omitempty"`
}

type datasourceCredentialCreated struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// ListDatasourceCredentials lists the organisation's data source
// credentials, metadata only.
func (client *Client) ListDatasourceCredentials(ctx context.Context) ([]Credential, error) {
	var credentials []Credential
	if requestError := client.doRequest(ctx, http.MethodGet, datasourceCredentialsPath, nil, &credentials); requestError != nil {
		return nil, requestError
	}
	return credentials, nil
}

// CreateDatasourceCredential stores a data source credential and returns
// its id.
func (client *Client) CreateDatasourceCredential(ctx context.Context, create DatasourceCredentialCreate) (string, error) {
	var created datasourceCredentialCreated
	if requestError := client.doRequest(ctx, http.MethodPost, datasourceCredentialsPath, create, &created); requestError != nil {
		return "", requestError
	}
	return created.ID, nil
}
