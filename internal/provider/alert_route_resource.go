// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64default"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"ankra.io/terraform-provider-ankra/internal/client"
)

var (
	_ resource.Resource                   = (*alertRouteResource)(nil)
	_ resource.ResourceWithConfigure      = (*alertRouteResource)(nil)
	_ resource.ResourceWithImportState    = (*alertRouteResource)(nil)
	_ resource.ResourceWithValidateConfig = (*alertRouteResource)(nil)
	_ resource.ResourceWithModifyPlan     = (*alertRouteResource)(nil)
)

type alertRouteResource struct {
	configuredClient
}

type alertRouteModel struct {
	ID               types.String `tfsdk:"id"`
	DestinationID    types.String `tfsdk:"destination_id"`
	DestinationType  types.String `tfsdk:"destination_type"`
	Kinds            types.List   `tfsdk:"kinds"`
	KindsNegated     types.Bool   `tfsdk:"kinds_negated"`
	Kind             types.String `tfsdk:"kind"`
	Severity         types.String `tfsdk:"severity"`
	ClusterID        types.String `tfsdk:"cluster_id"`
	SourceID         types.String `tfsdk:"source_id"`
	ApplicationID    types.String `tfsdk:"application_id"`
	ReferencePattern types.String `tfsdk:"reference_pattern"`
	Priority         types.Int64  `tfsdk:"priority"`
	StopOnMatch      types.Bool   `tfsdk:"stop_on_match"`
	Mode             types.String `tfsdk:"mode"`
	Enabled          types.Bool   `tfsdk:"enabled"`
}

// alertRouteResourceModel is the resource's model: the route as the platform
// returns it, plus the project the create stamps, which the platform never
// returns.
type alertRouteResourceModel struct {
	alertRouteModel
	ProjectID        types.String `tfsdk:"project_id"`
	OrganisationWide types.Bool   `tfsdk:"organisation_wide"`
}

// alertRouteImportedPrivateKey marks, in private state, a route imported
// since its project was last known: the platform never returns the project,
// so the next apply adopts the configured project_id and organisation_wide
// in place and clears the mark.
const alertRouteImportedPrivateKey = "alert_route_imported"

// alertRouteImported reports whether the private state marks the route as
// imported and not yet adopted. Every other route, including one created by
// a provider version without these arguments, has a known project.
func alertRouteImported(ctx context.Context, private privateStateReader) bool {
	if private == nil {
		return false
	}
	imported, diagnostics := private.GetKey(ctx, alertRouteImportedPrivateKey)
	return !diagnostics.HasError() && len(imported) > 0
}

// alertRouteProjectReplace replaces the route when its project changes,
// because the platform cannot move a route. After an import the project is
// unknowable, so the first apply adopts the configuration in place instead.
func alertRouteProjectReplace() planmodifier.String {
	return stringplanmodifier.RequiresReplaceIf(
		func(ctx context.Context, request planmodifier.StringRequest, response *stringplanmodifier.RequiresReplaceIfFuncResponse) {
			response.RequiresReplace = !alertRouteImported(ctx, request.Private)
		},
		"Changing the project replaces the route, except on the first apply after an import.",
		"Changing the project replaces the route, except on the first apply after an import.",
	)
}

// alertRouteOrganisationWideReplace is alertRouteProjectReplace for
// organisation_wide.
func alertRouteOrganisationWideReplace() planmodifier.Bool {
	return boolplanmodifier.RequiresReplaceIf(
		func(ctx context.Context, request planmodifier.BoolRequest, response *boolplanmodifier.RequiresReplaceIfFuncResponse) {
			// A route from a provider version without organisation_wide holds
			// null, which means the same as false.
			if request.StateValue.IsNull() && !request.PlanValue.ValueBool() {
				return
			}
			response.RequiresReplace = !alertRouteImported(ctx, request.Private)
		},
		"Changing organisation_wide replaces the route, except on the first apply after an import.",
		"Changing organisation_wide replaces the route, except on the first apply after an import.",
	)
}

// NewAlertRouteResource returns the ankra_alert_route resource, the
// counterpart of `ankra alerts routes create|update|delete`.
func NewAlertRouteResource() resource.Resource {
	return &alertRouteResource{}
}

func (routeResource *alertRouteResource) Metadata(_ context.Context, request resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_alert_route"
}

