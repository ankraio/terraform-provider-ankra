// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"ankra.io/terraform-provider-ankra/internal/client"
)

var (
	_ resource.Resource                = (*aiReviewRelatedRepositoryResource)(nil)
	_ resource.ResourceWithConfigure   = (*aiReviewRelatedRepositoryResource)(nil)
	_ resource.ResourceWithImportState = (*aiReviewRelatedRepositoryResource)(nil)
)

type aiReviewRelatedRepositoryResource struct {
	configuredClient
}

type aiReviewRelatedRepositoryModel struct {
	ID                  types.String `tfsdk:"id"`
	Provider            types.String `tfsdk:"scm_provider"`
	BindingExternalID   types.String `tfsdk:"binding_external_id"`
	RepoFullName        types.String `tfsdk:"repo_full_name"`
	RelatedRepoFullName types.String `tfsdk:"related_repo_full_name"`
}

// NewAIReviewRelatedRepositoryResource returns the
// ankra_ai_review_related_repository resource, the counterpart of
// `ankra org ai-review related-repos add` and `remove`.
func NewAIReviewRelatedRepositoryResource() resource.Resource {
	return &aiReviewRelatedRepositoryResource{}
}

func (related *aiReviewRelatedRepositoryResource) Metadata(_ context.Context, request resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_ai_review_related_repository"
}

func (related *aiReviewRelatedRepositoryResource) Schema(_ context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: "Relates two repositories so an AI code review of either may read the other. The counterpart of " +
			"`ankra org ai-review related-repos add` and `remove`. The platform checks that the GitHub App installation reaches " +
			"both repositories. A pair is unordered: relating B to A is the same pair as A to B. Changes need organisation admin.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Relationship id.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"scm_provider": schema.StringAttribute{
				MarkdownDescription: "Source-control provider. Defaults to `github`, the only provider related repositories support today.",
				Optional:            true,
				Computed:            true,
				Default:             stringdefault.StaticString("github"),
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"binding_external_id": schema.StringAttribute{
				MarkdownDescription: "GitHub App installation id the relationship belongs to.",
				Required:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"repo_full_name": schema.StringAttribute{
				MarkdownDescription: "One repository, `owner/name`.",
				Required:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"related_repo_full_name": schema.StringAttribute{
				MarkdownDescription: "The other repository, `owner/name`.",
				Required:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
		},
	}
}

func (related *aiReviewRelatedRepositoryResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	apiClient := related.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var plan aiReviewRelatedRepositoryModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}
	created, err := apiClient.CreateAIReviewRelatedRepository(ctx, client.AIReviewRelatedRepositoryCreate{
		Provider:            plan.Provider.ValueString(),
		BindingExternalID:   plan.BindingExternalID.ValueString(),
		RepoFullName:        plan.RepoFullName.ValueString(),
		RelatedRepoFullName: plan.RelatedRepoFullName.ValueString(),
	})
	if err != nil {
		response.Diagnostics.AddError("Unable to relate repositories", err.Error())
		return
	}
	plan.ID = types.StringValue(created.ID)
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
}

func (related *aiReviewRelatedRepositoryResource) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	apiClient := related.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state aiReviewRelatedRepositoryModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	listing, err := apiClient.ListAIReviewRelatedRepositories(ctx, state.Provider.ValueString(), state.BindingExternalID.ValueString())
	if removeWhenNotFound(ctx, err, response) {
		return
	}
	if err != nil {
		response.Diagnostics.AddError("Unable to read related repositories", err.Error())
		return
	}
	for _, stored := range listing {
		if stored.ID != state.ID.ValueString() {
			continue
		}
		if !aiReviewPairMatches(state, stored) {
			state.RepoFullName = types.StringValue(stored.RepoFullName)
			state.RelatedRepoFullName = types.StringValue(stored.RelatedRepoFullName)
		}
		response.Diagnostics.Append(response.State.Set(ctx, &state)...)
		return
	}
	response.State.RemoveResource(ctx)
}

// Update is never reached with a change: every configurable attribute
// forces replacement.
func (related *aiReviewRelatedRepositoryResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	if related.requireClient(&response.Diagnostics) == nil {
		return
	}
	var plan aiReviewRelatedRepositoryModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
}

func (related *aiReviewRelatedRepositoryResource) Delete(ctx context.Context, request resource.DeleteRequest, response *resource.DeleteResponse) {
	apiClient := related.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state aiReviewRelatedRepositoryModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	if err := apiClient.DeleteAIReviewRelatedRepository(ctx, state.ID.ValueString()); err != nil {
		response.Diagnostics.AddError("Unable to remove related repositories", err.Error())
	}
}

// ImportState imports by "<scm_provider>/<binding_external_id>/<id>". The
// repository names are filled from the listing as the platform stores them.
func (related *aiReviewRelatedRepositoryResource) ImportState(ctx context.Context, request resource.ImportStateRequest, response *resource.ImportStateResponse) {
	parts, err := organisationSplitImportID(request.ID, 3, "<scm_provider>/<binding_external_id>/<id>")
	if err != nil {
		response.Diagnostics.AddError("Invalid import id", err.Error())
		return
	}
	response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("scm_provider"), parts[0])...)
	response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("binding_external_id"), parts[1])...)
	response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("id"), parts[2])...)
}

// aiReviewPairMatches reports whether the stored pair is the configured one,
// in either order and any case: the platform stores it lowercased and
// ordered.
func aiReviewPairMatches(model aiReviewRelatedRepositoryModel, stored client.AIReviewRelatedRepository) bool {
	first := strings.ToLower(strings.Trim(strings.TrimSpace(model.RepoFullName.ValueString()), "/"))
	second := strings.ToLower(strings.Trim(strings.TrimSpace(model.RelatedRepoFullName.ValueString()), "/"))
	return (first == stored.RepoFullName && second == stored.RelatedRepoFullName) ||
		(first == stored.RelatedRepoFullName && second == stored.RepoFullName)
}
