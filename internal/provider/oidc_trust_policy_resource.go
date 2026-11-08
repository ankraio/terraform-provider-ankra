// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64default"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/setplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"ankra.io/terraform-provider-ankra/internal/client"
)

var (
	_ resource.Resource                = (*oidcTrustPolicyResource)(nil)
	_ resource.ResourceWithConfigure   = (*oidcTrustPolicyResource)(nil)
	_ resource.ResourceWithImportState = (*oidcTrustPolicyResource)(nil)
)

// The platform's trust policy rules (go/internal/usecase/oidctrust).
var (
	oidcTrustPolicyNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
	oidcTrustGitHubNamePattern = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,100}$`)
	oidcTrustExactValuePattern = regexp.MustCompile(`^[A-Za-z0-9_./@:#+=-]{1,300}$`)
	oidcTrustRefPattern        = regexp.MustCompile(`^refs/[A-Za-z0-9_./@:#+=-]{1,295}$`)
)

const (
	oidcTrustDefaultTTLSeconds int64 = 900
	oidcTrustMinimumTTLSeconds int64 = 60
	oidcTrustMaximumTTLSeconds int64 = 3600
)

// oidcTrustRoleSlugs are the built-in roles a trust policy may hand its
// service account; custom roles are refused.
var oidcTrustRoleSlugs = []string{"owner", "admin", "operator", "member", "viewer"}

// oidcTrustScopes are the token scopes the platform accepts.
var oidcTrustScopes = []string{"mcp:read", "mcp:write"}

type oidcTrustPolicyResource struct {
	configuredClient
}

type oidcTrustPolicyModel struct {
	ID                types.String `tfsdk:"id"`
	Name              types.String `tfsdk:"name"`
	RepositoryOwner   types.String `tfsdk:"repository_owner"`
	RepositoryName    types.String `tfsdk:"repository_name"`
	RepositoryOwnerID types.Int64  `tfsdk:"repository_owner_id"`
	RepositoryID      types.Int64  `tfsdk:"repository_id"`
	Ref               types.String `tfsdk:"ref"`
	Environment       types.String `tfsdk:"environment"`
	JobWorkflowRef    types.String `tfsdk:"job_workflow_ref"`
	AllowPullRequest  types.Bool   `tfsdk:"allow_pull_request"`
	RoleSlug          types.String `tfsdk:"role_slug"`
	Scopes            types.Set    `tfsdk:"scopes"`
	TTLSeconds        types.Int64  `tfsdk:"ttl_seconds"`
	ServiceAccountID  types.String `tfsdk:"service_account_id"`
	CreatedAt         types.String `tfsdk:"created_at"`
}

// NewOIDCTrustPolicyResource returns the ankra_oidc_trust_policy resource,
// the counterpart of `ankra org oidc-trust create` and `delete`.
func NewOIDCTrustPolicyResource() resource.Resource {
	return &oidcTrustPolicyResource{}
}

func (trust *oidcTrustPolicyResource) Metadata(_ context.Context, request resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_oidc_trust_policy"
}

func oidcTrustReplacedString(description string, isRequired bool, validators ...validator.String) schema.StringAttribute {
	return schema.StringAttribute{
		MarkdownDescription: description,
		Required:            isRequired,
		Optional:            !isRequired,
		Validators:          validators,
		PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
	}
}

func oidcTrustGitHubIdentifier(description string) schema.Int64Attribute {
	return schema.Int64Attribute{
		MarkdownDescription: description,
		Required:            true,
		Validators:          []validator.Int64{deployInt64Range{minimum: 1, maximum: 1<<63 - 1}},
		PlanModifiers:       []planmodifier.Int64{int64planmodifier.RequiresReplace()},
	}
}

