// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/objectplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"ankra.io/terraform-provider-ankra/internal/client"
)

// credentialImportedKey marks, in private state, a credential that was
// imported and whose write-only settings have not been applied yet. The
// platform never returns a credential's secrets or connection settings, so
// an imported credential's state holds them as null; the first apply adopts
// the configured values instead of replacing the credential.
const credentialImportedKey = "credential_imported"

const credentialReplaceDescription = "The platform cannot change this setting on an existing credential, so changing it replaces the credential. " +
	"Right after an import the configured value is adopted without a replacement."

// credentialOperationalNotes are the permission, project and cost notes every
// credential resource documents.
const credentialOperationalNotes = "\n\n" +
	"**Permissions.** The API token needs `credentials.read` as well as `credentials.write` (and `credentials.delete` to destroy): " +
	"the provider reads the credential listing right after a create to fill in the read-only attributes. " +
	"If that read fails, the credential is kept in state and marked tainted, so run `terraform untaint` to keep it. " +
	"(A platform from before 2026-10-18 does not answer a create with the credential's id; there the error names the `terraform import` to run.)\n\n" +
	"**Projects.** A credential belongs to the provider's project. After the provider's `project_id` changes, a credential from the old " +
	"project is reported as not visible in the current scope; it is neither recreated nor reported destroyed.\n\n" +
	"**Refresh cost.** Every refresh reads the credential listing for this provider. For AWS credentials the platform reads each AWS " +
	"credential's secret to fill in `scope` and `auth_method`, so a refresh costs one secret read per AWS credential in the organisation."

type credentialPrivateReader interface {
	GetKey(ctx context.Context, key string) ([]byte, diag.Diagnostics)
}

// credentialRequiresReplace decides a replacement for a changed write-only
// setting: always, except when the state holds no value because the
// credential was just imported.
func credentialRequiresReplace(ctx context.Context, stateIsNull bool, private credentialPrivateReader) bool {
	if !stateIsNull {
		return true
	}
	marker, _ := private.GetKey(ctx, credentialImportedKey)
	return len(marker) == 0
}

func credentialReplaceString() planmodifier.String {
	return stringplanmodifier.RequiresReplaceIf(func(ctx context.Context, request planmodifier.StringRequest, response *stringplanmodifier.RequiresReplaceIfFuncResponse) {
		response.RequiresReplace = credentialRequiresReplace(ctx, request.StateValue.IsNull(), request.Private)
	}, credentialReplaceDescription, credentialReplaceDescription)
}

func credentialReplaceBool() planmodifier.Bool {
	return boolplanmodifier.RequiresReplaceIf(func(ctx context.Context, request planmodifier.BoolRequest, response *boolplanmodifier.RequiresReplaceIfFuncResponse) {
		response.RequiresReplace = credentialRequiresReplace(ctx, request.StateValue.IsNull(), request.Private)
	}, credentialReplaceDescription, credentialReplaceDescription)
}

func credentialReplaceObject() planmodifier.Object {
	return objectplanmodifier.RequiresReplaceIf(func(ctx context.Context, request planmodifier.ObjectRequest, response *objectplanmodifier.RequiresReplaceIfFuncResponse) {
		response.RequiresReplace = credentialRequiresReplace(ctx, request.StateValue.IsNull(), request.Private)
	}, credentialReplaceDescription, credentialReplaceDescription)
}

// credentialListedAttributes are the read-only attributes every credential
// resource refreshes from the platform's credential listing.
func credentialListedAttributes() map[string]schema.Attribute {
	return map[string]schema.Attribute{
		"id": schema.StringAttribute{
			MarkdownDescription: "Identifier of the credential: the value cluster resources take as `credential_id` or `ssh_key_credential_id`.",
			Computed:            true,
			PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
		},
		"display_name": schema.StringAttribute{
			MarkdownDescription: "Label shown instead of the name in the Ankra portal. Changed in place. " +
				"Left unset, the label set in the portal (if any) is kept and read back; removing it from the configuration does not clear it.",
			Optional:      true,
			Computed:      true,
			PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			Validators:    []validator.String{credentialDisplayNameValidator{}},
		},
		"available": schema.BoolAttribute{
			MarkdownDescription: "Whether the platform considers the credential usable.",
			Computed:            true,
			PlanModifiers:       []planmodifier.Bool{boolplanmodifier.UseStateForUnknown()},
		},
		"state": schema.StringAttribute{
			MarkdownDescription: "Lifecycle state the platform reports, such as `up`.",
			Computed:            true,
			PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
		},
		"created_at": schema.StringAttribute{
			MarkdownDescription: "When the credential was created.",
			Computed:            true,
			PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
		},
	}
}

// credentialNameAttribute is the name every credential carries. Names are
// unique per provider within an organisation, and the token API cannot
// rename a credential.
func credentialNameAttribute() schema.StringAttribute {
	return schema.StringAttribute{
		MarkdownDescription: "Credential name, unique per provider in the organisation. Changing it replaces the credential.",
		Required:            true,
		PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
	}
}

