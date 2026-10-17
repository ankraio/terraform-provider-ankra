// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"ankra.io/terraform-provider-ankra/internal/client"
)

var (
	_ datasource.DataSource                   = (*credentialsDataSource)(nil)
	_ datasource.DataSourceWithConfigure      = (*credentialsDataSource)(nil)
	_ datasource.DataSource                   = (*credentialDataSource)(nil)
	_ datasource.DataSourceWithConfigure      = (*credentialDataSource)(nil)
	_ datasource.DataSourceWithValidateConfig = (*credentialDataSource)(nil)
)

// credentialItemModel is one credential as the listing describes it. It
// holds no secret: the listing carries none.
type credentialItemModel struct {
	ID           types.String `tfsdk:"id"`
	Name         types.String `tfsdk:"name"`
	DisplayName  types.String `tfsdk:"display_name"`
	Provider     types.String `tfsdk:"credential_provider"`
	System       types.Bool   `tfsdk:"system"`
	Available    types.Bool   `tfsdk:"available"`
	State        types.String `tfsdk:"state"`
	CreatedAt    types.String `tfsdk:"created_at"`
	UpdatedAt    types.String `tfsdk:"updated_at"`
	Scope        types.String `tfsdk:"scope"`
	AuthMethod   types.String `tfsdk:"auth_method"`
	Managed      types.Bool   `tfsdk:"managed"`
	SSHPublicKey types.String `tfsdk:"ssh_public_key"`
	Fingerprint  types.String `tfsdk:"fingerprint"`
}

func credentialItemFrom(credential *client.Credential) credentialItemModel {
	return credentialItemModel{
		ID:           types.StringValue(credential.ID),
		Name:         types.StringValue(credential.Name),
		DisplayName:  types.StringPointerValue(credential.DisplayName),
		Provider:     types.StringValue(credential.Provider),
		System:       types.BoolValue(credential.System),
		Available:    types.BoolValue(credential.Available),
		State:        types.StringPointerValue(credential.State),
		CreatedAt:    types.StringValue(credential.CreatedAt),
		UpdatedAt:    types.StringPointerValue(credential.UpdatedAt),
		Scope:        types.StringPointerValue(credential.Scope),
		AuthMethod:   types.StringPointerValue(credential.AuthMethod),
		Managed:      types.BoolPointerValue(credential.Managed),
		SSHPublicKey: types.StringPointerValue(credential.SSHPublicKey),
		Fingerprint:  types.StringPointerValue(credential.Fingerprint),
	}
}

// credentialItemAttributes describes credentialItemModel. lookup names the
// attributes the single-credential data source takes as input.
func credentialItemAttributes(lookup map[string]bool) map[string]schema.Attribute {
	descriptions := map[string]string{
		"id":                  "Identifier of the credential.",
		"name":                "Credential name.",
		"display_name":        "Label shown instead of the name in the Ankra portal, when one was set.",
		"credential_provider": "Provider the credential is for, such as `hetzner`, `ssh-key`, `aws` or `github`.",
		"state":               "Lifecycle state the platform reports.",
		"created_at":          "When the credential was created.",
		"updated_at":          "When the credential was last changed.",
		"scope":               "AWS credentials only: the onboarding scope (`cost`, `provisioning` or `self_managed`).",
		"auth_method":         "AWS credentials only: `role` or `keys`.",
		"ssh_public_key":      "SSH key credentials only: the stored public key.",
		"fingerprint":         "SSH key credentials only: the OpenSSH SHA256 fingerprint.",
	}
	attributes := map[string]schema.Attribute{}
	for name, description := range descriptions {
		attributes[name] = schema.StringAttribute{
			MarkdownDescription: description,
			Computed:            true,
			Optional:            lookup[name],
		}
	}
	attributes["system"] = schema.BoolAttribute{MarkdownDescription: "Whether the platform owns the credential.", Computed: true}
	attributes["available"] = schema.BoolAttribute{MarkdownDescription: "Whether the platform considers the credential usable.", Computed: true}
	attributes["managed"] = schema.BoolAttribute{MarkdownDescription: "Ankra Cloud credentials only: whether it is the built-in credential the platform mints and renews.", Computed: true}
	return attributes
}

type credentialsDataSource struct {
	configuredDataSourceClient
}

type credentialsDataSourceModel struct {
	ID          types.String          `tfsdk:"id"`
	Provider    types.String          `tfsdk:"credential_provider"`
	Credentials []credentialItemModel `tfsdk:"credentials"`
}

