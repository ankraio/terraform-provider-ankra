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

	"ankra.io/terraform-provider-ankra/internal/client"
)

var (
	_ resource.Resource                = (*aiKeyResource)(nil)
	_ resource.ResourceWithConfigure   = (*aiKeyResource)(nil)
	_ resource.ResourceWithImportState = (*aiKeyResource)(nil)
)

// aiKeyKind is one bring-your-own provider key the organisation can store:
// the platform keeps one per kind and only ever returns a masked preview.
type aiKeyKind struct {
	typeSuffix  string
	displayName string
	keyPrefix   string
	command     string
	// maskPrefix is how many leading characters the platform keeps in the
	// key preview.
	maskPrefix int
	save       func(*client.Client, context.Context, string) (*client.AIKeyStatus, error)
	remove     func(*client.Client, context.Context) error
	status     func(*client.AIProviderStatus) client.AIKeyStatus
}

var (
	aiAnthropicKeyKind = aiKeyKind{
		typeSuffix:  "_ai_anthropic_key",
		displayName: "Anthropic",
		keyPrefix:   "sk-ant-",
		maskPrefix:  7,
		command:     "`ankra ai anthropic set` and `ankra ai anthropic delete`",
		save:        (*client.Client).SaveAIAnthropicKey,
		remove:      (*client.Client).DeleteAIAnthropicKey,
		status:      func(status *client.AIProviderStatus) client.AIKeyStatus { return status.Anthropic },
	}
	aiOpenRouterKeyKind = aiKeyKind{
		typeSuffix:  "_ai_openrouter_key",
		displayName: "OpenRouter",
		keyPrefix:   "sk-or-",
		maskPrefix:  6,
		command:     "`ankra ai openrouter set` and `ankra ai openrouter remove`",
		save:        (*client.Client).SaveAIOpenRouterKey,
		remove:      (*client.Client).DeleteAIOpenRouterKey,
		status:      func(status *client.AIProviderStatus) client.AIKeyStatus { return status.OpenRouter },
	}
)

type aiKeyResource struct {
	configuredClient
	kind aiKeyKind
}

type aiKeyModel struct {
	ID         types.String `tfsdk:"id"`
	APIKey     types.String `tfsdk:"api_key"`
	KeyPreview types.String `tfsdk:"key_preview"`
}

// NewAIAnthropicKeyResource returns the ankra_ai_anthropic_key resource.
func NewAIAnthropicKeyResource() resource.Resource {
	return &aiKeyResource{kind: aiAnthropicKeyKind}
}

// NewAIOpenRouterKeyResource returns the ankra_ai_openrouter_key resource.
func NewAIOpenRouterKeyResource() resource.Resource {
	return &aiKeyResource{kind: aiOpenRouterKeyKind}
}

func (keyResource *aiKeyResource) Metadata(_ context.Context, request resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + keyResource.kind.typeSuffix
}

func (keyResource *aiKeyResource) Schema(_ context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	kind := keyResource.kind
	response.Schema = schema.Schema{
		MarkdownDescription: "The organisation's own " + kind.displayName + " API key (bring your own key), a singleton: declare it once per organisation. " +
			"The counterpart of " + kind.command + ". Storing the key does not activate it; set `ankra_ai_provider` for that. " +
			"Destroying it removes the key, and when " + kind.displayName + " was the active provider the platform falls back to `ankra`.\n\n" +
			"The platform never returns the key, so a key replaced outside Terraform is detected by comparing the platform's masked `key_preview` with the preview of the configured key, and planned as an update that stores the configured key again; a replacement key with the same leading characters and last four characters is not detected.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Always `" + kind.typeSuffix[1:] + "`.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"api_key": schema.StringAttribute{
				MarkdownDescription: "The " + kind.displayName + " API key; it must start with `" + kind.keyPrefix + "`. The platform validates it against " + kind.displayName + " before storing it.",
				Required:            true,
				Sensitive:           true,
				Validators:          []validator.String{aiStringPrefix{prefixes: []string{kind.keyPrefix}, subject: kind.displayName + " API key"}},
			},
			"key_preview": schema.StringAttribute{
				MarkdownDescription: "Masked preview of the stored key.",
				Computed:            true,
			},
		},
	}
}

func (keyResource *aiKeyResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	var plan aiKeyModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}
	keyResource.save(ctx, &plan, &response.State, &response.Diagnostics)
}

func (keyResource *aiKeyResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	var plan aiKeyModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}
	keyResource.save(ctx, &plan, &response.State, &response.Diagnostics)
}

func (keyResource *aiKeyResource) save(ctx context.Context, plan *aiKeyModel, state *tfsdk.State, diagnostics *diag.Diagnostics) {
	apiClient := keyResource.requireClient(diagnostics)
	if apiClient == nil {
		return
	}
	status, err := keyResource.kind.save(apiClient, ctx, plan.APIKey.ValueString())
	if err != nil {
		diagnostics.AddError("Unable to store "+keyResource.kind.displayName+" API key", err.Error())
		return
	}
	plan.ID = types.StringValue(keyResource.kind.typeSuffix[1:])
	plan.KeyPreview = types.StringPointerValue(status.KeyPreview)
	diagnostics.Append(state.Set(ctx, plan)...)
}

func (keyResource *aiKeyResource) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	apiClient := keyResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state aiKeyModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	providerStatus, err := apiClient.GetAIProviderStatus(ctx)
	if err != nil {
		response.Diagnostics.AddError("Unable to read "+keyResource.kind.displayName+" API key status", err.Error())
		return
	}
	keyStatus := keyResource.kind.status(providerStatus)
	if !keyStatus.Configured {
		response.State.RemoveResource(ctx)
		return
	}
	state.ID = types.StringValue(keyResource.kind.typeSuffix[1:])
	if aiKeyReplacedOutside(state.APIKey, keyStatus.KeyPreview, keyResource.kind.maskPrefix, 12) {
		state.APIKey = types.StringNull()
	}
	state.KeyPreview = types.StringPointerValue(keyStatus.KeyPreview)
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
}

func (keyResource *aiKeyResource) Delete(ctx context.Context, _ resource.DeleteRequest, response *resource.DeleteResponse) {
	apiClient := keyResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	if err := keyResource.kind.remove(apiClient, ctx); err != nil {
		response.Diagnostics.AddError("Unable to remove "+keyResource.kind.displayName+" API key", err.Error())
	}
}

// ImportState imports the stored key. The key itself cannot be read back, so
// the first plan after an import shows the configured api_key as a change.
func (keyResource *aiKeyResource) ImportState(ctx context.Context, _ resource.ImportStateRequest, response *resource.ImportStateResponse) {
	response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("id"), keyResource.kind.typeSuffix[1:])...)
}
