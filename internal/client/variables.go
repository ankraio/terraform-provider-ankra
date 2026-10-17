// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package client

import (
	"context"
	"net/http"
	"net/url"
)

const organisationVariablesPath = "/api/v1/org/variables"

// OrganisationVariable is one organisation variable as the platform lists it.
// Value is nil when IsSecret is set: the listing never returns a secret.
type OrganisationVariable struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Value       *string `json:"value"`
	Description *string `json:"description"`
	IsSecret    bool    `json:"is_secret"`
}

// OrganisationVariableCreate is the create body.
type OrganisationVariableCreate struct {
	Name        string  `json:"name"`
	Value       string  `json:"value"`
	Description *string `json:"description,omitempty"`
	IsSecret    bool    `json:"is_secret"`
}

// OrganisationVariableUpdate is the update body. Description is always sent:
// an explicit null clears the stored description, while an absent key would
// keep it, so a description removed from configuration would never clear.
type OrganisationVariableUpdate struct {
	Value       string  `json:"value"`
	Description *string `json:"description"`
	IsSecret    bool    `json:"is_secret"`
}

type organisationVariableEnvelope struct {
	Variable OrganisationVariable `json:"variable"`
}

type organisationVariableListEnvelope struct {
	Variables []OrganisationVariable `json:"variables"`
}

// ListOrganisationVariables returns every variable in the organisation.
func (client *Client) ListOrganisationVariables(ctx context.Context) ([]OrganisationVariable, error) {
	var envelope organisationVariableListEnvelope
	if err := client.doRequest(ctx, http.MethodGet, organisationVariablesPath, nil, &envelope); err != nil {
		return nil, err
	}
	return envelope.Variables, nil
}

// GetOrganisationVariable finds one variable by name in the listing, which is
// the only read the platform offers. A missing name is a NotFound APIError.
func (client *Client) GetOrganisationVariable(ctx context.Context, name string) (*OrganisationVariable, error) {
	variables, err := client.ListOrganisationVariables(ctx)
	if err != nil {
		return nil, err
	}
	for index := range variables {
		if variables[index].Name == name {
			return &variables[index], nil
		}
	}
	return nil, &APIError{Method: http.MethodGet, Path: organisationVariablesPath, StatusCode: http.StatusNotFound, Body: "variable " + name + " not found"}
}

// CreateOrganisationVariable creates a variable.
func (client *Client) CreateOrganisationVariable(ctx context.Context, request OrganisationVariableCreate) (*OrganisationVariable, error) {
	var envelope organisationVariableEnvelope
	if err := client.doRequest(ctx, http.MethodPost, organisationVariablesPath, request, &envelope); err != nil {
		return nil, err
	}
	return &envelope.Variable, nil
}

// UpdateOrganisationVariable replaces a variable's value, description and
// secrecy.
func (client *Client) UpdateOrganisationVariable(ctx context.Context, name string, request OrganisationVariableUpdate) (*OrganisationVariable, error) {
	var envelope organisationVariableEnvelope
	if err := client.doRequest(ctx, http.MethodPut, organisationVariablesPath+"/"+url.PathEscape(name), request, &envelope); err != nil {
		return nil, err
	}
	return &envelope.Variable, nil
}

// DeleteOrganisationVariable deletes a variable. A variable already gone is a
// NotFound APIError: the caller decides whether that counts as deleted, since
// it can also mean the variable is outside the current project scope.
func (client *Client) DeleteOrganisationVariable(ctx context.Context, name string) error {
	return client.doRequest(ctx, http.MethodDelete, organisationVariablesPath+"/"+url.PathEscape(name), nil, nil)
}
