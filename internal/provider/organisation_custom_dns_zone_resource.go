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
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"ankra.io/terraform-provider-ankra/internal/client"
)

var (
	_ resource.Resource                = (*organisationCustomDNSZoneResource)(nil)
	_ resource.ResourceWithConfigure   = (*organisationCustomDNSZoneResource)(nil)
	_ resource.ResourceWithImportState = (*organisationCustomDNSZoneResource)(nil)
	_ resource.ResourceWithModifyPlan  = (*organisationCustomDNSZoneResource)(nil)
)

type organisationCustomDNSZoneResource struct {
	configuredClient
}

type organisationCustomDNSZoneModel struct {
	ID                 types.String `tfsdk:"id"`
	Zone               types.String `tfsdk:"zone"`
	CredentialName     types.String `tfsdk:"credential_name"`
	CredentialProvider types.String `tfsdk:"credential_provider"`
	Policy             types.String `tfsdk:"policy"`
}

// NewOrganisationCustomDNSZoneResource returns the
// ankra_organisation_custom_dns_zone resource, the counterpart of
// `ankra org custom-dns-zones add|remove`.
func NewOrganisationCustomDNSZoneResource() resource.Resource {
	return &organisationCustomDNSZoneResource{}
}

func (zoneResource *organisationCustomDNSZoneResource) Metadata(_ context.Context, request resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_organisation_custom_dns_zone"
}

func (zoneResource *organisationCustomDNSZoneResource) Schema(_ context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: "A DNS zone you own that every cluster in the organisation serves, publishing records with your own credential " +
			"(an `ankra_dns_credential` webhook, an `ankra_cloudflare_credential`, or an Avura credential). Removing it withdraws the zone from " +
			"every cluster that did not declare it itself; the zone and its records are left untouched. " +
			"The counterpart of `ankra org custom-dns-zones add|remove`.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "The zone name.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"zone": schema.StringAttribute{
				MarkdownDescription: "Zone to serve, such as `example.com`, in lower case without a trailing dot. Changing it replaces the declaration.",
				Required:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
				Validators:          []validator.String{alertDNSLowerCaseValidator{}},
			},
			"credential_name": schema.StringAttribute{
				MarkdownDescription: "Name of the organisation credential that publishes into the zone. Changing it rebinds the zone in place.",
				Required:            true,
			},
			"credential_provider": schema.StringAttribute{
				MarkdownDescription: "Credential family: `webhook`, `cloudflare` or `avura`. The platform derives it from the credential; " +
					"set it to assert the family, which the platform checks. Sent as `provider` only when set here. Rebinding to another credential re-derives it.",
				Optional:      true,
				Computed:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
				Validators:    []validator.String{alertDNSOneOfValidator{allowed: []string{"webhook", "cloudflare", "avura"}}},
			},
			"policy": schema.StringAttribute{
				MarkdownDescription: "external-dns policy the clusters run for the zone, as the platform chose it for the credential family.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
		},
	}
}

// ModifyPlan marks the derived credential family and policy unknown when the
// zone is rebound to another credential, which may be of another family:
// carrying the old values forward would assert a family the new credential
// does not have.
func (zoneResource *organisationCustomDNSZoneResource) ModifyPlan(ctx context.Context, request resource.ModifyPlanRequest, response *resource.ModifyPlanResponse) {
	if request.State.Raw.IsNull() || request.Plan.Raw.IsNull() {
		return
	}
	var state, plan, configuration organisationCustomDNSZoneModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	response.Diagnostics.Append(request.Config.Get(ctx, &configuration)...)
	if response.Diagnostics.HasError() || plan.CredentialName.Equal(state.CredentialName) {
		return
	}
	if configuration.CredentialProvider.IsNull() {
		plan.CredentialProvider = types.StringUnknown()
	}
	plan.Policy = types.StringUnknown()
	response.Diagnostics.Append(response.Plan.Set(ctx, &plan)...)
}

