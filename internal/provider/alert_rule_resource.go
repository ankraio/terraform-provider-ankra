// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"
	"slices"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"ankra.io/terraform-provider-ankra/internal/client"
)

// alertRuleSeverities are the severities the platform accepts on a rule.
var alertRuleSeverities = []string{"critical", "warning", "info"}

// alertRuleLogicalOperators join a rule's conditions.
var alertRuleLogicalOperators = []string{"AND", "OR"}

// alertRuleMinFailureDurationMaximum is the platform's bound on
// min_failure_duration_minutes: one day.
const alertRuleMinFailureDurationMaximum = 1440

var (
	_ resource.Resource                = (*alertRuleResource)(nil)
	_ resource.ResourceWithConfigure   = (*alertRuleResource)(nil)
	_ resource.ResourceWithImportState = (*alertRuleResource)(nil)
)

type alertRuleResource struct {
	configuredClient
}

type alertRuleModel struct {
	ID               types.String               `tfsdk:"id"`
	Name             types.String               `tfsdk:"name"`
	Description      types.String               `tfsdk:"description"`
	Enabled          types.Bool                 `tfsdk:"enabled"`
	DestinationNames types.Set                  `tfsdk:"destination_names"`
	Rules            []alertRuleDefinitionModel `tfsdk:"rules"`
	RuleCount        types.Int64                `tfsdk:"rule_count"`
	Severity         types.String               `tfsdk:"severity"`
	State            types.String               `tfsdk:"state"`
}

type alertRuleDefinitionModel struct {
	Name                      types.String              `tfsdk:"name"`
	Description               types.String              `tfsdk:"description"`
	ResourceType              types.String              `tfsdk:"resource_type"`
	ResourceID                types.String              `tfsdk:"resource_id"`
	ClusterIDs                types.Set                 `tfsdk:"cluster_ids"`
	AllClusters               types.Bool                `tfsdk:"all_clusters"`
	Environments              types.Set                 `tfsdk:"environments"`
	ClusterResourceTypes      types.Set                 `tfsdk:"cluster_resource_types"`
	ResourceNamePattern       types.String              `tfsdk:"resource_name_pattern"`
	Severity                  types.String              `tfsdk:"severity"`
	Enabled                   types.Bool                `tfsdk:"enabled"`
	CooldownMinutes           types.Int64               `tfsdk:"cooldown_minutes"`
	MinFailureDurationMinutes types.Int64               `tfsdk:"min_failure_duration_minutes"`
	Conditions                []alertRuleConditionModel `tfsdk:"conditions"`
}

type alertRuleConditionModel struct {
	ConditionType     types.String  `tfsdk:"condition_type"`
	ConditionOperator types.String  `tfsdk:"condition_operator"`
	ConditionValue    types.String  `tfsdk:"condition_value"`
	ThresholdValue    types.Float64 `tfsdk:"threshold_value"`
	LogicalOperator   types.String  `tfsdk:"logical_operator"`
}

// NewAlertRuleResource returns the ankra_alert_rule resource, the
// counterpart of `ankra alerts rules`.
func NewAlertRuleResource() resource.Resource {
	return &alertRuleResource{}
}

func (ruleResource *alertRuleResource) Metadata(_ context.Context, request resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_alert_rule"
}

