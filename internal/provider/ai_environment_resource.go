// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/objectplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"ankra.io/terraform-provider-ankra/internal/client"
)

var (
	_ resource.Resource                   = (*aiEnvironmentResource)(nil)
	_ resource.ResourceWithConfigure      = (*aiEnvironmentResource)(nil)
	_ resource.ResourceWithImportState    = (*aiEnvironmentResource)(nil)
	_ resource.ResourceWithModifyPlan     = (*aiEnvironmentResource)(nil)
	_ resource.ResourceWithValidateConfig = (*aiEnvironmentResource)(nil)
)

const aiEnvironmentID = "ai_environment"

// aiEnvironmentDefaults is what the platform answers for an organisation
// that never chose (usecase/aienvironment defaultSettings). Destroy writes
// these back for the fields the configuration set. staging_cluster_id has no
// entry: clearing it would stop every AI workspace and demo, so destroy
// leaves it as it is.
var aiEnvironmentDefaults = map[string]any{
	"workspace_ttl_hours":               8,
	"demo_ttl_hours":                    24,
	"default_gateway_mode":              "ask",
	"demo_base_domain":                  nil,
	"demo_ingress_class_name":           nil,
	"demo_tls_secret_name":              nil,
	"demo_cert_issuer_name":             nil,
	"home_slack_channel_id":             nil,
	aiEnvironmentRoleCapKey("admin"):    720,
	aiEnvironmentRoleCapKey("operator"): 168,
	aiEnvironmentRoleCapKey("member"):   72,
}

// aiEnvironmentRoleCapTiers are the role tiers a demo lifetime cap is set
// for: owner shares the admin tier, viewer and custom roles the member tier.
var aiEnvironmentRoleCapTiers = []string{"admin", "operator", "member"}

const aiEnvironmentRoleCapsField = "demo_ttl_role_caps"

// aiEnvironmentRoleCapKey names one tier of demo_ttl_role_caps in the
// managed-settings record, so destroy resets only the tiers configured.
func aiEnvironmentRoleCapKey(tier string) string {
	return aiEnvironmentRoleCapsField + "." + tier
}

var aiEnvironmentRoleCapsAttributeTypes = map[string]attr.Type{
	"admin":    types.Int64Type,
	"operator": types.Int64Type,
	"member":   types.Int64Type,
}

type aiEnvironmentResource struct {
	configuredClient
}

type aiEnvironmentModel struct {
	ID                   types.String `tfsdk:"id"`
	StagingClusterID     types.String `tfsdk:"staging_cluster_id"`
	StagingClusterName   types.String `tfsdk:"staging_cluster_name"`
	WorkspaceTTLHours    types.Int64  `tfsdk:"workspace_ttl_hours"`
	DemoTTLHours         types.Int64  `tfsdk:"demo_ttl_hours"`
	DefaultGatewayMode   types.String `tfsdk:"default_gateway_mode"`
	DemoBaseDomain       types.String `tfsdk:"demo_base_domain"`
	DemoIngressClassName types.String `tfsdk:"demo_ingress_class_name"`
	DemoTLSSecretName    types.String `tfsdk:"demo_tls_secret_name"`
	DemoCertIssuerName   types.String `tfsdk:"demo_cert_issuer_name"`
	DemoTTLRoleCaps      types.Object `tfsdk:"demo_ttl_role_caps"`
	HomeSlackChannelID   types.String `tfsdk:"home_slack_channel_id"`
	HomeSlackChannelName types.String `tfsdk:"home_slack_channel_name"`
	HomeSlackTeamID      types.String `tfsdk:"home_slack_team_id"`
}

// NewAIEnvironmentResource returns the ankra_ai_environment resource, the
// counterpart of `ankra org ai-environment set`.
func NewAIEnvironmentResource() resource.Resource {
	return &aiEnvironmentResource{}
}

func (environment *aiEnvironmentResource) Metadata(_ context.Context, request resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_ai_environment"
}

func aiEnvironmentString(description string, validators ...validator.String) schema.StringAttribute {
	return schema.StringAttribute{
		MarkdownDescription: description, Optional: true, Computed: true, Validators: validators,
		PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
	}
}

func aiEnvironmentInt64(description string) schema.Int64Attribute {
	return schema.Int64Attribute{
		MarkdownDescription: description, Optional: true, Computed: true,
		PlanModifiers: []planmodifier.Int64{int64planmodifier.UseStateForUnknown()},
	}
}

