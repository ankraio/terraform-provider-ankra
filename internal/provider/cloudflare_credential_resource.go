// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"ankra.io/terraform-provider-ankra/internal/client"
)

var (
	_ resource.Resource                = (*cloudflareCredentialResource)(nil)
	_ resource.ResourceWithConfigure   = (*cloudflareCredentialResource)(nil)
	_ resource.ResourceWithImportState = (*cloudflareCredentialResource)(nil)
)

// cloudflareCredentialProvider is the provider the organisation-wide
// credential listing files Cloudflare credentials under.
const cloudflareCredentialProvider = "cloudflare"

type cloudflareCredentialResource struct {
	configuredClient
}

type cloudflareCredentialModel struct {
	ID             types.String `tfsdk:"id"`
	Name           types.String `tfsdk:"name"`
	APIToken       types.String `tfsdk:"api_token"`
	AccountID      types.String `tfsdk:"account_id"`
	AccountName    types.String `tfsdk:"account_name"`
	TokenID        types.String `tfsdk:"token_id"`
	TokenKind      types.String `tfsdk:"token_kind"`
	TokenExpiresAt types.String `tfsdk:"token_expires_at"`
	State          types.String `tfsdk:"state"`
	NeedsReconnect types.Bool   `tfsdk:"needs_reconnect"`
}

// NewCloudflareCredentialResource returns the ankra_cloudflare_credential
// resource, the counterpart of `ankra org cloudflare connect`.
func NewCloudflareCredentialResource() resource.Resource {
	return &cloudflareCredentialResource{}
}

func (credentialResource *cloudflareCredentialResource) Metadata(_ context.Context, request resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_cloudflare_credential"
}

func (credentialResource *cloudflareCredentialResource) Schema(_ context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	computed := func(description string) schema.StringAttribute {
		return schema.StringAttribute{MarkdownDescription: description, Computed: true}
	}
	response.Schema = schema.Schema{
		MarkdownDescription: "A Cloudflare API token connected to the organisation. The platform verifies the token when it is connected; " +
			"`ankra_cloudflare_dns_record` and `ankra_organisation_custom_dns_zone` use it by name. The token cannot be changed in place, " +
			"so a new token replaces the credential. Destroying it deletes the credential, which the platform refuses while a cluster uses it. " +
			"The counterpart of `ankra org cloudflare connect`.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Identifier of the credential.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				MarkdownDescription: "Credential name, unique among the organisation's credentials. Changing it replaces the credential.",
				Required:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"api_token": schema.StringAttribute{
				MarkdownDescription: "Cloudflare API token with DNS edit access to the zones it should manage. The platform never returns it, so a change made outside Terraform is not detected.",
				Required:            true,
				Sensitive:           true,
				PlanModifiers:       []planmodifier.String{alertDNSReplaceUnlessImported()},
			},
			"account_id": schema.StringAttribute{
				MarkdownDescription: "Cloudflare account, needed only for an account-owned token that reaches several accounts. When unset the platform records the account the token resolves to. Changing it replaces the credential.",
				Optional:            true,
				Computed:            true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
					stringplanmodifier.RequiresReplaceIfConfigured(),
				},
			},
			"account_name":     computed("Name of the Cloudflare account."),
			"token_id":         computed("Cloudflare's id for the token."),
			"token_kind":       computed("Whether the token is user-owned or account-owned."),
			"token_expires_at": computed("When the token expires, if it does."),
			"state":            computed("Health of the credential; `needs-reconnect` once the platform no longer holds the token."),
			"needs_reconnect": schema.BoolAttribute{
				MarkdownDescription: "Whether the token is gone and the credential must be connected again.",
				Computed:            true,
			},
		},
	}
}

