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
	_ datasource.DataSource              = (*oidcTrustPoliciesDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*oidcTrustPoliciesDataSource)(nil)
)

type oidcTrustPoliciesDataSource struct {
	configuredDataSourceClient
}

type oidcTrustPolicyItemModel struct {
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
	Scopes            types.List   `tfsdk:"scopes"`
	TTLSeconds        types.Int64  `tfsdk:"ttl_seconds"`
	ServiceAccountID  types.String `tfsdk:"service_account_id"`
	CreatedAt         types.String `tfsdk:"created_at"`
}

type oidcTrustPoliciesDataSourceModel struct {
	ID       types.String               `tfsdk:"id"`
	Policies []oidcTrustPolicyItemModel `tfsdk:"policies"`
}

// NewOIDCTrustPoliciesDataSource returns the ankra_oidc_trust_policies data
// source, the counterpart of `ankra org oidc-trust list`.
func NewOIDCTrustPoliciesDataSource() datasource.DataSource {
	return &oidcTrustPoliciesDataSource{}
}

func (policiesSource *oidcTrustPoliciesDataSource) Metadata(_ context.Context, request datasource.MetadataRequest, response *datasource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_oidc_trust_policies"
}

func (policiesSource *oidcTrustPoliciesDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, response *datasource.SchemaResponse) {
	computedInt64 := func(description string) schema.Int64Attribute {
		return schema.Int64Attribute{MarkdownDescription: description, Computed: true}
	}
	response.Schema = schema.Schema{
		MarkdownDescription: "Lists the organisation's GitHub Actions OIDC trust policies, oldest first. Needs `members.manage` on a " +
			"person's own API token: service tokens and tokens pinned to a project are refused.",
		Attributes: map[string]schema.Attribute{
			"id": computedStringAttribute("Placeholder identifier for the data source."),
			"policies": schema.ListNestedAttribute{
				MarkdownDescription: "Trust policies of the organisation.",
				Computed:            true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id":                  computedStringAttribute("Trust policy ID."),
						"name":                computedStringAttribute("Policy name."),
						"repository_owner":    computedStringAttribute("GitHub owner of the trusted repository."),
						"repository_name":     computedStringAttribute("Name of the trusted repository."),
						"repository_owner_id": computedInt64("GitHub's numeric ID of the owner."),
						"repository_id":       computedInt64("GitHub's numeric ID of the repository."),
						"ref":                 computedStringAttribute("Git ref a run must be on; null for any."),
						"environment":         computedStringAttribute("Deployment environment the job must run in; null for any."),
						"job_workflow_ref":    computedStringAttribute("Workflow a run must come from; null for any."),
						"allow_pull_request":  computedBoolAttribute("Whether pull request runs may exchange tokens."),
						"role_slug":           computedStringAttribute("Role of the policy's service account."),
						"scopes":              computedListString("Scopes the minted tokens are narrowed to; empty for the role's full authority."),
						"ttl_seconds":         computedInt64("Lifetime of each minted token, in seconds."),
						"service_account_id":  computedStringAttribute("Service account the minted tokens belong to."),
						"created_at":          computedStringAttribute("When the policy was created."),
					},
				},
			},
		},
	}
}

func (policiesSource *oidcTrustPoliciesDataSource) Read(ctx context.Context, _ datasource.ReadRequest, response *datasource.ReadResponse) {
	apiClient := policiesSource.requireOrganisationClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	policies, listError := apiClient.ListOIDCTrustPolicies(ctx)
	if listError != nil {
		response.Diagnostics.AddError("Unable to list OIDC trust policies", listError.Error())
		return
	}
	state := oidcTrustPoliciesDataSourceModel{
		ID:       types.StringValue("ankra_oidc_trust_policies"),
		Policies: make([]oidcTrustPolicyItemModel, 0, len(policies)),
	}
	for _, policy := range policies {
		state.Policies = append(state.Policies, oidcTrustPolicyItemModel{
			ID:                types.StringValue(policy.ID),
			Name:              types.StringValue(policy.Name),
			RepositoryOwner:   types.StringValue(policy.RepositoryOwner),
			RepositoryName:    types.StringValue(policy.RepositoryName),
			RepositoryOwnerID: types.Int64Value(policy.RepositoryOwnerID),
			RepositoryID:      types.Int64Value(policy.RepositoryID),
			Ref:               types.StringPointerValue(policy.Ref),
			Environment:       types.StringPointerValue(policy.Environment),
			JobWorkflowRef:    types.StringPointerValue(policy.JobWorkflowRef),
			AllowPullRequest:  types.BoolValue(policy.AllowPullRequest),
			RoleSlug:          types.StringValue(policy.RoleSlug),
			Scopes:            listFromStringsOrEmpty(policy.Scopes),
			TTLSeconds:        types.Int64Value(policy.TTLSeconds),
			ServiceAccountID:  types.StringValue(policy.ServiceAccountID),
			CreatedAt:         types.StringValue(policy.CreatedAt),
		})
	}
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
}