func (environment *aiEnvironmentResource) Schema(_ context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: "The organisation's AI environment: the staging cluster AI workspaces and pull request demos run on, " +
			"their lifetimes, the default AI Gateway mode, and the preview domain demos are published under. The counterpart of " +
			"`ankra org ai-environment set`. One per organisation; changes need organisation admin.\n\n" +
			"Only the attributes you set are managed: an attribute left unset keeps the organisation's value and is read back as a " +
			"computed value, and removing one from configuration stops managing it without clearing it. Destroy resets the " +
			"attributes the configuration set to the platform defaults, except `staging_cluster_id`, which is left as it is because " +
			"clearing it would stop every AI workspace and demo. The organisation's DNS root domain lives on the same platform " +
			"object but is never touched here.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Always `ai_environment`.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"staging_cluster_id": aiEnvironmentString("Cluster AI workspaces and demos run on. It must be an eligible cluster of the organisation. The provider never clears it."),
			"staging_cluster_name": schema.StringAttribute{
				MarkdownDescription: "Name of the staging cluster.",
				Computed:            true,
			},
			"workspace_ttl_hours": aiEnvironmentInt64("AI workspace lifetime, 1 to 168 hours. Platform default 8."),
			"demo_ttl_hours":      aiEnvironmentInt64("Demo lifetime, 1 to 720 hours. Platform default 24."),
			"default_gateway_mode": aiEnvironmentString("Default AI Gateway mode for integrations: `ask` (the platform default) or `agent`.",
				organisationStringOneOf{values: []string{"ask", "agent"}}),
			"demo_base_domain":        aiEnvironmentString("Wildcard zone demos are published under; null falls back to the staging cluster's own DNS zone. Stored lowercased."),
			"demo_ingress_class_name": aiEnvironmentString("Ingress class for demo ingresses. Stored lowercased."),
			"demo_tls_secret_name":    aiEnvironmentString("Secret holding a certificate for the preview domain. Stored lowercased."),
			"demo_cert_issuer_name":   aiEnvironmentString("cert-manager ClusterIssuer demo ingresses ask instead of the cluster's own. Needs `demo_base_domain`. Stored lowercased."),
			"demo_ttl_role_caps": schema.SingleNestedAttribute{
				MarkdownDescription: "Longest demo lifetime, in hours (1 to 720), each role tier may ask for. Owner shares the `admin` tier; viewer and custom " +
					"roles share the `member` tier. Only the tiers you set are managed: an unset tier keeps the organisation's value. " +
					"Platform defaults: admin 720, operator 168, member 72.",
				Optional:      true,
				Computed:      true,
				PlanModifiers: []planmodifier.Object{objectplanmodifier.UseStateForUnknown()},
				Attributes: map[string]schema.Attribute{
					"admin":    aiEnvironmentRoleCap("Cap for the admin tier (owner and admin)."),
					"operator": aiEnvironmentRoleCap("Cap for the operator tier."),
					"member":   aiEnvironmentRoleCap("Cap for the member tier (member, viewer and custom roles)."),
				},
			},
			"home_slack_channel_id": aiEnvironmentString("Slack channel id of the organisation's home channel, where AI notifications go by default. " +
				"Needs Slack connected to the organisation. Destroy clears it when the configuration set it."),
			"home_slack_channel_name": schema.StringAttribute{
				MarkdownDescription: "Display name of the home Slack channel. Needs `home_slack_channel_id`.",
				Optional:            true,
				Computed:            true,
			},
			"home_slack_team_id": schema.StringAttribute{
				MarkdownDescription: "Slack workspace of the home channel.",
				Computed:            true,
			},
		},
	}
}

// aiEnvironmentBody names the configured settings only. A null in
// configuration is indistinguishable from an unset attribute, so the provider
// never sends a null staging_cluster_id.
func aiEnvironmentBody(model aiEnvironmentModel) map[string]any {
	body := map[string]any{}
	putString := func(key string, value types.String) {
		if !value.IsNull() && !value.IsUnknown() {
			body[key] = value.ValueString()
		}
	}
	putInt64 := func(key string, value types.Int64) {
		if !value.IsNull() && !value.IsUnknown() {
			body[key] = value.ValueInt64()
		}
	}
	putString("staging_cluster_id", model.StagingClusterID)
	putInt64("workspace_ttl_hours", model.WorkspaceTTLHours)
	putInt64("demo_ttl_hours", model.DemoTTLHours)
	putString("default_gateway_mode", model.DefaultGatewayMode)
	putString("demo_base_domain", model.DemoBaseDomain)
	putString("demo_ingress_class_name", model.DemoIngressClassName)
	putString("demo_tls_secret_name", model.DemoTLSSecretName)
	putString("demo_cert_issuer_name", model.DemoCertIssuerName)
	if !model.HomeSlackChannelID.IsNull() && !model.HomeSlackChannelID.IsUnknown() {
		putString("home_slack_channel_id", model.HomeSlackChannelID)
		putString("home_slack_channel_name", model.HomeSlackChannelName)
	}
	if caps := aiEnvironmentConfiguredRoleCaps(model.DemoTTLRoleCaps); len(caps) > 0 {
		body[aiEnvironmentRoleCapsField] = caps
	}
	return body
}

