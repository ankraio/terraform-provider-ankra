// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"ankra.io/terraform-provider-ankra/internal/client"
)

var (
	_ resource.Resource                   = (*sshKeyCredentialResource)(nil)
	_ resource.ResourceWithConfigure      = (*sshKeyCredentialResource)(nil)
	_ resource.ResourceWithImportState    = (*sshKeyCredentialResource)(nil)
	_ resource.ResourceWithValidateConfig = (*sshKeyCredentialResource)(nil)
)

type sshKeyCredentialResource struct {
	configuredClient
}

type sshKeyCredentialModel struct {
	ID           types.String `tfsdk:"id"`
	Name         types.String `tfsdk:"name"`
	DisplayName  types.String `tfsdk:"display_name"`
	Available    types.Bool   `tfsdk:"available"`
	State        types.String `tfsdk:"state"`
	CreatedAt    types.String `tfsdk:"created_at"`
	SSHPublicKey types.String `tfsdk:"ssh_public_key"`
	Generate     types.Bool   `tfsdk:"generate"`
	PrivateKey   types.String `tfsdk:"private_key"`
	Fingerprint  types.String `tfsdk:"fingerprint"`
	KeyType      types.String `tfsdk:"key_type"`
	Comment      types.String `tfsdk:"comment"`
}

// NewSSHKeyCredentialResource returns the ankra_ssh_key_credential resource,
// the counterpart of `ankra credentials <provider> ssh-key create`.
func NewSSHKeyCredentialResource() resource.Resource {
	return &sshKeyCredentialResource{}
}

func (sshKeyResource *sshKeyCredentialResource) Metadata(_ context.Context, request resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_ssh_key_credential"
}

func (sshKeyResource *sshKeyCredentialResource) Schema(_ context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	attributes := credentialListedAttributes()
	attributes["name"] = credentialNameAttribute()
	attributes["ssh_public_key"] = schema.StringAttribute{
		MarkdownDescription: "OpenSSH public key, such as `ssh-ed25519 AAAA... me@laptop`. Set it, or set `generate`. " +
			"Leading and trailing whitespace is ignored. Changing it replaces the credential.",
		Optional: true,
		Computed: true,
		PlanModifiers: []planmodifier.String{
			stringplanmodifier.UseStateForUnknown(),
			stringplanmodifier.RequiresReplaceIf(sshKeyCredentialKeyChanged,
				"Replaces the credential when the key changes, ignoring surrounding whitespace.",
				"Replaces the credential when the key changes, ignoring surrounding whitespace."),
		},
	}
	attributes["generate"] = schema.BoolAttribute{
		MarkdownDescription: "Have the platform generate an RSA keypair; the private key is returned once, in `private_key`. Defaults to `false`.",
		Optional:            true,
		Computed:            true,
		Default:             booldefault.StaticBool(false),
		PlanModifiers:       []planmodifier.Bool{credentialReplaceBool()},
	}
	attributes["private_key"] = schema.StringAttribute{
		MarkdownDescription: "Private key of a generated keypair. The platform returns it once, at creation, and never stores it, so it is null for an imported credential.",
		Computed:            true,
		Sensitive:           true,
		PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
	}
	for name, description := range map[string]string{
		"fingerprint": "OpenSSH SHA256 fingerprint of the key.",
		"key_type":    "Key algorithm, such as `ssh-ed25519`.",
		"comment":     "Comment after the key material.",
	} {
		attributes[name] = schema.StringAttribute{
			MarkdownDescription: description,
			Computed:            true,
			PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
		}
	}
	response.Schema = schema.Schema{
		MarkdownDescription: "An organisation-wide SSH key credential, referenced by a cluster's `ssh_key_credential_id`. " +
			"The counterpart of `ankra credentials <provider> ssh-key create` and `ankra credentials delete`: every provider's " +
			"`ssh-key` command creates the same organisation-wide credential." + credentialOperationalNotes,
		Attributes: attributes,
	}
}