func (ruleResource *alertRuleResource) Schema(_ context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: "An alert definition (`ankra alerts rules`): the rules Ankra evaluates against the organisation's clusters, " +
			"and the alert destinations it notifies when one fires. Setting `enabled = false` mutes the alert: its history is kept, " +
			"an open incident is settled, and nothing new fires until it is enabled again. The platform reports how many rules an " +
			"alert holds but not the rules themselves, so `rules` is kept as configured; a rule count that no longer matches plans " +
			"the rules again, and every change to `rules` replaces the alert's whole rule set. An imported alert adopts the " +
			"configured `rules` on the first apply. Destroy deletes the alert and its rules. " +
			"Needs an Ankra platform from 2026-11-08 or later.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Identifier of the alert.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				MarkdownDescription: "Alert name, unique in the organisation.",
				Required:            true,
			},
			"description": schema.StringAttribute{
				MarkdownDescription: "Free-text description.",
				Optional:            true,
			},
			"enabled": schema.BoolAttribute{
				MarkdownDescription: "Whether the alert is evaluated and notifies. Defaults to `true`; `false` mutes it.",
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(true),
			},
			"destination_names": schema.SetAttribute{
				MarkdownDescription: "Names of the alert destinations (`ankra_alert_destination`) notified when the alert fires. " +
					"A name that matches no destination fails the apply.",
				Optional:    true,
				ElementType: types.StringType,
			},
			"rules": schema.ListNestedAttribute{
				MarkdownDescription: "The rules of the alert, at least one. The alert fires when any rule's conditions hold.",
				Required:            true,
				Validators:          []validator.List{alertRuleListNotEmpty{}},
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"name": schema.StringAttribute{
							MarkdownDescription: "Rule name.",
							Required:            true,
						},
						"description": schema.StringAttribute{
							MarkdownDescription: "Free-text description of the rule.",
							Optional:            true,
						},
						"resource_type": schema.StringAttribute{
							MarkdownDescription: "Kind of resource the rule evaluates, such as `cluster`, `node`, `pod` or `addon`.",
							Required:            true,
						},
						"resource_id": schema.StringAttribute{
							MarkdownDescription: "Identifier of the one resource the rule evaluates; unset evaluates every resource of `resource_type` the rule's clusters hold.",
							Optional:            true,
						},
						"cluster_ids": schema.SetAttribute{
							MarkdownDescription: "Clusters the rule applies to.",
							Optional:            true,
							ElementType:         types.StringType,
						},
						"all_clusters": schema.BoolAttribute{
							MarkdownDescription: "Whether the rule applies to every cluster of the organisation, including clusters added later.",
							Optional:            true,
						},
						"environments": schema.SetAttribute{
							MarkdownDescription: "Cluster environments the rule applies to, such as `production`.",
							Optional:            true,
							ElementType:         types.StringType,
						},
						"cluster_resource_types": schema.SetAttribute{
							MarkdownDescription: "Kubernetes resource kinds a cluster-wide rule evaluates.",
							Optional:            true,
							ElementType:         types.StringType,
						},
						"resource_name_pattern": schema.StringAttribute{
							MarkdownDescription: "Pattern a resource's name must match for the rule to evaluate it.",
							Optional:            true,
						},
						"severity": schema.StringAttribute{
							MarkdownDescription: "`critical`, `warning` or `info`. The platform default is `warning`.",
							Optional:            true,
							Validators:          []validator.String{organisationStringOneOf{values: alertRuleSeverities}},
						},
						"enabled": schema.BoolAttribute{
							MarkdownDescription: "Whether the rule is evaluated. The platform default is `true`.",
							Optional:            true,
						},
						"cooldown_minutes": schema.Int64Attribute{
							MarkdownDescription: "Minutes after a firing before the rule may fire again. The platform default is 5.",
							Optional:            true,
							Validators:          []validator.Int64{alertDNSRangeValidator{minimum: 0, maximum: 1 << 31}},
						},
						"min_failure_duration_minutes": schema.Int64Attribute{
							MarkdownDescription: fmt.Sprintf("Minutes the conditions must hold before the rule fires, 0 to %d; unset uses the evaluator's default.",
								alertRuleMinFailureDurationMaximum),
							Optional:   true,
							Validators: []validator.Int64{alertDNSRangeValidator{minimum: 0, maximum: alertRuleMinFailureDurationMaximum}},
						},
						"conditions": schema.ListNestedAttribute{
							MarkdownDescription: "Conditions of the rule, joined by each condition's `logical_operator`.",
							Required:            true,
							NestedObject: schema.NestedAttributeObject{
								Attributes: map[string]schema.Attribute{
									"condition_type": schema.StringAttribute{
										MarkdownDescription: "What the condition reads, such as `status` or a metric.",
										Required:            true,
									},
									"condition_operator": schema.StringAttribute{
										MarkdownDescription: "How the value is compared, such as `equals` or `greater_than`.",
										Required:            true,
									},
									"condition_value": schema.StringAttribute{
										MarkdownDescription: "The value compared against, such as `DiskPressure`.",
										Required:            true,
									},
									"threshold_value": schema.Float64Attribute{
										MarkdownDescription: "Numeric threshold of a metric condition.",
										Optional:            true,
									},
									"logical_operator": schema.StringAttribute{
										MarkdownDescription: "`AND` or `OR`, joining the condition to the others. The platform default is `AND`.",
										Optional:            true,
										Validators:          []validator.String{organisationStringOneOf{values: alertRuleLogicalOperators}},
									},
								},
							},
						},
					},
				},
			},
			"rule_count": schema.Int64Attribute{
				MarkdownDescription: "How many rules the platform stores for the alert.",
				Computed:            true,
			},
			"severity": schema.StringAttribute{
				MarkdownDescription: "The highest severity among the alert's rules.",
				Computed:            true,
			},
			"state": schema.StringAttribute{
				MarkdownDescription: "Lifecycle state of the alert on the platform.",
				Computed:            true,
			},
		},
	}
}

