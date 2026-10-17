// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ datasource.DataSource              = (*organisationRolesDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*organisationRolesDataSource)(nil)
	_ datasource.DataSource              = (*apiTokensDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*apiTokensDataSource)(nil)
	_ datasource.DataSource              = (*serviceTokensDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*serviceTokensDataSource)(nil)
)

func computedListString(description string) schema.ListAttribute {
	return schema.ListAttribute{MarkdownDescription: description, Computed: true, ElementType: types.StringType}
}

func computedStringAttribute(description string) schema.StringAttribute {
	return schema.StringAttribute{MarkdownDescription: description, Computed: true}
}

func computedBoolAttribute(description string) schema.BoolAttribute {
	return schema.BoolAttribute{MarkdownDescription: description, Computed: true}
}

// listFromStringsOrEmpty is listFromStrings for a computed list the API
// always answers: nil reads as an empty list rather than null.
func listFromStringsOrEmpty(values []string) types.List {
	if values == nil {
		return types.ListValueMust(types.StringType, []attr.Value{})
	}
	return listFromStrings(values)
}

type organisationRolesDataSource struct {
	configuredDataSourceClient
}

type organisationRoleItemModel struct {
	ID          types.String `tfsdk:"id"`
	Slug        types.String `tfsdk:"slug"`
	Name        types.String `tfsdk:"name"`
	Description types.String `tfsdk:"description"`
	Builtin     types.Bool   `tfsdk:"builtin"`
	Permissions types.List   `tfsdk:"permissions"`
}

type organisationRolesDataSourceModel struct {
	ID    types.String                `tfsdk:"id"`
	Roles []organisationRoleItemModel `tfsdk:"roles"`
}

// NewOrganisationRolesDataSource returns the ankra_organisation_roles data
// source, the counterpart of `ankra org roles`.
func NewOrganisationRolesDataSource() datasource.DataSource {
	return &organisationRolesDataSource{}
}

func (rolesDataSource *organisationRolesDataSource) Metadata(_ context.Context, request datasource.MetadataRequest, response *datasource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_organisation_roles"
}

func (rolesDataSource *organisationRolesDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, response *datasource.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: "Lists the roles the organisation can grant: the built-in roles first, in privilege order, then the custom roles by slug. " +
			"Use a role's `slug` for `ankra_service_token.role_slug` or a member's role. Needs the `members.read` permission.",
		Attributes: map[string]schema.Attribute{
			"id": computedStringAttribute("Placeholder identifier for the data source."),
			"roles": schema.ListNestedAttribute{
				MarkdownDescription: "Roles of the organisation.",
				Computed:            true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id":          computedStringAttribute("Identifier of a custom role; null for a built-in role."),
						"slug":        computedStringAttribute("Slug of the role."),
						"name":        computedStringAttribute("Display name of the role."),
						"description": computedStringAttribute("Description of the role; empty when none."),
						"builtin":     computedBoolAttribute("Whether the role is built in."),
						"permissions": computedListString("Permissions the role grants."),
					},
				},
			},
		},
	}
}

func (rolesDataSource *organisationRolesDataSource) Read(ctx context.Context, _ datasource.ReadRequest, response *datasource.ReadResponse) {
	apiClient := rolesDataSource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	roles, err := apiClient.ListOrganisationRoles(ctx)
	if err != nil {
		response.Diagnostics.AddError("Unable to list organisation roles", err.Error())
		return
	}
	state := organisationRolesDataSourceModel{
		ID:    types.StringValue("ankra_organisation_roles"),
		Roles: make([]organisationRoleItemModel, 0, len(roles)),
	}
	for _, role := range roles {
		state.Roles = append(state.Roles, organisationRoleItemModel{
			ID:          types.StringPointerValue(role.ID),
			Slug:        types.StringValue(role.Slug),
			Name:        types.StringValue(role.Name),
			Description: types.StringValue(role.Description),
			Builtin:     types.BoolValue(role.Builtin),
			Permissions: listFromStringsOrEmpty(role.Permissions),
		})
	}
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
}

type apiTokensDataSource struct {
	configuredDataSourceClient
}