// NewCredentialsDataSource returns the ankra_credentials data source, the
// counterpart of `ankra credentials list` and `ankra credentials <provider>
// list`.
func NewCredentialsDataSource() datasource.DataSource {
	return &credentialsDataSource{}
}

func (credentialsSource *credentialsDataSource) Metadata(_ context.Context, request datasource.MetadataRequest, response *datasource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_credentials"
}

func (credentialsSource *credentialsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, response *datasource.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: "Lists the organisation's credentials. The counterpart of `ankra credentials list` and `ankra credentials <provider> list`. " +
			"The listing is metadata only and never carries a secret.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Placeholder identifier for the data source.",
				Computed:            true,
			},
			"credential_provider": schema.StringAttribute{
				MarkdownDescription: "Only list credentials for this provider, such as `hetzner`, `ssh-key` or `aws`.",
				Optional:            true,
			},
			"credentials": schema.ListNestedAttribute{
				MarkdownDescription: "Credentials in the organisation.",
				Computed:            true,
				NestedObject:        schema.NestedAttributeObject{Attributes: credentialItemAttributes(nil)},
			},
		},
	}
}

func (credentialsSource *credentialsDataSource) Read(ctx context.Context, request datasource.ReadRequest, response *datasource.ReadResponse) {
	apiClient := credentialsSource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var config credentialsDataSourceModel
	response.Diagnostics.Append(request.Config.Get(ctx, &config)...)
	if response.Diagnostics.HasError() {
		return
	}

	credentials, err := apiClient.ListCredentials(ctx, config.Provider.ValueString())
	if err != nil {
		response.Diagnostics.AddError("Unable to list credentials", err.Error())
		return
	}

	state := credentialsDataSourceModel{
		ID:          types.StringValue("ankra_credentials"),
		Provider:    config.Provider,
		Credentials: make([]credentialItemModel, 0, len(credentials)),
	}
	for index := range credentials {
		state.Credentials = append(state.Credentials, credentialItemFrom(&credentials[index]))
	}
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
}

type credentialDataSource struct {
	configuredDataSourceClient
}

// NewCredentialDataSource returns the ankra_credential data source: one
// credential looked up by id, or by provider and name. It reads the
// metadata-only listing, never the secret-revealing detail behind `ankra
// credentials get`.
func NewCredentialDataSource() datasource.DataSource {
	return &credentialDataSource{}
}

func (credentialSource *credentialDataSource) Metadata(_ context.Context, request datasource.MetadataRequest, response *datasource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_credential"
}

func (credentialSource *credentialDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, response *datasource.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: "Looks up one credential by `id`, or by `credential_provider` and `name`, for example to pass an existing credential's id " +
			"to a cluster's `credential_id`. It reads the credential listing, so it returns no secret and needs only read access; " +
			"unlike `ankra credentials get`, which reveals the secret.",
		Attributes: credentialItemAttributes(map[string]bool{"id": true, "name": true, "credential_provider": true}),
	}
}

func (credentialSource *credentialDataSource) ValidateConfig(ctx context.Context, request datasource.ValidateConfigRequest, response *datasource.ValidateConfigResponse) {
	var config credentialItemModel
	response.Diagnostics.Append(request.Config.Get(ctx, &config)...)
	if response.Diagnostics.HasError() || config.ID.IsUnknown() || config.Name.IsUnknown() || config.Provider.IsUnknown() {
		return
	}
	byID := !config.ID.IsNull()
	byName := !config.Name.IsNull()
	if byID == byName {
		response.Diagnostics.AddAttributeError(path.Root("id"), "Set exactly one of id and name",
			"Look a credential up by id, or by provider and name.")
		return
	}
	if byName && config.Provider.IsNull() {
		response.Diagnostics.AddAttributeError(path.Root("credential_provider"), "Missing credential_provider",
			"Names are unique per provider, so a lookup by name needs credential_provider too.")
	}
}

func (credentialSource *credentialDataSource) Read(ctx context.Context, request datasource.ReadRequest, response *datasource.ReadResponse) {
	apiClient := credentialSource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var config credentialItemModel
	response.Diagnostics.Append(request.Config.Get(ctx, &config)...)
	if response.Diagnostics.HasError() {
		return
	}

	var credential *client.Credential
	var err error
	if !config.ID.IsNull() {
		credential, err = apiClient.GetCredential(ctx, config.Provider.ValueString(), config.ID.ValueString())
	} else {
		credential, err = apiClient.GetCredentialByName(ctx, config.Provider.ValueString(), config.Name.ValueString())
	}
	if err != nil {
		response.Diagnostics.AddError("Unable to find credential", err.Error())
		return
	}
	state := credentialItemFrom(credential)
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
}
