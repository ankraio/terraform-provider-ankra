// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"ankra.io/terraform-provider-ankra/internal/client"
)

var addonChartLinkTypes = []string{"include", "exclude"}

var (
	_ resource.Resource                = (*addonDefinitionResource)(nil)
	_ resource.ResourceWithConfigure   = (*addonDefinitionResource)(nil)
	_ resource.ResourceWithImportState = (*addonDefinitionResource)(nil)
)

type addonDefinitionResource struct {
	configuredClient
}

type addonDefinitionModel struct {
	ID                   types.String `tfsdk:"id"`
	Name                 types.String `tfsdk:"name"`
	DisplayName          types.String `tfsdk:"display_name"`
	Description          types.String `tfsdk:"description"`
	WebsiteURL           types.String `tfsdk:"website_url"`
	DocumentationURL     types.String `tfsdk:"documentation_url"`
	IconURL              types.String `tfsdk:"icon_url"`
	Category             types.String `tfsdk:"category"`
	MatchChartNames      types.Set    `tfsdk:"match_chart_names"`
	MatchKeywords        types.Set    `tfsdk:"match_keywords"`
	MatchHomeURLPrefixes types.Set    `tfsdk:"match_home_url_prefixes"`
	ChartLinks           types.Set    `tfsdk:"chart_links"`
}

type addonChartLinkModel struct {
	ChartName   types.String `tfsdk:"chart_name"`
	RegistryURL types.String `tfsdk:"registry_url"`
	LinkType    types.String `tfsdk:"link_type"`
}

var addonChartLinkType = types.ObjectType{AttrTypes: map[string]attr.Type{
	"chart_name":   types.StringType,
	"registry_url": types.StringType,
	"link_type":    types.StringType,
}}

// NewAddonDefinitionResource returns the ankra_addon_definition resource, the
// counterpart of `ankra helm addon-definitions`.
func NewAddonDefinitionResource() resource.Resource {
	return &addonDefinitionResource{}
}

func (definition *addonDefinitionResource) Metadata(_ context.Context, request resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_addon_definition"
}

func addonDefinitionOptionalString(description string) schema.StringAttribute {
	return schema.StringAttribute{MarkdownDescription: description, Optional: true}
}

func addonDefinitionOptionalSet(description string) schema.SetAttribute {
	return schema.SetAttribute{MarkdownDescription: description, Optional: true, ElementType: types.StringType}
}

func (definition *addonDefinitionResource) Schema(_ context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: "An organisation add-on definition (`ankra helm addon-definitions`): a curated add-on in the organisation's " +
			"catalogue, the rules that match charts to it, and explicit chart links that include or exclude one chart of a registry " +
			"whatever the rules say. Fields and chart links change in place; changing `name` replaces the definition. Ankra's " +
			"global definitions are read-only and cannot be managed. Destroy deletes the definition.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Identifier of the definition.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				MarkdownDescription: "Definition name: lower-case letters, digits and hyphens, at most 100 characters. Changing it replaces the definition.",
				Required:            true,
				Validators:          []validator.String{clusterSettingsNoSlashValidator{}},
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"display_name": schema.StringAttribute{
				MarkdownDescription: "Name shown in the catalogue, at most 200 characters.",
				Required:            true,
			},
			"description":             addonDefinitionOptionalString("Free-text description."),
			"website_url":             addonDefinitionOptionalString("Project website."),
			"documentation_url":       addonDefinitionOptionalString("Documentation address."),
			"icon_url":                addonDefinitionOptionalString("Icon address."),
			"category":                addonDefinitionOptionalString("Catalogue category."),
			"match_chart_names":       addonDefinitionOptionalSet("Chart names that match the definition."),
			"match_keywords":          addonDefinitionOptionalSet("Chart keywords that match the definition."),
			"match_home_url_prefixes": addonDefinitionOptionalSet("Chart home address prefixes that match the definition."),
			"chart_links": schema.SetNestedAttribute{
				MarkdownDescription: "Explicit links that include or exclude one chart of a registry, overriding the match rules for it.",
				Optional:            true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"chart_name":   schema.StringAttribute{MarkdownDescription: "Chart name.", Required: true},
						"registry_url": schema.StringAttribute{MarkdownDescription: "Address of the registry holding the chart.", Required: true},
						"link_type": schema.StringAttribute{
							MarkdownDescription: "`include` or `exclude`.",
							Required:            true,
							Validators:          []validator.String{organisationStringOneOf{values: addonChartLinkTypes}},
						},
					},
				},
			},
		},
	}
}

