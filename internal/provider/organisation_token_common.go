// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"ankra.io/terraform-provider-ankra/internal/client"
)

// organisationTokenOperations are the token calls the in-place replacement
// needs, for personal and service tokens alike. bridge is set for service
// tokens only: it mints a short-lived token on the same account, so the
// account is never left without one while a revoked token is cleared away.
type organisationTokenOperations struct {
	rotate func(ctx context.Context, tokenID string) (*client.OrganisationTokenRotated, error)
	revoke func(ctx context.Context, tokenID string) error
	delete func(ctx context.Context, tokenID string) error
	bridge func(ctx context.Context) (string, error)
}

// organisationTokenCondition is what the platform holds for the token in
// state when a re-mint starts.
type organisationTokenCondition int

const (
	// organisationTokenLive is a working token under its own name.
	organisationTokenLive organisationTokenCondition = iota
	// organisationTokenGone is a token the platform no longer lists.
	organisationTokenGone
	// organisationTokenRevoked is a revoked token, which still holds its name.
	organisationTokenRevoked
	// organisationTokenRenamed is a token an interrupted re-mint renewed: its
	// name is free and it keeps working until the overlap ends.
	organisationTokenRenamed
)

// organisationTokenLifecyclePrivateKey holds the re-mint bookkeeping a token
// resource keeps between applies.
const organisationTokenLifecyclePrivateKey = "ankra_token_lifecycle"

// organisationTokenLifecycle is that bookkeeping. Rotating names the token a
// re-mint renewed to free its name, set before the renewal and cleared once
// the replacement is minted, so a later read can tell an interrupted re-mint
// from a renewal made outside Terraform. PendingRetire lists replaced tokens
// whose revoke or delete failed; every later apply retries them.
type organisationTokenLifecycle struct {
	Rotating      string   `json:"rotating,omitempty"`
	PendingRetire []string `json:"pending_retire,omitempty"`
}

func organisationReadTokenLifecycle(ctx context.Context, private privateStateReader) organisationTokenLifecycle {
	var lifecycle organisationTokenLifecycle
	if private == nil {
		return lifecycle
	}
	encoded, diagnostics := private.GetKey(ctx, organisationTokenLifecyclePrivateKey)
	if diagnostics.HasError() || len(encoded) == 0 {
		return lifecycle
	}
	_ = json.Unmarshal(encoded, &lifecycle)
	return lifecycle
}

func organisationWriteTokenLifecycle(ctx context.Context, lifecycle organisationTokenLifecycle, private privateStateWriter) diag.Diagnostics {
	if private == nil {
		return nil
	}
	if lifecycle.Rotating == "" && len(lifecycle.PendingRetire) == 0 {
		return private.SetKey(ctx, organisationTokenLifecyclePrivateKey, nil)
	}
	encoded, err := json.Marshal(lifecycle)
	if err != nil {
		return nil
	}
	return private.SetKey(ctx, organisationTokenLifecyclePrivateKey, encoded)
}

// organisationTokenLifecycleNotes is the documentation both token resources
// carry about what replaces the token value and when.
const organisationTokenLifecycleNotes = "\n\n" +
	"**What changes the token value.** The platform cannot edit a token, so changing `expires_at`, `scopes` or `project_id` " +
	"(and `name` or `label`) mints a new token value in place: the provider mints the replacement first and only then revokes " +
	"and deletes the old token, so there is no window without a working token, but every holder of the old value must be " +
	"given the new `token`. When the name stays the same, the provider first renews the old token (`-replaced-` suffix, " +
	"24 hours of overlap) to free the name. If minting then fails, the next apply finishes the re-mint. An old token that " +
	"could not be revoked and deleted is retried on every later apply until it is gone. Nothing else re-mints the token.\n\n" +
	"**Drift.** A token revoked outside Terraform stays in state with `revoked = true` and is re-minted in place on the next " +
	"apply (a revoked token cannot be renewed, so a personal token's revoked predecessor is deleted first, and a service " +
	"token's account is kept alive by a short-lived bridge token meanwhile). A token renewed outside Terraform " +
	"(`ankra tokens rotate`, which renames it `<name>-replaced-<stamp>`) stops every plan with an error naming what to do. " +
	"A change of `expires_at`, `scopes` or `project_id` made outside Terraform re-mints the token on the next apply.\n\n" +
	"~> Do not compute `expires_at` from `timestamp()`, for example `timeadd(timestamp(), \"720h\")`: the value changes on " +
	"every plan, so every apply mints a new token. Use a fixed date, or `time_offset` from the `time` provider."

