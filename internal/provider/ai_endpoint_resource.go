// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"ankra.io/terraform-provider-ankra/internal/client"
)

var (
	_ resource.Resource                = (*aiEndpointResource)(nil)
	_ resource.ResourceWithConfigure   = (*aiEndpointResource)(nil)
	_ resource.ResourceWithImportState = (*aiEndpointResource)(nil)
)

type aiEndpointResource struct {
	configuredClient
}

type aiEndpointModel struct {
	ID         types.String `tfsdk:"id"`
	Name       types.String `tfsdk:"name"`
	BaseURL    types.String `tfsdk:"base_url"`
	APIKey     types.String `tfsdk:"api_key"`
	Provider   types.String `tfsdk:"provider_kind"`
	KeyPreview types.String `tfsdk:"key_preview"`
}

// NewAIEndpointResource returns the ankra_ai_endpoint resource, the
// counterpart of `ankra ai endpoints create|update|delete`.
func NewAIEndpointResource() resource.Resource {
	return &aiEndpointResource{}
}

func (endpointResource *aiEndpointResource) Metadata(_ context.Context, request resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_ai_endpoint"
}

func (endpointResource *aiEndpointResource) Schema(_ context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: "A named OpenAI-compatible endpoint (OpenRouter, vLLM, LiteLLM, Ollama, ...) that catalogue models can be served from. " +
			"The counterpart of `ankra ai endpoints create`, `update` and `delete`. The platform probes the endpoint before saving it.\n\n" +
			"Destroying an endpoint also deletes every catalogue model on it. The platform never returns the key, " +
			"so a key replaced outside Terraform is detected by comparing the platform's masked `key_preview` with the preview of the configured key, and planned as an update that stores the configured key again; a replacement key with the same leading characters and last four characters is not detected.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Identifier of the endpoint.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				MarkdownDescription: "Endpoint name, unique in the organisation.",
				Required:            true,
				Validators:          []validator.String{platformServicesTrimmed{}},
			},
			"base_url": schema.StringAttribute{
				MarkdownDescription: "OpenAI-compatible base URL. A trailing slash is ignored.",
				Required:            true,
				Validators:          []validator.String{aiBaseURLPrefix},
			},
			"api_key": schema.StringAttribute{
				MarkdownDescription: "API key for the endpoint. Changing it rotates the stored key.",
				Required:            true,
				Sensitive:           true,
			},
			"provider_kind": schema.StringAttribute{
				MarkdownDescription: "Provider kind the platform records for the endpoint.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"key_preview": schema.StringAttribute{
				MarkdownDescription: "Masked preview of the stored key.",
				Computed:            true,
			},
		},
	}
}

func (endpointResource *aiEndpointResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	apiClient := endpointResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var plan aiEndpointModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}
	created, err := apiClient.CreateAIEndpoint(ctx, client.AIEndpointWrite{
		Name:    plan.Name.ValueString(),
		BaseURL: plan.BaseURL.ValueString(),
		APIKey:  stringPointer(plan.APIKey),
	})
	if err != nil {
		response.Diagnostics.AddError("Unable to create AI endpoint", err.Error())
		return
	}
	plan.applyAPI(created)
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
}

func (endpointResource *aiEndpointResource) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	apiClient := endpointResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state aiEndpointModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	endpoint, err := apiClient.GetAIEndpoint(ctx, state.ID.ValueString())
	if removeWhenNotFound(ctx, err, response) {
		return
	}
	if err != nil {
		response.Diagnostics.AddError("Unable to read AI endpoint", err.Error())
		return
	}
	state.Name = types.StringValue(endpoint.Name)
	if aiKeyReplacedOutside(state.APIKey, &endpoint.KeyPreview, 3, 8) {
		state.APIKey = types.StringNull()
	}
	state.applyAPI(endpoint)
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
}

func (endpointResource *aiEndpointResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	apiClient := endpointResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var plan, state aiEndpointModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	write := client.AIEndpointWrite{
		Name:    plan.Name.ValueString(),
		BaseURL: plan.BaseURL.ValueString(),
	}
	if !plan.APIKey.Equal(state.APIKey) {
		write.APIKey = stringPointer(plan.APIKey)
	}
	updated, err := apiClient.UpdateAIEndpoint(ctx, state.ID.ValueString(), write)
	if err != nil {
		response.Diagnostics.AddError("Unable to update AI endpoint", err.Error())
		return
	}
	plan.ID = state.ID
	plan.applyAPI(updated)
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
}

func (endpointResource *aiEndpointResource) Delete(ctx context.Context, request resource.DeleteRequest, response *resource.DeleteResponse) {
	apiClient := endpointResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state aiEndpointModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	if err := apiClient.DeleteAIEndpoint(ctx, state.ID.ValueString()); err != nil {
		response.Diagnostics.AddError("Unable to delete AI endpoint", err.Error())
	}
}

// ImportState imports by endpoint id. The key cannot be read back, so the
// first plan after an import shows the configured api_key as a change, which
// rotates the stored key to the configured one.
func (endpointResource *aiEndpointResource) ImportState(ctx context.Context, request resource.ImportStateRequest, response *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), request, response)
}

// applyAPI copies what the platform answered into the model. The name is
// left to the caller: create and update keep the configured spelling.
func (model *aiEndpointModel) applyAPI(endpoint *client.AIEndpoint) {
	model.ID = types.StringValue(endpoint.ID)
	model.BaseURL = aiKeepEquivalentURL(model.BaseURL, &endpoint.BaseURL)
	model.Provider = types.StringValue(endpoint.Provider)
	model.KeyPreview = types.StringValue(endpoint.KeyPreview)
}
