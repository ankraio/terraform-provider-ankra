// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/objectplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"ankra.io/terraform-provider-ankra/internal/client"
)

var securityEvidenceKinds = []string{"vanta", "wazuh", "webhook"}

var (
	_ resource.Resource                = (*securityEvidenceDestinationResource)(nil)
	_ resource.ResourceWithConfigure   = (*securityEvidenceDestinationResource)(nil)
	_ resource.ResourceWithImportState = (*securityEvidenceDestinationResource)(nil)
)

type securityEvidenceDestinationResource struct {
	configuredClient
}

type securityEvidenceDestinationModel struct {
	ID                   types.String                            `tfsdk:"id"`
	Name                 types.String                            `tfsdk:"name"`
	Kind                 types.String                            `tfsdk:"kind"`
	Config               *securityEvidenceDestinationConfigModel `tfsdk:"config"`
	CredentialWO         types.Map                               `tfsdk:"credential_wo"`
	CredentialWOVersion  types.Int64                             `tfsdk:"credential_wo_version"`
	Enabled              types.Bool                              `tfsdk:"enabled"`
	CredentialConfigured types.Bool                              `tfsdk:"credential_configured"`
}

type securityEvidenceDestinationConfigModel struct {
	APIBaseURL types.String `tfsdk:"api_base_url"`
	DocumentID types.String `tfsdk:"document_id"`
	URL        types.String `tfsdk:"url"`
}

// NewSecurityEvidenceDestinationResource returns the
// ankra_security_evidence_destination resource, the counterpart of
// `ankra security evidence destinations`.
func NewSecurityEvidenceDestinationResource() resource.Resource {
	return &securityEvidenceDestinationResource{}
}

func (destination *securityEvidenceDestinationResource) Metadata(_ context.Context, request resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_security_evidence_destination"
}

func securityEvidenceConfigString(description string) schema.StringAttribute {
	return schema.StringAttribute{MarkdownDescription: description, Optional: true}
}

func (destination *securityEvidenceDestinationResource) Schema(_ context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: "A compliance evidence destination (`ankra security evidence destinations`): a Vanta document, a Wazuh relay " +
			"or a signed webhook that security evidence deliveries are sent to. The credential is write-only (Terraform 1.11 or " +
			"later): it goes to the platform's secret store and never reaches plan or state. The platform cannot change a " +
			"destination, so any change, including a new `credential_wo_version`, replaces it. Destroy revokes the destination: " +
			"it is disabled, its credential deleted and its undelivered deliveries cancelled, and its record stays for the history. " +
			"Sending deliveries stays imperative (`ankra security evidence deliveries send`).",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Identifier of the destination.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				MarkdownDescription: "Destination name. Changing it replaces the destination.",
				Required:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"kind": schema.StringAttribute{
				MarkdownDescription: "`vanta`, `wazuh` or `webhook`. Changing it replaces the destination.",
				Required:            true,
				Validators:          []validator.String{organisationStringOneOf{values: securityEvidenceKinds}},
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"config": schema.SingleNestedAttribute{
				MarkdownDescription: "Where evidence goes. Addresses must be `https`. Changing it replaces the destination.",
				Required:            true,
				PlanModifiers:       []planmodifier.Object{objectplanmodifier.RequiresReplace()},
				Attributes: map[string]schema.Attribute{
					"api_base_url": securityEvidenceConfigString("Vanta API base address."),
					"document_id":  securityEvidenceConfigString("Vanta document the evidence is attached to."),
					"url":          securityEvidenceConfigString("Wazuh relay or webhook address."),
				},
			},
			"credential_wo": schema.MapAttribute{
				MarkdownDescription: "The destination's credential, such as a Vanta OAuth client or a webhook signing secret, as a write-only attribute.",
				Required:            true,
				Sensitive:           true,
				WriteOnly:           true,
				ElementType:         types.StringType,
			},
			"credential_wo_version": schema.Int64Attribute{
				MarkdownDescription: "Version of `credential_wo`. Terraform cannot see a change to a write-only value, so change this to store a new credential; that replaces the destination.",
				Required:            true,
				PlanModifiers:       []planmodifier.Int64{writeOnlyVersionReplaceUnlessImported()},
			},
			"enabled": schema.BoolAttribute{
				MarkdownDescription: "Whether the destination receives deliveries.",
				Computed:            true,
			},
			"credential_configured": schema.BoolAttribute{
				MarkdownDescription: "Whether the platform holds the destination's credential.",
				Computed:            true,
			},
		},
	}
}