// declare sends the declaration. provider is sent only when configuration
// sets it: otherwise the platform derives the family from the credential.
func (zoneResource *organisationCustomDNSZoneResource) declare(ctx context.Context, apiClient *client.Client, configuredProvider types.String, plan *organisationCustomDNSZoneModel) error {
	declared, err := apiClient.DeclareOrganisationCustomDNSZone(ctx, client.OrganisationCustomDNSZoneDeclaration{
		Zone:           plan.Zone.ValueString(),
		CredentialName: plan.CredentialName.ValueString(),
		Provider:       stringPointer(configuredProvider),
	})
	if err != nil {
		return err
	}
	organisationCustomDNSZoneApply(declared, plan)
	return nil
}

func (zoneResource *organisationCustomDNSZoneResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	apiClient := zoneResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var plan organisationCustomDNSZoneModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	var configuredProvider types.String
	response.Diagnostics.Append(request.Config.GetAttribute(ctx, path.Root("credential_provider"), &configuredProvider)...)
	if response.Diagnostics.HasError() {
		return
	}
	if err := zoneResource.declare(ctx, apiClient, configuredProvider, &plan); err != nil {
		response.Diagnostics.AddError("Unable to declare organisation custom DNS zone", err.Error())
		return
	}
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
	response.Diagnostics.Append(trackScope(ctx, apiClient, response.Private)...)
}

func (zoneResource *organisationCustomDNSZoneResource) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	apiClient := zoneResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state organisationCustomDNSZoneModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}

	zone, err := apiClient.GetOrganisationCustomDNSZone(ctx, state.ID.ValueString())
	if removeWhenNotFound(ctx, err, apiClient, response) {
		return
	}
	if err != nil {
		response.Diagnostics.AddError("Unable to read organisation custom DNS zone", err.Error())
		return
	}
	organisationCustomDNSZoneApply(zone, &state)
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
	response.Diagnostics.Append(trackScope(ctx, apiClient, response.Private)...)
}

func (zoneResource *organisationCustomDNSZoneResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	apiClient := zoneResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var plan organisationCustomDNSZoneModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	var configuredProvider types.String
	response.Diagnostics.Append(request.Config.GetAttribute(ctx, path.Root("credential_provider"), &configuredProvider)...)
	if response.Diagnostics.HasError() {
		return
	}
	if err := zoneResource.declare(ctx, apiClient, configuredProvider, &plan); err != nil {
		response.Diagnostics.AddError("Unable to rebind organisation custom DNS zone", err.Error())
		return
	}
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
	response.Diagnostics.Append(trackScope(ctx, apiClient, response.Private)...)
}

func (zoneResource *organisationCustomDNSZoneResource) Delete(ctx context.Context, request resource.DeleteRequest, response *resource.DeleteResponse) {
	apiClient := zoneResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state organisationCustomDNSZoneModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	if err := apiClient.RemoveOrganisationCustomDNSZone(ctx, state.ID.ValueString()); err != nil {
		response.Diagnostics.AddError("Unable to withdraw organisation custom DNS zone", err.Error())
	}
}

// ImportState imports by zone name.
func (zoneResource *organisationCustomDNSZoneResource) ImportState(ctx context.Context, request resource.ImportStateRequest, response *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), request, response)
	resource.ImportStatePassthroughID(ctx, path.Root("zone"), request, response)
}

func organisationCustomDNSZoneApply(zone *client.OrganisationCustomDNSZone, model *organisationCustomDNSZoneModel) {
	model.ID = types.StringValue(zone.Zone)
	model.Zone = types.StringValue(zone.Zone)
	model.CredentialName = types.StringValue(zone.CredentialName)
	model.CredentialProvider = optionalString(alertDNSStringValue(zone.Provider))
	model.Policy = optionalString(alertDNSStringValue(zone.Policy))
}
