// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
)

// catalogueStringValidator rejects at plan time the values the platform
// refuses deterministically at apply: names outside its grammar, reserved
// names, and enum members it does not know.
type catalogueStringValidator struct {
	description string
	pattern     *regexp.Regexp
	minimum     int
	maximum     int
	allowed     []string
	reserved    []string
	forbidden   []string
}

// The grammars below mirror the platform's own checks
// (go/internal/usecase/organisation/registry_projects.go and
// registry_robots.go, usecase/backupvaults, usecase/objectstoragebuckets,
// usecase/stackprofiles). None of them admits "/", which the API could not
// route in a path segment anyway.
var (
	catalogueRegistryProjectNameValidator = catalogueStringValidator{
		description: "a project name is 2 to 30 lower-case letters, digits and single hyphens, starting and ending on a letter or digit, and is not \"default\"",
		pattern:     regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`),
		minimum:     2,
		maximum:     30,
		reserved:    []string{"default"},
	}
	catalogueRegistryRobotNameValidator = catalogueStringValidator{
		description: "a robot name is 2 to 32 lower-case letters, digits and hyphens, starting and ending on a letter or digit",
		pattern:     regexp.MustCompile(`^[a-z0-9][a-z0-9-]*[a-z0-9]$`),
		maximum:     32,
	}
	catalogueLabelNameValidator = catalogueStringValidator{
		description: "a name is a lower-case label of at most 63 letters, digits and hyphens, starting and ending on a letter or digit",
		pattern:     regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`),
		maximum:     63,
	}
	catalogueBucketNameValidator = catalogueStringValidator{
		description: "a bucket name is an S3 bucket name: 3 to 63 lower-case letters, digits, dots and hyphens",
		pattern:     regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$`),
		maximum:     63,
		forbidden:   []string{".."},
	}
	catalogueLowerCaseUUIDValidator = catalogueStringValidator{
		description: "an application ID is a lower-case UUID, as the platform reports it",
		pattern:     regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`),
	}
	catalogueRobotScopeValidator = catalogueStringValidator{
		description: "scope is push or pull",
		allowed:     []string{"push", "pull"},
	}
	catalogueBackupVaultProviderValidator = catalogueStringValidator{
		description: "provider is one of upcloud, digitalocean, scaleway, ovh, hetzner, aws, gcp, azure or other",
		allowed:     []string{"upcloud", "digitalocean", "scaleway", "ovh", "hetzner", "aws", "gcp", "azure", "other"},
	}
	catalogueEndpointValidator = catalogueStringValidator{
		description: "endpoint is an http(s) URL with no path or trailing slash, like https://fsn1.your-objectstorage.com",
		pattern:     regexp.MustCompile(`^https?://[^/\s]+$`),
	}
	catalogueStackProfileVisibilityValidator = catalogueStringValidator{
		description: "visibility is organisation or public",
		allowed:     []string{"organisation", "public"},
	}
	catalogueDeprecationReasonValidator = catalogueStringValidator{
		description: "reason is incompatibility, critical_bug or cve",
		allowed:     []string{"incompatibility", "critical_bug", "cve"},
	}
	catalogueLogoContentTypeValidator = catalogueStringValidator{
		description: "content_type is image/png, image/jpeg or image/webp",
		allowed:     []string{"image/png", "image/jpeg", "image/webp"},
	}
)

func (catalogueValidator catalogueStringValidator) Description(_ context.Context) string {
	return catalogueValidator.description
}

func (catalogueValidator catalogueStringValidator) MarkdownDescription(ctx context.Context) string {
	return catalogueValidator.Description(ctx)
}

func (catalogueValidator catalogueStringValidator) ValidateString(_ context.Context, request validator.StringRequest, response *validator.StringResponse) {
	if request.ConfigValue.IsNull() || request.ConfigValue.IsUnknown() {
		return
	}
	value := request.ConfigValue.ValueString()
	if !catalogueValidator.accepts(value) {
		response.Diagnostics.AddAttributeError(request.Path, "Invalid value",
			fmt.Sprintf("%q is refused by the platform: %s.", value, catalogueValidator.description))
	}
}

func (catalogueValidator catalogueStringValidator) accepts(value string) bool {
	if len(value) < catalogueValidator.minimum {
		return false
	}
	if catalogueValidator.maximum > 0 && len(value) > catalogueValidator.maximum {
		return false
	}
	if catalogueValidator.pattern != nil && !catalogueValidator.pattern.MatchString(value) {
		return false
	}
	for _, reserved := range catalogueValidator.reserved {
		if value == reserved {
			return false
		}
	}
	for _, forbidden := range catalogueValidator.forbidden {
		if strings.Contains(value, forbidden) {
			return false
		}
	}
	if len(catalogueValidator.allowed) == 0 {
		return true
	}
	for _, allowed := range catalogueValidator.allowed {
		if value == allowed {
			return true
		}
	}
	return false
}

// catalogueRobotDescriptionValidator mirrors the platform's robot
// description rules: at most 200 characters, and trimmed. A description with
// surrounding whitespace would read back trimmed and replace the robot on
// every apply.
type catalogueRobotDescriptionValidator struct{}

func (catalogueRobotDescriptionValidator) Description(_ context.Context) string {
	return "description is at most 200 characters with no leading or trailing whitespace"
}

func (descriptionValidator catalogueRobotDescriptionValidator) MarkdownDescription(ctx context.Context) string {
	return descriptionValidator.Description(ctx)
}

func (descriptionValidator catalogueRobotDescriptionValidator) ValidateString(ctx context.Context, request validator.StringRequest, response *validator.StringResponse) {
	if request.ConfigValue.IsNull() || request.ConfigValue.IsUnknown() {
		return
	}
	value := request.ConfigValue.ValueString()
	if strings.TrimSpace(value) != value || len([]rune(value)) > 200 {
		response.Diagnostics.AddAttributeError(request.Path, "Invalid value",
			fmt.Sprintf("%q is refused by the platform: %s.", value, descriptionValidator.Description(ctx)))
	}
}