func (destination *securityEvidenceDestinationResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	apiClient := destination.requireOrganisationClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var plan securityEvidenceDestinationModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	credential := writeOnlyCredentialMap(ctx, request.Config, &response.Diagnostics)
	if response.Diagnostics.HasError() {
		return
	}
	create := client.SecurityEvidenceDestinationCreate{
		Name:       plan.Name.ValueString(),
		Kind:       plan.Kind.ValueString(),
		Credential: credential,
	}
	if plan.Config != nil {
		create.Config = client.SecurityEvidenceDestinationConfig{
			APIBaseURL: plan.Config.APIBaseURL.ValueString(), DocumentID: plan.Config.DocumentID.ValueString(), URL: plan.Config.URL.ValueString(),
		}
	}
	created, createError := apiClient.CreateSecurityEvidenceDestination(ctx, create)
	if createError != nil {
		response.Diagnostics.AddError("Unable to create the security evidence destination", createError.Error())
		return
	}
	plan.ID = types.StringValue(created.ID)
	plan.Enabled = types.BoolValue(created.Enabled)
	plan.CredentialConfigured = types.BoolValue(created.CredentialConfigured)
	plan.CredentialWO = types.MapNull(types.StringType)
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
	response.Diagnostics.Append(trackScope(ctx, apiClient, response.Private)...)
}

func (destination *securityEvidenceDestinationResource) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	apiClient := destination.requireOrganisationClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state securityEvidenceDestinationModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	destinations, listError := apiClient.ListSecurityEvidenceDestinations(ctx)
	if listError != nil {
		response.Diagnostics.AddError("Unable to read the security evidence destinations", listError.Error())
		return
	}
	for _, listed := range destinations {
		if listed.ID != state.ID.ValueString() {
			continue
		}
		if listed.RevokedAt != nil {
			break
		}
		state.Name = types.StringValue(listed.Name)
		state.Kind = types.StringValue(listed.Kind)
		state.Config = &securityEvidenceDestinationConfigModel{
			APIBaseURL: optionalString(listed.Config.APIBaseURL),
			DocumentID: optionalString(listed.Config.DocumentID),
			URL:        optionalString(listed.Config.URL),
		}
		state.Enabled = types.BoolValue(listed.Enabled)
		state.CredentialConfigured = types.BoolValue(listed.CredentialConfigured)
		response.Diagnostics.Append(response.State.Set(ctx, &state)...)
		response.Diagnostics.Append(trackScope(ctx, apiClient, response.Private)...)
		return
	}
	removeWhenTargetChanged(ctx, apiClient, response)
}

// Update only records a credential version right after an import; every
// other change replaces the destination.
func (destination *securityEvidenceDestinationResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	var plan securityEvidenceDestinationModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
	response.Diagnostics.Append(response.Private.SetKey(ctx, credentialImportedKey, nil)...)
}

func (destination *securityEvidenceDestinationResource) Delete(ctx context.Context, request resource.DeleteRequest, response *resource.DeleteResponse) {
	apiClient := destination.requireOrganisationClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state securityEvidenceDestinationModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	revokeError := apiClient.RevokeSecurityEvidenceDestination(ctx, state.ID.ValueString())
	if revokeError == nil || deletedWhenNotFound(ctx, revokeError, apiClient, request.Private, &response.Diagnostics) || response.Diagnostics.HasError() {
		return
	}
	response.Diagnostics.AddError("Unable to revoke the security evidence destination", revokeError.Error())
}

// ImportState imports by destination id. The credential is never read back;
// the first apply records the configured credential_wo_version.
func (destination *securityEvidenceDestinationResource) ImportState(ctx context.Context, request resource.ImportStateRequest, response *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), request, response)
	response.Diagnostics.Append(response.Private.SetKey(ctx, credentialImportedKey, []byte("true"))...)
}
