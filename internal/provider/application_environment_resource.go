// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"

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

var operatingExpectationsDataRegions = []string{"eu", "us", "any"}

const operatingExpectationsRoutineCare = "review_required"

var (
	_ resource.Resource                = (*applicationEnvironmentResource)(nil)
	_ resource.ResourceWithConfigure   = (*applicationEnvironmentResource)(nil)
	_ resource.ResourceWithImportState = (*applicationEnvironmentResource)(nil)
)

type applicationEnvironmentResource struct {
	configuredClient
}

type applicationEnvironmentModel struct {
	ID                            types.String                `tfsdk:"id"`
	ApplicationID                 types.String                `tfsdk:"application_id"`
	EnvironmentID                 types.String                `tfsdk:"environment_id"`
	Name                          types.String                `tfsdk:"name"`
	Role                          types.String                `tfsdk:"role"`
	Level                         types.String                `tfsdk:"level"`
	Areas                         types.Map                   `tfsdk:"areas"`
	Limits                        *autonomyLimitsModel        `tfsdk:"limits"`
	OperatingExpectations         *operatingExpectationsModel `tfsdk:"operating_expectations"`
	OperatingExpectationsRevision types.Int64                 `tfsdk:"operating_expectations_revision"`
}

type operatingExpectationsModel struct {
	AvailabilityPercent types.Float64 `tfsdk:"availability_percent"`
	DataRegion          types.String  `tfsdk:"data_region"`
	RecoveryMinutes     types.Int64   `tfsdk:"recovery_minutes"`
	DataLossMinutes     types.Int64   `tfsdk:"data_loss_minutes"`
	RetentionDays       types.Int64   `tfsdk:"retention_days"`
	MonthlyBudgetCents  types.Int64   `tfsdk:"monthly_budget_cents"`
}

// NewApplicationEnvironmentResource returns the
// ankra_application_environment resource, the counterpart of
// `ankra application environments set`.
func NewApplicationEnvironmentResource() resource.Resource {
	return &applicationEnvironmentResource{}
}

func (environment *applicationEnvironmentResource) Metadata(_ context.Context, request resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_application_environment"
}

func operatingExpectationsInt64(description string, minimum, maximum int64) schema.Int64Attribute {
	return schema.Int64Attribute{
		MarkdownDescription: description,
		Required:            true,
		Validators:          []validator.Int64{alertDNSRangeValidator{minimum: minimum, maximum: maximum}},
	}
}

