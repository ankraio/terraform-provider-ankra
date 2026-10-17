// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package client

import (
	"context"
	"net/http"
	"net/url"
)

const secretSlotsPath = "/api/v1/org/secret-slots"

// SecretSlot is one organisation secret slot as the platform lists it. The
// value is never part of any answer: clients only ever see Sentinel, the
// ${SECRET_SLOT:<id>} marker substituted when a manifest is applied.
type SecretSlot struct {
	SlotID          string  `json:"slot_id"`
	Label           string  `json:"label"`
	Hint            *string `json:"hint"`
	Sentinel        string  `json:"sentinel"`
	CreatedVia      string  `json:"created_via"`
	CreatedAt       string  `json:"created_at"`
	ExpiresAt       *string `json:"expires_at"`
	PinnedAt        *string `json:"pinned_at"`
	LastAccessedAt  *string `json:"last_accessed_at"`
	AccessCount     int64   `json:"access_count"`
	CreatedByUserID *string `json:"created_by_user_id"`
}

// SecretSlotCreate is the create body. No ttl is sent: a slot holding a
// value it was given never expires.
type SecretSlotCreate struct {
	Label string  `json:"label"`
	Value string  `json:"value"`
	Hint  *string `json:"hint,omitempty"`
}

type secretSlotValue struct {
	Value string `json:"value"`
}

type secretSlotListEnvelope struct {
	Slots []SecretSlot `json:"slots"`
}

// ListSecretSlots lists the organisation's secret slots, newest first.
func (client *Client) ListSecretSlots(ctx context.Context) ([]SecretSlot, error) {
	var envelope secretSlotListEnvelope
	if err := client.doRequest(ctx, http.MethodGet, secretSlotsPath, nil, &envelope); err != nil {
		return nil, err
	}
	return envelope.Slots, nil
}

// GetSecretSlot finds one slot by id in the listing, the only read the
// platform offers. A missing id is a NotFound APIError.
func (client *Client) GetSecretSlot(ctx context.Context, slotID string) (*SecretSlot, error) {
	slots, err := client.ListSecretSlots(ctx)
	if err != nil {
		return nil, err
	}
	for index := range slots {
		if slots[index].SlotID == slotID {
			return &slots[index], nil
		}
	}
	return nil, &APIError{Method: http.MethodGet, Path: secretSlotsPath, StatusCode: http.StatusNotFound, Body: "secret slot " + slotID + " not found"}
}

// CreateSecretSlot stores a value under a new slot. A label already used in
// the organisation is a 409.
func (client *Client) CreateSecretSlot(ctx context.Context, request SecretSlotCreate) (*SecretSlot, error) {
	var slot SecretSlot
	if err := client.doRequest(ctx, http.MethodPost, secretSlotsPath, request, &slot); err != nil {
		return nil, err
	}
	return &slot, nil
}

// ReplaceSecretSlotValue replaces a slot's value in place, keeping its id, so
// everything referencing the marker receives the new value.
func (client *Client) ReplaceSecretSlotValue(ctx context.Context, slotID, value string) (*SecretSlot, error) {
	var slot SecretSlot
	if err := client.doRequest(ctx, http.MethodPut, secretSlotsPath+"/"+url.PathEscape(slotID), secretSlotValue{Value: value}, &slot); err != nil {
		return nil, err
	}
	return &slot, nil
}

// DeleteSecretSlot deletes a slot. One a live resource still references is
// a 409; one already gone is a NotFound APIError.
func (client *Client) DeleteSecretSlot(ctx context.Context, slotID string) error {
	return client.doRequest(ctx, http.MethodDelete, secretSlotsPath+"/"+url.PathEscape(slotID), nil, nil)
}
