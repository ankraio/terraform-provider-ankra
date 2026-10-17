// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"strings"

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
	_ resource.Resource                = (*organisationDomainResource)(nil)
	_ resource.ResourceWithConfigure   = (*organisationDomainResource)(nil)
	_ resource.ResourceWithImportState = (*organisationDomainResource)(nil)
)

// organisationDomainSingletonID is the fixed id of the singleton.
const organisationDomainSingletonID = "organisation_domain"

type organisationDomainResource struct {
	configuredClient
}

type organisationDomainModel struct {
	ID                   types.String `tfsdk:"id"`
	DNSRootDomain        types.String `tfsdk:"dns_root_domain"`
	DNSRootDomainDefault types.String `tfsdk:"dns_root_domain_default"`
}

// NewOrganisationDomainResource returns the ankra_organisation_domain
// resource, the counterpart of `ankra org domain set`.
func NewOrganisationDomainResource() resource.Resource {
	return &organisationDomainResource{}
}

func (domainResource *organisationDomainResource) Metadata(_ context.Context, request resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_organisation_domain"
}

func (domainResource *organisationDomainResource) Schema(_ context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: "The organisation's own Ankra root domain, under which cluster zones and the organisation's DNS zone are created. " +
			"A singleton: declare it once per organisation. Destroying it restores the platform default (`dns_root_domain_default`). " +
			"The platform refuses a change, including the destroy, while cluster zones or DNS records still hang off the current domain: give " +
			"`ankra_organisation_dns_record` and `ankra_organisation_custom_dns_zone` resources a `depends_on` on this resource so Terraform removes them first. " +
			"Creating it takes over a domain already set in the portal, and destroying it then restores the default rather than that earlier domain. " +
			"The counterpart of `ankra org domain set`.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Fixed identifier of the singleton.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"dns_root_domain": schema.StringAttribute{
				MarkdownDescription: "Root domain delegated to Ankra, such as `apps.example.com`, in lower case.",
				Required:            true,
				Validators:          []validator.String{alertDNSLowerCaseValidator{}},
			},
			"dns_root_domain_default": schema.StringAttribute{
				MarkdownDescription: "The platform default root domain, used when none is set.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
		},
	}
}

func (domainResource *organisationDomainResource) set(ctx context.Context, apiClient *client.Client, plan *organisationDomainModel) error {
	domain, err := apiClient.SetOrganisationDNSRootDomain(ctx, stringPointer(plan.DNSRootDomain))
	if err != nil {
		return err
	}
	plan.ID = types.StringValue(organisationDomainSingletonID)
	plan.DNSRootDomainDefault = types.StringValue(domain.DNSRootDomainDefault)
	return nil
}

func (domainResource *organisationDomainResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	apiClient := domainResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var plan organisationDomainModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}
	if err := domainResource.set(ctx, apiClient, &plan); err != nil {
		response.Diagnostics.AddError("Unable to set organisation domain", err.Error())
		return
	}
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
	response.Diagnostics.Append(trackScope(ctx, apiClient, response.Private)...)
}

func (domainResource *organisationDomainResource) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	apiClient := domainResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state organisationDomainModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}

	domain, err := apiClient.GetOrganisationDNSRootDomain(ctx)
	if err != nil {
		response.Diagnostics.AddError("Unable to read organisation domain", err.Error())
		return
	}
	if domain.DNSRootDomain == nil {
		removeWhenTargetChanged(ctx, apiClient, response)
		return
	}
	if !strings.EqualFold(state.DNSRootDomain.ValueString(), *domain.DNSRootDomain) {
		state.DNSRootDomain = types.StringValue(*domain.DNSRootDomain)
	}
	state.ID = types.StringValue(organisationDomainSingletonID)
	state.DNSRootDomainDefault = types.StringValue(domain.DNSRootDomainDefault)
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
	response.Diagnostics.Append(trackScope(ctx, apiClient, response.Private)...)
}

func (domainResource *organisationDomainResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	apiClient := domainResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var plan organisationDomainModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}
	if err := domainResource.set(ctx, apiClient, &plan); err != nil {
		response.Diagnostics.AddError("Unable to set organisation domain", err.Error())
		return
	}
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
	response.Diagnostics.Append(trackScope(ctx, apiClient, response.Private)...)
}

// Delete restores the platform default root domain.
func (domainResource *organisationDomainResource) Delete(ctx context.Context, _ resource.DeleteRequest, response *resource.DeleteResponse) {
	apiClient := domainResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	if _, err := apiClient.SetOrganisationDNSRootDomain(ctx, nil); err != nil {
		response.Diagnostics.AddError("Unable to restore the default organisation domain", err.Error())
	}
}

// ImportState imports the singleton; any import id is accepted and replaced
// by the fixed one.
func (domainResource *organisationDomainResource) ImportState(ctx context.Context, _ resource.ImportStateRequest, response *resource.ImportStateResponse) {
	response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("id"), organisationDomainSingletonID)...)
}