func (ruleResource *alertRuleResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	apiClient := ruleResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var plan alertRuleModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}
	write := alertRuleWriteFromPlan(ctx, plan, &response.Diagnostics)
	write.Rules = alertRuleDefinitionsToAPI(ctx, plan.Rules, &response.Diagnostics)
	if response.Diagnostics.HasError() {
		return
	}
	alertID, createError := apiClient.CreateAlertRule(ctx, write)
	if createError != nil {
		response.Diagnostics.AddError("Unable to create alert rule", createError.Error())
		return
	}
	plan.ID = types.StringValue(alertID)
	response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("id"), plan.ID)...)
	alert, readError := apiClient.GetAlertRule(ctx, alertID)
	if readError != nil {
		response.Diagnostics.AddError("Unable to read the alert rule back", readError.Error())
		return
	}
	alertRuleApply(&plan, alert)
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
	response.Diagnostics.Append(trackScope(ctx, apiClient, response.Private)...)
}

func (ruleResource *alertRuleResource) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	apiClient := ruleResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state alertRuleModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	alert, readError := apiClient.GetAlertRule(ctx, state.ID.ValueString())
	if removeWhenNotFound(ctx, readError, apiClient, response) {
		return
	}
	if readError != nil {
		response.Diagnostics.AddError("Unable to read alert rule", readError.Error())
		return
	}
	state.Name = types.StringValue(alert.Name)
	state.Description = optionalString(alertDNSStringValue(alert.Description))
	state.Enabled = types.BoolValue(alert.Enabled)
	destinationNames, namesError := alertRuleDestinationNames(ctx, apiClient, alert.WebhookIDs)
	if namesError != nil {
		response.Diagnostics.AddError("Unable to read the alert rule's destinations", namesError.Error())
		return
	}
	switch {
	case len(destinationNames) > 0:
		state.DestinationNames = clusterSettingsSetFromStrings(destinationNames)
	case !state.DestinationNames.IsNull():
		state.DestinationNames = types.SetValueMust(types.StringType, []attr.Value{})
	}
	if alert.RuleCount != len(state.Rules) {
		state.Rules = nil
	}
	alertRuleApply(&state, alert)
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
	response.Diagnostics.Append(trackScope(ctx, apiClient, response.Private)...)
}

func (ruleResource *alertRuleResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	apiClient := ruleResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var plan, state alertRuleModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	write := alertRuleWriteFromPlan(ctx, plan, &response.Diagnostics)
	if write.Description == nil {
		cleared := ""
		write.Description = &cleared
	}
	if !alertRuleDefinitionsEqual(plan.Rules, state.Rules) {
		write.Rules = alertRuleDefinitionsToAPI(ctx, plan.Rules, &response.Diagnostics)
	}
	if response.Diagnostics.HasError() {
		return
	}
	alertID := state.ID.ValueString()
	if updateError := apiClient.UpdateAlertRule(ctx, alertID, write); updateError != nil {
		response.Diagnostics.AddError("Unable to update alert rule", updateError.Error())
		return
	}
	alert, readError := apiClient.GetAlertRule(ctx, alertID)
	if readError != nil {
		response.Diagnostics.AddError("Unable to read the alert rule back", readError.Error())
		return
	}
	plan.ID = state.ID
	alertRuleApply(&plan, alert)
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
	response.Diagnostics.Append(trackScope(ctx, apiClient, response.Private)...)
}