func (sshKeyResource *sshKeyCredentialResource) ValidateConfig(ctx context.Context, request resource.ValidateConfigRequest, response *resource.ValidateConfigResponse) {
	var config sshKeyCredentialModel
	response.Diagnostics.Append(request.Config.Get(ctx, &config)...)
	if response.Diagnostics.HasError() || config.SSHPublicKey.IsUnknown() || config.Generate.IsUnknown() {
		return
	}
	hasPublicKey := !config.SSHPublicKey.IsNull()
	isGenerated := config.Generate.ValueBool()
	if hasPublicKey == isGenerated {
		response.Diagnostics.AddAttributeError(path.Root("ssh_public_key"), "Set exactly one of ssh_public_key and generate",
			"An SSH key credential holds either a public key you provide or a keypair the platform generates.")
	}
}

// applyListed copies the listing into the model. The stored public key is
// the configured one trimmed, so a configured key that differs only in
// surrounding whitespace is kept as configured.
func (sshKeyResource *sshKeyCredentialResource) applyListed(model *sshKeyCredentialModel, credential *client.Credential) {
	model.ID = types.StringValue(credential.ID)
	model.Name = types.StringValue(credential.Name)
	model.DisplayName = types.StringPointerValue(credential.DisplayName)
	model.Available = types.BoolValue(credential.Available)
	model.State = types.StringPointerValue(credential.State)
	model.CreatedAt = types.StringValue(credential.CreatedAt)
	model.Fingerprint = types.StringPointerValue(credential.Fingerprint)
	model.KeyType = types.StringPointerValue(credential.KeyType)
	model.Comment = types.StringPointerValue(credential.Comment)
	if credential.SSHPublicKey != nil {
		current := model.SSHPublicKey
		if current.IsNull() || current.IsUnknown() || strings.TrimSpace(current.ValueString()) != strings.TrimSpace(*credential.SSHPublicKey) {
			model.SSHPublicKey = types.StringValue(*credential.SSHPublicKey)
		}
	}
	if model.SSHPublicKey.IsUnknown() {
		model.SSHPublicKey = types.StringNull()
	}
}

func (sshKeyResource *sshKeyCredentialResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	apiClient := sshKeyResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var plan sshKeyCredentialModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}

	created, err := apiClient.CreateSSHKeyCredential(ctx, client.CredentialSSHKeyCreate{
		Name:         plan.Name.ValueString(),
		SSHPublicKey: stringPointer(plan.SSHPublicKey),
		Generate:     plan.Generate.ValueBool(),
	})
	if err != nil {
		response.Diagnostics.AddError("Unable to create SSH key credential", err.Error())
		return
	}
	plan.ID = types.StringValue(created.ID)
	plan.PrivateKey = types.StringPointerValue(created.PrivateKey)

	credential, err := apiClient.GetCredential(ctx, client.CredentialSSHKeyProvider, created.ID)
	if err != nil {
		plan.DisplayName = types.StringNull()
		plan.Available = types.BoolNull()
		plan.State = types.StringNull()
		plan.CreatedAt = types.StringNull()
		plan.Fingerprint = types.StringNull()
		plan.KeyType = types.StringNull()
		plan.Comment = types.StringNull()
		if plan.SSHPublicKey.IsUnknown() {
			plan.SSHPublicKey = types.StringNull()
		}
		response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
		response.Diagnostics.Append(trackScope(ctx, apiClient, response.Private)...)
		response.Diagnostics.AddError("Unable to read back SSH key credential",
			"The credential "+created.ID+" was created but could not be read back: "+err.Error())
		return
	}
	plannedDisplayName := plan.DisplayName
	sshKeyResource.applyListed(&plan, credential)
	displayName, displayNameDiagnostics := credentialCreateDisplayName(ctx, apiClient, created.ID, plannedDisplayName, plan.DisplayName)
	response.Diagnostics.Append(displayNameDiagnostics...)
	plan.DisplayName = displayName
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
	response.Diagnostics.Append(trackScope(ctx, apiClient, response.Private)...)
}

func (sshKeyResource *sshKeyCredentialResource) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	apiClient := sshKeyResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state sshKeyCredentialModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}

	credential, err := apiClient.GetCredential(ctx, client.CredentialSSHKeyProvider, state.ID.ValueString())
	if removeWhenNotFound(ctx, err, apiClient, response) {
		return
	}
	if err != nil {
		response.Diagnostics.AddError("Unable to read SSH key credential", err.Error())
		return
	}
	sshKeyResource.applyListed(&state, credential)
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
	response.Diagnostics.Append(trackScope(ctx, apiClient, response.Private)...)
}