func (routeResource *alertRouteResource) Schema(_ context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: "A notification routing rule. A notification that matches every filter that is set is delivered to (`mode = \"include\"`) or withheld from " +
			"(`mode = \"exclude\"`) the destination. Rules are evaluated in ascending `priority`; `stop_on_match` ends the walk at this rule. " +
			"An unset filter matches anything. The counterpart of `ankra alerts routes create|update|delete`.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Identifier of the route.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"destination_id": schema.StringAttribute{
				MarkdownDescription: "Where matching notifications go: an `ankra_alert_destination` id, or `push:<audience>` for a mobile push audience.",
				Required:            true,
			},
			"destination_type": schema.StringAttribute{
				MarkdownDescription: "`webhook` or `push`, as the platform reads `destination_id`.",
				Computed:            true,
			},
			"kinds": schema.ListAttribute{
				MarkdownDescription: "Notification kinds the route matches, such as `alert_trigger_fired` or `pipeline_run_failed`. Unset matches every kind. Must not be empty or repeat a kind.",
				Optional:            true,
				ElementType:         types.StringType,
			},
			"kinds_negated": schema.BoolAttribute{
				MarkdownDescription: "Match every kind except those in `kinds`. Requires `kinds`. Defaults to `false`.",
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(false),
			},
			"kind": schema.StringAttribute{
				MarkdownDescription: "The legacy single-kind filter the platform derives from `kinds`: the kind when `kinds` holds exactly one and is not negated, otherwise null.",
				Computed:            true,
			},
			"severity": schema.StringAttribute{
				MarkdownDescription: "Severity the route matches: `info`, `warning` or `critical`. Unset matches every severity.",
				Optional:            true,
				Validators:          []validator.String{alertDNSOneOfValidator{allowed: []string{"critical", "warning", "info"}}},
			},
			"cluster_id": schema.StringAttribute{
				MarkdownDescription: "Cluster whose notifications the route matches. Unset matches every cluster.",
				Optional:            true,
			},
			"source_id": schema.StringAttribute{
				MarkdownDescription: "Source (for example an alert id) whose notifications the route matches. Unset matches every source.",
				Optional:            true,
			},
			"application_id": schema.StringAttribute{
				MarkdownDescription: "Application whose notifications the route matches. Unset matches every application.",
				Optional:            true,
			},
			"reference_pattern": schema.StringAttribute{
				MarkdownDescription: "Glob over the branch, tag or pull request a notification is about, such as `main`, `release/*` or `pull/*`. Unset matches every reference.",
				Optional:            true,
			},
			"priority": schema.Int64Attribute{
				MarkdownDescription: "Evaluation order; lower runs first. Defaults to `100`.",
				Optional:            true,
				Computed:            true,
				Default:             int64default.StaticInt64(100),
			},
			"stop_on_match": schema.BoolAttribute{
				MarkdownDescription: "Stop evaluating further routes once this one matches. Defaults to `false`.",
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(false),
			},
			"mode": schema.StringAttribute{
				MarkdownDescription: "`include` delivers matching notifications to the destination, `exclude` withholds them from it. Defaults to `include`.",
				Optional:            true,
				Computed:            true,
				Default:             stringdefault.StaticString("include"),
				Validators:          []validator.String{alertDNSOneOfValidator{allowed: []string{"include", "exclude"}}},
			},
			"enabled": schema.BoolAttribute{
				MarkdownDescription: "Whether the route is evaluated. Defaults to `true`.",
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(true),
			},
			"project_id": schema.StringAttribute{
				MarkdownDescription: "Project the route belongs to; it then matches only that project's notifications. Unset places the route in the provider's " +
					"`project_id`, as the platform does with any create. When the provider sets `project_id`, this must be unset or equal to it, because " +
					"a route in another project is not visible to the provider. Conflicts with `organisation_wide`. The platform cannot move a route, so " +
					"changing it replaces the route. The platform does not return it: after `terraform import`, set it to match the route; the first apply " +
					"adopts the configured value without replacing the route.",
				Optional:      true,
				PlanModifiers: []planmodifier.String{alertRouteProjectReplace()},
			},
			"organisation_wide": schema.BoolAttribute{
				MarkdownDescription: "Make the route organisation-wide, so it matches every project's notifications, even when the provider sets `project_id`. " +
					"Needs alert management rights at organisation scope. Conflicts with `project_id`. Defaults to `false`. Changing it replaces the route, " +
					"with the same import rule as `project_id`.",
				Optional:      true,
				Computed:      true,
				Default:       booldefault.StaticBool(false),
				PlanModifiers: []planmodifier.Bool{alertRouteOrganisationWideReplace()},
			},
		},
	}
}

