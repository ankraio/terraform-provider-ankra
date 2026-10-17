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
	_ datasource.DataSource              = (*aiModelsDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*aiModelsDataSource)(nil)
	_ datasource.DataSource              = (*aiEndpointsDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*aiEndpointsDataSource)(nil)
	_ datasource.DataSource              = (*aiLanesDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*aiLanesDataSource)(nil)
)

type aiModelsDataSource struct {
	configuredDataSourceClient
}

type aiModelItemModel struct {
	ID                  types.String `tfsdk:"id"`
	Key                 types.String `tfsdk:"key"`
	DisplayName         types.String `tfsdk:"display_name"`
	Description         types.String `tfsdk:"description"`
	Provider            types.String `tfsdk:"provider"`
	EndpointID          types.String `tfsdk:"endpoint_id"`
	ModelID             types.String `tfsdk:"model_id"`
	Tier                types.String `tfsdk:"tier"`
	ContextWindowTokens types.Int64  `tfsdk:"context_window_tokens"`
	MaxOutputTokens     types.Int64  `tfsdk:"max_output_tokens"`
	SupportsTools       types.Bool   `tfsdk:"supports_tools"`
	SupportsThinking    types.Bool   `tfsdk:"supports_thinking"`
	SupportsImages      types.Bool   `tfsdk:"supports_images"`
	IsEnabled           types.Bool   `tfsdk:"is_enabled"`
	SortOrder           types.Int64  `tfsdk:"sort_order"`
	IsDefault           types.Bool   `tfsdk:"is_default"`
}

type aiModelsDataSourceModel struct {
	ID     types.String       `tfsdk:"id"`
	Models []aiModelItemModel `tfsdk:"models"`
}

// NewAIModelsDataSource returns the ankra_ai_models data source, the
// counterpart of `ankra ai models list`.
func NewAIModelsDataSource() datasource.DataSource {
	return &aiModelsDataSource{}
}

func (modelsDataSource *aiModelsDataSource) Metadata(_ context.Context, request datasource.MetadataRequest, response *datasource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_ai_models"
}

func aiComputedString(description string) schema.StringAttribute {
	return schema.StringAttribute{MarkdownDescription: description, Computed: true}
}

func aiComputedBool(description string) schema.BoolAttribute {
	return schema.BoolAttribute{MarkdownDescription: description, Computed: true}
}

func aiComputedInt64(description string) schema.Int64Attribute {
	return schema.Int64Attribute{MarkdownDescription: description, Computed: true}
}

func (modelsDataSource *aiModelsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, response *datasource.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: "Lists the organisation's AI model catalogue, built-in entries included. The counterpart of `ankra ai models list`.",
		Attributes: map[string]schema.Attribute{
			"id": aiComputedString("Placeholder identifier for the data source."),
			"models": schema.ListNestedAttribute{
				MarkdownDescription: "Catalogue entries in picker order.",
				Computed:            true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id":                    aiComputedString("Row identifier; null for a built-in entry the organisation never changed."),
						"key":                   aiComputedString("Catalogue key."),
						"display_name":          aiComputedString("Name shown in the picker."),
						"description":           aiComputedString("Short description."),
						"provider":              aiComputedString("`ankra` or `openai_compatible`."),
						"endpoint_id":           aiComputedString("Endpoint the model is served from, if any."),
						"model_id":              aiComputedString("Provider model id."),
						"tier":                  aiComputedString("Auto-routing tier, if any."),
						"context_window_tokens": aiComputedInt64("Context window in tokens."),
						"max_output_tokens":     aiComputedInt64("Maximum output tokens."),
						"supports_tools":        aiComputedBool("Whether the model supports tool calling."),
						"supports_thinking":     aiComputedBool("Whether the model has a thinking channel."),
						"supports_images":       aiComputedBool("Whether the model accepts images."),
						"is_enabled":            aiComputedBool("Whether the model is selectable."),
						"sort_order":            aiComputedInt64("Position in the picker."),
						"is_default":            aiComputedBool("Whether the key is a built-in tier key."),
					},
				},
			},
		},
	}
}