// aiEnvironmentConfiguredRoleCaps is the configured tiers of
// demo_ttl_role_caps. The platform merges the map it receives into the stored
// one, so the tiers left out keep their values.
func aiEnvironmentConfiguredRoleCaps(object types.Object) map[string]int64 {
	if object.IsNull() || object.IsUnknown() {
		return nil
	}
	caps := map[string]int64{}
	attributes := object.Attributes()
	for _, tier := range aiEnvironmentRoleCapTiers {
		value, isInt64 := attributes[tier].(types.Int64)
		if isInt64 && !value.IsNull() && !value.IsUnknown() {
			caps[tier] = value.ValueInt64()
		}
	}
	return caps
}

// aiEnvironmentManagedKeys is the managed-settings record of a write body:
// its top-level keys, with demo_ttl_role_caps recorded per tier.
func aiEnvironmentManagedKeys(body map[string]any) map[string]any {
	managed := map[string]any{}
	for key, value := range body {
		if key == "home_slack_channel_name" {
			continue
		}
		if caps, isCaps := value.(map[string]int64); isCaps && key == aiEnvironmentRoleCapsField {
			for tier := range caps {
				managed[aiEnvironmentRoleCapKey(tier)] = true
			}
			continue
		}
		managed[key] = value
	}
	return managed
}

// aiEnvironmentNestRoleCaps folds per-tier reset entries back into the
// demo_ttl_role_caps map the platform takes.
func aiEnvironmentNestRoleCaps(body map[string]any) map[string]any {
	caps := map[string]any{}
	for _, tier := range aiEnvironmentRoleCapTiers {
		if value, present := body[aiEnvironmentRoleCapKey(tier)]; present {
			caps[tier] = value
			delete(body, aiEnvironmentRoleCapKey(tier))
		}
	}
	if len(caps) > 0 {
		body[aiEnvironmentRoleCapsField] = caps
	}
	return body
}

func aiEnvironmentRoleCap(description string) schema.Int64Attribute {
	return schema.Int64Attribute{
		MarkdownDescription: description, Optional: true, Computed: true,
		PlanModifiers: []planmodifier.Int64{int64planmodifier.UseStateForUnknown()},
		Validators:    []validator.Int64{deployInt64Range{minimum: 1, maximum: 720}},
	}
}

// ValidateConfig refuses a home channel name without its id: the platform
// keys the home channel on the id and would ignore the name.
func (environment *aiEnvironmentResource) ValidateConfig(ctx context.Context, request resource.ValidateConfigRequest, response *resource.ValidateConfigResponse) {
	var config aiEnvironmentModel
	response.Diagnostics.Append(request.Config.Get(ctx, &config)...)
	if response.Diagnostics.HasError() {
		return
	}
	if !config.HomeSlackChannelName.IsNull() && config.HomeSlackChannelID.IsNull() {
		response.Diagnostics.AddAttributeError(path.Root("home_slack_channel_name"), "Home Slack channel name without its id",
			"Set home_slack_channel_id too: the platform identifies the home channel by its id and ignores a name on its own.")
	}
}

// ModifyPlan plans an update when the set of configured settings changed,
// so the apply records which ones destroy resets.
func (environment *aiEnvironmentResource) ModifyPlan(ctx context.Context, request resource.ModifyPlanRequest, response *resource.ModifyPlanResponse) {
	if request.State.Raw.IsNull() || request.Plan.Raw.IsNull() {
		return
	}
	var config aiEnvironmentModel
	response.Diagnostics.Append(request.Config.Get(ctx, &config)...)
	if response.Diagnostics.HasError() {
		return
	}
	if organisationManagedKeysChanged(ctx, aiEnvironmentManagedKeys(aiEnvironmentBody(config)), request.Private) {
		response.Diagnostics.Append(response.Plan.SetAttribute(ctx, path.Root("staging_cluster_name"), types.StringUnknown())...)
	}
}

func (environment *aiEnvironmentResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	environment.write(ctx, request.Config, request.Plan, &response.State, response.Private, &response.Diagnostics)
}

func (environment *aiEnvironmentResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	environment.write(ctx, request.Config, request.Plan, &response.State, response.Private, &response.Diagnostics)
}

