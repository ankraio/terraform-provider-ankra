// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	datasourceschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
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

const (
	cloudDNSAccessModePublic       = "public"
	cloudDNSAccessModeOrganisation = "organisation"
)

var (
	_ resource.Resource                   = (*cloudDNSZoneAccessResource)(nil)
	_ resource.ResourceWithConfigure      = (*cloudDNSZoneAccessResource)(nil)
	_ resource.ResourceWithImportState    = (*cloudDNSZoneAccessResource)(nil)
	_ resource.ResourceWithValidateConfig = (*cloudDNSZoneAccessResource)(nil)
)

type cloudDNSZoneAccessResource struct {
	configuredClient
}

type cloudDNSZoneAccessModel struct {
	ID            types.String `tfsdk:"id"`
	ZoneID        types.String `tfsdk:"zone_id"`
	Mode          types.String `tfsdk:"mode"`
	Roles         types.Set    `tfsdk:"roles"`
	ProjectID     types.String `tfsdk:"project_id"`
	InheritedFrom types.String `tfsdk:"inherited_from"`
}

// NewCloudDNSZoneAccessResource returns the ankra_cloud_dns_zone_access
// resource, the counterpart of `ankra org access-gateway set`.
func NewCloudDNSZoneAccessResource() resource.Resource {
	return &cloudDNSZoneAccessResource{}
}

func (access *cloudDNSZoneAccessResource) Metadata(_ context.Context, request resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_cloud_dns_zone_access"
}

func (access *cloudDNSZoneAccessResource) Schema(_ context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: "The access gateway policy of an Ankra Cloud DNS zone (`ankra org access-gateway set`): who may reach the " +
			"hostnames the Ankra Cloud proxy serves in the zone. `public` lets anyone through; `organisation` admits only signed-in " +
			"members of the organisation, optionally only those holding one of `roles` and only those who can reach `project_id`. " +
			"A zone without a policy of its own inherits its parent zone's.\n\n" +
			"Needs the `members.manage` permission. Destroy removes the resource from state and leaves the zone's policy in force, " +
			"so a destroy never opens a gated zone to the public.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "The zone id.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"zone_id": schema.StringAttribute{
				MarkdownDescription: "Identifier of the zone, from `ankra_cloud_dns_zones`. Changing it replaces the resource.",
				Required:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
				Validators:          []validator.String{costStringLengthValidator{minimum: 1, maximum: 255}},
			},
			"mode": schema.StringAttribute{
				MarkdownDescription: "`public` (anyone) or `organisation` (signed-in members of the organisation only).",
				Required:            true,
				Validators:          []validator.String{organisationStringOneOf{values: []string{cloudDNSAccessModePublic, cloudDNSAccessModeOrganisation}}},
			},
			"roles": schema.SetAttribute{
				MarkdownDescription: "Organisation roles admitted, by slug; unset admits every member. Only with `mode = \"organisation\"`.",
				ElementType:         types.StringType,
				Optional:            true,
				Validators:          []validator.Set{cloudDNSRolesNotEmpty{}},
			},
			"project_id": schema.StringAttribute{
				MarkdownDescription: "Admit only the members who can reach this project, by ID or slug; unset removes the limit. " +
					"Only with `mode = \"organisation\"`. Writing the same project as its slug or its ID plans no change.",
				Optional:   true,
				Validators: []validator.String{costStringLengthValidator{minimum: 1, maximum: 255}},
			},
			"inherited_from": schema.StringAttribute{
				MarkdownDescription: "The zone the effective policy is inherited from, when the zone has no policy of its own " +
					"(for example after it was removed outside Terraform); null while the zone's own policy applies.",
				Computed: true,
			},
		},
	}
}

func (access *cloudDNSZoneAccessResource) ValidateConfig(ctx context.Context, request resource.ValidateConfigRequest, response *resource.ValidateConfigResponse) {
	var config cloudDNSZoneAccessModel
	response.Diagnostics.Append(request.Config.Get(ctx, &config)...)
	if response.Diagnostics.HasError() || config.Mode.IsUnknown() || config.Mode.ValueString() != cloudDNSAccessModePublic {
		return
	}
	if !config.Roles.IsNull() {
		response.Diagnostics.AddAttributeError(path.Root("roles"), "roles needs mode organisation",
			"A public zone admits everyone, so it cannot be limited to roles.")
	}
	if !config.ProjectID.IsNull() {
		response.Diagnostics.AddAttributeError(path.Root("project_id"), "project_id needs mode organisation",
			"A public zone admits everyone, so it cannot be limited to a project.")
	}
}

func (access *cloudDNSZoneAccessResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	var plan cloudDNSZoneAccessModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if access.write(ctx, &plan, &response.Diagnostics) {
		response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
		response.Diagnostics.Append(trackScope(ctx, access.client, response.Private)...)
	}
}

func (access *cloudDNSZoneAccessResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	var plan cloudDNSZoneAccessModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if access.write(ctx, &plan, &response.Diagnostics) {
		response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
	}
}

func (access *cloudDNSZoneAccessResource) write(ctx context.Context, plan *cloudDNSZoneAccessModel, diagnostics *diag.Diagnostics) bool {
	apiClient := access.requireClient(diagnostics)
	if apiClient == nil || diagnostics.HasError() {
		return false
	}
	projectID := ""
	if !plan.ProjectID.IsNull() && !plan.ProjectID.IsUnknown() {
		resolved, resolveError := apiClient.ProjectIDFor(ctx, plan.ProjectID.ValueString())
		if resolveError != nil {
			diagnostics.AddAttributeError(path.Root("project_id"), "Unable to resolve project_id", resolveError.Error())
			return false
		}
		projectID = resolved
	}
	roles := organisationNonNilStrings(organisationStringsFromSet(ctx, plan.Roles, diagnostics))
	if diagnostics.HasError() {
		return false
	}
	written, writeError := apiClient.SetCloudDNSZoneAccess(ctx, plan.ZoneID.ValueString(), client.CloudDNSZoneAccessWrite{
		Mode:      plan.Mode.ValueString(),
		Roles:     roles,
		ProjectID: &projectID,
	})
	if writeError != nil {
		diagnostics.AddError("Unable to set the zone's access policy", cloudDNSAccessErrorText(writeError))
		return false
	}
	plan.ID = plan.ZoneID
	plan.InheritedFrom = optionalString(written.InheritedFrom)
	return true
}

func (access *cloudDNSZoneAccessResource) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	apiClient := access.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state cloudDNSZoneAccessModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	stored, readError := apiClient.GetCloudDNSZoneAccess(ctx, state.ZoneID.ValueString())
	if removeWhenNotFound(ctx, readError, apiClient, response) {
		return
	}
	if readError != nil {
		response.Diagnostics.AddError("Unable to read the zone's access policy", cloudDNSAccessErrorText(readError))
		return
	}
	state.ID = state.ZoneID
	state.Mode = types.StringValue(stored.Mode)
	state.Roles = organisationOptionalSet(state.Roles, stored.Roles)
	state.ProjectID = cloudDNSZoneAccessProject(ctx, apiClient, state.ProjectID, stored.ProjectID)
	state.InheritedFrom = optionalString(stored.InheritedFrom)
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
	response.Diagnostics.Append(trackScope(ctx, apiClient, response.Private)...)
}

// Delete leaves the zone's policy in force, so a destroy never opens a gated
// zone.
func (access *cloudDNSZoneAccessResource) Delete(_ context.Context, _ resource.DeleteRequest, response *resource.DeleteResponse) {
	response.Diagnostics.AddWarning("Zone access policy left in force",
		"The zone's access policy is no longer managed by Terraform, but it still applies. "+
			"Change it with `ankra org access-gateway set` or in the portal.")
}

// ImportState imports by zone id.
func (access *cloudDNSZoneAccessResource) ImportState(ctx context.Context, request resource.ImportStateRequest, response *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("zone_id"), request, response)
	response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("id"), request.ID)...)
}

// cloudDNSZoneAccessProject maps the project the platform reports to state:
// the configured spelling is kept while it names the same project.
func cloudDNSZoneAccessProject(ctx context.Context, apiClient *client.Client, configured types.String, reported string) types.String {
	if reported == "" {
		return types.StringNull()
	}
	if configured.IsNull() || configured.IsUnknown() {
		return types.StringValue(reported)
	}
	if configured.ValueString() == reported {
		return configured
	}
	resolved, resolveError := apiClient.ProjectIDFor(ctx, configured.ValueString())
	if resolveError == nil && resolved == reported {
		return configured
	}
	return types.StringValue(reported)
}