func (ruleResource *alertRuleResource) Delete(ctx context.Context, request resource.DeleteRequest, response *resource.DeleteResponse) {
	apiClient := ruleResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state alertRuleModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	deleteError := apiClient.DeleteAlertRule(ctx, state.ID.ValueString())
	if deleteError == nil || deletedWhenNotFound(ctx, deleteError, apiClient, request.Private, &response.Diagnostics) || response.Diagnostics.HasError() {
		return
	}
	response.Diagnostics.AddError("Unable to delete alert rule", deleteError.Error())
}

// ImportState imports by alert id. The platform does not report the rules,
// so the first apply after an import writes the configured ones.
func (ruleResource *alertRuleResource) ImportState(ctx context.Context, request resource.ImportStateRequest, response *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), request, response)
}

// alertRuleWriteFromPlan maps the alert-level members of the plan. An unset
// destination_names is sent as an empty list, which notifies no destination.
func alertRuleWriteFromPlan(ctx context.Context, plan alertRuleModel, diagnostics *diag.Diagnostics) client.AlertRuleWrite {
	destinationNames := []string{}
	if !plan.DestinationNames.IsNull() && !plan.DestinationNames.IsUnknown() {
		destinationNames = clusterSettingsStringsFromSet(ctx, plan.DestinationNames, diagnostics)
	}
	return client.AlertRuleWrite{
		Name:             plan.Name.ValueString(),
		Description:      stringPointer(plan.Description),
		Enabled:          boolPointer(plan.Enabled),
		DestinationNames: destinationNames,
	}
}

func alertRuleDefinitionsToAPI(ctx context.Context, rules []alertRuleDefinitionModel, diagnostics *diag.Diagnostics) []client.AlertRuleDefinition {
	definitions := make([]client.AlertRuleDefinition, 0, len(rules))
	for _, rule := range rules {
		conditions := make([]client.AlertRuleCondition, 0, len(rule.Conditions))
		for _, condition := range rule.Conditions {
			var threshold *float64
			if !condition.ThresholdValue.IsNull() && !condition.ThresholdValue.IsUnknown() {
				value := condition.ThresholdValue.ValueFloat64()
				threshold = &value
			}
			conditions = append(conditions, client.AlertRuleCondition{
				ConditionType:     condition.ConditionType.ValueString(),
				ConditionOperator: condition.ConditionOperator.ValueString(),
				ConditionValue:    condition.ConditionValue.ValueString(),
				ThresholdValue:    threshold,
				LogicalOperator:   stringPointer(condition.LogicalOperator),
			})
		}
		definition := client.AlertRuleDefinition{
			Name:                      rule.Name.ValueString(),
			Description:               stringPointer(rule.Description),
			ResourceType:              rule.ResourceType.ValueString(),
			ResourceID:                stringPointer(rule.ResourceID),
			AllClusters:               boolPointer(rule.AllClusters),
			ResourceNamePattern:       stringPointer(rule.ResourceNamePattern),
			Severity:                  stringPointer(rule.Severity),
			Enabled:                   boolPointer(rule.Enabled),
			CooldownMinutes:           int64Pointer(rule.CooldownMinutes),
			MinFailureDurationMinutes: int64Pointer(rule.MinFailureDurationMinutes),
			Conditions:                conditions,
		}
		for _, member := range []struct {
			set    types.Set
			target *[]string
		}{
			{rule.ClusterIDs, &definition.ClusterIDs},
			{rule.Environments, &definition.Environments},
			{rule.ClusterResourceTypes, &definition.ClusterResourceTypes},
		} {
			if !member.set.IsNull() && !member.set.IsUnknown() {
				*member.target = clusterSettingsStringsFromSet(ctx, member.set, diagnostics)
			}
		}
		definitions = append(definitions, definition)
	}
	return definitions
}