func (modelsDataSource *aiModelsDataSource) Read(ctx context.Context, _ datasource.ReadRequest, response *datasource.ReadResponse) {
	apiClient := modelsDataSource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	models, err := apiClient.ListAICatalogModels(ctx)
	if err != nil {
		response.Diagnostics.AddError("Unable to list AI models", err.Error())
		return
	}
	state := aiModelsDataSourceModel{ID: types.StringValue("ankra_ai_models"), Models: make([]aiModelItemModel, 0, len(models))}
	for index := range models {
		var resourceModel aiModelModel
		resourceModel.applyAPI(&models[index])
		state.Models = append(state.Models, aiModelItemModel{
			ID:                  resourceModel.ID,
			Key:                 resourceModel.Key,
			DisplayName:         resourceModel.DisplayName,
			Description:         resourceModel.Description,
			Provider:            resourceModel.Provider,
			EndpointID:          resourceModel.EndpointID,
			ModelID:             resourceModel.ModelID,
			Tier:                resourceModel.Tier,
			ContextWindowTokens: resourceModel.ContextWindowTokens,
			MaxOutputTokens:     resourceModel.MaxOutputTokens,
			SupportsTools:       resourceModel.SupportsTools,
			SupportsThinking:    resourceModel.SupportsThinking,
			SupportsImages:      resourceModel.SupportsImages,
			IsEnabled:           resourceModel.IsEnabled,
			SortOrder:           resourceModel.SortOrder,
			IsDefault:           resourceModel.IsDefault,
		})
	}
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
}

type aiEndpointsDataSource struct {
	configuredDataSourceClient
}

type aiEndpointItemModel struct {
	ID         types.String `tfsdk:"id"`
	Name       types.String `tfsdk:"name"`
	Provider   types.String `tfsdk:"provider"`
	BaseURL    types.String `tfsdk:"base_url"`
	KeyPreview types.String `tfsdk:"key_preview"`
}

type aiEndpointsDataSourceModel struct {
	ID        types.String          `tfsdk:"id"`
	Endpoints []aiEndpointItemModel `tfsdk:"endpoints"`
}

// NewAIEndpointsDataSource returns the ankra_ai_endpoints data source, the
// counterpart of `ankra ai endpoints list`.
func NewAIEndpointsDataSource() datasource.DataSource {
	return &aiEndpointsDataSource{}
}

func (endpointsDataSource *aiEndpointsDataSource) Metadata(_ context.Context, request datasource.MetadataRequest, response *datasource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_ai_endpoints"
}

func (endpointsDataSource *aiEndpointsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, response *datasource.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: "Lists the organisation's OpenAI-compatible endpoints. The counterpart of `ankra ai endpoints list`; needs the `ai.manage` permission.",
		Attributes: map[string]schema.Attribute{
			"id": aiComputedString("Placeholder identifier for the data source."),
			"endpoints": schema.ListNestedAttribute{
				MarkdownDescription: "Configured endpoints.",
				Computed:            true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id":          aiComputedString("Identifier of the endpoint."),
						"name":        aiComputedString("Endpoint name."),
						"provider":    aiComputedString("Provider kind."),
						"base_url":    aiComputedString("Base URL."),
						"key_preview": aiComputedString("Masked preview of the stored key."),
					},
				},
			},
		},
	}
}