func (routeResource *alertRouteResource) ValidateConfig(ctx context.Context, request resource.ValidateConfigRequest, response *resource.ValidateConfigResponse) {
	var configuration alertRouteResourceModel
	response.Diagnostics.Append(request.Config.Get(ctx, &configuration)...)
	if response.Diagnostics.HasError() {
		return
	}
	if configuration.OrganisationWide.ValueBool() && !configuration.ProjectID.IsNull() {
		response.Diagnostics.AddAttributeError(path.Root("organisation_wide"), "Invalid alert route",
			"organisation_wide and project_id conflict: an organisation-wide route belongs to no project.")
	}
	if configuration.Kinds.IsUnknown() {
		return
	}
	if configuration.Kinds.IsNull() {
		if configuration.KindsNegated.ValueBool() {
			response.Diagnostics.AddAttributeError(path.Root("kinds_negated"), "Invalid alert route",
				"kinds_negated needs kinds: a negated empty set would match nothing.")
		}
		return
	}
	elements := configuration.Kinds.Elements()
	if len(elements) == 0 {
		response.Diagnostics.AddAttributeError(path.Root("kinds"), "Invalid alert route",
			"kinds must not be empty; leave it unset to match every kind.")
		return
	}
	seen := map[string]bool{}
	for _, element := range elements {
		kind, isString := element.(types.String)
		if !isString || kind.IsUnknown() || kind.IsNull() {
			continue
		}
		if seen[kind.ValueString()] {
			response.Diagnostics.AddAttributeError(path.Root("kinds"), "Invalid alert route",
				"kinds lists "+kind.ValueString()+" twice; the platform stores each kind once.")
			return
		}
		seen[kind.ValueString()] = true
	}
}

func (routeResource *alertRouteResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	apiClient := routeResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var plan alertRouteResourceModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}
	project := alertRouteProjectFromPlan(plan, apiClient.ProjectID, &response.Diagnostics)
	body := alertRouteWriteFromPlan(ctx, plan.alertRouteModel, &response.Diagnostics)
	if response.Diagnostics.HasError() {
		return
	}

	route, err := apiClient.CreateAlertRoute(ctx, body, project)
	if err != nil {
		response.Diagnostics.AddError("Unable to create alert route", err.Error())
		return
	}
	alertRouteApply(route, &plan.alertRouteModel)
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
	response.Diagnostics.Append(trackScope(ctx, apiClient, response.Private)...)
}

func (routeResource *alertRouteResource) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	apiClient := routeResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state alertRouteResourceModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}

	route, err := apiClient.GetAlertRoute(ctx, state.ID.ValueString())
	if removeWhenNotFound(ctx, err, apiClient, response) {
		return
	}
	if err != nil {
		response.Diagnostics.AddError("Unable to read alert route", err.Error())
		return
	}
	alertRouteApply(route, &state.alertRouteModel)
	if state.OrganisationWide.IsNull() {
		// Imported, or created by a provider version without the argument:
		// the platform does not say whether the route is organisation-wide,
		// so state takes the default.
		state.OrganisationWide = types.BoolValue(false)
	}
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
	response.Diagnostics.Append(trackScope(ctx, apiClient, response.Private)...)
}

func (routeResource *alertRouteResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	apiClient := routeResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var plan alertRouteResourceModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}
	alertRouteProjectFromPlan(plan, apiClient.ProjectID, &response.Diagnostics)
	body := alertRouteWriteFromPlan(ctx, plan.alertRouteModel, &response.Diagnostics)
	if response.Diagnostics.HasError() {
		return
	}

	route, err := apiClient.UpdateAlertRoute(ctx, plan.ID.ValueString(), body)
	if err != nil {
		response.Diagnostics.AddError("Unable to update alert route", err.Error())
		return
	}
	alertRouteApply(route, &plan.alertRouteModel)
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
	// This apply adopted the configured project of an imported route; from
	// now on a change to it replaces the route.
	response.Diagnostics.Append(response.Private.SetKey(ctx, alertRouteImportedPrivateKey, nil)...)
	response.Diagnostics.Append(trackScope(ctx, apiClient, response.Private)...)
}

// ModifyPlan refuses, at plan time, a project_id the provider could not read
// the route back from.
func (routeResource *alertRouteResource) ModifyPlan(ctx context.Context, request resource.ModifyPlanRequest, response *resource.ModifyPlanResponse) {
	if request.Plan.Raw.IsNull() || routeResource.client == nil {
		return
	}
	var projectID types.String
	response.Diagnostics.Append(request.Plan.GetAttribute(ctx, path.Root("project_id"), &projectID)...)
	if response.Diagnostics.HasError() {
		return
	}
	alertRouteCheckProject(projectID, routeResource.client.ProjectID, &response.Diagnostics)
}

