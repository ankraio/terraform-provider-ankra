// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"ankra.io/terraform-provider-ankra/internal/client"
)

// organisationTokenOperations are the token calls the in-place replacement
// needs, for personal and service tokens alike.
type organisationTokenOperations struct {
	rotate func(ctx context.Context, tokenID string) (*client.OrganisationTokenRotated, error)
	revoke func(ctx context.Context, tokenID string) error
	delete func(ctx context.Context, tokenID string) error
}

// organisationTokenLifecycleNotes is the documentation both token resources
// carry about what replaces the token value and when.
const organisationTokenLifecycleNotes = "\n\n" +
	"**What changes the token value.** The platform cannot edit a token, so changing `expires_at`, `scopes` or `project_id` " +
	"(and `name` or `label`) mints a new token value in place: the provider mints the replacement first and only then revokes " +
	"and deletes the old token, so there is no window without a working token, but every holder of the old value must be " +
	"given the new `token`. When the name stays the same, the provider first renews the old token (`-replaced-` suffix, " +
	"24 hours of overlap) to free the name. Nothing else re-mints the token.\n\n" +
	"**Drift.** A token revoked outside Terraform stays in state with `revoked = true` and is replaced on the next apply. " +
	"A token renewed outside Terraform (`ankra tokens rotate`, which renames it `<name>-replaced-<stamp>`) stops every plan " +
	"with an error naming what to do. A change of `expires_at`, `scopes` or `project_id` made outside Terraform re-mints the " +
	"token on the next apply.\n\n" +
	"~> Do not compute `expires_at` from `timestamp()`, for example `timeadd(timestamp(), \"720h\")`: the value changes on " +
	"every plan, so every apply mints a new token. Use a fixed date, or `time_offset` from the `time` provider."

// organisationReplaceToken mints a replacement for an existing token without
// a window in which neither works: when the name is kept it first renews the
// old token, which renames it and frees the name, and retires the renewal's
// own replacement; then it mints, and only then retires the old token. A
// failure to retire the old token is a warning: the new token is live and
// must reach state.
func organisationReplaceToken(ctx context.Context, operations organisationTokenOperations, oldTokenID string, isNameKept bool,
	mint func() (string, error), diagnostics *diag.Diagnostics) bool {
	if isNameKept {
		rotated, err := operations.rotate(ctx, oldTokenID)
		if err != nil {
			diagnostics.AddError("Unable to free the token name for its replacement", err.Error())
			return false
		}
		if err := organisationRetireToken(ctx, operations, rotated.ID); err != nil {
			diagnostics.AddError("Unable to retire the intermediate renewal",
				fmt.Sprintf("Renewing token %s to free its name produced token %s, which could not be revoked and deleted: %s. "+
					"Revoke it with `ankra tokens revoke %s`, then apply again.", oldTokenID, rotated.ID, err, rotated.ID))
			return false
		}
	}
	newTokenID, err := mint()
	if err != nil {
		detail := err.Error()
		if isNameKept {
			detail += fmt.Sprintf("\n\nThe old token %s was renewed to free its name and keeps working for 24 hours; the next plan reports it.", oldTokenID)
		}
		diagnostics.AddError("Unable to mint the replacement token", detail)
		return false
	}
	if err := organisationRetireToken(ctx, operations, oldTokenID); err != nil {
		diagnostics.AddWarning("Old token not retired",
			fmt.Sprintf("The replacement %s is live, but the old token %s could not be revoked and deleted: %s. Revoke it by hand.", newTokenID, oldTokenID, err))
	}
	return true
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

// organisationTokenRevokedReplacement plans a revoked token for replacement:
// it flips the planned revoked flag and marks it as forcing replacement.
func organisationTokenRevokedReplacement(ctx context.Context, state, plan types.Bool, response *resource.ModifyPlanResponse) {
	if state.IsNull() || state.IsUnknown() || !state.ValueBool() || plan.IsNull() {
		return
	}
	response.Diagnostics.Append(response.Plan.SetAttribute(ctx, path.Root("revoked"), false)...)
	response.RequiresReplace = append(response.RequiresReplace, path.Root("revoked"))
}
