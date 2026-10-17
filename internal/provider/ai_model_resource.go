// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64default"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"ankra.io/terraform-provider-ankra/internal/client"
)

var (
	_ resource.Resource                = (*aiModelResource)(nil)
	_ resource.ResourceWithConfigure   = (*aiModelResource)(nil)
	_ resource.ResourceWithImportState = (*aiModelResource)(nil)
)

type aiModelResource struct {
	configuredClient
}

type aiModelModel struct {
	ID                  types.String `tfsdk:"id"`
	Key                 types.String `tfsdk:"key"`
	DisplayName         types.String `tfsdk:"display_name"`
	ModelID             types.String `tfsdk:"model_id"`
	Description         types.String `tfsdk:"description"`
	EndpointID          types.String `tfsdk:"endpoint_id"`
	Tier                types.String `tfsdk:"tier"`
	ContextWindowTokens types.Int64  `tfsdk:"context_window_tokens"`
	MaxOutputTokens     types.Int64  `tfsdk:"max_output_tokens"`
	SupportsTools       types.Bool   `tfsdk:"supports_tools"`
	SupportsThinking    types.Bool   `tfsdk:"supports_thinking"`
	SupportsImages      types.Bool   `tfsdk:"supports_images"`
	IsEnabled           types.Bool   `tfsdk:"is_enabled"`
	SortOrder           types.Int64  `tfsdk:"sort_order"`
	Provider            types.String `tfsdk:"provider_kind"`
	IsDefault           types.Bool   `tfsdk:"is_default"`
}

// NewAIModelResource returns the ankra_ai_model resource, the counterpart of
// `ankra ai models create|update|delete`.
func NewAIModelResource() resource.Resource {
	return &aiModelResource{}
}

func (modelResource *aiModelResource) Metadata(_ context.Context, request resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_ai_model"
}

func (modelResource *aiModelResource) Schema(_ context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: "An entry in the organisation's AI model catalogue: a model the chat picker offers and auto-routing can target. " +
			"The counterpart of `ankra ai models create`, `update` and `delete`.\n\n" +
			"An organisation that never changed its catalogue is served the built-in `expert`, `think` and `quick` entries. " +
			"The first entry Terraform creates stores those built-ins as ordinary entries next to it, so creating one with a built-in key fails; " +
			"import a built-in by its key (`terraform import ankra_ai_model.expert expert`) to manage it instead.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Row identifier of the entry; null for a built-in entry the organisation has not changed yet.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{aiKeepKnownIdentifier{}},
			},
			"key": schema.StringAttribute{
				MarkdownDescription: "Catalogue key, the value a chat's model mode selects: lower-case letters, digits, `-` and `_`. `auto` is reserved.",
				Required:            true,
				Validators:          []validator.String{aiCatalogueKeyValidator{}},
			},
			"display_name": schema.StringAttribute{
				MarkdownDescription: "Name shown in the model picker.",
				Required:            true,
			},
			"model_id": schema.StringAttribute{
				MarkdownDescription: "Provider model id, for example `claude-opus-5` or `gpt-4o`.",
				Required:            true,
			},
			"description": schema.StringAttribute{
				MarkdownDescription: "Short description.",
				Optional:            true,
			},
			"endpoint_id": schema.StringAttribute{
				MarkdownDescription: "`ankra_ai_endpoint` the model is served from. Unset routes the model through the Ankra-managed Claude route.",
				Optional:            true,
			},
			"tier": schema.StringAttribute{
				MarkdownDescription: "Auto-routing tier: `expert`, `think` or `quick`. Unset makes the model reachable only by explicit selection.",
				Optional:            true,
				Validators:          []validator.String{aiStringOneOf{allowed: []string{"expert", "think", "quick"}}},
			},
			"context_window_tokens": schema.Int64Attribute{
				MarkdownDescription: "Context window in tokens, 8192 to 2000000. The platform defaults it to 200000.",
				Optional:            true,
				Computed:            true,
				PlanModifiers:       []planmodifier.Int64{int64planmodifier.UseStateForUnknown()},
			},
			"max_output_tokens": schema.Int64Attribute{
				MarkdownDescription: "Maximum output tokens, 256 to 200000 and below the context window. The platform defaults it to 8192.",
				Optional:            true,
				Computed:            true,
				PlanModifiers:       []planmodifier.Int64{int64planmodifier.UseStateForUnknown()},
			},
			"supports_tools": schema.BoolAttribute{
				MarkdownDescription: "Whether the model supports tool calling. Defaults to `true`.",
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(true),
			},
			"supports_thinking": schema.BoolAttribute{
				MarkdownDescription: "Whether the model has a thinking or reasoning channel. Defaults to `false`.",
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(false),
			},
			"supports_images": schema.BoolAttribute{
				MarkdownDescription: "Whether the model accepts image inputs. Defaults to `false`.",
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(false),
			},
			"is_enabled": schema.BoolAttribute{
				MarkdownDescription: "Whether the model is selectable. Defaults to `true`.",
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(true),
			},
			"sort_order": schema.Int64Attribute{
				MarkdownDescription: "Position in the picker, lowest first. Defaults to `0`.",
				Optional:            true,
				Computed:            true,
				Default:             int64default.StaticInt64(0),
			},
			"provider_kind": schema.StringAttribute{
				MarkdownDescription: "`ankra` for the Ankra-managed route, `openai_compatible` for a model on an endpoint.",
				Computed:            true,
			},
			"is_default": schema.BoolAttribute{
				MarkdownDescription: "Whether the key is one of the built-in tier keys.",
				Computed:            true,
			},
		},
	}
}

