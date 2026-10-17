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
	_ datasource.DataSource              = (*organisationDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*organisationDataSource)(nil)
	_ datasource.DataSource              = (*organisationsDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*organisationsDataSource)(nil)
)

type organisationDataSource struct {
	configuredDataSourceClient
}

type organisationMemberItemModel struct {
	ID       types.String `tfsdk:"id"`
	Email    types.String `tfsdk:"email"`
	Role     types.String `tfsdk:"role"`
	Status   types.String `tfsdk:"status"`
	InviteID types.String `tfsdk:"invite_id"`
}

type organisationDataSourceModel struct {
	ID          types.String                  `tfsdk:"id"`
	Name        types.String                  `tfsdk:"name"`
	Slug        types.String                  `tfsdk:"slug"`
	CreatedAt   types.String                  `tfsdk:"created_at"`
	MFARequired types.Bool                    `tfsdk:"mfa_required"`
	Members     []organisationMemberItemModel `tfsdk:"members"`
}

// NewOrganisationDataSource returns the ankra_organisation data source, the
// counterpart of `ankra org current` and `ankra org members`.
func NewOrganisationDataSource() datasource.DataSource {
	return &organisationDataSource{}
}

func (organisation *organisationDataSource) Metadata(_ context.Context, request datasource.MetadataRequest, response *datasource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_organisation"
}

func (organisation *organisationDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, response *datasource.SchemaResponse) {
	memberAttributes := map[string]schema.Attribute{
		"id":        schema.StringAttribute{MarkdownDescription: "User id.", Computed: true},
		"email":     schema.StringAttribute{MarkdownDescription: "Email address; null for a machine identity.", Computed: true},
		"role":      schema.StringAttribute{MarkdownDescription: "Role in the organisation.", Computed: true},
		"status":    schema.StringAttribute{MarkdownDescription: "`active` or `pending`.", Computed: true},
		"invite_id": schema.StringAttribute{MarkdownDescription: "Identifier of the membership row.", Computed: true},
	}
	response.Schema = schema.Schema{
		MarkdownDescription: "One organisation with its members: by default the one requests act in (the provider's `organisation_id`, " +
			"or the token's current organisation). The counterpart of `ankra org current` and `ankra org members`.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Organisation id. Leave unset for the current organisation.",
				Optional:            true,
				Computed:            true,
			},
			"name":         schema.StringAttribute{MarkdownDescription: "Organisation name.", Computed: true},
			"slug":         schema.StringAttribute{MarkdownDescription: "URL slug.", Computed: true},
			"created_at":   schema.StringAttribute{MarkdownDescription: "Creation time.", Computed: true},
			"mfa_required": schema.BoolAttribute{MarkdownDescription: "Whether members must enrol in MFA.", Computed: true},
			"members": schema.ListNestedAttribute{
				MarkdownDescription: "Members and pending invites.",
				Computed:            true,
				NestedObject:        schema.NestedAttributeObject{Attributes: memberAttributes},
			},
		},
	}
}

func (organisation *organisationDataSource) Read(ctx context.Context, request datasource.ReadRequest, response *datasource.ReadResponse) {
	apiClient := organisation.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var config organisationDataSourceModel
	response.Diagnostics.Append(request.Config.Get(ctx, &config)...)
	if response.Diagnostics.HasError() {
		return
	}

	organisationID := config.ID.ValueString()
	if config.ID.IsNull() || config.ID.IsUnknown() {
		currentID, err := apiClient.CurrentOrganisationID(ctx)
		if err != nil {
			response.Diagnostics.AddError("Unable to resolve the organisation", err.Error())
			return
		}
		organisationID = currentID
	}
	detail, err := apiClient.GetOrganisation(ctx, organisationID)
	if err != nil {
		response.Diagnostics.AddError("Unable to read organisation", err.Error())
		return
	}

	state := organisationDataSourceModel{
		ID:          types.StringValue(detail.OrganisationID),
		Name:        types.StringPointerValue(detail.Name),
		Slug:        types.StringPointerValue(detail.Slug),
		CreatedAt:   types.StringPointerValue(detail.CreatedAt),
		MFARequired: types.BoolValue(detail.MFARequired),
		Members:     make([]organisationMemberItemModel, 0, len(detail.Users)),
	}
	for _, member := range detail.Users {
		state.Members = append(state.Members, organisationMemberItemModel{
			ID:       types.StringPointerValue(member.ID),
			Email:    types.StringPointerValue(member.Email),
			Role:     types.StringPointerValue(member.Role),
			Status:   types.StringPointerValue(member.Status),
			InviteID: types.StringPointerValue(member.InviteID),
		})
	}
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
}

type organisationsDataSource struct {
	configuredDataSourceClient
}

type organisationsItemModel struct {
	ID          types.String `tfsdk:"id"`
	Name        types.String `tfsdk:"name"`
	Slug        types.String `tfsdk:"slug"`
	Role        types.String `tfsdk:"role"`
	UserCurrent types.Bool   `tfsdk:"user_current"`
}

type organisationsDataSourceModel struct {
	ID            types.String             `tfsdk:"id"`
	Organisations []organisationsItemModel `tfsdk:"organisations"`
}

// NewOrganisationsDataSource returns the ankra_organisations data source,
// the counterpart of `ankra org list`.
func NewOrganisationsDataSource() datasource.DataSource {
	return &organisationsDataSource{}
}

func (organisations *organisationsDataSource) Metadata(_ context.Context, request datasource.MetadataRequest, response *datasource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_organisations"
}

func (organisations *organisationsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, response *datasource.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: "Lists the organisations the token's user is an active member of. The counterpart of `ankra org list`.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{MarkdownDescription: "Placeholder identifier for the data source.", Computed: true},
			"organisations": schema.ListNestedAttribute{
				MarkdownDescription: "Organisations, ordered by name.",
				Computed:            true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id":           schema.StringAttribute{MarkdownDescription: "Organisation id.", Computed: true},
						"name":         schema.StringAttribute{MarkdownDescription: "Organisation name.", Computed: true},
						"slug":         schema.StringAttribute{MarkdownDescription: "URL slug.", Computed: true},
						"role":         schema.StringAttribute{MarkdownDescription: "The user's role in it.", Computed: true},
						"user_current": schema.BoolAttribute{MarkdownDescription: "Whether requests act in this organisation.", Computed: true},
					},
				},
			},
		},
	}
}

func (organisations *organisationsDataSource) Read(ctx context.Context, _ datasource.ReadRequest, response *datasource.ReadResponse) {
	apiClient := organisations.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	listing, err := apiClient.ListOrganisations(ctx)
	if err != nil {
		response.Diagnostics.AddError("Unable to list organisations", err.Error())
		return
	}
	state := organisationsDataSourceModel{
		ID:            types.StringValue("ankra_organisations"),
		Organisations: make([]organisationsItemModel, 0, len(listing)),
	}
	for _, organisation := range listing {
		state.Organisations = append(state.Organisations, organisationsItemModel{
			ID:          types.StringValue(organisation.OrganisationID),
			Name:        types.StringPointerValue(organisation.Name),
			Slug:        types.StringPointerValue(organisation.Slug),
			Role:        types.StringPointerValue(organisation.Role),
			UserCurrent: types.BoolValue(organisation.UserCurrent),
		})
	}
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
}
