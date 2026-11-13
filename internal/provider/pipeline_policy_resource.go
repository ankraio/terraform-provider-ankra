// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/mapplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/setplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"ankra.io/terraform-provider-ankra/internal/client"
)

const pipelinePolicyID = "pipeline_policy"

var (
	_ resource.Resource                = (*pipelinePolicyResource)(nil)
	_ resource.ResourceWithConfigure   = (*pipelinePolicyResource)(nil)
	_ resource.ResourceWithImportState = (*pipelinePolicyResource)(nil)
)

type pipelinePolicyResource struct {
	configuredClient
}

type pipelinePolicyModel struct {
	ID                      types.String `tfsdk:"id"`
	AllowedNetworkTiers     types.Set    `tfsdk:"allowed_network_tiers"`
	AllowedCredentials      types.Set    `tfsdk:"allowed_credentials"`
	AllowedNodeSelectors    types.Map    `tfsdk:"allowed_node_selectors"`
	AllowedTolerations      types.Set    `tfsdk:"allowed_tolerations"`
	AllowedRuntimeClasses   types.Set    `tfsdk:"allowed_runtime_classes"`
	MaxCPU                  types.String `tfsdk:"max_cpu"`
	MaxMemory               types.String `tfsdk:"max_memory"`
	MaxCacheSize            types.String `tfsdk:"max_cache_size"`
	MaxGPU                  types.Int64  `tfsdk:"max_gpu"`
	MaxAgentToolCallsPerRun types.Int64  `tfsdk:"max_agent_tool_calls_per_run"`
	WebhookAllowedHosts     types.Set    `tfsdk:"webhook_allowed_hosts"`
	ExternalProviders       types.Set    `tfsdk:"external_providers"`
	AllowedImagePrefixes    types.Set    `tfsdk:"allowed_image_prefixes"`
	UpdatedBy               types.String `tfsdk:"updated_by"`
}

type pipelinePolicyTolerationModel struct {
	Key      types.String `tfsdk:"key"`
	Operator types.String `tfsdk:"operator"`
	Value    types.String `tfsdk:"value"`
	Effect   types.String `tfsdk:"effect"`
}

var pipelinePolicyTolerationType = types.ObjectType{AttrTypes: map[string]attr.Type{
	"key":      types.StringType,
	"operator": types.StringType,
	"value":    types.StringType,
	"effect":   types.StringType,
}}

var pipelinePolicyNodeSelectorType = types.ListType{ElemType: types.StringType}

// NewPipelinePolicyResource returns the ankra_pipeline_policy resource, the
// counterpart of `ankra pipeline policy`.
func NewPipelinePolicyResource() resource.Resource {
	return &pipelinePolicyResource{}
}

func (policy *pipelinePolicyResource) Metadata(_ context.Context, request resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_pipeline_policy"
}

func pipelinePolicyInt64(description string) schema.Int64Attribute {
	return schema.Int64Attribute{
		MarkdownDescription: description,
		Optional:            true,
		Computed:            true,
		PlanModifiers:       []planmodifier.Int64{int64planmodifier.UseStateForUnknown()},
	}
}