func (environment *applicationEnvironmentResource) Schema(_ context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	attributes := autonomyLayerAttributes("the environment")
	attributes["id"] = schema.StringAttribute{
		MarkdownDescription: "`<application_id>/<environment_id>`.",
		Computed:            true,
		PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
	}
	attributes["application_id"] = schema.StringAttribute{
		MarkdownDescription: "Identifier of the application. Changing it replaces the resource.",
		Required:            true,
		PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
	}
	attributes["environment_id"] = schema.StringAttribute{
		MarkdownDescription: "Identifier of one of the application's environments. Changing it replaces the resource.",
		Required:            true,
		PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
	}
	attributes["name"] = schema.StringAttribute{
		MarkdownDescription: "Name of the environment. Setting it renames the environment; one still named after its cluster cannot be renamed yet.",
		Optional:            true,
		Computed:            true,
		PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
	}
	attributes["role"] = schema.StringAttribute{
		MarkdownDescription: "Role of the environment: `playground`, `production`, `staging`, `development` or `custom`.",
		Computed:            true,
		PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
	}
	attributes["operating_expectations"] = schema.SingleNestedAttribute{
		MarkdownDescription: "The environment's operating expectations: intent Ankra plans and reports against. They do not provision or certify runtime controls.",
		Optional:            true,
		Attributes: map[string]schema.Attribute{
			"availability_percent": schema.Float64Attribute{
				MarkdownDescription: "Availability aimed for, 90 to 100 percent.",
				Required:            true,
			},
			"data_region": schema.StringAttribute{
				MarkdownDescription: "Where data may live: `eu`, `us` or `any`.",
				Required:            true,
				Validators:          []validator.String{organisationStringOneOf{values: operatingExpectationsDataRegions}},
			},
			"recovery_minutes":     operatingExpectationsInt64("Longest acceptable recovery, 1 to 10080 minutes.", 1, 10080),
			"data_loss_minutes":    operatingExpectationsInt64("Most data loss acceptable, 0 to 10080 minutes.", 0, 10080),
			"retention_days":       operatingExpectationsInt64("How long backups are kept, 1 to 3650 days.", 1, 3650),
			"monthly_budget_cents": operatingExpectationsInt64("Monthly budget, in cents.", 0, 100000000),
		},
	}
	attributes["operating_expectations_revision"] = schema.Int64Attribute{
		MarkdownDescription: "Revision of the adopted operating expectations; null when none was adopted.",
		Computed:            true,
	}
	response.Schema = schema.Schema{
		MarkdownDescription: "Settings of one environment of an application (`ankra application environments set`): its name, its own " +
			"\"who runs it\" layer, which applies over the application's, and its operating expectations. Environments come from " +
			"where the application is deployed, so this resource adopts an existing one and never creates or deletes it. The layer " +
			"is managed only when `level`, `areas` or `limits` is set, and is replaced as a whole, starting from the stored layer " +
			"so a change made meanwhile is refused. Operating expectations are adopted as a new revision over the current one. " +
			"Each part is written only when it changes. A caller confined to a project manages only environments placed in it. " +
			"Cloning an environment onto another cluster stays imperative. Destroy leaves the environment's settings as they are.",
		Attributes: attributes,
	}
}

func (environment *applicationEnvironmentResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	var plan applicationEnvironmentModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if environment.write(ctx, &plan, nil, &response.Diagnostics) {
		response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
		response.Diagnostics.Append(trackScope(ctx, environment.client, response.Private)...)
	}
}

func (environment *applicationEnvironmentResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	var plan, state applicationEnvironmentModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if environment.write(ctx, &plan, &state, &response.Diagnostics) {
		response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
	}
}

func (environment *applicationEnvironmentResource) write(ctx context.Context, plan, state *applicationEnvironmentModel, diagnostics *diag.Diagnostics) bool {
	apiClient := environment.requireClient(diagnostics)
	if apiClient == nil || diagnostics.HasError() {
		return false
	}
	applicationID, environmentID := plan.ApplicationID.ValueString(), plan.EnvironmentID.ValueString()
	current, findError := applicationEnvironmentFind(ctx, apiClient, applicationID, environmentID)
	if findError != nil {
		diagnostics.AddError("Unable to read the application's environments", findError.Error())
		return false
	}
	if current == nil {
		diagnostics.AddAttributeError(path.Root("environment_id"), "Environment not found",
			fmt.Sprintf("Application %s has no environment %s visible to this token.", applicationID, environmentID))
		return false
	}
	if !plan.Name.IsUnknown() && plan.Name.ValueString() != current.Name {
		if renameError := apiClient.RenameApplicationEnvironment(ctx, applicationID, environmentID, plan.Name.ValueString()); renameError != nil {
			diagnostics.AddError("Unable to rename the environment", renameError.Error())
			return false
		}
	} else if plan.Name.IsUnknown() {
		plan.Name = types.StringValue(current.Name)
	}
	plan.Role = types.StringValue(current.Role)
	isLayerChanged := autonomyLayerIsManaged(plan.Level, plan.Areas, plan.Limits)
	if state != nil {
		isLayerChanged = autonomyLayerChanged(plan.Level, state.Level, plan.Areas, state.Areas, plan.Limits, state.Limits)
	}
	if isLayerChanged && !environment.writeLayer(ctx, apiClient, plan, diagnostics) {
		return false
	}
	if !environment.writeExpectations(ctx, apiClient, plan, state, diagnostics) {
		return false
	}
	plan.ID = types.StringValue(applicationID + "/" + environmentID)
	return true
}

