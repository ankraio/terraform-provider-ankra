// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"ankra.io/terraform-provider-ankra/internal/client"
)

// The platform's bounds on an application alert, as its create and update
// routes refuse them.
const (
	applicationAlertConditionType       = "app_metric"
	applicationAlertOperator            = "gt"
	applicationAlertPercentMetric       = "failed_share_pct"
	applicationAlertPercentMaximum      = 100
	applicationAlertHoldMinutesMinimum  = 1
	applicationAlertHoldMinutesMaximum  = 60
	applicationAlertNameMaximum         = 100
	applicationAlertDestinationsMaximum = 5
)

// applicationAlertMetrics are the readings an application alert can watch.
var applicationAlertMetrics = []string{"failed_share_pct", "p95_ms", "restarts"}

// applicationAlertUUIDPattern is a UUID in the lower-case form the platform
// reports, so a configured id reads back unchanged.
var applicationAlertUUIDPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

var (
	_ resource.Resource                   = (*applicationAlertResource)(nil)
	_ resource.ResourceWithConfigure      = (*applicationAlertResource)(nil)
	_ resource.ResourceWithImportState    = (*applicationAlertResource)(nil)
	_ resource.ResourceWithValidateConfig = (*applicationAlertResource)(nil)
)

type applicationAlertResource struct {
	configuredClient
}

type applicationAlertModel struct {
	ID            types.String                    `tfsdk:"id"`
	ApplicationID types.String                    `tfsdk:"application_id"`
	EnvironmentID types.String                    `tfsdk:"environment_id"`
	Name          types.String                    `tfsdk:"name"`
	Condition     *applicationAlertConditionModel `tfsdk:"condition"`
	SendTo        types.Set                       `tfsdk:"send_to"`
	Severity      types.String                    `tfsdk:"severity"`
	RuleID        types.String                    `tfsdk:"rule_id"`
	Enabled       types.Bool                      `tfsdk:"enabled"`
}

type applicationAlertConditionModel struct {
	ConditionType types.String  `tfsdk:"condition_type"`
	Metric        types.String  `tfsdk:"metric"`
	Operator      types.String  `tfsdk:"operator"`
	Threshold     types.Float64 `tfsdk:"threshold"`
	HoldMinutes   types.Int64   `tfsdk:"hold_minutes"`
}

// NewApplicationAlertResource returns the ankra_application_alert resource,
// the counterpart of `ankra application alerts`.
func NewApplicationAlertResource() resource.Resource {
	return &applicationAlertResource{}
}

func (alertResource *applicationAlertResource) Metadata(_ context.Context, request resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_application_alert"
}