// write sends only the configured fields; the platform keeps every key the
// body does not carry.
func (environment *aiEnvironmentResource) write(ctx context.Context, config tfsdk.Config, plan tfsdk.Plan, state *tfsdk.State, private privateStateWriter, diagnostics *diag.Diagnostics) {
	apiClient := environment.requireClient(diagnostics)
	if apiClient == nil {
		return
	}
	var model aiEnvironmentModel
	var configured aiEnvironmentModel
	diagnostics.Append(config.Get(ctx, &configured)...)
	diagnostics.Append(plan.Get(ctx, &model)...)
	if diagnostics.HasError() {
		return
	}
	body := aiEnvironmentBody(configured)

	var stored *client.OrganisationAIEnvironment
	var err error
	if len(body) == 0 {
		stored, err = apiClient.GetAIEnvironment(ctx)
	} else {
		stored, err = apiClient.UpdateAIEnvironment(ctx, body)
	}
	if err != nil {
		diagnostics.AddError("Unable to write AI environment", err.Error())
		return
	}
	aiEnvironmentApply(&model, stored)
	diagnostics.Append(state.Set(ctx, &model)...)
	diagnostics.Append(organisationRecordManagedKeys(ctx, aiEnvironmentManagedKeys(body), private)...)
	diagnostics.Append(trackScope(ctx, apiClient, private)...)
}

func (environment *aiEnvironmentResource) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	apiClient := environment.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state aiEnvironmentModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	stored, err := apiClient.GetAIEnvironment(ctx)
	if err != nil {
		response.Diagnostics.AddError("Unable to read AI environment", err.Error())
		return
	}
	aiEnvironmentApply(&state, stored)
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
	response.Diagnostics.Append(trackScope(ctx, apiClient, response.Private)...)
}

// Delete resets the fields the configuration set and leaves the rest, and
// the staging cluster, alone.
func (environment *aiEnvironmentResource) Delete(ctx context.Context, request resource.DeleteRequest, response *resource.DeleteResponse) {
	apiClient := environment.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	body := aiEnvironmentNestRoleCaps(organisationResetBody(organisationManagedKeys(ctx, request.Private), aiEnvironmentDefaults))
	if len(body) == 0 {
		return
	}
	if _, err := apiClient.UpdateAIEnvironment(ctx, body); err != nil {
		response.Diagnostics.AddError("Unable to restore the default AI environment", err.Error())
	}
}

// ImportState accepts any id: there is one AI environment per organisation.
func (environment *aiEnvironmentResource) ImportState(ctx context.Context, _ resource.ImportStateRequest, response *resource.ImportStateResponse) {
	response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("id"), aiEnvironmentID)...)
}

// aiEnvironmentNormalised keeps the configured spelling of a setting the
// platform stores trimmed and lowercased.
func aiEnvironmentNormalised(stateValue types.String, stored *string) types.String {
	if stored == nil {
		return types.StringNull()
	}
	if !stateValue.IsNull() && !stateValue.IsUnknown() && strings.EqualFold(strings.TrimSpace(stateValue.ValueString()), *stored) {
		return stateValue
	}
	return types.StringValue(*stored)
}

func aiEnvironmentApply(model *aiEnvironmentModel, stored *client.OrganisationAIEnvironment) {
	model.ID = types.StringValue(aiEnvironmentID)
	model.StagingClusterID = aiEnvironmentNormalised(model.StagingClusterID, stored.StagingClusterID)
	model.StagingClusterName = types.StringPointerValue(stored.StagingClusterName)
	model.WorkspaceTTLHours = types.Int64Value(stored.WorkspaceTTLHours)
	model.DemoTTLHours = types.Int64Value(stored.DemoTTLHours)
	model.DefaultGatewayMode = types.StringValue(stored.DefaultGatewayMode)
	model.DemoBaseDomain = aiEnvironmentNormalised(model.DemoBaseDomain, stored.DemoBaseDomain)
	model.DemoIngressClassName = aiEnvironmentNormalised(model.DemoIngressClassName, stored.DemoIngressClassName)
	model.DemoTLSSecretName = aiEnvironmentNormalised(model.DemoTLSSecretName, stored.DemoTLSSecretName)
	model.DemoCertIssuerName = aiEnvironmentNormalised(model.DemoCertIssuerName, stored.DemoCertIssuerName)
	model.HomeSlackChannelID = types.StringPointerValue(stored.HomeSlackChannelID)
	model.HomeSlackChannelName = types.StringPointerValue(stored.HomeSlackChannelName)
	model.HomeSlackTeamID = types.StringPointerValue(stored.HomeSlackTeamID)
	model.DemoTTLRoleCaps = types.ObjectValueMust(aiEnvironmentRoleCapsAttributeTypes, map[string]attr.Value{
		"admin":    aiEnvironmentRoleCapValue(stored.DemoTTLRoleCaps, "admin"),
		"operator": aiEnvironmentRoleCapValue(stored.DemoTTLRoleCaps, "operator"),
		"member":   aiEnvironmentRoleCapValue(stored.DemoTTLRoleCaps, "member"),
	})
}

func aiEnvironmentRoleCapValue(caps map[string]int64, tier string) types.Int64 {
	value, present := caps[tier]
	if !present {
		return types.Int64Null()
	}
	return types.Int64Value(value)
}