func (trust *oidcTrustPolicyResource) Schema(_ context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: "A GitHub Actions OIDC trust policy: lets workflow runs of one GitHub repository exchange their GitHub ID " +
			"token (audience `https://ankra.ai/oidc/github`) for a short-lived Ankra token, so CI needs no stored Ankra secret. The " +
			"counterpart of `ankra org oidc-trust create` and `delete`; a workflow signs in with `ankra login --github-oidc`.\n\n" +
			"Each policy gets its own service account holding `role_slug`, and every exchange mints a token on that account for at " +
			"most `ttl_seconds`, narrowed by `scopes` when set. Without `ref`, `environment` or `job_workflow_ref`, any branch, tag, " +
			"environment and workflow of the repository matches, so pin at least one for anything above `viewer`.\n\n" +
			"**Who can manage it.** The platform requires the `members.manage` permission on a person's own API token (not a service " +
			"token, and not a token pinned to a project) and refuses a `role_slug` carrying permissions the token's user does not hold.\n\n" +
			"**Replacement.** The platform has no update for a trust policy, so changing any argument replaces it: the old policy is " +
			"deleted and every token it minted is revoked at once, then the new one is created. Destroy does the same. The policy " +
			"belongs to the organisation, so it needs no provider `project_id`.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Trust policy ID.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": oidcTrustReplacedString("Policy name, unique in the organisation: 1 to 64 letters, digits, dots, dashes or underscores, "+
				"starting with a letter or digit. It also names the policy's service account.", true,
				deployPatternValidator{pattern: oidcTrustPolicyNamePattern, subject: "name"}),
			"repository_owner": oidcTrustReplacedString("GitHub owner (user or organisation) of the trusted repository, exactly as GitHub names it.", true,
				deployPatternValidator{pattern: oidcTrustGitHubNamePattern, subject: "repository_owner"}),
			"repository_name": oidcTrustReplacedString("Name of the trusted repository, exactly as GitHub names it.", true,
				deployPatternValidator{pattern: oidcTrustGitHubNamePattern, subject: "repository_name"}),
			"repository_owner_id": oidcTrustGitHubIdentifier("GitHub's numeric ID of the repository owner (`gh api users/<owner> --jq .id`). " +
				"Matching on the ID stops a renamed or re-created owner from inheriting the policy."),
			"repository_id": oidcTrustGitHubIdentifier("GitHub's numeric ID of the repository (`gh api repos/<owner>/<name> --jq .id`). " +
				"Matching on the ID stops a renamed or re-created repository from inheriting the policy."),
			"ref": oidcTrustReplacedString("Exact Git ref a run must be on, such as `refs/heads/main` or `refs/tags/v1.2.3`. No wildcards. "+
				"Unset matches every ref.", false, deployPatternValidator{pattern: oidcTrustRefPattern, subject: "ref"}),
			"environment": oidcTrustReplacedString("Exact GitHub deployment environment the job must run in, such as `production`. "+
				"Unset matches every environment and jobs without one.", false,
				deployPatternValidator{pattern: oidcTrustExactValuePattern, subject: "environment"}),
			"job_workflow_ref": oidcTrustReplacedString("Exact `job_workflow_ref` claim the run must carry, such as "+
				"`acme/infra/.github/workflows/deploy.yml@refs/heads/main`, to trust one (reusable) workflow only. Unset matches every workflow.", false,
				deployPatternValidator{pattern: oidcTrustExactValuePattern, subject: "job_workflow_ref"}),
			"allow_pull_request": schema.BoolAttribute{
				MarkdownDescription: "Whether runs triggered by `pull_request` events may exchange tokens. Defaults to `false`: a pull request " +
					"runs code nobody has reviewed yet.",
				Optional:      true,
				Computed:      true,
				Default:       booldefault.StaticBool(false),
				PlanModifiers: []planmodifier.Bool{boolplanmodifier.RequiresReplace()},
			},
			"role_slug": oidcTrustReplacedString("Built-in role the policy's service account holds: `"+strings.Join(oidcTrustRoleSlugs, "`, `")+
				"`. Custom roles are refused, and so is a role carrying permissions the token's user lacks.", true,
				organisationStringOneOf{values: oidcTrustRoleSlugs}),
			"scopes": schema.SetAttribute{
				MarkdownDescription: "Narrows the minted tokens to these scopes: `mcp:read` and/or `mcp:write` (an MCP-only token). Unset keeps " +
					"them at the role's full authority; an empty set is refused, because the platform reads it as unset.",
				ElementType:   types.StringType,
				Optional:      true,
				Validators:    []validator.Set{organisationSetNotEmpty{}, oidcTrustScopeValidator{}},
				PlanModifiers: []planmodifier.Set{setplanmodifier.RequiresReplace()},
			},
			"ttl_seconds": schema.Int64Attribute{
				MarkdownDescription: fmt.Sprintf("Lifetime of each minted token, %d to %d seconds. Defaults to %d (15 minutes).",
					oidcTrustMinimumTTLSeconds, oidcTrustMaximumTTLSeconds, oidcTrustDefaultTTLSeconds),
				Optional:      true,
				Computed:      true,
				Default:       int64default.StaticInt64(oidcTrustDefaultTTLSeconds),
				Validators:    []validator.Int64{deployInt64Range{minimum: oidcTrustMinimumTTLSeconds, maximum: oidcTrustMaximumTTLSeconds}},
				PlanModifiers: []planmodifier.Int64{int64planmodifier.RequiresReplace()},
			},
			"service_account_id": schema.StringAttribute{
				MarkdownDescription: "ID of the service account the minted tokens belong to.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"created_at": schema.StringAttribute{
				MarkdownDescription: "When the policy was created.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
		},
	}
}

func (trust *oidcTrustPolicyResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	apiClient := trust.requireOrganisationClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var plan oidcTrustPolicyModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}
	scopes := organisationStringsFromSet(ctx, plan.Scopes, &response.Diagnostics)
	if response.Diagnostics.HasError() {
		return
	}
	created, createError := apiClient.CreateOIDCTrustPolicy(ctx, client.OIDCTrustPolicyCreate{
		Name:              plan.Name.ValueString(),
		RepositoryOwner:   plan.RepositoryOwner.ValueString(),
		RepositoryName:    plan.RepositoryName.ValueString(),
		RepositoryOwnerID: plan.RepositoryOwnerID.ValueInt64(),
		RepositoryID:      plan.RepositoryID.ValueInt64(),
		Ref:               stringPointer(plan.Ref),
		Environment:       stringPointer(plan.Environment),
		JobWorkflowRef:    stringPointer(plan.JobWorkflowRef),
		AllowPullRequest:  plan.AllowPullRequest.ValueBool(),
		RoleSlug:          plan.RoleSlug.ValueString(),
		Scopes:            scopes,
		TTLSeconds:        plan.TTLSeconds.ValueInt64(),
	})
	if createError != nil {
		response.Diagnostics.AddError("Unable to create OIDC trust policy", createError.Error())
		return
	}
	oidcTrustPolicyApply(&plan, created)
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
	response.Diagnostics.Append(trackScope(ctx, apiClient, response.Private)...)
}

