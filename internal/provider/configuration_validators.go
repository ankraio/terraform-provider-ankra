// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
)

// configurationPathNameValidator rejects at plan time an empty name or one
// containing "/". The platform addresses Helm registries, registry
// credentials, cluster variables and stacks by name in the URL path and does
// not route an escaped slash, so such an object could be created but never
// read, updated or deleted; a slash would also make the composite import ID
// ambiguous.
type configurationPathNameValidator struct{}

func (configurationPathNameValidator) Description(_ context.Context) string {
	return "name must not be empty or contain \"/\""
}

func (nameValidator configurationPathNameValidator) MarkdownDescription(ctx context.Context) string {
	return nameValidator.Description(ctx)
}

func (configurationPathNameValidator) ValidateString(_ context.Context, request validator.StringRequest, response *validator.StringResponse) {
	if request.ConfigValue.IsNull() || request.ConfigValue.IsUnknown() {
		return
	}
	name := request.ConfigValue.ValueString()
	if strings.TrimSpace(name) == "" {
		response.Diagnostics.AddAttributeError(request.Path, "Invalid name", "The name must not be empty.")
		return
	}
	if strings.Contains(name, "/") {
		response.Diagnostics.AddAttributeError(request.Path, "Invalid name",
			"Names cannot contain \"/\": the platform could not read, update or delete the object afterwards.")
	}
}

// helmReservedNameValidator rejects at plan time the Helm registry and
// registry credential names the platform owns (cluster
// enginekit/harborclient/lane.go: IsReservedRegistryName and
// IsPlatformManagedCredentialName). A reserved registry name is refused on
// create; a managed credential name cannot be updated or deleted.
type helmReservedNameValidator struct {
	isCredential bool
}

const (
	helmReservedRegistryName                = "ankra-harbor"
	helmReservedCredentialCI                = "ankra-harbor-ci"
	helmReservedCredentialPull              = "ankra-harbor-pull"
	helmReservedCredentialApplicationPrefix = "ankra-harbor-app-"
	helmReservedCredentialRobotPrefix       = "ankra-harbor-robot-"
)

func (helmReservedNameValidator) Description(_ context.Context) string {
	return "name must not be one the platform's Ankra registry owns"
}

func (nameValidator helmReservedNameValidator) MarkdownDescription(ctx context.Context) string {
	return nameValidator.Description(ctx)
}

func (nameValidator helmReservedNameValidator) ValidateString(_ context.Context, request validator.StringRequest, response *validator.StringResponse) {
	if request.ConfigValue.IsNull() || request.ConfigValue.IsUnknown() {
		return
	}
	name := strings.TrimSpace(request.ConfigValue.ValueString())
	if nameValidator.isCredential && isHelmReservedCredentialName(name) {
		response.Diagnostics.AddAttributeError(request.Path, "Reserved credential name",
			"\""+name+"\" is a registry login the platform manages; it cannot be created, updated or deleted here.")
		return
	}
	if !nameValidator.isCredential && strings.EqualFold(name, helmReservedRegistryName) {
		response.Diagnostics.AddAttributeError(request.Path, "Reserved registry name",
			"\""+name+"\" is reserved for the organisation's Ankra registry.")
	}
}

func isHelmReservedCredentialName(name string) bool {
	if name == helmReservedCredentialCI || name == helmReservedCredentialPull {
		return true
	}
	for _, prefix := range []string{helmReservedCredentialApplicationPrefix, helmReservedCredentialRobotPrefix} {
		if strings.HasPrefix(name, prefix) && len(name) > len(prefix) {
			return true
		}
	}
	return false
}

// configurationSplitImportID splits a composite import ID on "/" into exactly
// the named parts, none of them empty.
func configurationSplitImportID(importID string, partNames ...string) ([]string, error) {
	expected := strings.Join(partNames, "/")
	parts := strings.Split(importID, "/")
	if len(parts) != len(partNames) {
		return nil, fmt.Errorf("expected an import ID of the form %s, got %q", expected, importID)
	}
	for index, part := range parts {
		if strings.TrimSpace(part) == "" {
			return nil, fmt.Errorf("expected an import ID of the form %s, got %q: %s is empty", expected, importID, partNames[index])
		}
	}
	return parts, nil
}