// organisationReplaceToken mints a replacement for the token in state
// without leaving a window in which neither works, and without ever leaving a
// service account without a token:
//
//   - a live token whose name is kept is renewed first, which renames it and
//     frees the name; the renewal's own replacement is retired; the marker in
//     lifecycle records the renewal before it happens;
//   - a revoked token keeps its name and cannot be renewed, so it is retired
//     before the mint, with a bridge token holding a service account open;
//   - a renamed token (an interrupted earlier re-mint) or a gone one has
//     already freed the name.
//
// The old token is retired after the mint; a failure there is recorded in
// lifecycle for the next apply and reported as a warning, because the new
// token is live and must reach state. A bridge token goes into
// lifecycle.PendingRetire at once: the caller retires pending tokens only
// after a successful mint, so a failed mint never strands an account without
// a token. On an error the caller keeps the prior state and must persist
// lifecycle.
func organisationReplaceToken(ctx context.Context, operations organisationTokenOperations, lifecycle *organisationTokenLifecycle,
	oldTokenID string, condition organisationTokenCondition, isNameKept bool, mint func() (string, error),
	diagnostics *diag.Diagnostics) bool {
	switch {
	case condition == organisationTokenRevoked:
		if operations.bridge != nil {
			bridged, err := operations.bridge(ctx)
			if err != nil {
				diagnostics.AddError("Unable to mint a bridge token for the account", err.Error())
				return false
			}
			lifecycle.PendingRetire = append(lifecycle.PendingRetire, bridged)
		}
		if err := organisationRetireToken(ctx, operations, oldTokenID); err != nil {
			diagnostics.AddError("Unable to clear the revoked token", err.Error())
			return false
		}
	case condition == organisationTokenLive && isNameKept:
		lifecycle.Rotating = oldTokenID
		rotated, err := operations.rotate(ctx, oldTokenID)
		if err != nil {
			lifecycle.Rotating = ""
			diagnostics.AddError("Unable to free the token name for its replacement", err.Error())
			return false
		}
		if err := organisationRetireToken(ctx, operations, rotated.ID); err != nil {
			lifecycle.PendingRetire = append(lifecycle.PendingRetire, rotated.ID)
			diagnostics.AddError("Unable to retire the intermediate renewal",
				fmt.Sprintf("Renewing token %s to free its name produced token %s, which could not be revoked and deleted: %s. "+
					"The next apply retries it and finishes the re-mint.", oldTokenID, rotated.ID, err))
			return false
		}
	}

	newTokenID, err := mint()
	if err != nil {
		detail := err.Error()
		if lifecycle.Rotating != "" {
			detail += fmt.Sprintf("\n\nThe old token %s was renewed to free its name and keeps working for 24 hours; the next apply finishes the re-mint.", oldTokenID)
		}
		diagnostics.AddError("Unable to mint the replacement token", detail)
		return false
	}
	lifecycle.Rotating = ""
	if condition != organisationTokenRevoked && condition != organisationTokenGone {
		if err := organisationRetireToken(ctx, operations, oldTokenID); err != nil {
			lifecycle.PendingRetire = append(lifecycle.PendingRetire, oldTokenID)
			diagnostics.AddWarning("Old token not retired yet",
				fmt.Sprintf("The replacement %s is live, but the old token %s could not be revoked and deleted: %s. The next apply retries it.", newTokenID, oldTokenID, err))
		}
	}
	return true
}