func (environment *applicationEnvironmentResource) writeLayer(ctx context.Context, apiClient *client.Client, plan *applicationEnvironmentModel, diagnostics *diag.Diagnostics) bool {
	applicationID, environmentID := plan.ApplicationID.ValueString(), plan.EnvironmentID.ValueString()
	layers, readError := apiClient.GetApplicationAutonomyLayers(ctx, applicationID)
	if readError != nil {
		diagnostics.AddError("Unable to read the application's autonomy", readError.Error())
		return false
	}
	if layers == nil {
		diagnostics.AddError("Unable to read the application's autonomy", "The platform could not read the stored layers, so no write can be built from them. Try again.")
		return false
	}
	var stored *client.AutonomyLayer
	if layer, isStored := layers.Environments[environmentID]; isStored {
		stored = &layer
	}
	write := autonomyLayerWrite(ctx, plan.Level, plan.Areas, plan.Limits, stored, diagnostics)
	if diagnostics.HasError() {
		return false
	}
	if setError := apiClient.SetApplicationEnvironmentAutonomyLayer(ctx, applicationID, environmentID, write); setError != nil {
		diagnostics.AddError("Unable to set who runs the environment", setError.Error())
		return false
	}
	return true
}

func (environment *applicationEnvironmentResource) writeExpectations(ctx context.Context, apiClient *client.Client, plan, state *applicationEnvironmentModel,
	diagnostics *diag.Diagnostics) bool {
	if plan.OperatingExpectations == nil {
		if plan.OperatingExpectationsRevision.IsUnknown() {
			plan.OperatingExpectationsRevision = types.Int64Null()
			if state != nil {
				plan.OperatingExpectationsRevision = state.OperatingExpectationsRevision
			}
		}
		return true
	}
	if state != nil && state.OperatingExpectations != nil && operatingExpectationsEqual(*state.OperatingExpectations, *plan.OperatingExpectations) {
		plan.OperatingExpectationsRevision = state.OperatingExpectationsRevision
		return true
	}
	applicationID, environmentID := plan.ApplicationID.ValueString(), plan.EnvironmentID.ValueString()
	current, readError := apiClient.GetOperatingExpectations(ctx, applicationID, environmentID)
	if readError != nil {
		diagnostics.AddError("Unable to read the operating expectations", readError.Error())
		return false
	}
	expectedRevision := int64(0)
	if current != nil {
		expectedRevision = current.Revision
	}
	adopted, adoptError := apiClient.AdoptOperatingExpectations(ctx, applicationID, environmentID, client.OperatingExpectations{
		AvailabilityPercent: plan.OperatingExpectations.AvailabilityPercent.ValueFloat64(),
		DataRegion:          plan.OperatingExpectations.DataRegion.ValueString(),
		RecoveryMinutes:     plan.OperatingExpectations.RecoveryMinutes.ValueInt64(),
		DataLossMinutes:     plan.OperatingExpectations.DataLossMinutes.ValueInt64(),
		RetentionDays:       plan.OperatingExpectations.RetentionDays.ValueInt64(),
		MonthlyBudgetCents:  plan.OperatingExpectations.MonthlyBudgetCents.ValueInt64(),
		RoutineCare:         operatingExpectationsRoutineCare,
	}, expectedRevision)
	if adoptError != nil {
		diagnostics.AddError("Unable to adopt the operating expectations", adoptError.Error())
		return false
	}
	plan.OperatingExpectationsRevision = types.Int64Null()
	if adopted != nil {
		plan.OperatingExpectationsRevision = types.Int64Value(adopted.Revision)
	}
	return true
}

