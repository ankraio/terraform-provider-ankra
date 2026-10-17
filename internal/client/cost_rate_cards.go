// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package client

import (
	"context"
	"net/http"
	"net/url"
)

const costRateCardsPath = "/api/v1/org/cloud-cost/rate-cards"

// CostRateCardOrganisation is the key of the organisation's fallback card,
// which prices private-cloud clusters whose credential has no card of its
// own.
const CostRateCardOrganisation = "organisation"

// CostRateCardSourceCredential marks a credential priced by its own card.
const CostRateCardSourceCredential = "credential"

// CostRateCardRates are the monthly amounts a card charges, as decimal
// strings in the card's currency.
type CostRateCardRates struct {
	Currency               string `json:"currency"`
	VCPUMonthlyAmount      string `json:"vcpu_monthly_amount"`
	MemoryGBMonthlyAmount  string `json:"memory_gb_monthly_amount"`
	StorageGBMonthlyAmount string `json:"storage_gb_monthly_amount"`
}

// CostRateCardCredential is one private-cloud credential with the card that
// prices it. Source says where its rates come from; only "credential" means
// it has a card of its own.
type CostRateCardCredential struct {
	CredentialID   string `json:"credential_id"`
	CredentialName string `json:"credential_name"`
	Provider       string `json:"provider"`
	Source         string `json:"source"`
	CostRateCardRates
}

// CostRateCards is the organisation's rate card overview.
type CostRateCards struct {
	Currency         string                   `json:"currency"`
	OrganisationCard *CostRateCardRates       `json:"organisation_card"`
	Credentials      []CostRateCardCredential `json:"credentials"`
}

// CostRateCardWrite stores one card. A nil CredentialID writes the
// organisation's fallback card.
type CostRateCardWrite struct {
	CredentialID *string `json:"credential_id"`
	CostRateCardRates
}

// GetCostRateCards reads the organisation's rate cards.
func (client *Client) GetCostRateCards(ctx context.Context) (*CostRateCards, error) {
	var cards CostRateCards
	if err := client.doRequest(ctx, http.MethodGet, costRateCardsPath, nil, &cards); err != nil {
		return nil, err
	}
	return &cards, nil
}

// GetCostRateCard finds one card in the overview, the only read the
// platform offers: the organisation card when credentialID is empty,
// otherwise the credential's own card. A card not set is a NotFound
// APIError.
func (client *Client) GetCostRateCard(ctx context.Context, credentialID string) (*CostRateCardRates, error) {
	cards, err := client.GetCostRateCards(ctx)
	if err != nil {
		return nil, err
	}
	if credentialID == "" {
		if cards.OrganisationCard != nil {
			return cards.OrganisationCard, nil
		}
	} else {
		for index := range cards.Credentials {
			credential := cards.Credentials[index]
			if credential.CredentialID == credentialID && credential.Source == CostRateCardSourceCredential {
				return &credential.CostRateCardRates, nil
			}
		}
	}
	key := credentialID
	if key == "" {
		key = CostRateCardOrganisation
	}
	return nil, &APIError{Method: http.MethodGet, Path: costRateCardsPath, StatusCode: http.StatusNotFound, Body: "rate card " + key + " not set"}
}

// PutCostRateCard creates or replaces one card. Writing it needs
// organisation admin.
func (client *Client) PutCostRateCard(ctx context.Context, request CostRateCardWrite) (*CostRateCardRates, error) {
	var rates CostRateCardRates
	if err := client.doRequest(ctx, http.MethodPut, costRateCardsPath, request, &rates); err != nil {
		return nil, err
	}
	return &rates, nil
}

// DeleteCostRateCard removes one card; an empty credentialID removes the
// organisation card. Removing a card not set is not an error.
func (client *Client) DeleteCostRateCard(ctx context.Context, credentialID string) error {
	key := credentialID
	if key == "" {
		key = CostRateCardOrganisation
	}
	return client.doRequest(ctx, http.MethodDelete, costRateCardsPath+"/"+url.PathEscape(key), nil, nil)
}