func (definition *addonDefinitionResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	apiClient := definition.requireOrganisationClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var plan addonDefinitionModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	write := addonDefinitionWrite(ctx, plan, &response.Diagnostics)
	links := addonChartLinks(ctx, plan.ChartLinks, &response.Diagnostics)
	if response.Diagnostics.HasError() {
		return
	}
	write.Name = plan.Name.ValueString()
	created, createError := apiClient.CreateAddonDefinition(ctx, write)
	if createError != nil {
		response.Diagnostics.AddError("Unable to create the add-on definition", createError.Error())
		return
	}
	plan.ID = types.StringValue(created.ID)
	for _, link := range links {
		if linkError := apiClient.LinkAddonChart(ctx, created.Name, link); linkError != nil {
			plan.ChartLinks = types.SetNull(addonChartLinkType)
			response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
			response.Diagnostics.AddError("Unable to link a chart to the add-on definition", linkError.Error())
			return
		}
	}
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
	response.Diagnostics.Append(trackScope(ctx, apiClient, response.Private)...)
}

func (definition *addonDefinitionResource) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	apiClient := definition.requireOrganisationClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state addonDefinitionModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	found, readError := apiClient.GetAddonDefinition(ctx, state.Name.ValueString())
	if removeWhenNotFound(ctx, readError, apiClient, response) {
		return
	}
	if readError != nil {
		response.Diagnostics.AddError("Unable to read the add-on definition", readError.Error())
		return
	}
	if readTargetChanged(state.ID, found.ID) {
		removeWhenTargetChanged(ctx, apiClient, response)
		return
	}
	if found.Source == "global" {
		response.Diagnostics.AddError("Global add-on definition", fmt.Sprintf("%q is one of Ankra's global definitions, which are read-only.", found.Name))
		return
	}
	addonDefinitionApply(&state, found)
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
	response.Diagnostics.Append(trackScope(ctx, apiClient, response.Private)...)
}

func (definition *addonDefinitionResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	apiClient := definition.requireOrganisationClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var plan, state addonDefinitionModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	write := addonDefinitionWrite(ctx, plan, &response.Diagnostics)
	wanted := addonChartLinks(ctx, plan.ChartLinks, &response.Diagnostics)
	current := addonChartLinks(ctx, state.ChartLinks, &response.Diagnostics)
	if response.Diagnostics.HasError() {
		return
	}
	name := state.Name.ValueString()
	if _, updateError := apiClient.UpdateAddonDefinition(ctx, name, write); updateError != nil {
		response.Diagnostics.AddError("Unable to update the add-on definition", updateError.Error())
		return
	}
	for _, link := range current {
		if addonChartLinkIn(wanted, link) {
			continue
		}
		if unlinkError := apiClient.UnlinkAddonChart(ctx, name, link); unlinkError != nil && !client.IsNotFound(unlinkError) {
			response.Diagnostics.AddError("Unable to unlink a chart from the add-on definition", unlinkError.Error())
			return
		}
	}
	for _, link := range wanted {
		if addonChartLinkIn(current, link) {
			continue
		}
		if linkError := apiClient.LinkAddonChart(ctx, name, link); linkError != nil {
			response.Diagnostics.AddError("Unable to link a chart to the add-on definition", linkError.Error())
			return
		}
	}
	plan.ID = state.ID
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
}

func (definition *addonDefinitionResource) Delete(ctx context.Context, request resource.DeleteRequest, response *resource.DeleteResponse) {
	apiClient := definition.requireOrganisationClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state addonDefinitionModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	deleteError := apiClient.DeleteAddonDefinition(ctx, state.Name.ValueString())
	if deleteError == nil || deletedWhenNotFound(ctx, deleteError, apiClient, request.Private, &response.Diagnostics) || response.Diagnostics.HasError() {
		return
	}
	response.Diagnostics.AddError("Unable to delete the add-on definition", deleteError.Error())
}

