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
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"ankra.io/terraform-provider-ankra/internal/client"
)

var (
	_ resource.Resource                = (*aiReviewRepositoryResource)(nil)
	_ resource.ResourceWithConfigure   = (*aiReviewRepositoryResource)(nil)
	_ resource.ResourceWithImportState = (*aiReviewRepositoryResource)(nil)
)

type aiReviewRepositoryResource struct {
	configuredClient
}

type aiReviewRepositoryModel struct {
	ID                types.String `tfsdk:"id"`
	Provider          types.String `tfsdk:"scm_provider"`
	BindingExternalID types.String `tfsdk:"binding_external_id"`
	RepoFullName      types.String `tfsdk:"repo_full_name"`
	AIReview          types.Bool   `tfsdk:"ai_review"`
	MentionReplies    types.Bool   `tfsdk:"mention_replies"`
	PRPreviews        types.Bool   `tfsdk:"pr_previews"`
	ReviewModel       types.String `tfsdk:"review_model"`
	ReviewDrafts      types.Bool   `tfsdk:"review_drafts"`
	MaxReviewsPerPR   types.Int64  `tfsdk:"max_reviews_per_pr"`
}

// NewAIReviewRepositoryResource returns the ankra_ai_review_repository
// resource, the counterpart of `ankra org ai-review repos set` and `unset`.
func NewAIReviewRepositoryResource() resource.Resource {
	return &aiReviewRepositoryResource{}
}

func (rule *aiReviewRepositoryResource) Metadata(_ context.Context, request resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_ai_review_repository"
}

func (rule *aiReviewRepositoryResource) Schema(_ context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: "One repository's AI review rule on a source-control binding, overriding the binding's settings. " +
			"The counterpart of `ankra org ai-review repos set`; destroy reverts the repository to the binding's settings like " +
			"`ankra org ai-review repos unset`. Changes need organisation admin.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "`<scm_provider>/<binding_external_id>/<repo_full_name>`, with the repository as the platform stores it (lowercased).",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"scm_provider": schema.StringAttribute{
				MarkdownDescription: "Source-control provider of the binding, such as `github`, `gitlab` or `bitbucket`.",
				Required:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"binding_external_id": schema.StringAttribute{
				MarkdownDescription: "The binding's external id (a GitHub App installation id, for example), as `ankra org ai-review repos list` shows it.",
				Required:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"repo_full_name": schema.StringAttribute{
				MarkdownDescription: "Repository path, `owner/name` (or a GitLab group path). The platform stores it lowercased.",
				Required:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"ai_review": schema.BoolAttribute{
				MarkdownDescription: "Run the AI review on the repository's pull requests.",
				Required:            true,
			},
			"mention_replies": schema.BoolAttribute{
				MarkdownDescription: "Answer @mentions on the repository.",
				Required:            true,
			},
			"pr_previews": schema.BoolAttribute{
				MarkdownDescription: "Build pull request previews for the repository.",
				Required:            true,
			},
			"review_model": schema.StringAttribute{
				MarkdownDescription: "Model catalogue key reviews run on. Unset inherits the binding's model.",
				Optional:            true,
			},
			"review_drafts": schema.BoolAttribute{
				MarkdownDescription: "Review draft pull requests automatically too. Unset keeps what the platform holds.",
				Optional:            true,
				Computed:            true,
				PlanModifiers:       []planmodifier.Bool{boolplanmodifier.UseStateForUnknown()},
			},
			"max_reviews_per_pr": schema.Int64Attribute{
				MarkdownDescription: "Cap on automatic reviews per pull request; `0` is no cap. Unset keeps what the platform holds.",
				Optional:            true,
				Computed:            true,
				PlanModifiers:       []planmodifier.Int64{int64planmodifier.UseStateForUnknown()},
			},
		},
	}
}

func (rule *aiReviewRepositoryResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	rule.write(ctx, request.Plan, &response.State, response.Private, &response.Diagnostics)
}

func (rule *aiReviewRepositoryResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	rule.write(ctx, request.Plan, &response.State, response.Private, &response.Diagnostics)
}