// alertRuleDefinitionsEqual reports whether two rule lists are the same, so
// an update that changes only the alert's own members keeps its rules, and
// with them their evaluation state.
func alertRuleDefinitionsEqual(left, right []alertRuleDefinitionModel) bool {
	return slices.EqualFunc(left, right, func(leftRule, rightRule alertRuleDefinitionModel) bool {
		return leftRule.Name.Equal(rightRule.Name) &&
			leftRule.Description.Equal(rightRule.Description) &&
			leftRule.ResourceType.Equal(rightRule.ResourceType) &&
			leftRule.ResourceID.Equal(rightRule.ResourceID) &&
			leftRule.ClusterIDs.Equal(rightRule.ClusterIDs) &&
			leftRule.AllClusters.Equal(rightRule.AllClusters) &&
			leftRule.Environments.Equal(rightRule.Environments) &&
			leftRule.ClusterResourceTypes.Equal(rightRule.ClusterResourceTypes) &&
			leftRule.ResourceNamePattern.Equal(rightRule.ResourceNamePattern) &&
			leftRule.Severity.Equal(rightRule.Severity) &&
			leftRule.Enabled.Equal(rightRule.Enabled) &&
			leftRule.CooldownMinutes.Equal(rightRule.CooldownMinutes) &&
			leftRule.MinFailureDurationMinutes.Equal(rightRule.MinFailureDurationMinutes) &&
			slices.EqualFunc(leftRule.Conditions, rightRule.Conditions, alertRuleConditionsEqual)
	})
}

func alertRuleConditionsEqual(left, right alertRuleConditionModel) bool {
	return left.ConditionType.Equal(right.ConditionType) &&
		left.ConditionOperator.Equal(right.ConditionOperator) &&
		left.ConditionValue.Equal(right.ConditionValue) &&
		left.ThresholdValue.Equal(right.ThresholdValue) &&
		left.LogicalOperator.Equal(right.LogicalOperator)
}

// alertRuleDestinationNames maps the destination ids the platform reports
// to their names. An id no listed destination carries is left out.
func alertRuleDestinationNames(ctx context.Context, apiClient *client.Client, destinationIDs []string) ([]string, error) {
	if len(destinationIDs) == 0 {
		return nil, nil
	}
	destinations, listError := apiClient.ListAlertDestinations(ctx)
	if listError != nil {
		return nil, listError
	}
	namesByID := make(map[string]string, len(destinations))
	for _, destination := range destinations {
		namesByID[destination.ID] = destination.Name
	}
	names := make([]string, 0, len(destinationIDs))
	for _, destinationID := range destinationIDs {
		if name, found := namesByID[destinationID]; found {
			names = append(names, name)
		}
	}
	return names, nil
}

func alertRuleApply(model *alertRuleModel, alert *client.AlertRule) {
	model.RuleCount = types.Int64Value(int64(alert.RuleCount))
	model.Severity = optionalString(alert.Severity)
	model.State = optionalString(alert.State)
}

// alertRuleListNotEmpty requires at least one rule: the platform reads an
// empty rule list on an update as no change, so an alert could never be
// emptied from Terraform.
type alertRuleListNotEmpty struct{}

func (alertRuleListNotEmpty) Description(_ context.Context) string {
	return "at least one rule"
}

func (notEmpty alertRuleListNotEmpty) MarkdownDescription(ctx context.Context) string {
	return notEmpty.Description(ctx)
}

func (alertRuleListNotEmpty) ValidateList(_ context.Context, request validator.ListRequest, response *validator.ListResponse) {
	if request.ConfigValue.IsNull() || request.ConfigValue.IsUnknown() {
		return
	}
	if len(request.ConfigValue.Elements()) == 0 {
		response.Diagnostics.AddAttributeError(request.Path, "No rules", "An alert needs at least one rule.")
	}
}
