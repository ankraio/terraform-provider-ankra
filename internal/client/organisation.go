// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package client

import (
	"context"
	"errors"
	"net/http"
	"net/url"
)

const organisationPath = "/api/v1/org/organisation"

// OrganisationSummary is one row of the organisations the token's user
// belongs to. UserCurrent marks the organisation the request acts in.
type OrganisationSummary struct {
	OrganisationID string  `json:"organisation_id"`
	Name           *string `json:"name"`
	Slug           *string `json:"slug"`
	UserCurrent    bool    `json:"user_current"`
	Status         *string `json:"status"`
	Role           *string `json:"role"`
}

// OrganisationMember is one row of an organisation's roster. A pending invite
// already carries the invitee's user id: the platform creates the account
// when it sends the invite.
type OrganisationMember struct {
	ID                   *string `json:"id"`
	InviteID             *string `json:"invite_id"`
	Email                *string `json:"email"`
	Status               *string `json:"status"`
	Role                 *string `json:"role"`
	ServicePrincipalKind *string `json:"service_principal_kind"`
	ServicePrincipalName *string `json:"service_principal_name"`
}

// OrganisationDetail is the organisation with its roster.
type OrganisationDetail struct {
	OrganisationID string               `json:"organisation_id"`
	Name           *string              `json:"name"`
	Slug           *string              `json:"slug"`
	CreatedAt      *string              `json:"created_at"`
	Users          []OrganisationMember `json:"users"`
	MFARequired    bool                 `json:"mfa_required"`
	UserIsAdmin    bool                 `json:"user_is_admin"`
}

// OrganisationCreate is the create body. Country is left out when nil.
type OrganisationCreate struct {
	Name    string  `json:"name"`
	Country *string `json:"country,omitempty"`
}

// OrganisationCreated is the create answer.
type OrganisationCreated struct {
	OrganisationID string `json:"organisation_id"`
	Slug           string `json:"slug"`
	Message        string `json:"message"`
}

// OrganisationInvite is the invite body. Role is one of member, admin or
// read-only.
type OrganisationInvite struct {
	OrganisationID string `json:"organisation_id"`
	InviteeEmail   string `json:"invitee_email"`
	Role           string `json:"role"`
}

// OrganisationInvited is the invite answer. PendingUser is the invitee's
// roster row, read-your-writes, so the caller need not wait for a replica.
type OrganisationInvited struct {
	Success     bool                `json:"success"`
	Message     string              `json:"message"`
	PendingUser *OrganisationMember `json:"pending_user"`
}

type organisationMemberRemoval struct {
	UserID         string `json:"user_id"`
	OrganisationID string `json:"organisation_id"`
}

// ListOrganisations returns the organisations the token's user is an active
// member of.
func (client *Client) ListOrganisations(ctx context.Context) ([]OrganisationSummary, error) {
	var organisations []OrganisationSummary
	if err := client.doRequest(ctx, http.MethodGet, organisationPath, nil, &organisations); err != nil {
		return nil, err
	}
	return organisations, nil
}

// GetOrganisation returns one organisation with its roster. An organisation
// the user is not an active member of, or that was deleted, is a NotFound
// APIError.
func (client *Client) GetOrganisation(ctx context.Context, organisationID string) (*OrganisationDetail, error) {
	var detail OrganisationDetail
	if err := client.doRequest(ctx, http.MethodGet, organisationPath+"/"+url.PathEscape(organisationID), nil, &detail); err != nil {
		return nil, err
	}
	return &detail, nil
}

// CreateOrganisation creates an organisation with the token's user as its
// admin. The platform also makes it that user's current organisation.
func (client *Client) CreateOrganisation(ctx context.Context, request OrganisationCreate) (*OrganisationCreated, error) {
	var created OrganisationCreated
	if err := client.doRequest(ctx, http.MethodPost, organisationPath, request, &created); err != nil {
		return nil, err
	}
	return &created, nil
}

// InviteOrganisationMember invites an email address to an organisation.
func (client *Client) InviteOrganisationMember(ctx context.Context, request OrganisationInvite) (*OrganisationInvited, error) {
	var invited OrganisationInvited
	if err := client.doRequest(ctx, http.MethodPost, organisationPath+"/invite", request, &invited); err != nil {
		return nil, err
	}
	return &invited, nil
}

// RemoveOrganisationMember removes a member or a pending invite by user id.
// A membership already gone is not an error.
func (client *Client) RemoveOrganisationMember(ctx context.Context, organisationID, userID string) error {
	body := organisationMemberRemoval{UserID: userID, OrganisationID: organisationID}
	return client.doRequest(ctx, http.MethodDelete, organisationPath+"/user", body, nil, http.StatusNotFound)
}

// CurrentOrganisationID is the organisation requests act in: the provider's
// organisation override when set, otherwise the organisation the token's
// listing marks current.
func (client *Client) CurrentOrganisationID(ctx context.Context) (string, error) {
	if client.OrganisationID != "" {
		return client.OrganisationID, nil
	}
	organisations, err := client.ListOrganisations(ctx)
	if err != nil {
		return "", err
	}
	for _, organisation := range organisations {
		if organisation.UserCurrent {
			return organisation.OrganisationID, nil
		}
	}
	return "", errors.New("the platform marks none of the token's organisations as current; set organisation_id on the provider")
}