// credentialSetListed writes the listing's view of a credential into state.
func credentialSetListed(ctx context.Context, state *tfsdk.State, credential *client.Credential) diag.Diagnostics {
	var diagnostics diag.Diagnostics
	diagnostics.Append(state.SetAttribute(ctx, path.Root("id"), types.StringValue(credential.ID))...)
	diagnostics.Append(state.SetAttribute(ctx, path.Root("name"), types.StringValue(credential.Name))...)
	diagnostics.Append(state.SetAttribute(ctx, path.Root("display_name"), types.StringPointerValue(credential.DisplayName))...)
	diagnostics.Append(state.SetAttribute(ctx, path.Root("available"), types.BoolValue(credential.Available))...)
	diagnostics.Append(state.SetAttribute(ctx, path.Root("state"), types.StringPointerValue(credential.State))...)
	diagnostics.Append(state.SetAttribute(ctx, path.Root("created_at"), types.StringValue(credential.CreatedAt))...)
	return diagnostics
}

// credentialSetUnknownListed fills, after an Update, only the read-only
// attributes the plan left unknown. Known planned values are kept as planned:
// Terraform refuses an apply result that differs from a known planned value,
// and the next Read refreshes them anyway.
func credentialSetUnknownListed(ctx context.Context, plan tfsdk.Plan, state *tfsdk.State, credential *client.Credential) diag.Diagnostics {
	var diagnostics diag.Diagnostics
	listedStrings := map[string]types.String{
		"id":           types.StringValue(credential.ID),
		"display_name": types.StringPointerValue(credential.DisplayName),
		"state":        types.StringPointerValue(credential.State),
		"created_at":   types.StringValue(credential.CreatedAt),
	}
	for attributeName, listedValue := range listedStrings {
		var planned types.String
		diagnostics.Append(plan.GetAttribute(ctx, path.Root(attributeName), &planned)...)
		if planned.IsUnknown() {
			diagnostics.Append(state.SetAttribute(ctx, path.Root(attributeName), listedValue)...)
		}
	}
	var plannedAvailable types.Bool
	diagnostics.Append(plan.GetAttribute(ctx, path.Root("available"), &plannedAvailable)...)
	if plannedAvailable.IsUnknown() {
		diagnostics.Append(state.SetAttribute(ctx, path.Root("available"), types.BoolValue(credential.Available))...)
	}
	return diagnostics
}

// credentialWarnWhenAdopting warns, on the first apply after an import, that
// the configured write-only settings were recorded without being checked.
func credentialWarnWhenAdopting(ctx context.Context, private credentialPrivateReader, diagnostics *diag.Diagnostics, label string) {
	marker, _ := private.GetKey(ctx, credentialImportedKey)
	if len(marker) == 0 {
		return
	}
	diagnostics.AddWarning("Adopted "+label+" credential settings were not checked",
		"This credential was imported. The platform never returns its secrets (such as tokens and keys) or some of its connection settings "+
			"(such as jumphost or internal_api_url), so the configured values were recorded in state as they are, without being "+
			"compared with what the platform holds. If they differ, the platform keeps using its own values until a secret is changed "+
			"in the configuration, which rotates it, or the credential is replaced.")
}

// credentialApplyDisplayName sets the display name when the plan names one
// that differs from the state, and answers the value to record: the planned
// one after a write, the current one otherwise.
func credentialApplyDisplayName(ctx context.Context, apiClient *client.Client, credentialID string, planned types.String, current types.String) (types.String, diag.Diagnostics) {
	var diagnostics diag.Diagnostics
	if planned.IsNull() || planned.IsUnknown() || planned.Equal(current) {
		return current, diagnostics
	}
	if _, err := apiClient.SetCredentialDisplayName(ctx, credentialID, planned.ValueStringPointer()); err != nil {
		diagnostics.AddAttributeError(path.Root("display_name"), "Unable to set the credential's display name", credentialAPIErrorText(err))
		return current, diagnostics
	}
	return planned, diagnostics
}

// credentialCreateDisplayName sets the configured display name right after a
// create. A failure there is a warning, not an error: an error would leave
// the new, working credential tainted, and the next apply would replace it
// over a label. Terraform requires the state to hold the configured label,
// so it is recorded as configured; the platform keeps the label it listed,
// and the next refresh reads that back, so the next plan sets it again.
func credentialCreateDisplayName(ctx context.Context, apiClient *client.Client, credentialID string, planned types.String, listed types.String) (types.String, diag.Diagnostics) {
	var diagnostics diag.Diagnostics
	if planned.IsNull() || planned.IsUnknown() || planned.Equal(listed) {
		if planned.IsUnknown() {
			return listed, diagnostics
		}
		return planned, diagnostics
	}
	if _, err := apiClient.SetCredentialDisplayName(ctx, credentialID, planned.ValueStringPointer()); err != nil {
		diagnostics.AddAttributeWarning(path.Root("display_name"), "The credential was created, but its display name was not set",
			"The platform refused the display name, so the credential keeps the label it had: "+credentialAPIErrorText(err)+
				"\n\nThe credential works and is kept. The next plan shows the display name as a change and sets it again.")
	}
	return planned, diagnostics
}

