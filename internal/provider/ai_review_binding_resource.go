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
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"ankra.io/terraform-provider-ankra/internal/client"
)

var (
	_ resource.Resource                = (*aiReviewBindingResource)(nil)
	_ resource.ResourceWithConfigure   = (*aiReviewBindingResource)(nil)
	_ resource.ResourceWithImportState = (*aiReviewBindingResource)(nil)
)

type aiReviewBindingResource struct {
	configuredClient
}

type aiReviewBindingModel struct {
	ID                 types.String `tfsdk:"id"`
	Provider           types.String `tfsdk:"scm_provider"`
	BindingExternalID  types.String `tfsdk:"binding_external_id"`
	DisplayName        types.String `tfsdk:"display_name"`
	AIReview           types.Bool   `tfsdk:"ai_review"`
	MentionReplies     types.Bool   `tfsdk:"mention_replies"`
	PRPreviews         types.Bool   `tfsdk:"pr_previews"`
	MergeWhenGreen     types.Bool   `tfsdk:"merge_when_green"`
	ReviewModel        types.String `tfsdk:"review_model"`
	ReviewDrafts       types.Bool   `tfsdk:"review_drafts"`
	MaxReviewsPerPR    types.Int64  `tfsdk:"max_reviews_per_pr"`
	CustomInstructions types.String `tfsdk:"custom_instructions"`
}

// NewAIReviewBindingResource returns the ankra_ai_review_binding resource.
func NewAIReviewBindingResource() resource.Resource {
	return &aiReviewBindingResource{}
}

func (binding *aiReviewBindingResource) Metadata(_ context.Context, request resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_ai_review_binding"
}

func aiReviewBindingOptionalBool(description string) schema.BoolAttribute {
	return schema.BoolAttribute{
		MarkdownDescription: description, Optional: true, Computed: true,
		PlanModifiers: []planmodifier.Bool{boolplanmodifier.UseStateForUnknown()},
	}
}

func (binding *aiReviewBindingResource) Schema(_ context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: "The AI Gateway settings of one source-control binding (a GitHub App installation, a GitLab or Bitbucket connection): " +
			"AI review, @mention replies, pull request previews, merge when green and the review defaults its repositories inherit. " +
			"Per-repository rules are `ankra_ai_review_repository`. Changes need organisation admin.\n\n" +
			"The binding itself is created by connecting the source-control provider; this resource only manages its settings. `ai_review` and " +
			"`mention_replies` are always written; every other setting is managed only when set, and an unset one keeps the organisation's value " +
			"and is read back as computed. Destroy stops managing the settings and leaves them as they are.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "`<scm_provider>/<binding_external_id>`.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"scm_provider": schema.StringAttribute{
				MarkdownDescription: "Source-control provider of the binding: `github`, `gitlab` or `bitbucket`.",
				Required:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
				Validators:          []validator.String{organisationStringOneOf{values: []string{"github", "gitlab", "bitbucket"}}},
			},
			"binding_external_id": schema.StringAttribute{
				MarkdownDescription: "The binding's external id: the GitHub App installation id, or the GitLab or Bitbucket connection's id.",
				Required:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"display_name": schema.StringAttribute{
				MarkdownDescription: "Display name of the binding.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"ai_review": schema.BoolAttribute{
				MarkdownDescription: "Run the AI review on pull requests of the binding's repositories.",
				Required:            true,
			},
			"mention_replies": schema.BoolAttribute{
				MarkdownDescription: "Answer @mentions on the binding's repositories.",
				Required:            true,
			},
			"pr_previews":      aiReviewBindingOptionalBool("Build pull request previews."),
			"merge_when_green": aiReviewBindingOptionalBool("Merge a pull request once its checks pass. It takes effect only while the binding is in Agent mode."),
			"review_model": schema.StringAttribute{
				MarkdownDescription: "Model reviews run on: a model catalogue key, or an OpenRouter model id (`vendor/model`). An empty string inherits the platform default.",
				Optional:            true,
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"review_drafts": aiReviewBindingOptionalBool("Review draft pull requests automatically too."),
			"max_reviews_per_pr": schema.Int64Attribute{
				MarkdownDescription: "Cap on automatic reviews per pull request; `0` is no cap.",
				Optional:            true,
				Computed:            true,
				PlanModifiers:       []planmodifier.Int64{int64planmodifier.UseStateForUnknown()},
				Validators:          []validator.Int64{deployInt64Range{minimum: 0, maximum: 1 << 31}},
			},
			"custom_instructions": schema.StringAttribute{
				MarkdownDescription: "Extra instructions for the reviewer, at most 4000 characters. An empty string clears them.",
				Optional:            true,
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
				Validators:          []validator.String{aiReviewInstructionsLength{}},
			},
		},
	}
}

func (binding *aiReviewBindingResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	binding.write(ctx, request.Config, request.Plan, &response.State, response.Private, &response.Diagnostics)
}

func (binding *aiReviewBindingResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	binding.write(ctx, request.Config, request.Plan, &response.State, response.Private, &response.Diagnostics)
}

