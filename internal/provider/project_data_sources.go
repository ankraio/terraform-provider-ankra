// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"ankra.io/terraform-provider-ankra/internal/client"
)

var (
	_ datasource.DataSource              = (*projectsDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*projectsDataSource)(nil)
	_ datasource.DataSource              = (*projectDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*projectDataSource)(nil)
)

type projectItemModel struct {
	ID          types.String `tfsdk:"id"`
	Name        types.String `tfsdk:"name"`
	Slug        types.String `tfsdk:"slug"`
	Description types.String `tfsdk:"description"`
	IsDefault   types.Bool   `tfsdk:"is_default"`
	CreatedAt   types.String `tfsdk:"created_at"`
}

func projectItem(project client.Project) projectItemModel {
	return projectItemModel{
		ID:          types.StringValue(project.ID),
		Name:        types.StringValue(project.Name),
		Slug:        types.StringValue(project.Slug),
		Description: types.StringValue(project.Description),
		IsDefault:   types.BoolValue(project.IsDefault),
		CreatedAt:   types.StringValue(project.CreatedAt),
	}
}

func projectItemAttributes() map[string]schema.Attribute {
	return map[string]schema.Attribute{
		"id":          computedStringAttribute("Project ID."),
		"name":        computedStringAttribute("Display name."),
		"slug":        computedStringAttribute("Slug, which never changes."),
		"description": computedStringAttribute("Description; empty when none is set."),
		"is_default":  computedBoolAttribute("Whether this is the organisation's default project."),
		"created_at":  computedStringAttribute("When the project was created."),
	}
}

type projectsDataSource struct {
	configuredDataSourceClient
}

type projectsDataSourceModel struct {
	ID       types.String       `tfsdk:"id"`
	Projects []projectItemModel `tfsdk:"projects"`
}

// NewProjectsDataSource returns the ankra_projects data source, the
// counterpart of `ankra project list`.
func NewProjectsDataSource() datasource.DataSource {
	return &projectsDataSource{}
}

func (projectsSource *projectsDataSource) Metadata(_ context.Context, request datasource.MetadataRequest, response *datasource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_projects"
}

func (projectsSource *projectsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, response *datasource.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: "Lists the organisation's projects the token's user can reach, the default project included. " +
			"Fails while projects are not enabled for the organisation.",
		Attributes: map[string]schema.Attribute{
			"id": computedStringAttribute("Placeholder identifier for the data source."),
			"projects": schema.ListNestedAttribute{
				MarkdownDescription: "Projects of the organisation.",
				Computed:            true,
				NestedObject:        schema.NestedAttributeObject{Attributes: projectItemAttributes()},
			},
		},
	}
}

func (projectsSource *projectsDataSource) Read(ctx context.Context, _ datasource.ReadRequest, response *datasource.ReadResponse) {
	apiClient := projectsSource.requireOrganisationClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	projects, listError := apiClient.ListProjects(ctx)
	if listError != nil {
		response.Diagnostics.AddError("Unable to list projects", projectErrorDetail(listError))
		return
	}
	state := projectsDataSourceModel{ID: types.StringValue("ankra_projects"), Projects: make([]projectItemModel, 0, len(projects))}
	for _, project := range projects {
		state.Projects = append(state.Projects, projectItem(project))
	}
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
}

type projectDataSource struct {
	configuredDataSourceClient
}

type projectDataSourceModel struct {
	Project     types.String `tfsdk:"project"`
	ID          types.String `tfsdk:"id"`
	Name        types.String `tfsdk:"name"`
	Slug        types.String `tfsdk:"slug"`
	Description types.String `tfsdk:"description"`
	IsDefault   types.Bool   `tfsdk:"is_default"`
	CreatedAt   types.String `tfsdk:"created_at"`
}

// NewProjectDataSource returns the ankra_project data source, the
// counterpart of `ankra project get`.
func NewProjectDataSource() datasource.DataSource {
	return &projectDataSource{}
}

func (projectSource *projectDataSource) Metadata(_ context.Context, request datasource.MetadataRequest, response *datasource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_project"
}

func (projectSource *projectDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, response *datasource.SchemaResponse) {
	attributes := projectItemAttributes()
	attributes["project"] = schema.StringAttribute{
		MarkdownDescription: "The project to look up, by ID or slug (`default` names the organisation's default project).",
		Required:            true,
	}
	response.Schema = schema.Schema{
		MarkdownDescription: "Looks up one project of the organisation by ID or slug, for example to pin a token or scope a resource " +
			"to it by ID. Fails when no project the token's user can reach matches, and while projects are not enabled for the organisation.",
		Attributes: attributes,
	}
}

func (projectSource *projectDataSource) Read(ctx context.Context, request datasource.ReadRequest, response *datasource.ReadResponse) {
	apiClient := projectSource.requireOrganisationClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var config projectDataSourceModel
	response.Diagnostics.Append(request.Config.Get(ctx, &config)...)
	if response.Diagnostics.HasError() {
		return
	}
	reference := strings.TrimSpace(config.Project.ValueString())
	projects, listError := apiClient.ListProjects(ctx)
	if listError != nil {
		response.Diagnostics.AddError("Unable to list projects", projectErrorDetail(listError))
		return
	}
	for _, project := range projects {
		if strings.EqualFold(project.ID, reference) || project.Slug == reference {
			item := projectItem(project)
			config.ID, config.Name, config.Slug, config.Description = item.ID, item.Name, item.Slug, item.Description
			config.IsDefault, config.CreatedAt = item.IsDefault, item.CreatedAt
			response.Diagnostics.Append(response.State.Set(ctx, &config)...)
			return
		}
	}
	response.Diagnostics.AddError("Project not found",
		fmt.Sprintf("No project with ID or slug %q is visible to the token in this organisation.", reference))
}