// cloudDNSAccessErrorText is the platform's detail for a refused request,
// or the raw error when there is none.
func cloudDNSAccessErrorText(err error) string {
	var apiError *client.APIError
	if !errors.As(err, &apiError) {
		return err.Error()
	}
	var body struct {
		Detail string `json:"detail"`
	}
	if json.Unmarshal([]byte(apiError.Body), &body) != nil || strings.TrimSpace(body.Detail) == "" {
		return err.Error()
	}
	return fmt.Sprintf("%s (HTTP %d)", body.Detail, apiError.StatusCode)
}

// cloudDNSRolesNotEmpty refuses an empty roles set, which admits every
// member exactly like leaving roles unset and would plan a diff forever.
type cloudDNSRolesNotEmpty struct{}

func (cloudDNSRolesNotEmpty) Description(_ context.Context) string {
	return "roles must name at least one role; leave it unset to admit every member"
}

func (notEmpty cloudDNSRolesNotEmpty) MarkdownDescription(ctx context.Context) string {
	return notEmpty.Description(ctx)
}

func (notEmpty cloudDNSRolesNotEmpty) ValidateSet(ctx context.Context, request validator.SetRequest, response *validator.SetResponse) {
	if request.ConfigValue.IsNull() || request.ConfigValue.IsUnknown() {
		return
	}
	if len(request.ConfigValue.Elements()) == 0 {
		response.Diagnostics.AddAttributeError(request.Path, "Empty roles", notEmpty.Description(ctx)+".")
	}
}

var (
	_ datasource.DataSource              = (*cloudDNSZonesDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*cloudDNSZonesDataSource)(nil)
)

type cloudDNSZonesDataSource struct {
	configuredDataSourceClient
}

type cloudDNSZoneItemModel struct {
	ID           types.String `tfsdk:"id"`
	Name         types.String `tfsdk:"name"`
	ParentZoneID types.String `tfsdk:"parent_zone_id"`
}

type cloudDNSZonesDataSourceModel struct {
	ID    types.String            `tfsdk:"id"`
	Zones []cloudDNSZoneItemModel `tfsdk:"zones"`
}

// NewCloudDNSZonesDataSource returns the ankra_cloud_dns_zones data source,
// the counterpart of `ankra org access-gateway zones`.
func NewCloudDNSZonesDataSource() datasource.DataSource {
	return &cloudDNSZonesDataSource{}
}

func (zones *cloudDNSZonesDataSource) Metadata(_ context.Context, request datasource.MetadataRequest, response *datasource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_cloud_dns_zones"
}

func (zones *cloudDNSZonesDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, response *datasource.SchemaResponse) {
	response.Schema = datasourceschema.Schema{
		MarkdownDescription: "Lists the hosted zones in the organisation's Ankra Cloud account, whose access gateway policy " +
			"`ankra_cloud_dns_zone_access` sets. Needs the `members.read` permission.",
		Attributes: map[string]datasourceschema.Attribute{
			"id": computedStringAttribute("Placeholder identifier for the data source."),
			"zones": datasourceschema.ListNestedAttribute{
				MarkdownDescription: "Zones of the organisation's Ankra Cloud account.",
				Computed:            true,
				NestedObject: datasourceschema.NestedAttributeObject{
					Attributes: map[string]datasourceschema.Attribute{
						"id":             computedStringAttribute("Identifier of the zone."),
						"name":           computedStringAttribute("Domain name of the zone."),
						"parent_zone_id": computedStringAttribute("The zone this one is delegated from; null for a top-level zone."),
					},
				},
			},
		},
	}
}

func (zones *cloudDNSZonesDataSource) Read(ctx context.Context, _ datasource.ReadRequest, response *datasource.ReadResponse) {
	apiClient := zones.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	listed, listError := apiClient.ListCloudDNSZones(ctx)
	if listError != nil {
		response.Diagnostics.AddError("Unable to list Ankra Cloud DNS zones", cloudDNSAccessErrorText(listError))
		return
	}
	state := cloudDNSZonesDataSourceModel{
		ID:    types.StringValue("ankra_cloud_dns_zones"),
		Zones: make([]cloudDNSZoneItemModel, 0, len(listed)),
	}
	for _, zone := range listed {
		state.Zones = append(state.Zones, cloudDNSZoneItemModel{
			ID:           types.StringValue(zone.ID),
			Name:         types.StringValue(zone.Name),
			ParentZoneID: optionalString(zone.ParentZoneID),
		})
	}
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
}