func (alertResource *applicationAlertResource) Schema(_ context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: "An alert of one application on one of its environments (`ankra application alerts`): it fires when one of the " +
			"application's own metrics reads above a threshold for a number of minutes in a row, and notifies 1 to 5 alert destinations. " +
			"The platform stores it as an organisation alert named \"Application · Environment · name\" with one rule, so it also shows " +
			"under Alerts, where it can be muted; `enabled` reports that. Every member except `application_id` changes in place. " +
			"`severity` is `warning` when the alert is created without one, and leaving it out of the configuration later keeps the " +
			"severity the platform stores rather than resetting it. Destroy deletes the alert and its rule.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Identifier of the alert.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"application_id": schema.StringAttribute{
				MarkdownDescription: "Identifier of the application. Changing it replaces the alert.",
				Required:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
				Validators:          []validator.String{applicationAlertUUIDValidator{refusal: "application_id must be the id of an application"}},
			},
			"environment_id": schema.StringAttribute{
				MarkdownDescription: "Identifier of the application's environment the alert watches; `ankra application environments <application>` " +
					"lists them. An environment with nothing running on it yet is accepted: the alert reads unknown until something runs there.",
				Required:   true,
				Validators: []validator.String{applicationAlertUUIDValidator{refusal: "environment_id must be the id of one of this application's environments"}},
			},
			"name": schema.StringAttribute{
				MarkdownDescription: fmt.Sprintf("The alert's name on the application page, 1 to %d characters. Under Alerts it is named "+
					"\"Application · Environment · name\", which must be unique in the organisation.", applicationAlertNameMaximum),
				Required:   true,
				Validators: []validator.String{applicationAlertNameValidator{}},
			},
			"condition": schema.SingleNestedAttribute{
				MarkdownDescription: "When the alert fires: `metric` reads above `threshold` for `hold_minutes` in a row.",
				Required:            true,
				Attributes: map[string]schema.Attribute{
					"condition_type": schema.StringAttribute{
						MarkdownDescription: "`app_metric`, the one condition an application alert can have.",
						Optional:            true,
						Computed:            true,
						Default:             stringdefault.StaticString(applicationAlertConditionType),
						Validators:          []validator.String{organisationStringOneOf{values: []string{applicationAlertConditionType}}},
					},
					"metric": schema.StringAttribute{
						MarkdownDescription: "`failed_share_pct` (the share of requests that failed, in percent), `p95_ms` (the 95th " +
							"percentile response time, in milliseconds) or `restarts` (container restarts in the last 15 minutes).",
						Required:   true,
						Validators: []validator.String{organisationStringOneOf{values: applicationAlertMetrics}},
					},
					"operator": schema.StringAttribute{
						MarkdownDescription: "`gt`, the one operator the platform takes: the alert fires when the reading is above the threshold.",
						Optional:            true,
						Computed:            true,
						Default:             stringdefault.StaticString(applicationAlertOperator),
						Validators:          []validator.String{organisationStringOneOf{values: []string{applicationAlertOperator}}},
					},
					"threshold": schema.Float64Attribute{
						MarkdownDescription: fmt.Sprintf("The reading the metric must be above, zero or more; at most %d for `%s`.",
							applicationAlertPercentMaximum, applicationAlertPercentMetric),
						Required: true,
					},
					"hold_minutes": schema.Int64Attribute{
						MarkdownDescription: fmt.Sprintf("How many minutes the reading stays above the threshold before the alert fires, %d to %d.",
							applicationAlertHoldMinutesMinimum, applicationAlertHoldMinutesMaximum),
						Required: true,
						Validators: []validator.Int64{alertDNSRangeValidator{
							minimum: applicationAlertHoldMinutesMinimum, maximum: applicationAlertHoldMinutesMaximum}},
					},
				},
			},
			"send_to": schema.SetAttribute{
				MarkdownDescription: fmt.Sprintf("Identifiers of the alert destinations (`ankra_alert_destination`) the alert notifies, 1 to %d.",
					applicationAlertDestinationsMaximum),
				Required:    true,
				ElementType: types.StringType,
				Validators:  []validator.Set{applicationAlertSendToValidator{}},
			},
			"severity": schema.StringAttribute{
				MarkdownDescription: "`critical`, `warning` or `info`. Unset on create, the platform stores `warning`; unset on a later " +
					"apply, the platform keeps the severity it stores, including one changed outside Terraform, and the plan shows no change.",
				Optional:      true,
				Computed:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
				Validators:    []validator.String{organisationStringOneOf{values: alertRuleSeverities}},
			},
			"rule_id": schema.StringAttribute{
				MarkdownDescription: "Identifier of the alert's one rule.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"enabled": schema.BoolAttribute{
				MarkdownDescription: "Whether the alert and its rule are both on. It is `false` while the alert is muted under Alerts.",
				Computed:            true,
				PlanModifiers:       []planmodifier.Bool{boolplanmodifier.UseStateForUnknown()},
			},
		},
	}
}

// ValidateConfig refuses a percentage threshold above 100, which depends on
// two members of the condition.
func (alertResource *applicationAlertResource) ValidateConfig(ctx context.Context, request resource.ValidateConfigRequest, response *resource.ValidateConfigResponse) {
	var metric types.String
	var threshold types.Float64
	response.Diagnostics.Append(request.Config.GetAttribute(ctx, path.Root("condition").AtName("metric"), &metric)...)
	response.Diagnostics.Append(request.Config.GetAttribute(ctx, path.Root("condition").AtName("threshold"), &threshold)...)
	if response.Diagnostics.HasError() || threshold.IsNull() || threshold.IsUnknown() {
		return
	}
	value := threshold.ValueFloat64()
	if value < 0 {
		response.Diagnostics.AddAttributeError(path.Root("condition").AtName("threshold"), "Invalid threshold",
			"threshold must be a number of zero or more.")
		return
	}
	if !metric.IsNull() && !metric.IsUnknown() && metric.ValueString() == applicationAlertPercentMetric && value > applicationAlertPercentMaximum {
		response.Diagnostics.AddAttributeError(path.Root("condition").AtName("threshold"), "Invalid threshold",
			fmt.Sprintf("threshold is a percentage for %s, so it must be at most %d.", applicationAlertPercentMetric, applicationAlertPercentMaximum))
	}
}

func (alertResource *applicationAlertResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	apiClient := alertResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var plan applicationAlertModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	var configuredSeverity types.String
	response.Diagnostics.Append(request.Config.GetAttribute(ctx, path.Root("severity"), &configuredSeverity)...)
	if response.Diagnostics.HasError() {
		return
	}
	write := applicationAlertWriteFromPlan(ctx, plan, configuredSeverity, &response.Diagnostics)
	if response.Diagnostics.HasError() {
		return
	}
	alert, createError := apiClient.CreateApplicationAlert(ctx, plan.ApplicationID.ValueString(), write)
	if createError != nil {
		response.Diagnostics.AddError("Unable to create application alert", createError.Error())
		return
	}
	applicationAlertApply(&plan, alert)
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
	response.Diagnostics.Append(trackScope(ctx, apiClient, response.Private)...)
}

