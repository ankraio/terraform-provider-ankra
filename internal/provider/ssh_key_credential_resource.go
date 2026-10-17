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
			stringplanmodifier.RequiresReplace(),
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
			"`ssh-key` command creates the same organisation-wide credential.",
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
		response.Diagnostics.AddError("Unable to read back SSH key credential",
			"The credential "+created.ID+" was created but could not be read back: "+err.Error())
		return
	}
	sshKeyResource.applyListed(&plan, credential)
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
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
	if removeWhenNotFound(ctx, err, response) {
		return
	}
	if err != nil {
		response.Diagnostics.AddError("Unable to read SSH key credential", err.Error())
		return
	}
	sshKeyResource.applyListed(&state, credential)
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
}

// Update only adopts the configured generate flag of a just-imported
// credential; every real change is planned as a replacement.
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

	credential, err := apiClient.GetCredential(ctx, client.CredentialSSHKeyProvider, state.ID.ValueString())
	if err != nil {
		response.Diagnostics.AddError("Unable to read SSH key credential", err.Error())
		return
	}
	plan.PrivateKey = state.PrivateKey
	sshKeyResource.applyListed(&plan, credential)
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
	response.Diagnostics.Append(response.Private.SetKey(ctx, credentialImportedKey, nil)...)
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
	if err := apiClient.DeleteCredential(ctx, state.ID.ValueString()); err != nil {
		response.Diagnostics.AddError("Unable to delete SSH key credential", err.Error())
	}
}

// ImportState imports by credential id. The public key is read back; a
// generated key's private half is not, because the platform never kept it.
func (sshKeyResource *sshKeyCredentialResource) ImportState(ctx context.Context, request resource.ImportStateRequest, response *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), request, response)
	response.Diagnostics.Append(response.Private.SetKey(ctx, credentialImportedKey, []byte("true"))...)
}