// write sends the two required settings and every configured optional one,
// then reads the binding back: the platform's answer echoes only what was
// sent.
func (binding *aiReviewBindingResource) write(ctx context.Context, config tfsdk.Config, plan tfsdk.Plan, state *tfsdk.State, private privateStateWriter, diagnostics *diag.Diagnostics) {
	apiClient := binding.requireClient(diagnostics)
	if apiClient == nil {
		return
	}
	var model, configured aiReviewBindingModel
	diagnostics.Append(plan.Get(ctx, &model)...)
	diagnostics.Append(config.Get(ctx, &configured)...)
	if diagnostics.HasError() {
		return
	}
	provider, externalID := model.Provider.ValueString(), model.BindingExternalID.ValueString()
	err := apiClient.UpdateAIReviewBindingSettings(ctx, provider, externalID, client.AIReviewBindingSettingsWrite{
		AIReview:           configured.AIReview.ValueBool(),
		MentionReplies:     configured.MentionReplies.ValueBool(),
		PRPreviews:         boolPointer(configured.PRPreviews),
		MergeWhenGreen:     boolPointer(configured.MergeWhenGreen),
		ReviewModel:        stringPointer(configured.ReviewModel),
		ReviewDrafts:       boolPointer(configured.ReviewDrafts),
		MaxReviewsPerPR:    int64Pointer(configured.MaxReviewsPerPR),
		CustomInstructions: stringPointer(configured.CustomInstructions),
	})
	if err != nil {
		detail := err.Error()
		if client.IsNotFound(err) {
			detail += "\n\nThe organisation has no such binding: connect the source-control provider first."
		}
		diagnostics.AddError("Unable to write AI review binding settings", detail)
		return
	}
	stored, err := apiClient.GetAIReviewBinding(ctx, provider, externalID)
	if err != nil {
		diagnostics.AddError("Unable to read the AI review binding back", err.Error())
		return
	}
	aiReviewBindingApply(&model, stored)
	diagnostics.Append(state.Set(ctx, &model)...)
	diagnostics.Append(trackScope(ctx, apiClient, private)...)
}

func (binding *aiReviewBindingResource) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	apiClient := binding.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state aiReviewBindingModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	stored, err := apiClient.GetAIReviewBinding(ctx, state.Provider.ValueString(), state.BindingExternalID.ValueString())
	if removeWhenNotFound(ctx, err, apiClient, response) {
		return
	}
	if err != nil {
		response.Diagnostics.AddError("Unable to read AI review binding", err.Error())
		return
	}
	aiReviewBindingApply(&state, stored)
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
	response.Diagnostics.Append(trackScope(ctx, apiClient, response.Private)...)
}

// Delete leaves the settings as they are: the binding belongs to the
// source-control connection, and turning its review off on destroy would
// surprise more than it helps.
func (binding *aiReviewBindingResource) Delete(_ context.Context, _ resource.DeleteRequest, _ *resource.DeleteResponse) {
}

// ImportState imports by `<scm_provider>/<binding_external_id>`.
func (binding *aiReviewBindingResource) ImportState(ctx context.Context, request resource.ImportStateRequest, response *resource.ImportStateResponse) {
	provider, externalID, found := strings.Cut(request.ID, "/")
	if !found || provider == "" || externalID == "" {
		response.Diagnostics.AddError("Invalid import id", "Expected `<scm_provider>/<binding_external_id>`, such as `github/12345678`.")
		return
	}
	response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("id"), request.ID)...)
	response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("scm_provider"), provider)...)
	response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("binding_external_id"), externalID)...)
}

func aiReviewBindingApply(model *aiReviewBindingModel, stored *client.AIReviewBinding) {
	model.ID = types.StringValue(stored.Provider + "/" + stored.BindingExternalID)
	model.Provider = types.StringValue(stored.Provider)
	model.BindingExternalID = types.StringValue(stored.BindingExternalID)
	model.DisplayName = types.StringValue(stored.DisplayName)
	model.AIReview = types.BoolValue(stored.AIReview)
	model.MentionReplies = types.BoolValue(stored.MentionReplies)
	model.PRPreviews = types.BoolValue(stored.PRPreviews)
	model.MergeWhenGreen = types.BoolValue(stored.MergeWhenGreen)
	model.ReviewModel = types.StringValue(stored.ReviewModel)
	model.ReviewDrafts = types.BoolValue(stored.ReviewDrafts)
	model.MaxReviewsPerPR = types.Int64Value(stored.MaxReviewsPerPR)
	model.CustomInstructions = types.StringValue(stored.CustomInstructions)
}

// aiReviewInstructionsLength refuses custom instructions over the
// platform's 4000-character limit.
type aiReviewInstructionsLength struct{}

func (aiReviewInstructionsLength) Description(_ context.Context) string {
	return "value must be at most 4000 characters"
}

func (length aiReviewInstructionsLength) MarkdownDescription(ctx context.Context) string {
	return length.Description(ctx)
}

func (length aiReviewInstructionsLength) ValidateString(ctx context.Context, request validator.StringRequest, response *validator.StringResponse) {
	if request.ConfigValue.IsNull() || request.ConfigValue.IsUnknown() {
		return
	}
	if len([]rune(request.ConfigValue.ValueString())) > 4000 {
		response.Diagnostics.AddAttributeError(request.Path, "Custom instructions too long", "The platform refuses it: "+length.Description(ctx)+".")
	}
}