// write upserts the rule and reads it back. review_model is always sent, an
// empty value inheriting the binding's model, so removing it from
// configuration clears it; review_drafts and max_reviews_per_pr are sent
// only when configured.
func (rule *aiReviewRepositoryResource) write(ctx context.Context, plan tfsdk.Plan, state *tfsdk.State, private privateStateWriter, diagnostics *diag.Diagnostics) {
	apiClient := rule.requireClient(diagnostics)
	if apiClient == nil {
		return
	}
	var model aiReviewRepositoryModel
	diagnostics.Append(plan.Get(ctx, &model)...)
	if diagnostics.HasError() {
		return
	}
	reviewModel := model.ReviewModel.ValueString()
	if err := apiClient.PutAIReviewRepositoryRule(ctx, model.Provider.ValueString(), model.BindingExternalID.ValueString(), client.AIReviewRepositoryRuleWrite{
		RepoFullName:    model.RepoFullName.ValueString(),
		AIReview:        model.AIReview.ValueBool(),
		MentionReplies:  model.MentionReplies.ValueBool(),
		PRPreviews:      model.PRPreviews.ValueBool(),
		ReviewModel:     &reviewModel,
		ReviewDrafts:    boolPointer(model.ReviewDrafts),
		MaxReviewsPerPR: int64Pointer(model.MaxReviewsPerPR),
	}); err != nil {
		diagnostics.AddError("Unable to write AI review rule", err.Error())
		return
	}
	stored, err := aiReviewFindRule(ctx, apiClient, model)
	if err != nil {
		diagnostics.AddError("Unable to read the AI review rule back", err.Error())
		return
	}
	if stored == nil {
		diagnostics.AddError("AI review rule not found", "The platform accepted the rule for "+model.RepoFullName.ValueString()+" but does not list it.")
		return
	}
	aiReviewApplyRule(&model, stored)
	diagnostics.Append(state.Set(ctx, &model)...)
	diagnostics.Append(trackScope(ctx, apiClient, private)...)
}

func (rule *aiReviewRepositoryResource) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	apiClient := rule.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state aiReviewRepositoryModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	stored, err := aiReviewFindRule(ctx, apiClient, state)
	if err != nil {
		response.Diagnostics.AddError("Unable to read AI review rule", err.Error())
		return
	}
	if stored == nil {
		response.State.RemoveResource(ctx)
		return
	}
	aiReviewApplyRule(&state, stored)
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
	response.Diagnostics.Append(trackScope(ctx, apiClient, response.Private)...)
}

func (rule *aiReviewRepositoryResource) Delete(ctx context.Context, request resource.DeleteRequest, response *resource.DeleteResponse) {
	apiClient := rule.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state aiReviewRepositoryModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	err := apiClient.DeleteAIReviewRepositoryRule(ctx, state.Provider.ValueString(), state.BindingExternalID.ValueString(), state.RepoFullName.ValueString())
	if err == nil || deletedWhenNotFound(ctx, err, apiClient, request.Private, &response.Diagnostics) || response.Diagnostics.HasError() {
		return
	}
	response.Diagnostics.AddError("Unable to remove AI review rule", err.Error())
}

// ImportState imports by "<scm_provider>/<binding_external_id>/<repo_full_name>".
func (rule *aiReviewRepositoryResource) ImportState(ctx context.Context, request resource.ImportStateRequest, response *resource.ImportStateResponse) {
	parts, err := organisationSplitImportID(request.ID, 3, "<scm_provider>/<binding_external_id>/<repo_full_name>")
	if err != nil {
		response.Diagnostics.AddError("Invalid import id", err.Error())
		return
	}
	response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("scm_provider"), parts[0])...)
	response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("binding_external_id"), parts[1])...)
	response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("repo_full_name"), parts[2])...)
}

// aiReviewFindRule returns the repository's rule, or nil when the binding
// or the rule is gone.
func aiReviewFindRule(ctx context.Context, apiClient *client.Client, model aiReviewRepositoryModel) (*client.AIReviewRepositoryRule, error) {
	bindings, err := apiClient.ListAIReviewBindings(ctx)
	if err != nil {
		return nil, err
	}
	for _, binding := range bindings {
		if binding.Provider != model.Provider.ValueString() || binding.BindingExternalID != model.BindingExternalID.ValueString() {
			continue
		}
		for index := range binding.RepoOverrides {
			if strings.EqualFold(binding.RepoOverrides[index].RepoFullName, model.RepoFullName.ValueString()) {
				return &binding.RepoOverrides[index], nil
			}
		}
	}
	return nil, nil
}

func aiReviewApplyRule(model *aiReviewRepositoryModel, stored *client.AIReviewRepositoryRule) {
	model.ID = types.StringValue(model.Provider.ValueString() + "/" + model.BindingExternalID.ValueString() + "/" + stored.RepoFullName)
	if !strings.EqualFold(model.RepoFullName.ValueString(), stored.RepoFullName) {
		model.RepoFullName = types.StringValue(stored.RepoFullName)
	}
	model.AIReview = types.BoolValue(stored.AIReview)
	model.MentionReplies = types.BoolValue(stored.MentionReplies)
	model.PRPreviews = types.BoolValue(stored.PRPreviews)
	model.ReviewModel = optionalString(stored.ReviewModel)
	model.ReviewDrafts = types.BoolValue(stored.ReviewDrafts)
	model.MaxReviewsPerPR = types.Int64Value(stored.MaxReviewsPerPR)
}