// credentialAPIErrorText prefers the platform's own explanation (its detail
// or message) over the raw response body.
func credentialAPIErrorText(err error) string {
	var apiError *client.APIError
	if !errors.As(err, &apiError) {
		return err.Error()
	}
	var body struct {
		Detail  any    `json:"detail"`
		Message string `json:"message"`
	}
	if json.Unmarshal([]byte(apiError.Body), &body) == nil {
		if detail, isString := body.Detail.(string); isString && detail != "" {
			return fmt.Sprintf("%s (%s %s answered %d)", detail, apiError.Method, apiError.Path, apiError.StatusCode)
		}
		if body.Message != "" {
			return fmt.Sprintf("%s (%s %s answered %d)", body.Message, apiError.Method, apiError.Path, apiError.StatusCode)
		}
	}
	return err.Error()
}

// credentialDisplayNameValidator rejects at plan time a label the platform
// refuses, or stores differently from how it is written: the platform trims
// surrounding whitespace and treats an empty label as clearing it.
type credentialDisplayNameValidator struct{}

const credentialDisplayNameMaxLength = 100

func (credentialDisplayNameValidator) Description(_ context.Context) string {
	return "value must be 1 to 100 characters, without surrounding whitespace or control characters"
}

func (displayNameValidator credentialDisplayNameValidator) MarkdownDescription(ctx context.Context) string {
	return displayNameValidator.Description(ctx)
}

func (displayNameValidator credentialDisplayNameValidator) ValidateString(ctx context.Context, request validator.StringRequest, response *validator.StringResponse) {
	if request.ConfigValue.IsNull() || request.ConfigValue.IsUnknown() {
		return
	}
	value := request.ConfigValue.ValueString()
	isValid := value != "" && strings.TrimSpace(value) == value && utf8.RuneCountInString(value) <= credentialDisplayNameMaxLength &&
		!strings.ContainsFunc(value, unicode.IsControl)
	if !isValid {
		response.Diagnostics.AddAttributeError(request.Path, "Invalid display name", "The platform refuses or rewrites this label: "+displayNameValidator.Description(ctx)+".")
	}
}

// credentialAPIURLValidator rejects at plan time an API URL that carries a
// user name or password, which the platform refuses: the URL is listed to
// every member of the organisation, so credentials belong in the secret
// fields.
type credentialAPIURLValidator struct{}

func (credentialAPIURLValidator) Description(_ context.Context) string {
	return "value must not contain a user name or password"
}

func (apiURLValidator credentialAPIURLValidator) MarkdownDescription(ctx context.Context) string {
	return apiURLValidator.Description(ctx)
}

func (apiURLValidator credentialAPIURLValidator) ValidateString(ctx context.Context, request validator.StringRequest, response *validator.StringResponse) {
	if request.ConfigValue.IsNull() || request.ConfigValue.IsUnknown() {
		return
	}
	parsed, err := url.Parse(request.ConfigValue.ValueString())
	if err != nil || parsed.User == nil {
		return
	}
	response.Diagnostics.AddAttributeError(request.Path, "Invalid API URL",
		"The platform refuses an API URL that contains a user name or password; put the secret in the credential's secret fields.")
}

// credentialOneOfValidator rejects at plan time a value the platform refuses
// because it is not one of a fixed set.
type credentialOneOfValidator struct {
	allowed []string
}

func (oneOf credentialOneOfValidator) Description(_ context.Context) string {
	return "value must be one of: " + strings.Join(oneOf.allowed, ", ")
}

func (oneOf credentialOneOfValidator) MarkdownDescription(ctx context.Context) string {
	return oneOf.Description(ctx)
}

func (oneOf credentialOneOfValidator) ValidateString(ctx context.Context, request validator.StringRequest, response *validator.StringResponse) {
	if request.ConfigValue.IsNull() || request.ConfigValue.IsUnknown() {
		return
	}
	if slices.Contains(oneOf.allowed, request.ConfigValue.ValueString()) {
		return
	}
	response.Diagnostics.AddAttributeError(request.Path, "Invalid value", "The platform refuses this value: "+oneOf.Description(ctx)+".")
}

// credentialHTTPSValidator rejects at plan time an endpoint that is not an
// https URL, which the platform refuses.
type credentialHTTPSValidator struct{}

func (credentialHTTPSValidator) Description(_ context.Context) string {
	return "value must be an https:// URL"
}

func (httpsValidator credentialHTTPSValidator) MarkdownDescription(ctx context.Context) string {
	return httpsValidator.Description(ctx)
}

func (httpsValidator credentialHTTPSValidator) ValidateString(ctx context.Context, request validator.StringRequest, response *validator.StringResponse) {
	if request.ConfigValue.IsNull() || request.ConfigValue.IsUnknown() {
		return
	}
	if !strings.HasPrefix(request.ConfigValue.ValueString(), "https://") {
		response.Diagnostics.AddAttributeError(request.Path, "Invalid endpoint", "The platform refuses this endpoint: "+httpsValidator.Description(ctx)+".")
	}
}