func (endpointsDataSource *aiEndpointsDataSource) Read(ctx context.Context, _ datasource.ReadRequest, response *datasource.ReadResponse) {
	apiClient := endpointsDataSource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	endpoints, err := apiClient.ListAIEndpoints(ctx)
	if err != nil {
		response.Diagnostics.AddError("Unable to list AI endpoints", err.Error())
		return
	}
	state := aiEndpointsDataSourceModel{ID: types.StringValue("ankra_ai_endpoints"), Endpoints: make([]aiEndpointItemModel, 0, len(endpoints))}
	for _, endpoint := range endpoints {
		state.Endpoints = append(state.Endpoints, aiEndpointItemModel{
			ID:         types.StringValue(endpoint.ID),
			Name:       types.StringValue(endpoint.Name),
			Provider:   types.StringValue(endpoint.Provider),
			BaseURL:    types.StringValue(endpoint.BaseURL),
			KeyPreview: types.StringValue(endpoint.KeyPreview),
		})
	}
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
}

type aiLanesDataSource struct {
	configuredDataSourceClient
}

type aiLaneItemModel struct {
	Lane               types.String `tfsdk:"lane"`
	DisplayName        types.String `tfsdk:"display_name"`
	Description        types.String `tfsdk:"description"`
	DefaultTier        types.String `tfsdk:"default_tier"`
	SelectedModelKey   types.String `tfsdk:"selected_model_key"`
	EffectiveModelID   types.String `tfsdk:"effective_model_id"`
	EffectiveModelName types.String `tfsdk:"effective_model_name"`
	IsSelectionStale   types.Bool   `tfsdk:"is_selection_stale"`
}

type aiLanesDataSourceModel struct {
	ID    types.String      `tfsdk:"id"`
	Lanes []aiLaneItemModel `tfsdk:"lanes"`
}

// NewAILanesDataSource returns the ankra_ai_lanes data source, the
// counterpart of `ankra ai lanes list`.
func NewAILanesDataSource() datasource.DataSource {
	return &aiLanesDataSource{}
}

func (lanesDataSource *aiLanesDataSource) Metadata(_ context.Context, request datasource.MetadataRequest, response *datasource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_ai_lanes"
}

func (lanesDataSource *aiLanesDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, response *datasource.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: "Lists every AI lane with the model it runs on. The counterpart of `ankra ai lanes list`.",
		Attributes: map[string]schema.Attribute{
			"id": aiComputedString("Placeholder identifier for the data source."),
			"lanes": schema.ListNestedAttribute{
				MarkdownDescription: "AI lanes.",
				Computed:            true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"lane":                 aiComputedString("Lane name, the `lane` of `ankra_ai_lane_model`."),
						"display_name":         aiComputedString("Display name."),
						"description":          aiComputedString("What the lane does."),
						"default_tier":         aiComputedString("Tier the lane runs on without a selection."),
						"selected_model_key":   aiComputedString("The organisation's selection; null when the lane follows its default."),
						"effective_model_id":   aiComputedString("Model id the lane runs on now."),
						"effective_model_name": aiComputedString("Display name of that model."),
						"is_selection_stale":   aiComputedBool("Whether the selection no longer resolves."),
					},
				},
			},
		},
	}
}

func (lanesDataSource *aiLanesDataSource) Read(ctx context.Context, _ datasource.ReadRequest, response *datasource.ReadResponse) {
	apiClient := lanesDataSource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	lanes, err := apiClient.ListAILaneModels(ctx)
	if err != nil {
		response.Diagnostics.AddError("Unable to list AI lanes", err.Error())
		return
	}
	state := aiLanesDataSourceModel{ID: types.StringValue("ankra_ai_lanes"), Lanes: make([]aiLaneItemModel, 0, len(lanes))}
	for _, lane := range lanes {
		state.Lanes = append(state.Lanes, aiLaneItemModel{
			Lane:               types.StringValue(lane.Lane),
			DisplayName:        types.StringValue(lane.DisplayName),
			Description:        types.StringValue(lane.Description),
			DefaultTier:        types.StringValue(lane.DefaultTier),
			SelectedModelKey:   optionalString(lane.SelectedModelKey),
			EffectiveModelID:   types.StringValue(lane.EffectiveModelID),
			EffectiveModelName: types.StringValue(lane.EffectiveModelName),
			IsSelectionStale:   types.BoolValue(lane.IsSelectionStale),
		})
	}
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
}
