// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package client

import (
	"context"
	"net/http"
	"net/url"
)

// OrganisationRoleKubeGrant is one Kubernetes access grant a role carries:
// a cluster-wide or namespaced binding of a built-in Kubernetes role.
type OrganisationRoleKubeGrant struct {
	Scope     string  `json:"scope"`
	Namespace *string `json:"namespace"`
	K8sRole   string  `json:"k8s_role"`
}

// OrganisationCustomRole is a role as the role routes answer it, with its
// Kubernetes grants.
type OrganisationCustomRole struct {
	ID          string                      `json:"id"`
	Slug        string                      `json:"slug"`
	Name        string                      `json:"name"`
	Description string                      `json:"description"`
	Permissions []string                    `json:"permissions"`
	KubeGrants  []OrganisationRoleKubeGrant `json:"kube_grants"`
	Builtin     bool                        `json:"builtin"`
}

// OrganisationCustomRoleWrite is the body of a custom role create or
// update. Slug is sent on create only: the platform never changes it.
type OrganisationCustomRoleWrite struct {
	Slug        string                      `json:"slug,omitempty"`
	Name        string                      `json:"name"`
	Description string                      `json:"description"`
	Permissions []string                    `json:"permissions"`
	KubeGrants  []OrganisationRoleKubeGrant `json:"kube_grants"`
}

type organisationCustomRolesEnvelope struct {
	Roles []OrganisationCustomRole `json:"roles"`
}

const organisationRolesPath = organisationPath + "/roles"

// ListOrganisationCustomRoles lists the built-in and custom roles with their
// Kubernetes grants. It needs members.read.
func (client *Client) ListOrganisationCustomRoles(ctx context.Context) ([]OrganisationCustomRole, error) {
	var envelope organisationCustomRolesEnvelope
	if requestError := client.doRequest(ctx, http.MethodGet, organisationRolesPath, nil, &envelope); requestError != nil {
		return nil, requestError
	}
	return envelope.Roles, nil
}

// CreateOrganisationCustomRole creates a custom role. It needs members.manage.
func (client *Client) CreateOrganisationCustomRole(ctx context.Context, write OrganisationCustomRoleWrite) (*OrganisationCustomRole, error) {
	var role OrganisationCustomRole
	if requestError := client.doRequest(ctx, http.MethodPost, organisationRolesPath, write, &role); requestError != nil {
		return nil, requestError
	}
	return &role, nil
}

// UpdateOrganisationCustomRole replaces a custom role's name, description,
// permissions and Kubernetes grants.
func (client *Client) UpdateOrganisationCustomRole(ctx context.Context, roleID string, write OrganisationCustomRoleWrite) (*OrganisationCustomRole, error) {
	write.Slug = ""
	var role OrganisationCustomRole
	if requestError := client.doRequest(ctx, http.MethodPatch, organisationRolesPath+"/"+url.PathEscape(roleID), write, &role); requestError != nil {
		return nil, requestError
	}
	return &role, nil
}

// DeleteOrganisationCustomRole retires a custom role. While a member still
// holds it the platform answers 409 role_in_use.
func (client *Client) DeleteOrganisationCustomRole(ctx context.Context, roleID string) error {
	return client.doRequest(ctx, http.MethodDelete, organisationRolesPath+"/"+url.PathEscape(roleID), nil, nil)
}