// Update sets a changed display name. Otherwise it only records the
// configured generate flag of a just-imported credential, or a public key
// that differs from the stored one in surrounding whitespace only. Every
// other change is planned as a replacement.
func (sshKeyResource *sshKeyCredentialResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	apiClient := sshKeyResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var plan, state sshKeyCredentialModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	_, displayNameDiagnostics := credentialApplyDisplayName(ctx, apiClient, state.ID.ValueString(), plan.DisplayName, state.DisplayName)
	response.Diagnostics.Append(displayNameDiagnostics...)
	if response.Diagnostics.HasError() {
		response.State.Raw = request.State.Raw
		return
	}

	credential, err := apiClient.GetCredential(ctx, client.CredentialSSHKeyProvider, state.ID.ValueString())
	if err != nil {
		response.Diagnostics.AddError("Unable to read SSH key credential", err.Error())
		return
	}
	listed := plan
	sshKeyResource.applyListed(&listed, credential)
	plan.ID = state.ID
	plan.PrivateKey = state.PrivateKey
	plan.DisplayName = sshKeyCredentialKnownOr(plan.DisplayName, listed.DisplayName)
	plan.State = sshKeyCredentialKnownOr(plan.State, listed.State)
	plan.CreatedAt = sshKeyCredentialKnownOr(plan.CreatedAt, listed.CreatedAt)
	plan.Fingerprint = sshKeyCredentialKnownOr(plan.Fingerprint, listed.Fingerprint)
	plan.KeyType = sshKeyCredentialKnownOr(plan.KeyType, listed.KeyType)
	plan.Comment = sshKeyCredentialKnownOr(plan.Comment, listed.Comment)
	if plan.Available.IsUnknown() {
		plan.Available = listed.Available
	}
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
	credentialWarnWhenAdopting(ctx, request.Private, &response.Diagnostics, "SSH key")
	response.Diagnostics.Append(response.Private.SetKey(ctx, credentialImportedKey, nil)...)
}

// sshKeyCredentialKnownOr keeps a known planned value, which Terraform
// requires the apply result to match, and fills an unknown one from the
// listing.
func sshKeyCredentialKnownOr(planned types.String, listed types.String) types.String {
	if planned.IsUnknown() {
		return listed
	}
	return planned
}

// sshKeyCredentialKeyChanged replaces the credential only when the key
// differs from the stored one after trimming surrounding whitespace, which
// the platform strips on create. Terraform requires the plan to carry the
// configured value verbatim, so a whitespace-only difference (a key read
// with file() after an import) is an in-place update that sends nothing.
func sshKeyCredentialKeyChanged(_ context.Context, request planmodifier.StringRequest, response *stringplanmodifier.RequiresReplaceIfFuncResponse) {
	if request.PlanValue.IsUnknown() || request.StateValue.IsNull() {
		response.RequiresReplace = true
		return
	}
	response.RequiresReplace = strings.TrimSpace(request.PlanValue.ValueString()) != strings.TrimSpace(request.StateValue.ValueString())
}

func (sshKeyResource *sshKeyCredentialResource) Delete(ctx context.Context, request resource.DeleteRequest, response *resource.DeleteResponse) {
	apiClient := sshKeyResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state sshKeyCredentialModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	err := apiClient.DeleteCredential(ctx, state.ID.ValueString())
	if err == nil || deletedWhenNotFound(ctx, err, apiClient, request.Private, &response.Diagnostics) || response.Diagnostics.HasError() {
		return
	}
	response.Diagnostics.AddError("Unable to delete SSH key credential", err.Error())
}

// ImportState imports by credential id. The public key is read back; a
// generated key's private half is not, because the platform never kept it.
func (sshKeyResource *sshKeyCredentialResource) ImportState(ctx context.Context, request resource.ImportStateRequest, response *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), request, response)
	response.Diagnostics.Append(response.Private.SetKey(ctx, credentialImportedKey, []byte("true"))...)
}
