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
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"ankra.io/terraform-provider-ankra/internal/client"
)

const aiOpenAICompatibleID = "ai_openai_compatible"

var (
	_ resource.Resource                = (*aiOpenAICompatibleResource)(nil)
	_ resource.ResourceWithConfigure   = (*aiOpenAICompatibleResource)(nil)
	_ resource.ResourceWithImportState = (*aiOpenAICompatibleResource)(nil)
)

type aiOpenAICompatibleResource struct {
	configuredClient
}

type aiOpenAICompatibleModel struct {
	ID         types.String `tfsdk:"id"`
	BaseURL    types.String `tfsdk:"base_url"`
	Model      types.String `tfsdk:"model"`
	APIKey     types.String `tfsdk:"api_key"`
	KeyPreview types.String `tfsdk:"key_preview"`
}

// NewAIOpenAICompatibleResource returns the ankra_ai_openai_compatible
// resource, the counterpart of `ankra ai openai set|delete`.
func NewAIOpenAICompatibleResource() resource.Resource {
	return &aiOpenAICompatibleResource{}
}

func (compatibleResource *aiOpenAICompatibleResource) Metadata(_ context.Context, request resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_ai_openai_compatible"
}

func (compatibleResource *aiOpenAICompatibleResource) Schema(_ context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: "The organisation's legacy single OpenAI-compatible endpoint, which the `openai_compatible` provider uses; a singleton. " +
			"The counterpart of `ankra ai openai set` and `ankra ai openai delete`. For several endpoints and per-model routing use " +
			"`ankra_ai_endpoint` with `ankra_ai_model` instead.\n\n" +
			"The platform never returns the key, so a key replaced outside Terraform is detected by comparing the platform's masked `key_preview` with the preview of the configured key, and planned as an update that stores the configured key again; a replacement key with the same leading characters and last four characters is not detected.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Always `" + aiOpenAICompatibleID + "`.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"base_url": schema.StringAttribute{
				MarkdownDescription: "OpenAI-compatible base URL, for example `https://openrouter.ai/api/v1`. A trailing slash is ignored.",
				Required:            true,
				Validators:          []validator.String{aiBaseURLPrefix},
			},
			"model": schema.StringAttribute{
				MarkdownDescription: "Model id the endpoint serves.",
				Required:            true,
				Validators:          []validator.String{platformServicesTrimmed{}},
			},
			"api_key": schema.StringAttribute{
				MarkdownDescription: "API key for the endpoint.",
				Required:            true,
				Sensitive:           true,
			},
			"key_preview": schema.StringAttribute{
				MarkdownDescription: "Masked preview of the stored key.",
				Computed:            true,
			},
		},
	}
}

func (compatibleResource *aiOpenAICompatibleResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	var plan aiOpenAICompatibleModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}
	compatibleResource.save(ctx, &plan, &response.State, &response.Diagnostics)
}

func (compatibleResource *aiOpenAICompatibleResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	var plan aiOpenAICompatibleModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}
	compatibleResource.save(ctx, &plan, &response.State, &response.Diagnostics)
}

func (compatibleResource *aiOpenAICompatibleResource) save(ctx context.Context, plan *aiOpenAICompatibleModel, state *tfsdk.State, diagnostics *diag.Diagnostics) {
	apiClient := compatibleResource.requireClient(diagnostics)
	if apiClient == nil {
		return
	}
	status, err := apiClient.SaveAIOpenAICompatible(ctx, client.AIOpenAICompatibleConfiguration{
		BaseURL: plan.BaseURL.ValueString(),
		APIKey:  plan.APIKey.ValueString(),
		Model:   plan.Model.ValueString(),
	})
	if err != nil {
		diagnostics.AddError("Unable to store OpenAI-compatible endpoint", err.Error())
		return
	}
	plan.ID = types.StringValue(aiOpenAICompatibleID)
	plan.KeyPreview = types.StringPointerValue(status.KeyPreview)
	diagnostics.Append(state.Set(ctx, plan)...)
}

func (compatibleResource *aiOpenAICompatibleResource) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	apiClient := compatibleResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state aiOpenAICompatibleModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	providerStatus, err := apiClient.GetAIProviderStatus(ctx)
	if err != nil {
		response.Diagnostics.AddError("Unable to read OpenAI-compatible endpoint", err.Error())
		return
	}
	compatible := providerStatus.OpenAICompatible
	if !compatible.Configured {
		response.State.RemoveResource(ctx)
		return
	}
	state.ID = types.StringValue(aiOpenAICompatibleID)
	if aiKeyReplacedOutside(state.APIKey, compatible.KeyPreview, 3, 8) {
		state.APIKey = types.StringNull()
	}
	state.BaseURL = aiKeepEquivalentURL(state.BaseURL, compatible.BaseURL)
	state.Model = types.StringPointerValue(compatible.Model)
	state.KeyPreview = types.StringPointerValue(compatible.KeyPreview)
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
	response.Diagnostics.Append(trackScope(ctx, apiClient, response.Private)...)
}

func (compatibleResource *aiOpenAICompatibleResource) Delete(ctx context.Context, _ resource.DeleteRequest, response *resource.DeleteResponse) {
	apiClient := compatibleResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	if err := apiClient.DeleteAIOpenAICompatible(ctx); err != nil {
		response.Diagnostics.AddError("Unable to remove OpenAI-compatible endpoint", err.Error())
	}
}

// ImportState imports the stored configuration. The key cannot be read back,
// so the first plan after an import shows the configured api_key as a change.
func (compatibleResource *aiOpenAICompatibleResource) ImportState(ctx context.Context, _ resource.ImportStateRequest, response *resource.ImportStateResponse) {
	response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("id"), aiOpenAICompatibleID)...)
}

// aiKeepEquivalentURL maps a base URL the platform stored (trimmed, without
// a trailing slash) to state, keeping the configured spelling when the two
// differ only by that slash, so a trailing slash in configuration does not
// plan a change on every run.
func aiKeepEquivalentURL(current types.String, stored *string) types.String {
	if stored == nil {
		return types.StringNull()
	}
	if !current.IsNull() && !current.IsUnknown() &&
		strings.TrimRight(strings.TrimSpace(current.ValueString()), "/") == *stored {
		return current
	}
	return types.StringValue(*stored)
}