func (trust *oidcTrustPolicyResource) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	apiClient := trust.requireOrganisationClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state oidcTrustPolicyModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	policy, readError := apiClient.GetOIDCTrustPolicy(ctx, state.ID.ValueString())
	if removeWhenNotFound(ctx, readError, apiClient, response) {
		return
	}
	if readError != nil {
		response.Diagnostics.AddError("Unable to read OIDC trust policy", readError.Error())
		return
	}
	oidcTrustPolicyApply(&state, policy)
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
	response.Diagnostics.Append(trackScope(ctx, apiClient, response.Private)...)
}

// Update is never planned: every argument replaces the policy.
func (trust *oidcTrustPolicyResource) Update(_ context.Context, _ resource.UpdateRequest, response *resource.UpdateResponse) {
	response.Diagnostics.AddError("OIDC trust policies cannot be updated",
		"The platform has no update for a trust policy, and every argument plans a replacement. This is a bug in the provider.")
}

// Delete removes the policy, which revokes every token it minted.
func (trust *oidcTrustPolicyResource) Delete(ctx context.Context, request resource.DeleteRequest, response *resource.DeleteResponse) {
	apiClient := trust.requireOrganisationClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state oidcTrustPolicyModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	deleteError := apiClient.DeleteOIDCTrustPolicy(ctx, state.ID.ValueString())
	if deleteError == nil || deletedWhenNotFound(ctx, deleteError, apiClient, request.Private, &response.Diagnostics) || response.Diagnostics.HasError() {
		return
	}
	response.Diagnostics.AddError("Unable to delete OIDC trust policy", deleteError.Error())
}

// ImportState imports a policy by ID (`ankra org oidc-trust list`).
func (trust *oidcTrustPolicyResource) ImportState(ctx context.Context, request resource.ImportStateRequest, response *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), request, response)
}

// oidcTrustPolicyApply copies the platform's policy into the model.
func oidcTrustPolicyApply(model *oidcTrustPolicyModel, policy *client.OIDCTrustPolicy) {
	model.ID = types.StringValue(policy.ID)
	model.Name = types.StringValue(policy.Name)
	model.RepositoryOwner = types.StringValue(policy.RepositoryOwner)
	model.RepositoryName = types.StringValue(policy.RepositoryName)
	model.RepositoryOwnerID = types.Int64Value(policy.RepositoryOwnerID)
	model.RepositoryID = types.Int64Value(policy.RepositoryID)
	model.Ref = types.StringPointerValue(policy.Ref)
	model.Environment = types.StringPointerValue(policy.Environment)
	model.JobWorkflowRef = types.StringPointerValue(policy.JobWorkflowRef)
	model.AllowPullRequest = types.BoolValue(policy.AllowPullRequest)
	model.RoleSlug = types.StringValue(policy.RoleSlug)
	model.Scopes = organisationOptionalSet(model.Scopes, policy.Scopes)
	model.TTLSeconds = types.Int64Value(policy.TTLSeconds)
	model.ServiceAccountID = types.StringValue(policy.ServiceAccountID)
	model.CreatedAt = types.StringValue(policy.CreatedAt)
}

// oidcTrustScopeValidator refuses a scope the platform does not mint.
type oidcTrustScopeValidator struct{}

func (oidcTrustScopeValidator) Description(_ context.Context) string {
	return "each scope must be one of: " + strings.Join(oidcTrustScopes, ", ")
}

func (scopeValidator oidcTrustScopeValidator) MarkdownDescription(ctx context.Context) string {
	return scopeValidator.Description(ctx)
}

func (scopeValidator oidcTrustScopeValidator) ValidateSet(ctx context.Context, request validator.SetRequest, response *validator.SetResponse) {
	if request.ConfigValue.IsNull() || request.ConfigValue.IsUnknown() {
		return
	}
	for _, element := range request.ConfigValue.Elements() {
		scope, isString := element.(types.String)
		if !isString || scope.IsNull() || scope.IsUnknown() {
			continue
		}
		isKnown := false
		for _, allowed := range oidcTrustScopes {
			if scope.ValueString() == allowed {
				isKnown = true
				break
			}
		}
		if !isKnown {
			response.Diagnostics.AddAttributeError(request.Path, "Unknown scope",
				fmt.Sprintf("%q is refused by the platform: %s.", scope.ValueString(), scopeValidator.Description(ctx)))
		}
	}
}