func (policy *pipelinePolicyResource) Schema(_ context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: "The organisation's pipeline policy (`ankra pipeline policy`): the authority ceiling its Ankra Pipelines may use, " +
			"which network tiers, credentials, node selectors, tolerations, runtime classes, resources and webhook hosts. There is one " +
			"per organisation. Only the attributes set in configuration are written; an unset attribute keeps the stored value and " +
			"reports it. A value the platform refuses is reported against the setting that produced it. The authority request inbox " +
			"stays a human decision and is not managed here. Managing the policy needs `pipelines.manage`, the read included. " +
			"Destroy leaves the policy as it is.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Always `pipeline_policy`.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"allowed_network_tiers":   aiTicketOptionalSet("Network tiers pipeline steps may ask for."),
			"allowed_credentials":     aiTicketOptionalSet("Credentials pipelines may bind."),
			"allowed_runtime_classes": aiTicketOptionalSet("Runtime classes pipeline steps may run under."),
			"allowed_node_selectors": schema.MapAttribute{
				MarkdownDescription: "Node selector keys pipeline steps may use, each with the values allowed for it.",
				Optional:            true,
				Computed:            true,
				ElementType:         pipelinePolicyNodeSelectorType,
				PlanModifiers:       []planmodifier.Map{mapplanmodifier.UseStateForUnknown()},
			},
			"allowed_tolerations": schema.SetNestedAttribute{
				MarkdownDescription: "Tolerations pipeline steps may carry.",
				Optional:            true,
				Computed:            true,
				PlanModifiers:       []planmodifier.Set{setplanmodifier.UseStateForUnknown()},
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"key":      schema.StringAttribute{MarkdownDescription: "Taint key.", Required: true},
						"operator": schema.StringAttribute{MarkdownDescription: "`Equal` or `Exists`.", Required: true},
						"value":    schema.StringAttribute{MarkdownDescription: "Taint value; empty for `Exists`.", Required: true},
						"effect":   schema.StringAttribute{MarkdownDescription: "Taint effect, such as `NoSchedule`.", Required: true},
					},
				},
			},
			"max_cpu":                      aiTicketOptionalString("Most CPU one pipeline step may ask for, as a Kubernetes quantity such as `4`."),
			"max_memory":                   aiTicketOptionalString("Most memory one pipeline step may ask for, such as `8Gi`."),
			"max_cache_size":               aiTicketOptionalString("Largest pipeline cache, such as `20Gi`."),
			"max_gpu":                      pipelinePolicyInt64("Most GPUs one pipeline step may ask for."),
			"max_agent_tool_calls_per_run": pipelinePolicyInt64("Most tool calls an agent step may make in one run."),
			"webhook_allowed_hosts":        aiTicketOptionalSet("Hosts pipeline webhook steps may call."),
			"external_providers":           aiTicketOptionalSet("External providers pipelines may reach."),
			"allowed_image_prefixes": schema.SetAttribute{
				MarkdownDescription: "Image prefixes pipeline steps may run. Owned by the CI settings (`ankra_ci_settings`), so read-only here.",
				Computed:            true,
				ElementType:         types.StringType,
			},
			"updated_by": schema.StringAttribute{
				MarkdownDescription: "Who last changed the policy.",
				Computed:            true,
			},
		},
	}
}

func (policy *pipelinePolicyResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	var plan pipelinePolicyModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if policy.write(ctx, &plan, &response.Diagnostics) {
		response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
		response.Diagnostics.Append(trackScope(ctx, policy.client, response.Private)...)
	}
}

func (policy *pipelinePolicyResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	var plan pipelinePolicyModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if policy.write(ctx, &plan, &response.Diagnostics) {
		response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
	}
}

func (policy *pipelinePolicyResource) write(ctx context.Context, plan *pipelinePolicyModel, diagnostics *diag.Diagnostics) bool {
	apiClient := policy.requireOrganisationClient(diagnostics)
	if apiClient == nil || diagnostics.HasError() {
		return false
	}
	update := client.PipelinePolicyUpdate{}
	aiTicketPutSet(ctx, update, "allowed_network_tiers", plan.AllowedNetworkTiers, diagnostics)
	aiTicketPutSet(ctx, update, "allowed_credentials", plan.AllowedCredentials, diagnostics)
	aiTicketPutSet(ctx, update, "allowed_runtime_classes", plan.AllowedRuntimeClasses, diagnostics)
	aiTicketPutSet(ctx, update, "webhook_allowed_hosts", plan.WebhookAllowedHosts, diagnostics)
	aiTicketPutSet(ctx, update, "external_providers", plan.ExternalProviders, diagnostics)
	aiTicketPutString(update, "max_cpu", plan.MaxCPU)
	aiTicketPutString(update, "max_memory", plan.MaxMemory)
	aiTicketPutString(update, "max_cache_size", plan.MaxCacheSize)
	if number := int64Pointer(plan.MaxGPU); number != nil {
		update["max_gpu"] = *number
	}
	if number := int64Pointer(plan.MaxAgentToolCallsPerRun); number != nil {
		update["max_agent_tool_calls_per_run"] = *number
	}
	if !plan.AllowedNodeSelectors.IsNull() && !plan.AllowedNodeSelectors.IsUnknown() {
		selectors := map[string][]string{}
		diagnostics.Append(plan.AllowedNodeSelectors.ElementsAs(ctx, &selectors, false)...)
		update["allowed_node_selectors"] = selectors
	}
	if !plan.AllowedTolerations.IsNull() && !plan.AllowedTolerations.IsUnknown() {
		var tolerations []pipelinePolicyTolerationModel
		diagnostics.Append(plan.AllowedTolerations.ElementsAs(ctx, &tolerations, false)...)
		written := make([]client.PipelinePolicyToleration, 0, len(tolerations))
		for _, toleration := range tolerations {
			written = append(written, client.PipelinePolicyToleration{
				Key: toleration.Key.ValueString(), Operator: toleration.Operator.ValueString(),
				Value: toleration.Value.ValueString(), Effect: toleration.Effect.ValueString(),
			})
		}
		update["allowed_tolerations"] = written
	}
	if diagnostics.HasError() {
		return false
	}
	var stored *client.PipelinePolicy
	var requestError error
	if len(update) == 0 {
		stored, requestError = apiClient.GetPipelinePolicy(ctx)
	} else {
		stored, requestError = apiClient.UpdatePipelinePolicy(ctx, update)
	}
	if requestError != nil {
		diagnostics.AddError("Unable to set the pipeline policy", requestError.Error())
		return false
	}
	pipelinePolicyApply(plan, stored)
	return true
}

