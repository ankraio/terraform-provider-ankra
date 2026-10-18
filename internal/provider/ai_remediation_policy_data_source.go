// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ datasource.DataSource              = (*aiRemediationPolicyDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*aiRemediationPolicyDataSource)(nil)
)

type aiRemediationPolicyDataSource struct {
	configuredDataSourceClient
}

type aiRemediationPolicyModel struct {
	ID                    types.String `tfsdk:"id"`
	Enabled               types.Bool   `tfsdk:"enabled"`
	AutonomyLevel         types.String `tfsdk:"autonomy_level"`
	TierOverrides         types.Map    `tfsdk:"tier_overrides"`
	SlackWebhookID        types.String `tfsdk:"slack_webhook_id"`
	ApproverUserIDs       types.List   `tfsdk:"approver_user_ids"`
	MaxActionsPerIncident types.Int64  `tfsdk:"max_actions_per_incident"`
	CooldownMinutes       types.Int64  `tfsdk:"cooldown_minutes"`
	ClusterAllowList      types.List   `tfsdk:"cluster_allow_list"`
	UpdatedAt             types.String `tfsdk:"updated_at"`
}

// NewAIRemediationPolicyDataSource returns the ankra_ai_remediation_policy
// data source. The policy is set in the portal; there is no API write for a
// token, so it is read-only here.
func NewAIRemediationPolicyDataSource() datasource.DataSource {
	return &aiRemediationPolicyDataSource{}
}

func (policy *aiRemediationPolicyDataSource) Metadata(_ context.Context, request datasource.MetadataRequest, response *datasource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_ai_remediation_policy"
}

func (policy *aiRemediationPolicyDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, response *datasource.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: "Reads the organisation's AI remediation policy: whether Ankra AI may act on incidents, how much on its own, and " +
			"within which limits. An organisation that never set one reads as the platform default (disabled, `propose`). " +
			"The policy is changed in the portal by an organisation admin.",
		Attributes: map[string]schema.Attribute{
			"id":             serviceComputedString("Always `ai_remediation_policy`."),
			"enabled":        serviceComputedBool("Whether AI remediation is on."),
			"autonomy_level": serviceComputedString("`read_only`, `propose` or `auto`."),
			"tier_overrides": schema.MapAttribute{
				MarkdownDescription: "Per action tier: `auto`, `approval` or `never`.",
				ElementType:         types.StringType,
				Computed:            true,
			},
			"slack_webhook_id": serviceComputedString("Slack destination approvals are asked in, if any."),
			"approver_user_ids": schema.ListAttribute{
				MarkdownDescription: "Users who may approve actions.",
				ElementType:         types.StringType,
				Computed:            true,
			},
			"max_actions_per_incident": serviceComputedInt64("Most actions taken for one incident."),
			"cooldown_minutes":         serviceComputedInt64("Minutes between actions on the same incident."),
			"cluster_allow_list": schema.ListAttribute{
				MarkdownDescription: "Clusters remediation is limited to; null means every cluster.",
				ElementType:         types.StringType,
				Computed:            true,
			},
			"updated_at": serviceComputedString("When the policy last changed; null for the default."),
		},
	}
}

func (policy *aiRemediationPolicyDataSource) Read(ctx context.Context, _ datasource.ReadRequest, response *datasource.ReadResponse) {
	apiClient := policy.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	stored, err := apiClient.GetAIRemediationPolicy(ctx)
	if err != nil {
		response.Diagnostics.AddError("Unable to read the AI remediation policy", err.Error())
		return
	}
	tierOverrides, diagnostics := types.MapValueFrom(ctx, types.StringType, stored.TierOverrides)
	response.Diagnostics.Append(diagnostics...)
	approvers := stored.ApproverUserIDs
	if approvers == nil {
		approvers = []string{}
	}
	state := aiRemediationPolicyModel{
		ID:                    types.StringValue("ai_remediation_policy"),
		Enabled:               types.BoolValue(stored.Enabled),
		AutonomyLevel:         types.StringValue(stored.AutonomyLevel),
		TierOverrides:         tierOverrides,
		SlackWebhookID:        types.StringPointerValue(stored.SlackWebhookID),
		ApproverUserIDs:       listFromStrings(approvers),
		MaxActionsPerIncident: types.Int64Value(stored.MaxActionsPerIncident),
		CooldownMinutes:       types.Int64Value(stored.CooldownMinutes),
		ClusterAllowList:      listFromStrings(stored.ClusterAllowList),
		UpdatedAt:             types.StringPointerValue(stored.UpdatedAt),
	}
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
}
