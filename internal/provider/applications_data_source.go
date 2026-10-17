// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"ankra.io/terraform-provider-ankra/internal/client"
)

var (
	_ datasource.DataSource              = (*applicationsDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*applicationsDataSource)(nil)
	_ datasource.DataSource              = (*applicationDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*applicationDataSource)(nil)
)

type applicationItemModel struct {
	ID                types.String `tfsdk:"id"`
	Name              types.String `tfsdk:"name"`
	State             types.String `tfsdk:"state"`
	DeploymentState   types.String `tfsdk:"deployment_state"`
	AppRepoOwner      types.String `tfsdk:"app_repo_owner"`
	AppRepoName       types.String `tfsdk:"app_repo_name"`
	AppRepoBranch     types.String `tfsdk:"app_repo_branch"`
	AppRepoURL        types.String `tfsdk:"app_repo_url"`
	ContainerImageURL types.String `tfsdk:"container_image_url"`
	PipelineSource    types.String `tfsdk:"pipeline_source"`
	CreatedAt         types.String `tfsdk:"created_at"`
}

func applicationItemFrom(application client.Application) applicationItemModel {
	return applicationItemModel{
		ID:                types.StringValue(application.ID),
		Name:              types.StringValue(application.Name),
		State:             types.StringValue(application.State),
		DeploymentState:   optionalString(application.DeploymentState),
		AppRepoOwner:      types.StringValue(application.AppRepoOwner),
		AppRepoName:       types.StringValue(application.AppRepoName),
		AppRepoBranch:     types.StringValue(application.AppRepoBranch),
		AppRepoURL:        types.StringPointerValue(application.AppRepoURL),
		ContainerImageURL: types.StringPointerValue(application.ContainerImageURL),
		PipelineSource:    types.StringPointerValue(application.PipelineSource),
		CreatedAt:         types.StringValue(application.CreatedAt),
	}
}

func applicationItemAttributes(includeDeploymentState bool) map[string]schema.Attribute {
	computed := func(description string) schema.StringAttribute {
		return schema.StringAttribute{MarkdownDescription: description, Computed: true}
	}
	attributes := map[string]schema.Attribute{
		"id":                  computed("Identifier of the application."),
		"name":                computed("Application name."),
		"state":               computed("Lifecycle of the application record: `creating`, `up` or `error`."),
		"app_repo_owner":      computed("Repository owner."),
		"app_repo_name":       computed("Repository name."),
		"app_repo_branch":     computed("Branch Ankra builds and deploys."),
		"app_repo_url":        computed("Repository URL."),
		"container_image_url": computed("Image repository the application's builds publish to."),
		"pipeline_source":     computed("What builds the application: an Ankra pipeline, the repository's own CI, or nothing yet."),
		"created_at":          computed("When the application was registered."),
	}
	if includeDeploymentState {
		attributes["deployment_state"] = computed("Whether the application is running: `running`, `degraded`, `failed`, `deploying`, `not_deployed` or `unknown`.")
	} else {
		attributes["deployment_state"] = computed("Not reported by the listing; read one application with `ankra_application` for it.")
	}
	return attributes
}

type applicationsDataSource struct {
	configuredDataSourceClient
}

type applicationsDataSourceModel struct {
	ID           types.String           `tfsdk:"id"`
	Search       types.String           `tfsdk:"search"`
	Applications []applicationItemModel `tfsdk:"applications"`
}

// NewApplicationsDataSource returns the ankra_applications data source, the
// counterpart of `ankra application list`.
func NewApplicationsDataSource() datasource.DataSource {
	return &applicationsDataSource{}
}

func (listDataSource *applicationsDataSource) Metadata(_ context.Context, request datasource.MetadataRequest, response *datasource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_applications"
}

func (listDataSource *applicationsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, response *datasource.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: "Lists the applications of the organisation (in the provider's project). The counterpart of `ankra application list`.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Placeholder identifier for the data source.",
				Computed:            true,
			},
			"search": schema.StringAttribute{
				MarkdownDescription: "Only list applications whose name matches this text.",
				Optional:            true,
			},
			"applications": schema.ListNestedAttribute{
				MarkdownDescription: "Applications, newest first.",
				Computed:            true,
				NestedObject:        schema.NestedAttributeObject{Attributes: applicationItemAttributes(false)},
			},
		},
	}
}

func (listDataSource *applicationsDataSource) Read(ctx context.Context, request datasource.ReadRequest, response *datasource.ReadResponse) {
	apiClient := listDataSource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state applicationsDataSourceModel
	response.Diagnostics.Append(request.Config.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	applications, err := apiClient.ListApplications(ctx, state.Search.ValueString())
	if err != nil {
		response.Diagnostics.AddError("Unable to list applications", err.Error())
		return
	}
	state.ID = types.StringValue("ankra_applications")
	state.Applications = make([]applicationItemModel, 0, len(applications))
	for _, application := range applications {
		state.Applications = append(state.Applications, applicationItemFrom(application))
	}
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
}

type applicationDataSource struct {
	configuredDataSourceClient
}

// NewApplicationDataSource returns the ankra_application data source, the
// counterpart of `ankra application get`.
func NewApplicationDataSource() datasource.DataSource {
	return &applicationDataSource{}
}

func (singleDataSource *applicationDataSource) Metadata(_ context.Context, request datasource.MetadataRequest, response *datasource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_application"
}

func (singleDataSource *applicationDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, response *datasource.SchemaResponse) {
	attributes := applicationItemAttributes(true)
	attributes["id"] = schema.StringAttribute{
		MarkdownDescription: "Identifier of the application to read.",
		Required:            true,
	}
	response.Schema = schema.Schema{
		MarkdownDescription: "Reads one application by id. The counterpart of `ankra application get`.",
		Attributes:          attributes,
	}
}

func (singleDataSource *applicationDataSource) Read(ctx context.Context, request datasource.ReadRequest, response *datasource.ReadResponse) {
	apiClient := singleDataSource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state applicationItemModel
	response.Diagnostics.Append(request.Config.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	application, err := apiClient.GetApplication(ctx, state.ID.ValueString())
	if err != nil {
		response.Diagnostics.AddError("Unable to read application", err.Error())
		return
	}
	state = applicationItemFrom(*application)
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
}
