// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"slices"
	"strings"

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
	"the platform does not return a provider credential's id from its create, so the provider reads the credential listing right after. " +
	"If that read fails, the error names the `terraform import` to run, because the credential already exists.\n\n" +
	"**Projects.** A credential is created in the provider's project (`project_id`, or the user's last-selected project) and is listed only " +
	"in that project and its organisation. Changing the provider's `project_id` makes credentials pinned to the old project read as deleted, " +
	"and recreating them fails because credential names are unique per provider across the whole organisation. Move them with " +
	"`terraform state rm` and `terraform import` under the new project instead, or keep one provider block per project.\n\n" +
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
			MarkdownDescription: "Label shown instead of the name in the Ankra portal, when one was set there.",
			Computed:            true,
			PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
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
		"This credential was imported. The platform never returns its secrets or connection settings (such as tokens, keys, api_url, "+
			"project_id, region, jumphost or tls_insecure), so the configured values were recorded in state as they are, without being "+
			"compared with what the platform holds. If they differ, the platform keeps using its own values until the credential is replaced.")
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