type apiTokenItemModel struct {
	ID          types.String `tfsdk:"id"`
	Name        types.String `tfsdk:"name"`
	ExpiresAt   types.String `tfsdk:"expires_at"`
	CreatedAt   types.String `tfsdk:"created_at"`
	RevokedAt   types.String `tfsdk:"revoked_at"`
	LastUsedAt  types.String `tfsdk:"last_used_at"`
	Revoked     types.Bool   `tfsdk:"revoked"`
	Scopes      types.List   `tfsdk:"scopes"`
	ProjectID   types.String `tfsdk:"project_id"`
	ProjectName types.String `tfsdk:"project_name"`
}

type apiTokensDataSourceModel struct {
	ID     types.String        `tfsdk:"id"`
	Tokens []apiTokenItemModel `tfsdk:"tokens"`
}

// NewAPITokensDataSource returns the ankra_api_tokens data source, the
// counterpart of `ankra tokens list`.
func NewAPITokensDataSource() datasource.DataSource {
	return &apiTokensDataSource{}
}

func (tokensDataSource *apiTokensDataSource) Metadata(_ context.Context, request datasource.MetadataRequest, response *datasource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_api_tokens"
}

func (tokensDataSource *apiTokensDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, response *datasource.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: "Lists the personal API tokens of the provider token's user in the current organisation, revoked ones included. " +
			"Token values are never part of the listing. The counterpart of `ankra tokens list`.",
		Attributes: map[string]schema.Attribute{
			"id": computedStringAttribute("Placeholder identifier for the data source."),
			"tokens": schema.ListNestedAttribute{
				MarkdownDescription: "Personal tokens of the user.",
				Computed:            true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id":           computedStringAttribute("Identifier of the token."),
						"name":         computedStringAttribute("Name of the token."),
						"expires_at":   computedStringAttribute("When the token expires (UTC, as the platform renders it)."),
						"created_at":   computedStringAttribute("When the token was created."),
						"revoked_at":   computedStringAttribute("When the token was revoked; null while it is live."),
						"last_used_at": computedStringAttribute("When the token last authenticated; null if never."),
						"revoked":      computedBoolAttribute("Whether the token is revoked."),
						"scopes":       computedListString("Scopes the token is restricted to; empty for its user's full authority."),
						"project_id":   computedStringAttribute("Project the token is pinned to; null for an organisation-wide token."),
						"project_name": computedStringAttribute("Name of the pinned project."),
					},
				},
			},
		},
	}
}

func (tokensDataSource *apiTokensDataSource) Read(ctx context.Context, _ datasource.ReadRequest, response *datasource.ReadResponse) {
	apiClient := tokensDataSource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	tokens, err := apiClient.ListAPITokens(ctx)
	if err != nil {
		response.Diagnostics.AddError("Unable to list API tokens", err.Error())
		return
	}
	state := apiTokensDataSourceModel{
		ID:     types.StringValue("ankra_api_tokens"),
		Tokens: make([]apiTokenItemModel, 0, len(tokens)),
	}
	for _, token := range tokens {
		state.Tokens = append(state.Tokens, apiTokenItemModel{
			ID:          types.StringValue(token.ID),
			Name:        types.StringValue(token.Name),
			ExpiresAt:   types.StringValue(token.ExpiresAt),
			CreatedAt:   types.StringValue(token.CreatedAt),
			RevokedAt:   types.StringPointerValue(token.RevokedAt),
			LastUsedAt:  types.StringPointerValue(token.LastUsedAt),
			Revoked:     types.BoolValue(token.Revoked),
			Scopes:      listFromStringsOrEmpty(token.Scopes),
			ProjectID:   types.StringPointerValue(token.ProjectID),
			ProjectName: types.StringPointerValue(token.ProjectName),
		})
	}
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
}

type serviceTokensDataSource struct {
	configuredDataSourceClient
}