func (alertResource *applicationAlertResource) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	apiClient := alertResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state applicationAlertModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	alert, readError := apiClient.GetApplicationAlert(ctx, state.ApplicationID.ValueString(), state.ID.ValueString())
	if removeWhenNotFound(ctx, readError, apiClient, response) {
		return
	}
	if readError != nil {
		response.Diagnostics.AddError("Unable to read application alert", readError.Error())
		return
	}
	if alert.Condition == nil {
		response.Diagnostics.AddError("Unable to read application alert",
			fmt.Sprintf("the platform lists alert %s without its condition", state.ID.ValueString()))
		return
	}
	applicationAlertApply(&state, alert)
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
	response.Diagnostics.Append(trackScope(ctx, apiClient, response.Private)...)
}

func (alertResource *applicationAlertResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	apiClient := alertResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var plan, state applicationAlertModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	var configuredSeverity types.String
	response.Diagnostics.Append(request.Config.GetAttribute(ctx, path.Root("severity"), &configuredSeverity)...)
	if response.Diagnostics.HasError() {
		return
	}
	write := applicationAlertWriteFromPlan(ctx, plan, configuredSeverity, &response.Diagnostics)
	if response.Diagnostics.HasError() {
		return
	}
	alert, updateError := apiClient.UpdateApplicationAlert(ctx, state.ApplicationID.ValueString(), state.ID.ValueString(), write)
	if updateError != nil {
		response.Diagnostics.AddError("Unable to update application alert", updateError.Error())
		return
	}
	plan.ID = state.ID
	applicationAlertApply(&plan, alert)
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
	response.Diagnostics.Append(trackScope(ctx, apiClient, response.Private)...)
}

func (alertResource *applicationAlertResource) Delete(ctx context.Context, request resource.DeleteRequest, response *resource.DeleteResponse) {
	apiClient := alertResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state applicationAlertModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	deleteError := apiClient.DeleteApplicationAlert(ctx, state.ApplicationID.ValueString(), state.ID.ValueString())
	if deleteError == nil || deletedWhenNotFound(ctx, deleteError, apiClient, request.Private, &response.Diagnostics) || response.Diagnostics.HasError() {
		return
	}
	response.Diagnostics.AddError("Unable to delete application alert", deleteError.Error())
}

// ImportState imports by `<application_id>/<alert_id>`, the two ids the
// alert's routes need; the read that follows fills in every other member.
func (alertResource *applicationAlertResource) ImportState(ctx context.Context, request resource.ImportStateRequest, response *resource.ImportStateResponse) {
	parts, splitError := applicationSplitImportID(request.ID, "application_id", "alert_id")
	if splitError != nil {
		response.Diagnostics.AddError("Invalid import id", splitError.Error())
		return
	}
	response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("application_id"), parts[0])...)
	response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("id"), parts[1])...)
}

// applicationAlertWriteFromPlan maps the plan to the create or update body. Severity is sent
// only when the configuration sets it: the plan carries the stored severity
// when it does not, and leaving it out is what keeps that severity.
func applicationAlertWriteFromPlan(ctx context.Context, plan applicationAlertModel, configuredSeverity types.String,
	diagnostics *diag.Diagnostics) client.ApplicationAlertWrite {
	write := client.ApplicationAlertWrite{
		Name:          plan.Name.ValueString(),
		EnvironmentID: plan.EnvironmentID.ValueString(),
		SendTo:        clusterSettingsStringsFromSet(ctx, plan.SendTo, diagnostics),
		Severity:      stringPointer(configuredSeverity),
	}
	if plan.Condition != nil {
		write.Condition = client.ApplicationAlertCondition{
			ConditionType: plan.Condition.ConditionType.ValueString(),
			Metric:        plan.Condition.Metric.ValueString(),
			Operator:      plan.Condition.Operator.ValueString(),
			Threshold:     plan.Condition.Threshold.ValueFloat64(),
			HoldMinutes:   plan.Condition.HoldMinutes.ValueInt64(),
		}
	}
	return write
}

