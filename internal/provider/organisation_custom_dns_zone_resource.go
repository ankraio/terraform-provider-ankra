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
)

type organisationCustomDNSZoneResource struct {
	configuredClient
}

type organisationCustomDNSZoneModel struct {
	ID             types.String `tfsdk:"id"`
	Zone           types.String `tfsdk:"zone"`
	CredentialName types.String `tfsdk:"credential_name"`
	Provider       types.String `tfsdk:"provider_kind"`
	Policy         types.String `tfsdk:"policy"`
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
			"provider_kind": schema.StringAttribute{
				MarkdownDescription: "Credential family: `webhook`, `cloudflare` or `avura`. The platform derives it from the credential; " +
					"set it to assert the family, which the platform checks. Sent as `provider`.",
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

func (zoneResource *organisationCustomDNSZoneResource) declare(ctx context.Context, apiClient *client.Client, plan *organisationCustomDNSZoneModel) error {
	declared, err := apiClient.DeclareOrganisationCustomDNSZone(ctx, client.OrganisationCustomDNSZoneDeclaration{
		Zone:           plan.Zone.ValueString(),
		CredentialName: plan.CredentialName.ValueString(),
		Provider:       stringPointer(plan.Provider),
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
	if response.Diagnostics.HasError() {
		return
	}
	if err := zoneResource.declare(ctx, apiClient, &plan); err != nil {
		response.Diagnostics.AddError("Unable to declare organisation custom DNS zone", err.Error())
		return
	}
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
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
	if removeWhenNotFound(ctx, err, response) {
		return
	}
	if err != nil {
		response.Diagnostics.AddError("Unable to read organisation custom DNS zone", err.Error())
		return
	}
	organisationCustomDNSZoneApply(zone, &state)
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
}

func (zoneResource *organisationCustomDNSZoneResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	apiClient := zoneResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var plan organisationCustomDNSZoneModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}
	if err := zoneResource.declare(ctx, apiClient, &plan); err != nil {
		response.Diagnostics.AddError("Unable to rebind organisation custom DNS zone", err.Error())
		return
	}
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
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
	model.Provider = optionalString(alertDNSStringValue(zone.Provider))
	model.Policy = optionalString(alertDNSStringValue(zone.Policy))
}