// ImportState imports by definition name.
func (definition *addonDefinitionResource) ImportState(ctx context.Context, request resource.ImportStateRequest, response *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("name"), request, response)
}

func addonDefinitionWrite(ctx context.Context, plan addonDefinitionModel, diagnostics *diag.Diagnostics) client.AddonDefinitionWrite {
	return client.AddonDefinitionWrite{
		DisplayName:          plan.DisplayName.ValueString(),
		Description:          stringPointer(plan.Description),
		WebsiteURL:           stringPointer(plan.WebsiteURL),
		DocumentationURL:     stringPointer(plan.DocumentationURL),
		IconURL:              stringPointer(plan.IconURL),
		Category:             stringPointer(plan.Category),
		MatchChartNames:      organisationNonNilStrings(organisationStringsFromSet(ctx, plan.MatchChartNames, diagnostics)),
		MatchKeywords:        organisationNonNilStrings(organisationStringsFromSet(ctx, plan.MatchKeywords, diagnostics)),
		MatchHomeURLPrefixes: organisationNonNilStrings(organisationStringsFromSet(ctx, plan.MatchHomeURLPrefixes, diagnostics)),
	}
}

func addonChartLinks(ctx context.Context, set types.Set, diagnostics *diag.Diagnostics) []client.AddonChartLink {
	if set.IsNull() || set.IsUnknown() {
		return nil
	}
	var models []addonChartLinkModel
	diagnostics.Append(set.ElementsAs(ctx, &models, false)...)
	links := make([]client.AddonChartLink, 0, len(models))
	for _, model := range models {
		links = append(links, client.AddonChartLink{
			ChartName: model.ChartName.ValueString(), RegistryURL: model.RegistryURL.ValueString(), LinkType: model.LinkType.ValueString(),
		})
	}
	return links
}

func addonChartLinkIn(links []client.AddonChartLink, wanted client.AddonChartLink) bool {
	for _, link := range links {
		if link.ChartName == wanted.ChartName && link.RegistryURL == wanted.RegistryURL && link.LinkType == wanted.LinkType {
			return true
		}
	}
	return false
}

// addonDefinitionApply records the definition as read. Chart links Ankra
// added itself are not the organisation's, so only organisation links are
// tracked.
func addonDefinitionApply(model *addonDefinitionModel, found *client.AddonDefinition) {
	model.ID = types.StringValue(found.ID)
	model.Name = types.StringValue(found.Name)
	model.DisplayName = types.StringValue(found.DisplayName)
	model.Description = optionalStringFromPointer(found.Description)
	model.WebsiteURL = optionalStringFromPointer(found.WebsiteURL)
	model.DocumentationURL = optionalStringFromPointer(found.DocumentationURL)
	model.IconURL = optionalStringFromPointer(found.IconURL)
	model.Category = optionalStringFromPointer(found.Category)
	model.MatchChartNames = organisationOptionalSet(model.MatchChartNames, found.MatchChartNames)
	model.MatchKeywords = organisationOptionalSet(model.MatchKeywords, found.MatchKeywords)
	model.MatchHomeURLPrefixes = organisationOptionalSet(model.MatchHomeURLPrefixes, found.MatchHomeURLPrefixes)
	links := make([]attr.Value, 0, len(found.ChartLinks))
	for _, link := range found.ChartLinks {
		if link.Source == "global" {
			continue
		}
		links = append(links, types.ObjectValueMust(addonChartLinkType.AttrTypes, map[string]attr.Value{
			"chart_name": types.StringValue(link.ChartName), "registry_url": types.StringValue(link.RegistryURL),
			"link_type": types.StringValue(link.LinkType),
		}))
	}
	if len(links) == 0 && model.ChartLinks.IsNull() {
		return
	}
	model.ChartLinks = types.SetValueMust(addonChartLinkType, links)
}
