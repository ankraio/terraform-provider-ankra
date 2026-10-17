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
	_ datasource.DataSource              = (*registryProjectsDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*registryProjectsDataSource)(nil)
	_ datasource.DataSource              = (*registryRobotsDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*registryRobotsDataSource)(nil)
)

type registryProjectsDataSource struct {
	configuredDataSourceClient
}

type registryProjectItemModel struct {
	Name            types.String `tfsdk:"name"`
	Project         types.String `tfsdk:"project"`
	Host            types.String `tfsdk:"host"`
	IsDefault       types.Bool   `tfsdk:"is_default"`
	RepositoryCount types.Int64  `tfsdk:"repository_count"`
	RobotCount      types.Int64  `tfsdk:"robot_count"`
}

type registryProjectsDataSourceModel struct {
	ID       types.String               `tfsdk:"id"`
	Projects []registryProjectItemModel `tfsdk:"projects"`
}

// NewRegistryProjectsDataSource returns the ankra_registry_projects data
// source, the counterpart of `ankra registry projects list`.
func NewRegistryProjectsDataSource() datasource.DataSource {
	return &registryProjectsDataSource{}
}

func (projectsDataSource *registryProjectsDataSource) Metadata(_ context.Context, request datasource.MetadataRequest, response *datasource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_registry_projects"
}

func (projectsDataSource *registryProjectsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, response *datasource.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: "Lists the organisation's projects on the Ankra registry: its own (`default`) first, then its extra " +
			"projects. The counterpart of `ankra registry projects list`.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Placeholder identifier for the data source.",
				Computed:            true,
			},
			"projects": schema.ListNestedAttribute{
				MarkdownDescription: "Registry projects.",
				Computed:            true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"name":             schema.StringAttribute{MarkdownDescription: "Name the project is addressed by.", Computed: true},
						"project":          schema.StringAttribute{MarkdownDescription: "The project's name on the registry.", Computed: true},
						"host":             schema.StringAttribute{MarkdownDescription: "Registry host.", Computed: true},
						"is_default":       schema.BoolAttribute{MarkdownDescription: "Whether this is the organisation's own project.", Computed: true},
						"repository_count": schema.Int64Attribute{MarkdownDescription: "Repositories in the project.", Computed: true},
						"robot_count":      schema.Int64Attribute{MarkdownDescription: "The organisation's robots bound to the project.", Computed: true},
					},
				},
			},
		},
	}
}

func (projectsDataSource *registryProjectsDataSource) Read(ctx context.Context, _ datasource.ReadRequest, response *datasource.ReadResponse) {
	apiClient := projectsDataSource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	projects, err := apiClient.ListRegistryProjects(ctx)
	if err != nil {
		response.Diagnostics.AddError("Unable to list registry projects", err.Error())
		return
	}
	state := registryProjectsDataSourceModel{
		ID:       types.StringValue("ankra_registry_projects"),
		Projects: make([]registryProjectItemModel, 0, len(projects)),
	}
	for _, project := range projects {
		state.Projects = append(state.Projects, registryProjectItemModel{
			Name:            types.StringValue(project.Name),
			Project:         types.StringValue(project.Project),
			Host:            types.StringValue(project.Host),
			IsDefault:       types.BoolValue(project.IsDefault),
			RepositoryCount: types.Int64Value(project.RepositoryCount),
			RobotCount:      types.Int64Value(project.RobotCount),
		})
	}
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
}

type registryRobotsDataSource struct {
	configuredDataSourceClient
}

type registryRobotItemModel struct {
	Name           types.String `tfsdk:"name"`
	Kind           types.String `tfsdk:"kind"`
	Managed        types.Bool   `tfsdk:"managed"`
	RobotName      types.String `tfsdk:"robot_name"`
	Host           types.String `tfsdk:"host"`
	Project        types.String `tfsdk:"project"`
	Scope          types.String `tfsdk:"scope"`
	Permissions    types.List   `tfsdk:"permissions"`
	Description    types.String `tfsdk:"description"`
	CredentialName types.String `tfsdk:"credential_name"`
	ExpiresAt      types.String `tfsdk:"expires_at"`
	Registry       types.String `tfsdk:"registry"`
}

