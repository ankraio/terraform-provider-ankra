// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64default"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"ankra.io/terraform-provider-ankra/internal/client"
)

var (
	_ resource.Resource                = (*aiEnvironmentResource)(nil)
	_ resource.ResourceWithConfigure   = (*aiEnvironmentResource)(nil)
	_ resource.ResourceWithImportState = (*aiEnvironmentResource)(nil)
)

const aiEnvironmentID = "ai_environment"

// aiEnvironmentDefaults is what the platform answers for an organisation
// that never chose (usecase/aienvironment defaultSettings), and what
// destroy restores.
var aiEnvironmentDefaults = client.OrganisationAIEnvironmentUpdate{
	WorkspaceTTLHours:  8,
	DemoTTLHours:       24,
	DefaultGatewayMode: "ask",
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
}

// NewAIEnvironmentResource returns the ankra_ai_environment resource, the
// counterpart of `ankra org ai-environment set`.
func NewAIEnvironmentResource() resource.Resource {
	return &aiEnvironmentResource{}
}

func (environment *aiEnvironmentResource) Metadata(_ context.Context, request resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_ai_environment"
}

func (environment *aiEnvironmentResource) Schema(_ context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: "The organisation's AI environment: the staging cluster AI workspaces and pull request demos run on, " +
			"their lifetimes, the default AI Gateway mode, and the preview domain demos are published under. The counterpart of " +
			"`ankra org ai-environment set`. One per organisation; changes need organisation admin. Destroy restores the platform " +
			"defaults and clears the staging cluster and preview settings. The organisation's DNS root domain lives on the same " +
			"platform object but is not managed here.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Always `ai_environment`.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"staging_cluster_id": schema.StringAttribute{
				MarkdownDescription: "Cluster AI workspaces and demos run on. It must be an eligible cluster of the organisation.",
				Optional:            true,
			},
			"staging_cluster_name": schema.StringAttribute{
				MarkdownDescription: "Name of the staging cluster.",
				Computed:            true,
			},
			"workspace_ttl_hours": schema.Int64Attribute{
				MarkdownDescription: "AI workspace lifetime, 1 to 168 hours. Defaults to 8.",
				Optional:            true, Computed: true,
				Default: int64default.StaticInt64(aiEnvironmentDefaults.WorkspaceTTLHours),
			},
			"demo_ttl_hours": schema.Int64Attribute{
				MarkdownDescription: "Demo lifetime, 1 to 720 hours. Defaults to 24.",
				Optional:            true, Computed: true,
				Default: int64default.StaticInt64(aiEnvironmentDefaults.DemoTTLHours),
			},
			"default_gateway_mode": schema.StringAttribute{
				MarkdownDescription: "Default AI Gateway mode for integrations: `ask` (default) or `agent`.",
				Optional:            true, Computed: true,
				Default:    stringdefault.StaticString(aiEnvironmentDefaults.DefaultGatewayMode),
				Validators: []validator.String{organisationStringOneOf{values: []string{"ask", "agent"}}},
			},
			"demo_base_domain": schema.StringAttribute{
				MarkdownDescription: "Wildcard zone demos are published under. Unset falls back to the staging cluster's own DNS zone.",
				Optional:            true,
			},
			"demo_ingress_class_name": schema.StringAttribute{
				MarkdownDescription: "Ingress class for demo ingresses.",
				Optional:            true,
			},
			"demo_tls_secret_name": schema.StringAttribute{
				MarkdownDescription: "Secret holding a certificate for the preview domain.",
				Optional:            true,
			},
			"demo_cert_issuer_name": schema.StringAttribute{
				MarkdownDescription: "cert-manager ClusterIssuer demo ingresses ask instead of the cluster's own. Needs `demo_base_domain`.",
				Optional:            true,
			},
		},
	}
}

func (environment *aiEnvironmentResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	environment.write(ctx, request.Plan, &response.State, &response.Diagnostics)
}

func (environment *aiEnvironmentResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	environment.write(ctx, request.Plan, &response.State, &response.Diagnostics)
}

func (environment *aiEnvironmentResource) write(ctx context.Context, plan tfsdk.Plan, state *tfsdk.State, diagnostics *diag.Diagnostics) {
	apiClient := environment.requireClient(diagnostics)
	if apiClient == nil {
		return
	}
	var model aiEnvironmentModel
	diagnostics.Append(plan.Get(ctx, &model)...)
	if diagnostics.HasError() {
		return
	}
	stored, err := apiClient.UpdateAIEnvironment(ctx, client.OrganisationAIEnvironmentUpdate{
		StagingClusterID:     stringPointer(model.StagingClusterID),
		WorkspaceTTLHours:    model.WorkspaceTTLHours.ValueInt64(),
		DemoTTLHours:         model.DemoTTLHours.ValueInt64(),
		DefaultGatewayMode:   model.DefaultGatewayMode.ValueString(),
		DemoBaseDomain:       stringPointer(model.DemoBaseDomain),
		DemoIngressClassName: stringPointer(model.DemoIngressClassName),
		DemoTLSSecretName:    stringPointer(model.DemoTLSSecretName),
		DemoCertIssuerName:   stringPointer(model.DemoCertIssuerName),
	})
	if err != nil {
		diagnostics.AddError("Unable to write AI environment", err.Error())
		return
	}
	aiEnvironmentApply(&model, stored)
	diagnostics.Append(state.Set(ctx, &model)...)
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
}

// Delete restores the defaults and clears the staging cluster and preview
// settings.
func (environment *aiEnvironmentResource) Delete(ctx context.Context, _ resource.DeleteRequest, response *resource.DeleteResponse) {
	apiClient := environment.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	if _, err := apiClient.UpdateAIEnvironment(ctx, aiEnvironmentDefaults); err != nil {
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
}
