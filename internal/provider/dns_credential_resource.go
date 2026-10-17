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
	_ resource.Resource                = (*dnsCredentialResource)(nil)
	_ resource.ResourceWithConfigure   = (*dnsCredentialResource)(nil)
	_ resource.ResourceWithImportState = (*dnsCredentialResource)(nil)
)

type dnsCredentialResource struct {
	configuredClient
}

type dnsCredentialModel struct {
	ID                 types.String `tfsdk:"id"`
	Name               types.String `tfsdk:"name"`
	WebhookProviderURL types.String `tfsdk:"webhook_provider_url"`
}

// NewDNSCredentialResource returns the ankra_dns_credential resource, the
// counterpart of `ankra org dns credentials create` (and of
// `ankra credentials delete` for it).
func NewDNSCredentialResource() resource.Resource {
	return &dnsCredentialResource{}
}

func (credentialResource *dnsCredentialResource) Metadata(_ context.Context, request resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_dns_credential"
}

func (credentialResource *dnsCredentialResource) Schema(_ context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: "An external-dns webhook credential for a DNS zone you own, referenced by name from `ankra_organisation_custom_dns_zone`. " +
			"The platform cannot change a stored credential, so any change replaces it. The counterpart of `ankra org dns credentials create`.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Identifier of the credential.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				MarkdownDescription: "Credential name, unique among the organisation's credentials.",
				Required:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"webhook_provider_url": schema.StringAttribute{
				MarkdownDescription: "external-dns webhook endpoint, including its token. The platform never returns it, so a change made outside Terraform is not detected. Changing it replaces the credential.",
				Required:            true,
				Sensitive:           true,
				PlanModifiers:       []planmodifier.String{alertDNSReplaceUnlessImported()},
			},
		},
	}
}

func (credentialResource *dnsCredentialResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	apiClient := credentialResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var plan dnsCredentialModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}

	credentialID, err := apiClient.CreateDNSCredential(ctx, client.DNSCredentialCreate{
		Name:               plan.Name.ValueString(),
		WebhookProviderURL: plan.WebhookProviderURL.ValueString(),
	})
	if err != nil {
		response.Diagnostics.AddError("Unable to create DNS credential", err.Error())
		return
	}
	plan.ID = types.StringValue(credentialID)
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
}

func (credentialResource *dnsCredentialResource) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	apiClient := credentialResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state dnsCredentialModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}

	credentials, err := apiClient.ListDNSCredentials(ctx)
	if err != nil {
		response.Diagnostics.AddError("Unable to read DNS credential", err.Error())
		return
	}
	found := dnsCredentialFind(credentials, state.ID.ValueString(), state.Name.ValueString())
	if found == nil {
		response.State.RemoveResource(ctx)
		return
	}
	if readTargetChanged(state.ID, found.ID) {
		response.State.RemoveResource(ctx)
		return
	}
	state.ID = types.StringValue(found.ID)
	state.Name = types.StringValue(found.Name)
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
}

// dnsCredentialFind finds the credential by its id, or, before the id is
// known (on import), by name.
func dnsCredentialFind(credentials []client.DNSCredential, credentialID, name string) *client.DNSCredential {
	for index := range credentials {
		if credentialID != "" && credentials[index].ID == credentialID {
			return &credentials[index]
		}
	}
	if credentialID != "" {
		return nil
	}
	for index := range credentials {
		if credentials[index].Name == name {
			return &credentials[index]
		}
	}
	return nil
}

// Update only runs after an import, when the secret was unknown to state:
// it records the configured URL. Any other change replaces the credential.
func (credentialResource *dnsCredentialResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	if credentialResource.requireClient(&response.Diagnostics) == nil {
		return
	}
	var plan dnsCredentialModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
}

func (credentialResource *dnsCredentialResource) Delete(ctx context.Context, request resource.DeleteRequest, response *resource.DeleteResponse) {
	apiClient := credentialResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state dnsCredentialModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	if err := apiClient.DeleteDNSOrganisationCredential(ctx, state.ID.ValueString()); err != nil {
		response.Diagnostics.AddError("Unable to delete DNS credential", err.Error())
	}
}

// ImportState imports by credential name. The webhook URL cannot be read
// back; the first apply after importing records the configured one without
// replacing the credential.
func (credentialResource *dnsCredentialResource) ImportState(ctx context.Context, request resource.ImportStateRequest, response *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("name"), request, response)
}

// alertDNSReplaceUnlessImported forces replacement when a write-only secret
// changes, except when state holds none (just imported): then the configured
// value is recorded in place, since there is nothing to compare it with.
func alertDNSReplaceUnlessImported() planmodifier.String {
	return stringplanmodifier.RequiresReplaceIf(
		func(_ context.Context, request planmodifier.StringRequest, response *stringplanmodifier.RequiresReplaceIfFuncResponse) {
			response.RequiresReplace = !request.StateValue.IsNull()
		},
		"Changing the secret replaces the object, except right after an import, when the configured value is recorded.",
		"Changing the secret replaces the object, except right after an import, when the configured value is recorded.",
	)
}