type registryRobotsDataSourceModel struct {
	ID     types.String             `tfsdk:"id"`
	Robots []registryRobotItemModel `tfsdk:"robots"`
}

// NewRegistryRobotsDataSource returns the ankra_registry_robots data source,
// the counterpart of `ankra registry robots list`.
func NewRegistryRobotsDataSource() datasource.DataSource {
	return &registryRobotsDataSource{}
}

func (robotsDataSource *registryRobotsDataSource) Metadata(_ context.Context, request datasource.MetadataRequest, response *datasource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_registry_robots"
}

func (robotsDataSource *registryRobotsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, response *datasource.SchemaResponse) {
	computedString := func(description string) schema.StringAttribute {
		return schema.StringAttribute{MarkdownDescription: description, Computed: true}
	}
	response.Schema = schema.Schema{
		MarkdownDescription: "Lists every registry robot the organisation holds: its members' own and the ones Ankra manages. " +
			"Secrets are never listed. The counterpart of `ankra registry robots list`.",
		Attributes: map[string]schema.Attribute{
			"id": computedString("Placeholder identifier for the data source."),
			"robots": schema.ListNestedAttribute{
				MarkdownDescription: "Registry robots.",
				Computed:            true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"name":            computedString("Robot name."),
						"kind":            computedString("`user`, `organisation` or `application`."),
						"managed":         schema.BoolAttribute{MarkdownDescription: "Whether Ankra mints and rotates the robot itself.", Computed: true},
						"robot_name":      computedString("The robot's user name on the registry."),
						"host":            computedString("Registry host."),
						"project":         computedString("Registry project the robot is bound to."),
						"scope":           computedString("`push`, `pull`, or `custom`."),
						"permissions":     schema.ListAttribute{MarkdownDescription: "Grants, written `resource:action`.", ElementType: types.StringType, Computed: true},
						"description":     computedString("Free-text description."),
						"credential_name": computedString("Organisation credential holding the robot's login."),
						"expires_at":      computedString("When the robot expires; null if never."),
						"registry":        computedString("Integrated registry entry; null on the Ankra registry."),
					},
				},
			},
		},
	}
}

func (robotsDataSource *registryRobotsDataSource) Read(ctx context.Context, _ datasource.ReadRequest, response *datasource.ReadResponse) {
	apiClient := robotsDataSource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	robots, err := apiClient.ListRegistryRobots(ctx)
	if err != nil {
		response.Diagnostics.AddError("Unable to list registry robots", err.Error())
		return
	}
	state := registryRobotsDataSourceModel{
		ID:     types.StringValue("ankra_registry_robots"),
		Robots: make([]registryRobotItemModel, 0, len(robots)),
	}
	for _, robot := range robots {
		project := robot.ProjectName
		if project == "" {
			project = robot.Project
		}
		permissions := robot.Permissions
		if permissions == nil {
			permissions = []string{}
		}
		state.Robots = append(state.Robots, registryRobotItemModel{
			Name:           types.StringValue(robot.Name),
			Kind:           optionalString(robot.Kind),
			Managed:        types.BoolValue(robot.Managed),
			RobotName:      types.StringValue(robot.RobotName),
			Host:           types.StringValue(robot.Host),
			Project:        optionalString(project),
			Scope:          optionalString(robot.Scope),
			Permissions:    listFromStrings(permissions),
			Description:    optionalString(robot.Description),
			CredentialName: optionalString(robot.CredentialName),
			ExpiresAt:      types.StringPointerValue(robot.ExpiresAt),
			Registry:       optionalString(robot.Registry),
		})
	}
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
}