func (environment *applicationEnvironmentResource) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	apiClient := environment.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state applicationEnvironmentModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	applicationID, environmentID := state.ApplicationID.ValueString(), state.EnvironmentID.ValueString()
	current, findError := applicationEnvironmentFind(ctx, apiClient, applicationID, environmentID)
	if removeWhenNotFound(ctx, findError, apiClient, response) {
		return
	}
	if findError != nil {
		response.Diagnostics.AddError("Unable to read the application's environments", findError.Error())
		return
	}
	if current == nil {
		removeWhenTargetChanged(ctx, apiClient, response)
		return
	}
	state.ID = types.StringValue(applicationID + "/" + environmentID)
	state.Name = types.StringValue(current.Name)
	state.Role = types.StringValue(current.Role)
	if autonomyLayerIsManaged(state.Level, state.Areas, state.Limits) {
		layers, readError := apiClient.GetApplicationAutonomyLayers(ctx, applicationID)
		if readError != nil {
			response.Diagnostics.AddError("Unable to read the application's autonomy", readError.Error())
			return
		}
		if layers != nil {
			var stored *client.AutonomyLayer
			if layer, isStored := layers.Environments[environmentID]; isStored {
				stored = &layer
			}
			state.Level, state.Areas, state.Limits = autonomyLayerState(stored, state.Areas, state.Limits)
		}
	}
	expectations, readError := apiClient.GetOperatingExpectations(ctx, applicationID, environmentID)
	if readError != nil {
		response.Diagnostics.AddError("Unable to read the operating expectations", readError.Error())
		return
	}
	state.OperatingExpectationsRevision = types.Int64Null()
	if expectations != nil {
		state.OperatingExpectationsRevision = types.Int64Value(expectations.Revision)
	}
	if state.OperatingExpectations != nil {
		state.OperatingExpectations = nil
		if expectations != nil {
			state.OperatingExpectations = &operatingExpectationsModel{
				AvailabilityPercent: types.Float64Value(expectations.Expectations.AvailabilityPercent),
				DataRegion:          types.StringValue(expectations.Expectations.DataRegion),
				RecoveryMinutes:     types.Int64Value(expectations.Expectations.RecoveryMinutes),
				DataLossMinutes:     types.Int64Value(expectations.Expectations.DataLossMinutes),
				RetentionDays:       types.Int64Value(expectations.Expectations.RetentionDays),
				MonthlyBudgetCents:  types.Int64Value(expectations.Expectations.MonthlyBudgetCents),
			}
		}
	}
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
	response.Diagnostics.Append(trackScope(ctx, apiClient, response.Private)...)
}

// Delete leaves the environment's settings as they are: the environment
// itself lives as long as the application is deployed there.
func (environment *applicationEnvironmentResource) Delete(_ context.Context, _ resource.DeleteRequest, _ *resource.DeleteResponse) {
}

// ImportState imports by `<application_id>/<environment_id>`.
func (environment *applicationEnvironmentResource) ImportState(ctx context.Context, request resource.ImportStateRequest, response *resource.ImportStateResponse) {
	parts, splitError := organisationSplitImportID(request.ID, 2, "<application_id>/<environment_id>")
	if splitError != nil {
		response.Diagnostics.AddError("Invalid import id", splitError.Error())
		return
	}
	response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("application_id"), parts[0])...)
	response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("environment_id"), parts[1])...)
}

func applicationEnvironmentFind(ctx context.Context, apiClient *client.Client, applicationID, environmentID string) (*client.ApplicationEnvironmentSummary, error) {
	environments, listError := apiClient.ListApplicationEnvironments(ctx, applicationID)
	if listError != nil {
		return nil, listError
	}
	for index := range environments {
		if environments[index].ID == environmentID {
			return &environments[index], nil
		}
	}
	return nil, nil
}

// operatingExpectationsEqual compares two expectation blocks by value: a
// Float64 holds a pointer, so struct equality would never match.
func operatingExpectationsEqual(left, right operatingExpectationsModel) bool {
	return left.AvailabilityPercent.Equal(right.AvailabilityPercent) && left.DataRegion.Equal(right.DataRegion) &&
		left.RecoveryMinutes.Equal(right.RecoveryMinutes) && left.DataLossMinutes.Equal(right.DataLossMinutes) &&
		left.RetentionDays.Equal(right.RetentionDays) && left.MonthlyBudgetCents.Equal(right.MonthlyBudgetCents)
}
