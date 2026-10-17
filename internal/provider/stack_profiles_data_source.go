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
	_ datasource.DataSource              = (*stackProfilesDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*stackProfilesDataSource)(nil)
)

type stackProfilesDataSource struct {
	configuredDataSourceClient
}

type stackProfileItemModel struct {
	ID             types.String `tfsdk:"id"`
	OrganisationID types.String `tfsdk:"organisation_id"`
	Name           types.String `tfsdk:"name"`
	Description    types.String `tfsdk:"description"`
	Category       types.String `tfsdk:"category"`
	Tags           types.List   `tfsdk:"tags"`
	Visibility     types.String `tfsdk:"visibility"`
	CurrentVersion types.Int64  `tfsdk:"current_version"`
	LatestVersion  types.Int64  `tfsdk:"latest_version"`
	IsOwner        types.Bool   `tfsdk:"is_owner"`
	SharedWithYou  types.Bool   `tfsdk:"shared_with_you"`
	PublisherSlug  types.String `tfsdk:"publisher_slug"`
}

type stackProfilesDataSourceModel struct {
	ID       types.String            `tfsdk:"id"`
	Profiles []stackProfileItemModel `tfsdk:"profiles"`
}

// NewStackProfilesDataSource returns the ankra_stack_profiles data source,
// the counterpart of `ankra stack-profiles list`.
func NewStackProfilesDataSource() datasource.DataSource {
	return &stackProfilesDataSource{}
}

func (profilesDataSource *stackProfilesDataSource) Metadata(_ context.Context, request datasource.MetadataRequest, response *datasource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_stack_profiles"
}

func (profilesDataSource *stackProfilesDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, response *datasource.SchemaResponse) {
	computedString := func(description string) schema.StringAttribute {
		return schema.StringAttribute{MarkdownDescription: description, Computed: true}
	}
	response.Schema = schema.Schema{
		MarkdownDescription: "Lists the stack profiles the organisation can see: its own, public ones, and ones shared with it. " +
			"The counterpart of `ankra stack-profiles list`.",
		Attributes: map[string]schema.Attribute{
			"id": computedString("Placeholder identifier for the data source."),
			"profiles": schema.ListNestedAttribute{
				MarkdownDescription: "Stack profiles.",
				Computed:            true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id":              computedString("Identifier of the profile."),
						"organisation_id": computedString("Organisation that owns the profile."),
						"name":            computedString("Profile name."),
						"description":     computedString("Free-text description."),
						"category":        computedString("Catalogue category."),
						"tags":            schema.ListAttribute{MarkdownDescription: "Catalogue tags.", ElementType: types.StringType, Computed: true},
						"visibility":      computedString("`organisation` or `public`."),
						"current_version": schema.Int64Attribute{MarkdownDescription: "The version new deployments get.", Computed: true},
						"latest_version":  schema.Int64Attribute{MarkdownDescription: "The newest published version.", Computed: true},
						"is_owner":        schema.BoolAttribute{MarkdownDescription: "Whether the organisation owns the profile.", Computed: true},
						"shared_with_you": schema.BoolAttribute{MarkdownDescription: "Whether the profile is visible through a share grant.", Computed: true},
						"publisher_slug":  computedString("Slug of the publishing organisation."),
					},
				},
			},
		},
	}
}

func (profilesDataSource *stackProfilesDataSource) Read(ctx context.Context, _ datasource.ReadRequest, response *datasource.ReadResponse) {
	apiClient := profilesDataSource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	profiles, err := apiClient.ListStackProfiles(ctx)
	if err != nil {
		response.Diagnostics.AddError("Unable to list stack profiles", err.Error())
		return
	}
	state := stackProfilesDataSourceModel{
		ID:       types.StringValue("ankra_stack_profiles"),
		Profiles: make([]stackProfileItemModel, 0, len(profiles)),
	}
	for _, profile := range profiles {
		tags := profile.Tags
		if tags == nil {
			tags = []string{}
		}
		state.Profiles = append(state.Profiles, stackProfileItemModel{
			ID:             types.StringValue(profile.ID),
			OrganisationID: types.StringValue(profile.OrganisationID),
			Name:           types.StringValue(profile.Name),
			Description:    types.StringPointerValue(profile.Description),
			Category:       types.StringValue(profile.Category),
			Tags:           listFromStrings(tags),
			Visibility:     types.StringValue(profile.Visibility),
			CurrentVersion: types.Int64Value(profile.CurrentVersion),
			LatestVersion:  types.Int64Value(profile.LatestVersion),
			IsOwner:        types.BoolValue(profile.IsOwner),
			SharedWithYou:  types.BoolValue(profile.SharedWithYou),
			PublisherSlug:  types.StringPointerValue(profile.PublisherSlug),
		})
	}
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
}