func (policy *pipelinePolicyResource) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	apiClient := policy.requireOrganisationClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state pipelinePolicyModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	stored, readError := apiClient.GetPipelinePolicy(ctx)
	if readError != nil {
		response.Diagnostics.AddError("Unable to read the pipeline policy", readError.Error())
		return
	}
	pipelinePolicyApply(&state, stored)
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
	response.Diagnostics.Append(trackScope(ctx, apiClient, response.Private)...)
}

// Delete leaves the policy as it is: there is no platform default to return
// to, and widening the ceiling on destroy would grant pipelines authority
// nobody asked for.
func (policy *pipelinePolicyResource) Delete(_ context.Context, _ resource.DeleteRequest, _ *resource.DeleteResponse) {
}

// ImportState accepts any id: there is one pipeline policy per organisation.
func (policy *pipelinePolicyResource) ImportState(ctx context.Context, _ resource.ImportStateRequest, response *resource.ImportStateResponse) {
	response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("id"), pipelinePolicyID)...)
}

func pipelinePolicyApply(model *pipelinePolicyModel, stored *client.PipelinePolicy) {
	model.ID = types.StringValue(pipelinePolicyID)
	model.AllowedNetworkTiers = organisationSetFromStrings(organisationNonNilStrings(stored.AllowedNetworkTiers))
	model.AllowedCredentials = organisationSetFromStrings(organisationNonNilStrings(stored.AllowedCredentials))
	model.AllowedRuntimeClasses = organisationSetFromStrings(organisationNonNilStrings(stored.AllowedRuntimeClasses))
	model.WebhookAllowedHosts = organisationSetFromStrings(organisationNonNilStrings(stored.WebhookAllowedHosts))
	model.ExternalProviders = organisationSetFromStrings(organisationNonNilStrings(stored.ExternalProviders))
	model.AllowedImagePrefixes = organisationSetFromStrings(organisationNonNilStrings(stored.AllowedImagePrefixes))
	model.MaxCPU = types.StringValue(stored.MaxCPU)
	model.MaxMemory = types.StringValue(stored.MaxMemory)
	model.MaxCacheSize = types.StringValue(stored.MaxCacheSize)
	model.MaxGPU = types.Int64Value(stored.MaxGPU)
	model.MaxAgentToolCallsPerRun = types.Int64Value(stored.MaxAgentToolCallsPerRun)
	model.UpdatedBy = types.StringValue(stored.UpdatedBy)
	selectors := make(map[string]attr.Value, len(stored.AllowedNodeSelectors))
	for key, values := range stored.AllowedNodeSelectors {
		selectors[key] = listFromStrings(organisationNonNilStrings(values))
	}
	model.AllowedNodeSelectors = types.MapValueMust(pipelinePolicyNodeSelectorType, selectors)
	tolerations := make([]attr.Value, 0, len(stored.AllowedTolerations))
	for _, toleration := range stored.AllowedTolerations {
		tolerations = append(tolerations, types.ObjectValueMust(pipelinePolicyTolerationType.AttrTypes, map[string]attr.Value{
			"key": types.StringValue(toleration.Key), "operator": types.StringValue(toleration.Operator),
			"value": types.StringValue(toleration.Value), "effect": types.StringValue(toleration.Effect),
		}))
	}
	model.AllowedTolerations = types.SetValueMust(pipelinePolicyTolerationType, tolerations)
}