func (model aiModelModel) write() client.AICatalogModelWrite {
	write := client.AICatalogModelWrite{
		Key:              model.Key.ValueString(),
		DisplayName:      model.DisplayName.ValueString(),
		ModelID:          model.ModelID.ValueString(),
		Description:      model.Description.ValueString(),
		EndpointID:       stringPointer(model.EndpointID),
		Tier:             model.Tier.ValueString(),
		SupportsTools:    model.SupportsTools.ValueBool(),
		SupportsThinking: model.SupportsThinking.ValueBool(),
		SupportsImages:   model.SupportsImages.ValueBool(),
		IsEnabled:        model.IsEnabled.ValueBool(),
		SortOrder:        model.SortOrder.ValueInt64(),
	}
	if !model.ContextWindowTokens.IsUnknown() {
		write.ContextWindowTokens = model.ContextWindowTokens.ValueInt64()
	}
	if !model.MaxOutputTokens.IsUnknown() {
		write.MaxOutputTokens = model.MaxOutputTokens.ValueInt64()
	}
	return write
}

// reference is the path segment the platform addresses the entry by: its row
// id, or its key while it is a built-in the organisation never changed.
func (model aiModelModel) reference() string {
	if !model.ID.IsNull() && !model.ID.IsUnknown() {
		return model.ID.ValueString()
	}
	return model.Key.ValueString()
}

func (model *aiModelModel) applyAPI(entry *client.AICatalogModel) {
	model.ID = types.StringPointerValue(entry.ID)
	model.Key = types.StringValue(entry.Key)
	model.DisplayName = types.StringValue(entry.DisplayName)
	model.ModelID = types.StringValue(entry.ModelID)
	model.Description = optionalString(entry.Description)
	model.EndpointID = types.StringPointerValue(entry.EndpointID)
	model.Tier = types.StringNull()
	if entry.Tier != nil && *entry.Tier != "" {
		model.Tier = types.StringValue(*entry.Tier)
	}
	model.ContextWindowTokens = types.Int64Value(entry.ContextWindowTokens)
	model.MaxOutputTokens = types.Int64Value(entry.MaxOutputTokens)
	model.SupportsTools = types.BoolValue(entry.SupportsTools)
	model.SupportsThinking = types.BoolValue(entry.SupportsThinking)
	model.SupportsImages = types.BoolValue(entry.SupportsImages)
	model.IsEnabled = types.BoolValue(entry.IsEnabled)
	model.SortOrder = types.Int64Value(entry.SortOrder)
	model.Provider = types.StringValue(entry.Provider)
	model.IsDefault = types.BoolValue(entry.IsDefault)
}

func (modelResource *aiModelResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	apiClient := modelResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var plan aiModelModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}
	created, err := apiClient.CreateAICatalogModel(ctx, plan.write())
	if err != nil {
		response.Diagnostics.AddError("Unable to create AI model", err.Error())
		return
	}
	plan.applyAPI(created)
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
}

func (modelResource *aiModelResource) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	apiClient := modelResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state aiModelModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	entry, err := apiClient.GetAICatalogModel(ctx, state.reference())
	if removeWhenNotFound(ctx, err, response) {
		return
	}
	if err != nil {
		response.Diagnostics.AddError("Unable to read AI model", err.Error())
		return
	}
	if entry.ID != nil && readTargetChanged(state.ID, *entry.ID) {
		response.State.RemoveResource(ctx)
		return
	}
	state.applyAPI(entry)
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
}

func (modelResource *aiModelResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	apiClient := modelResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var plan, state aiModelModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	updated, err := apiClient.UpdateAICatalogModel(ctx, state.reference(), plan.write())
	if err != nil {
		response.Diagnostics.AddError("Unable to update AI model", err.Error())
		return
	}
	plan.applyAPI(updated)
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
}

func (modelResource *aiModelResource) Delete(ctx context.Context, request resource.DeleteRequest, response *resource.DeleteResponse) {
	apiClient := modelResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state aiModelModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	if err := apiClient.DeleteAICatalogModel(ctx, state.reference()); err != nil {
		response.Diagnostics.AddError("Unable to delete AI model", err.Error())
	}
}

// ImportState imports by row id or by catalogue key; a built-in entry the
// organisation never changed has only its key.
func (modelResource *aiModelResource) ImportState(ctx context.Context, request resource.ImportStateRequest, response *resource.ImportStateResponse) {
	if aiLooksLikeUUID(request.ID) {
		resource.ImportStatePassthroughID(ctx, path.Root("id"), request, response)
		return
	}
	response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("key"), request.ID)...)
}

// aiLooksLikeUUID reports whether value has the 8-4-4-4-12 hexadecimal shape
// of a row id. A catalogue key cannot: keys are lower-case slugs, and while a
// slug may contain hex digits and hyphens it would have to match this exact
// shape to be mistaken for one.
func aiLooksLikeUUID(value string) bool {
	if len(value) != 36 {
		return false
	}
	for index, character := range value {
		switch index {
		case 8, 13, 18, 23:
			if character != '-' {
				return false
			}
		default:
			isHexadecimal := (character >= '0' && character <= '9') || (character >= 'a' && character <= 'f') || (character >= 'A' && character <= 'F')
			if !isHexadecimal {
				return false
			}
		}
	}
	return true
}