// organisationRetirePending retries the tokens an earlier apply could not
// retire and keeps the ones that still fail.
func organisationRetirePending(ctx context.Context, operations organisationTokenOperations, lifecycle *organisationTokenLifecycle, diagnostics *diag.Diagnostics) {
	remaining := []string{}
	for _, tokenID := range lifecycle.PendingRetire {
		if err := organisationRetireToken(ctx, operations, tokenID); err != nil {
			remaining = append(remaining, tokenID)
			diagnostics.AddWarning("Old token still not retired", fmt.Sprintf("Token %s could not be revoked and deleted: %s. The next apply retries it.", tokenID, err))
		}
	}
	lifecycle.PendingRetire = remaining
}

// organisationRetireToken revokes and deletes a token. Revoking one already
// revoked is accepted by the client, so a half-finished earlier attempt is
// finished here; a token already gone counts as retired.
func organisationRetireToken(ctx context.Context, operations organisationTokenOperations, tokenID string) error {
	if err := operations.revoke(ctx, tokenID); err != nil && !client.IsNotFound(err) {
		return err
	}
	if err := operations.delete(ctx, tokenID); err != nil && !client.IsNotFound(err) {
		return err
	}
	return nil
}

// organisationTokenRenewedError is the diagnostic for a token renewed outside
// Terraform: the platform renamed it and minted a replacement Terraform
// does not know about.
func organisationTokenRenewedError(resourceType, tokenID, listedName string) (string, string) {
	return "Token renewed outside Terraform",
		fmt.Sprintf("Token %s is now named %q: it was renewed outside Terraform (`ankra tokens rotate`), and its replacement holds "+
			"the original name and a new value Terraform never saw. Either import the replacement "+
			"(`terraform state rm` this %s, then `terraform import` the replacement's id), or revoke the replacement and "+
			"`terraform state rm` this resource so the next apply mints a fresh token.", tokenID, listedName, resourceType)
}

// organisationTokenIsRenamed reports whether a listed name carries the
// renewal marker.
func organisationTokenIsRenamed(name string) bool {
	return strings.Contains(name, organisationTokenReplacedMarker)
}

// organisationTokenPlanRemint plans the in-place re-mint of a token marked
// revoked: the planned flag flips back, so the apply runs Update, which mints
// before it clears the old token. It never forces a replacement, because a
// destroy before the create would delete a service account's last token and
// stand the account down.
//
// An expiry left to the platform is planned anew too: the old token's own
// expiry may be close or past, so the re-mint takes the platform's default.
func organisationTokenPlanRemint(ctx context.Context, state types.Bool, request resource.ModifyPlanRequest, response *resource.ModifyPlanResponse) bool {
	if state.IsNull() || state.IsUnknown() || !state.ValueBool() {
		return false
	}
	var configuredExpiry types.String
	response.Diagnostics.Append(request.Config.GetAttribute(ctx, path.Root("expires_at"), &configuredExpiry)...)
	if configuredExpiry.IsNull() {
		response.Diagnostics.Append(response.Plan.SetAttribute(ctx, path.Root("expires_at"), types.StringUnknown())...)
	}
	response.Diagnostics.Append(response.Plan.SetAttribute(ctx, path.Root("revoked"), false)...)
	response.Diagnostics.Append(response.Plan.SetAttribute(ctx, path.Root("id"), types.StringUnknown())...)
	response.Diagnostics.Append(response.Plan.SetAttribute(ctx, path.Root("token"), types.StringUnknown())...)
	return true
}

// organisationTokenPlanPendingRetire plans an update while an earlier apply
// left old tokens to retire, so the next apply retries them even when nothing
// else changes.
func organisationTokenPlanPendingRetire(ctx context.Context, request resource.ModifyPlanRequest, response *resource.ModifyPlanResponse) {
	if len(organisationReadTokenLifecycle(ctx, request.Private).PendingRetire) == 0 {
		return
	}
	response.Diagnostics.Append(response.Plan.SetAttribute(ctx, path.Root("revoked"), types.BoolUnknown())...)
}
