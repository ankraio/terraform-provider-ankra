// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"encoding/json"
	"regexp"
	"sort"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
)

// organisationManagedKeysPrivateKey holds, for a singleton settings resource,
// the API fields its configuration set, which are the only ones destroy
// resets.
const organisationManagedKeysPrivateKey = "ankra_managed_settings"

// organisationRecordManagedKeys stores the fields a write named.
func organisationRecordManagedKeys(ctx context.Context, body map[string]any, private privateStateWriter) diag.Diagnostics {
	keys := make([]string, 0, len(body))
	for key := range body {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	encoded, err := json.Marshal(keys)
	if err != nil {
		return nil
	}
	return private.SetKey(ctx, organisationManagedKeysPrivateKey, encoded)
}

// organisationManagedKeys reads the fields the configuration last set.
func organisationManagedKeys(ctx context.Context, private privateStateReader) []string {
	encoded, diagnostics := private.GetKey(ctx, organisationManagedKeysPrivateKey)
	if diagnostics.HasError() || len(encoded) == 0 {
		return nil
	}
	var keys []string
	if err := json.Unmarshal(encoded, &keys); err != nil {
		return nil
	}
	return keys
}

// organisationResetBody is the write that puts the managed fields back to
// their platform defaults; a field without a default is left as it is.
func organisationResetBody(managed []string, defaults map[string]any) map[string]any {
	body := map[string]any{}
	for _, key := range managed {
		if value, hasDefault := defaults[key]; hasDefault {
			body[key] = value
		}
	}
	return body
}

// organisationSetNotEmpty refuses an empty set: the platform reads an empty
// scope list as no restriction at all, so `scopes = []` would mint a
// full-authority token while state showed it restricted.
type organisationSetNotEmpty struct{}

func (organisationSetNotEmpty) Description(_ context.Context) string {
	return "set must name at least one element; leave it unset for no restriction"
}

func (notEmpty organisationSetNotEmpty) MarkdownDescription(ctx context.Context) string {
	return notEmpty.Description(ctx)
}

func (organisationSetNotEmpty) ValidateSet(_ context.Context, request validator.SetRequest, response *validator.SetResponse) {
	if request.ConfigValue.IsNull() || request.ConfigValue.IsUnknown() {
		return
	}
	if len(request.ConfigValue.Elements()) == 0 {
		response.Diagnostics.AddAttributeError(request.Path, "Empty scopes",
			"An empty scope list grants the token its holder's full authority, exactly like leaving scopes unset. Leave it unset, or name at least one scope.")
	}
}

// organisationTokenReplacedMarker is the infix the platform gives a token it
// renewed (enginekit/tokenrotation).
const organisationTokenReplacedMarker = "-replaced-"

var organisationTokenNamePattern = regexp.MustCompile(`^[a-zA-Z0-9\s\-_.]+$`)

// organisationTokenNameValidator mirrors the token name rule both token
// surfaces enforce: 1 to 100 letters, digits, spaces, "-", "_" or ".", and
// never the "-replaced-" marker the platform reserves for a renewed token.
type organisationTokenNameValidator struct{}

func (organisationTokenNameValidator) Description(_ context.Context) string {
	return "must be 1 to 100 letters, digits, spaces, \"-\", \"_\" or \".\", without \"-replaced-\""
}

func (nameValidator organisationTokenNameValidator) MarkdownDescription(ctx context.Context) string {
	return nameValidator.Description(ctx)
}

func (nameValidator organisationTokenNameValidator) ValidateString(ctx context.Context, request validator.StringRequest, response *validator.StringResponse) {
	if request.ConfigValue.IsNull() || request.ConfigValue.IsUnknown() {
		return
	}
	name := request.ConfigValue.ValueString()
	if len([]rune(name)) > 100 || !organisationTokenNamePattern.MatchString(name) || strings.Contains(name, organisationTokenReplacedMarker) {
		response.Diagnostics.AddAttributeError(request.Path, "Invalid token name", "The value "+nameValidator.Description(ctx)+".")
	}
}