// alertRouteCheckProject refuses a project_id other than the provider's
// project_id: the platform shows a provider scoped to a project only that
// project's routes and the organisation-wide ones.
func alertRouteCheckProject(projectID types.String, providerProjectID string, diagnostics *diag.Diagnostics) {
	if projectID.IsNull() || projectID.IsUnknown() || providerProjectID == "" || projectID.ValueString() == providerProjectID {
		return
	}
	diagnostics.AddAttributeError(path.Root("project_id"), "Alert route project outside the provider's project",
		"project_id is "+projectID.ValueString()+" but the provider is scoped to project "+providerProjectID+". The platform only shows a provider "+
			"scoped to a project that project's routes and the organisation-wide ones, so Terraform could not read this route back. "+
			"Leave project_id unset, or manage the route from a provider configured with that project_id.")
}

// alertRouteProjectFromPlan renders the plan's project choice as the create
// sends it.
func alertRouteProjectFromPlan(plan alertRouteResourceModel, providerProjectID string, diagnostics *diag.Diagnostics) client.AlertRouteProject {
	if plan.OrganisationWide.ValueBool() {
		return client.AlertRouteProject{IsSet: true}
	}
	alertRouteCheckProject(plan.ProjectID, providerProjectID, diagnostics)
	if plan.ProjectID.IsNull() || plan.ProjectID.IsUnknown() || diagnostics.HasError() {
		return client.AlertRouteProject{}
	}
	projectID := plan.ProjectID.ValueString()
	return client.AlertRouteProject{IsSet: true, ProjectID: &projectID}
}

func (routeResource *alertRouteResource) Delete(ctx context.Context, request resource.DeleteRequest, response *resource.DeleteResponse) {
	apiClient := routeResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state alertRouteResourceModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}

	err := apiClient.DeleteAlertRoute(ctx, state.ID.ValueString())
	if err == nil || deletedWhenNotFound(ctx, err, apiClient, request.Private, &response.Diagnostics) || response.Diagnostics.HasError() {
		return
	}
	response.Diagnostics.AddError("Unable to delete alert route", err.Error())
}

// ImportState imports by route id.
func (routeResource *alertRouteResource) ImportState(ctx context.Context, request resource.ImportStateRequest, response *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), request, response)
	response.Diagnostics.Append(response.Private.SetKey(ctx, alertRouteImportedPrivateKey, []byte("true"))...)
}

// alertRouteWriteFromPlan renders the plan as the full write body. Every
// filter is sent, null included, so a filter removed from configuration is
// cleared rather than kept.
func alertRouteWriteFromPlan(ctx context.Context, plan alertRouteModel, diagnostics *diag.Diagnostics) client.AlertRouteWrite {
	return client.AlertRouteWrite{
		DestinationID:    plan.DestinationID.ValueString(),
		Kinds:            stringsFromList(ctx, plan.Kinds, diagnostics),
		KindsNegated:     plan.KindsNegated.ValueBool(),
		Severity:         stringPointer(plan.Severity),
		ClusterID:        stringPointer(plan.ClusterID),
		SourceID:         stringPointer(plan.SourceID),
		ApplicationID:    stringPointer(plan.ApplicationID),
		ReferencePattern: stringPointer(plan.ReferencePattern),
		Priority:         plan.Priority.ValueInt64(),
		StopOnMatch:      plan.StopOnMatch.ValueBool(),
		Mode:             plan.Mode.ValueString(),
		Enabled:          plan.Enabled.ValueBool(),
	}
}

// alertRouteApply copies a route as the platform stores it onto a model. An
// empty kinds set reads as unset, the "any kind" the platform stores for it.
func alertRouteApply(route *client.AlertRoute, model *alertRouteModel) {
	model.ID = types.StringValue(route.ID)
	model.DestinationID = types.StringValue(route.DestinationID)
	model.DestinationType = types.StringValue(route.DestinationType)
	if len(route.Kinds) == 0 {
		model.Kinds = listFromStrings(nil)
	} else {
		model.Kinds = listFromStrings(route.Kinds)
	}
	model.KindsNegated = types.BoolValue(route.KindsNegated)
	model.Kind = types.StringPointerValue(route.Kind)
	model.Severity = types.StringPointerValue(route.Severity)
	model.ClusterID = types.StringPointerValue(route.ClusterID)
	model.SourceID = types.StringPointerValue(route.SourceID)
	model.ApplicationID = types.StringPointerValue(route.ApplicationID)
	model.ReferencePattern = types.StringPointerValue(route.ReferencePattern)
	model.Priority = types.Int64Value(route.Priority)
	model.StopOnMatch = types.BoolValue(route.StopOnMatch)
	model.Mode = types.StringValue(route.Mode)
	model.Enabled = types.BoolValue(route.Enabled)
}