func (credentialResource *cloudflareCredentialResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	apiClient := credentialResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var plan cloudflareCredentialModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}

	credentialID, err := apiClient.ConnectCloudflareCredential(ctx, client.CloudflareCredentialConnect{
		Name:      plan.Name.ValueString(),
		APIToken:  plan.APIToken.ValueString(),
		AccountID: alertDNSStringValue(stringPointer(plan.AccountID)),
	})
	if err != nil {
		response.Diagnostics.AddError("Unable to connect Cloudflare credential", err.Error())
		return
	}
	plan.ID = types.StringValue(credentialID)

	credential, err := apiClient.GetCloudflareCredential(ctx, plan.Name.ValueString())
	if err != nil {
		cloudflareCredentialApply(&client.CloudflareCredential{Name: plan.Name.ValueString(), AccountID: alertDNSStringValue(stringPointer(plan.AccountID))}, &plan)
		response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
		response.Diagnostics.Append(trackScope(ctx, apiClient, response.Private)...)
		response.Diagnostics.AddError("Unable to read the connected Cloudflare credential", err.Error())
		return
	}
	cloudflareCredentialApply(credential, &plan)
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
	response.Diagnostics.Append(trackScope(ctx, apiClient, response.Private)...)
}

func (credentialResource *cloudflareCredentialResource) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	apiClient := credentialResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state cloudflareCredentialModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}

	credentials, err := apiClient.ListDNSOrganisationCredentials(ctx, cloudflareCredentialProvider)
	if err != nil {
		response.Diagnostics.AddError("Unable to read Cloudflare credential", err.Error())
		return
	}
	identity := cloudflareCredentialFind(credentials, state.ID.ValueString(), state.Name.ValueString())
	if identity == nil || readTargetChanged(state.ID, identity.ID) {
		removeWhenTargetChanged(ctx, apiClient, response)
		return
	}

	credential, err := apiClient.GetCloudflareCredential(ctx, identity.Name)
	if removeWhenNotFound(ctx, err, apiClient, response) {
		return
	}
	if err != nil {
		response.Diagnostics.AddError("Unable to read Cloudflare credential", err.Error())
		return
	}
	state.ID = types.StringValue(identity.ID)
	cloudflareCredentialApply(credential, &state)
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
	response.Diagnostics.Append(trackScope(ctx, apiClient, response.Private)...)
}

// cloudflareCredentialFind finds the credential by its id, or, before the id
// is known (on import), by name.
func cloudflareCredentialFind(credentials []client.DNSOrganisationCredential, credentialID, name string) *client.DNSOrganisationCredential {
	for index := range credentials {
		if credentialID != "" && credentials[index].ID == credentialID {
			return &credentials[index]
		}
		if credentialID == "" && credentials[index].Name == name {
			return &credentials[index]
		}
	}
	return nil
}

// Update only runs after an import, when the token was unknown to state: it
// records the configured token. Any other change replaces the credential.
func (credentialResource *cloudflareCredentialResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	apiClient := credentialResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var plan cloudflareCredentialModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
	response.Diagnostics.Append(trackScope(ctx, apiClient, response.Private)...)
}

func (credentialResource *cloudflareCredentialResource) Delete(ctx context.Context, request resource.DeleteRequest, response *resource.DeleteResponse) {
	apiClient := credentialResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state cloudflareCredentialModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	err := apiClient.DeleteDNSOrganisationCredential(ctx, state.ID.ValueString())
	if err == nil || deletedWhenNotFound(ctx, err, apiClient, request.Private, &response.Diagnostics) || response.Diagnostics.HasError() {
		return
	}
	response.Diagnostics.AddError("Unable to delete Cloudflare credential", err.Error())
}

// ImportState imports by credential name. The token cannot be read back; the
// first apply after importing records the configured one without replacing
// the credential.
func (credentialResource *cloudflareCredentialResource) ImportState(ctx context.Context, request resource.ImportStateRequest, response *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("name"), request, response)
}

func cloudflareCredentialApply(credential *client.CloudflareCredential, model *cloudflareCredentialModel) {
	model.Name = types.StringValue(credential.Name)
	model.AccountID = optionalString(credential.AccountID)
	model.AccountName = optionalString(credential.AccountName)
	model.TokenID = optionalString(credential.TokenID)
	model.TokenKind = optionalString(credential.TokenKind)
	model.TokenExpiresAt = optionalString(credential.TokenExpiresAt)
	model.State = types.StringValue(credential.State)
	model.NeedsReconnect = types.BoolValue(credential.NeedsReconnect)
}