// applicationAlertApply copies the alert as the platform stores it into the
// model.
func applicationAlertApply(model *applicationAlertModel, alert *client.ApplicationAlert) {
	model.ID = types.StringValue(alert.AlertID)
	model.Name = types.StringValue(alert.Name)
	model.EnvironmentID = types.StringPointerValue(alert.EnvironmentID)
	if alert.Condition != nil {
		operator := alert.Condition.Operator
		if operator == "" {
			operator = applicationAlertOperator
		}
		model.Condition = &applicationAlertConditionModel{
			ConditionType: types.StringValue(alert.Condition.ConditionType),
			Metric:        types.StringValue(alert.Condition.Metric),
			Operator:      types.StringValue(operator),
			Threshold:     types.Float64Value(alert.Condition.Threshold),
			HoldMinutes:   types.Int64Value(alert.Condition.HoldMinutes),
		}
	}
	destinationIDs := make([]string, 0, len(alert.SendTo))
	for _, destination := range alert.SendTo {
		destinationIDs = append(destinationIDs, destination.IntegrationID)
	}
	model.SendTo = clusterSettingsSetFromStrings(destinationIDs)
	model.Severity = types.StringValue(alert.Severity)
	model.RuleID = optionalString(alert.RuleID)
	model.Enabled = types.BoolValue(alert.Enabled)
}

// applicationAlertUUIDValidator requires a lower-case UUID, refusing in the
// platform's words: any other spelling is refused, or reads back changed.
type applicationAlertUUIDValidator struct {
	refusal string
}

func (uuidValidator applicationAlertUUIDValidator) Description(_ context.Context) string {
	return "value must be a lower-case UUID"
}

func (uuidValidator applicationAlertUUIDValidator) MarkdownDescription(ctx context.Context) string {
	return uuidValidator.Description(ctx)
}

func (uuidValidator applicationAlertUUIDValidator) ValidateString(_ context.Context, request validator.StringRequest, response *validator.StringResponse) {
	if request.ConfigValue.IsNull() || request.ConfigValue.IsUnknown() {
		return
	}
	if !applicationAlertUUIDPattern.MatchString(request.ConfigValue.ValueString()) {
		response.Diagnostics.AddAttributeError(request.Path, "Invalid identifier",
			fmt.Sprintf("%q is not accepted: %s, written as a lower-case UUID.", request.ConfigValue.ValueString(), uuidValidator.refusal))
	}
}

// applicationAlertNameValidator requires 1 to 100 characters without
// surrounding whitespace, which the platform trims away and would then read
// back as a change.
type applicationAlertNameValidator struct{}

func (applicationAlertNameValidator) Description(_ context.Context) string {
	return fmt.Sprintf("value must be 1 to %d characters, without leading or trailing whitespace", applicationAlertNameMaximum)
}

func (nameValidator applicationAlertNameValidator) MarkdownDescription(ctx context.Context) string {
	return nameValidator.Description(ctx)
}

func (nameValidator applicationAlertNameValidator) ValidateString(_ context.Context, request validator.StringRequest, response *validator.StringResponse) {
	if request.ConfigValue.IsNull() || request.ConfigValue.IsUnknown() {
		return
	}
	value := request.ConfigValue.ValueString()
	if length := utf8.RuneCountInString(value); length < 1 || length > applicationAlertNameMaximum {
		response.Diagnostics.AddAttributeError(request.Path, "Invalid name",
			fmt.Sprintf("name must be 1 to %d characters.", applicationAlertNameMaximum))
		return
	}
	if value != strings.TrimSpace(value) {
		response.Diagnostics.AddAttributeError(request.Path, "Invalid name",
			fmt.Sprintf("%q is not accepted: name must not start or end with whitespace, which the platform trims away.", value))
	}
}

// applicationAlertSendToValidator requires 1 to 5 destination ids, each a
// lower-case UUID.
type applicationAlertSendToValidator struct{}

func (applicationAlertSendToValidator) Description(_ context.Context) string {
	return fmt.Sprintf("1 to %d alert destination ids", applicationAlertDestinationsMaximum)
}

func (sendToValidator applicationAlertSendToValidator) MarkdownDescription(ctx context.Context) string {
	return sendToValidator.Description(ctx)
}

func (applicationAlertSendToValidator) ValidateSet(_ context.Context, request validator.SetRequest, response *validator.SetResponse) {
	if request.ConfigValue.IsNull() || request.ConfigValue.IsUnknown() {
		return
	}
	elements := request.ConfigValue.Elements()
	if len(elements) < 1 || len(elements) > applicationAlertDestinationsMaximum {
		response.Diagnostics.AddAttributeError(request.Path, "Invalid send_to",
			fmt.Sprintf("send_to must name 1 to %d places to send alerts to.", applicationAlertDestinationsMaximum))
		return
	}
	for _, element := range elements {
		destinationID, isString := element.(types.String)
		if !isString || destinationID.IsNull() || destinationID.IsUnknown() {
			continue
		}
		if !applicationAlertUUIDPattern.MatchString(destinationID.ValueString()) {
			response.Diagnostics.AddAttributeError(request.Path, "Invalid send_to",
				fmt.Sprintf("%q is not accepted: send_to must list the ids of places to send alerts to, written as lower-case UUIDs.",
					destinationID.ValueString()))
		}
	}
}