type serviceTokenItemModel struct {
	ID          types.String `tfsdk:"id"`
	AccountID   types.String `tfsdk:"account_id"`
	AccountName types.String `tfsdk:"account_name"`
	Label       types.String `tfsdk:"label"`
	RoleSlug    types.String `tfsdk:"role_slug"`
	ExpiresAt   types.String `tfsdk:"expires_at"`
	CreatedAt   types.String `tfsdk:"created_at"`
	RevokedAt   types.String `tfsdk:"revoked_at"`
	LastUsedAt  types.String `tfsdk:"last_used_at"`
	Revoked     types.Bool   `tfsdk:"revoked"`
	Scopes      types.List   `tfsdk:"scopes"`
	ProjectID   types.String `tfsdk:"project_id"`
	ProjectName types.String `tfsdk:"project_name"`
}

type serviceTokensDataSourceModel struct {
	ID     types.String            `tfsdk:"id"`
	Tokens []serviceTokenItemModel `tfsdk:"tokens"`
}

// NewServiceTokensDataSource returns the ankra_service_tokens data source.
func NewServiceTokensDataSource() datasource.DataSource {
	return &serviceTokensDataSource{}
}

func (tokensDataSource *serviceTokensDataSource) Metadata(_ context.Context, request datasource.MetadataRequest, response *datasource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_service_tokens"
}

func (tokensDataSource *serviceTokensDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, response *datasource.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: "Lists the organisation's service tokens, revoked ones included, ordered by service account. " +
			"Token values are never part of the listing. Needs the `members.manage` permission.",
		Attributes: map[string]schema.Attribute{
			"id": computedStringAttribute("Placeholder identifier for the data source."),
			"tokens": schema.ListNestedAttribute{
				MarkdownDescription: "Service tokens of the organisation.",
				Computed:            true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id":           computedStringAttribute("Identifier of the token."),
						"account_id":   computedStringAttribute("Identifier of the service account the token belongs to."),
						"account_name": computedStringAttribute("Name of the service account."),
						"label":        computedStringAttribute("Label of the token within its account."),
						"role_slug":    computedStringAttribute("Role the service account holds; empty when the account holds no role."),
						"expires_at":   computedStringAttribute("When the token expires (UTC, as the platform renders it)."),
						"created_at":   computedStringAttribute("When the token was created."),
						"revoked_at":   computedStringAttribute("When the token was revoked; null while it is live."),
						"last_used_at": computedStringAttribute("When the token last authenticated; null if never."),
						"revoked":      computedBoolAttribute("Whether the token is revoked."),
						"scopes":       computedListString("Scopes the token is restricted to; empty for the account's full authority."),
						"project_id":   computedStringAttribute("Project the token is pinned to; null for an organisation-wide token."),
						"project_name": computedStringAttribute("Name of the pinned project."),
					},
				},
			},
		},
	}
}

func (tokensDataSource *serviceTokensDataSource) Read(ctx context.Context, _ datasource.ReadRequest, response *datasource.ReadResponse) {
	apiClient := tokensDataSource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	tokens, err := apiClient.ListServiceTokens(ctx)
	if err != nil {
		response.Diagnostics.AddError("Unable to list service tokens", err.Error())
		return
	}
	state := serviceTokensDataSourceModel{
		ID:     types.StringValue("ankra_service_tokens"),
		Tokens: make([]serviceTokenItemModel, 0, len(tokens)),
	}
	for _, token := range tokens {
		state.Tokens = append(state.Tokens, serviceTokenItemModel{
			ID:          types.StringValue(token.ID),
			AccountID:   types.StringValue(token.AccountID),
			AccountName: types.StringValue(token.AccountName),
			Label:       types.StringValue(token.Label),
			RoleSlug:    types.StringValue(token.RoleSlug),
			ExpiresAt:   types.StringValue(token.ExpiresAt),
			CreatedAt:   types.StringValue(token.CreatedAt),
			RevokedAt:   types.StringPointerValue(token.RevokedAt),
			LastUsedAt:  types.StringPointerValue(token.LastUsedAt),
			Revoked:     types.BoolValue(token.Revoked),
			Scopes:      listFromStringsOrEmpty(token.Scopes),
			ProjectID:   types.StringPointerValue(token.ProjectID),
			ProjectName: types.StringPointerValue(token.ProjectName),
		})
	}
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
}
