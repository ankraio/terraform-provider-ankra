// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// aiDefaultProvider is the platform's provider for an organisation that
// chose none: Ankra-managed models.
const aiDefaultProvider = "ankra"

var (
	_ resource.Resource                = (*aiProviderResource)(nil)
	_ resource.ResourceWithConfigure   = (*aiProviderResource)(nil)
	_ resource.ResourceWithImportState = (*aiProviderResource)(nil)
)

type aiProviderResource struct {
	configuredClient
}

type aiProviderModel struct {
	ID       types.String `tfsdk:"id"`
	Provider types.String `tfsdk:"name"`
}

// NewAIProviderResource returns the ankra_ai_provider resource, the
// counterpart of `ankra ai provider`.
func NewAIProviderResource() resource.Resource {
	return &aiProviderResource{}
}

func (providerResource *aiProviderResource) Metadata(_ context.Context, request resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_ai_provider"
}

func (providerResource *aiProviderResource) Schema(_ context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: "The organisation's active AI provider, a singleton: declare it once per organisation. " +
			"The counterpart of `ankra ai provider`. Destroying it returns the organisation to the Ankra-managed provider. " +
			"Creating it while the organisation is already on a different, non-default provider chosen outside Terraform is refused: import it instead.\n\n" +
			"The platform refuses a provider whose credential is not stored, so reference the matching credential resource " +
			"(`ankra_ai_anthropic_key`, `ankra_ai_openrouter_key` or `ankra_ai_openai_compatible`) or add `depends_on`. " +
			"Removing that credential while its provider is active falls back to `ankra`, which the next plan shows as drift.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Always `ai_provider`.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				MarkdownDescription: "Active provider: `ankra`, `anthropic`, `openai_compatible` or `openrouter`.",
				Required:            true,
				Validators:          []validator.String{aiStringOneOf{allowed: []string{"ankra", "anthropic", "openai_compatible", "openrouter"}}},
			},
		},
	}
}

// Create refuses to take over a provider someone chose outside Terraform:
// destroying the resource returns the organisation to `ankra`, which would
// undo that choice. The organisation is adopted only while it is on the
// default or already on the configured provider.
func (providerResource *aiProviderResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	var plan aiProviderModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}
	apiClient := providerResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	status, err := apiClient.GetAIProviderStatus(ctx)
	if err != nil {
		response.Diagnostics.AddError("Unable to read AI provider", err.Error())
		return
	}
	if status.Provider != aiDefaultProvider && status.Provider != plan.Provider.ValueString() {
		response.Diagnostics.AddError("AI provider already chosen",
			"The organisation's AI provider was set to "+status.Provider+" outside Terraform. Creating this resource would overwrite it, and "+
				"destroying it would reset it to ankra, so import it instead: terraform import <address> ai_provider")
		return
	}
	providerResource.write(ctx, &plan, &response.State, &response.Diagnostics)
}

func (providerResource *aiProviderResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	var plan aiProviderModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}
	providerResource.write(ctx, &plan, &response.State, &response.Diagnostics)
}

func (providerResource *aiProviderResource) write(ctx context.Context, plan *aiProviderModel, state *tfsdk.State, diagnostics *diag.Diagnostics) {
	apiClient := providerResource.requireClient(diagnostics)
	if apiClient == nil {
		return
	}
	status, err := apiClient.SetAIProvider(ctx, plan.Provider.ValueString())
	if err != nil {
		diagnostics.AddError("Unable to set AI provider", err.Error())
		return
	}
	plan.ID = types.StringValue("ai_provider")
	plan.Provider = types.StringValue(status.Provider)
	diagnostics.Append(state.Set(ctx, plan)...)
}

func (providerResource *aiProviderResource) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	apiClient := providerResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state aiProviderModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	status, err := apiClient.GetAIProviderStatus(ctx)
	if err != nil {
		response.Diagnostics.AddError("Unable to read AI provider", err.Error())
		return
	}
	state.ID = types.StringValue("ai_provider")
	state.Provider = types.StringValue(status.Provider)
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
}

// Delete returns the organisation to the Ankra-managed provider.
func (providerResource *aiProviderResource) Delete(ctx context.Context, _ resource.DeleteRequest, response *resource.DeleteResponse) {
	apiClient := providerResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	if _, err := apiClient.SetAIProvider(ctx, aiDefaultProvider); err != nil {
		response.Diagnostics.AddError("Unable to reset AI provider", err.Error())
	}
}

// ImportState imports the singleton; any id is accepted and replaced.
func (providerResource *aiProviderResource) ImportState(ctx context.Context, _ resource.ImportStateRequest, response *resource.ImportStateResponse) {
	response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("id"), "ai_provider")...)
}
