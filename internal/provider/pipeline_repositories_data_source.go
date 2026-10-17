// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"ankra.io/terraform-provider-ankra/internal/client"
)

var (
	_ datasource.DataSource              = (*pipelineRepositoriesDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*pipelineRepositoriesDataSource)(nil)
)

type pipelineRepositoriesDataSource struct {
	configuredDataSourceClient
}

type pipelineRepositoryItemModel struct {
	ID                types.String `tfsdk:"id"`
	Provider          types.String `tfsdk:"provider_name"`
	Owner             types.String `tfsdk:"owner"`
	Name              types.String `tfsdk:"name"`
	CredentialName    types.String `tfsdk:"credential_name"`
	DefaultBranch     types.String `tfsdk:"default_branch"`
	ApplicationID     types.String `tfsdk:"application_id"`
	ClusterID         types.String `tfsdk:"cluster_id"`
	WorkspacesOnly    types.Bool   `tfsdk:"workspaces_only"`
	MergeTrainEnabled types.Bool   `tfsdk:"merge_train_enabled"`
	MergeTrainMaxCars types.Int64  `tfsdk:"merge_train_max_cars"`
}

type pipelineRepositoriesDataSourceModel struct {
	ID           types.String                  `tfsdk:"id"`
	Provider     types.String                  `tfsdk:"provider_name"`
	Owner        types.String                  `tfsdk:"owner"`
	Name         types.String                  `tfsdk:"name"`
	Repositories []pipelineRepositoryItemModel `tfsdk:"repositories"`
}

// NewPipelineRepositoriesDataSource returns the ankra_pipeline_repositories
// data source, the counterpart of `ankra pipeline repositories list`.
func NewPipelineRepositoriesDataSource() datasource.DataSource {
	return &pipelineRepositoriesDataSource{}
}

func (repositoriesDataSource *pipelineRepositoriesDataSource) Metadata(_ context.Context, request datasource.MetadataRequest, response *datasource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_pipeline_repositories"
}

func (repositoriesDataSource *pipelineRepositoriesDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, response *datasource.SchemaResponse) {
	computedText := func(description string) schema.StringAttribute {
		return schema.StringAttribute{MarkdownDescription: description, Computed: true}
	}
	response.Schema = schema.Schema{
		MarkdownDescription: "Lists the repositories connected to Ankra Pipelines, with or without an application. The counterpart of `ankra pipeline repositories list`.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Placeholder identifier for the data source.",
				Computed:            true,
			},
			"provider_name": schema.StringAttribute{
				MarkdownDescription: "Only list repositories of this provider: `github`, `gitlab` or `bitbucket_cloud`.",
				Optional:            true,
				Validators:          []validator.String{applicationStringOneOf{values: pipelineRepositoryProviders}},
			},
			"owner": schema.StringAttribute{
				MarkdownDescription: "Only list repositories of this owner.",
				Optional:            true,
			},
			"name": schema.StringAttribute{
				MarkdownDescription: "Only list repositories with this name.",
				Optional:            true,
			},
			"repositories": schema.ListNestedAttribute{
				MarkdownDescription: "Connected repositories, newest first.",
				Computed:            true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id":                   computedText("Identifier of the connected repository."),
						"provider_name":        computedText("Source-code provider."),
						"owner":                computedText("Repository owner."),
						"name":                 computedText("Repository name."),
						"credential_name":      computedText("Credential the repository is reached through."),
						"default_branch":       computedText("Branch the pipeline file is read from."),
						"application_id":       computedText("Application the repository is linked to."),
						"cluster_id":           computedText("Cluster the repository's runs go to, when it overrides the organisation's."),
						"workspaces_only":      schema.BoolAttribute{MarkdownDescription: "Whether the repository is connected for AI workspaces only.", Computed: true},
						"merge_train_enabled":  schema.BoolAttribute{MarkdownDescription: "Whether the merge train is on.", Computed: true},
						"merge_train_max_cars": schema.Int64Attribute{MarkdownDescription: "How many pull requests the merge train tests at once.", Computed: true},
					},
				},
			},
		},
	}
}

func (repositoriesDataSource *pipelineRepositoriesDataSource) Read(ctx context.Context, request datasource.ReadRequest, response *datasource.ReadResponse) {
	apiClient := repositoriesDataSource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state pipelineRepositoriesDataSourceModel
	response.Diagnostics.Append(request.Config.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	repositories, err := apiClient.ListPipelineRepositories(ctx, client.PipelineRepositoryFilter{
		Provider: state.Provider.ValueString(),
		Owner:    state.Owner.ValueString(),
		Name:     state.Name.ValueString(),
	})
	if err != nil {
		response.Diagnostics.AddError("Unable to list pipeline repositories", err.Error())
		return
	}
	state.ID = types.StringValue("ankra_pipeline_repositories")
	state.Repositories = make([]pipelineRepositoryItemModel, 0, len(repositories))
	for _, repository := range repositories {
		state.Repositories = append(state.Repositories, pipelineRepositoryItemModel{
			ID:                types.StringValue(repository.ID),
			Provider:          types.StringValue(repository.Provider),
			Owner:             types.StringValue(repository.Owner),
			Name:              types.StringValue(repository.Name),
			CredentialName:    optionalString(repository.CredentialName),
			DefaultBranch:     types.StringValue(repository.DefaultBranch),
			ApplicationID:     types.StringPointerValue(repository.ApplicationID),
			ClusterID:         types.StringPointerValue(repository.ClusterID),
			WorkspacesOnly:    types.BoolValue(repository.WorkspacesOnly),
			MergeTrainEnabled: types.BoolValue(repository.MergeTrainEnabled),
			MergeTrainMaxCars: types.Int64Value(repository.MergeTrainMaxCars),
		})
	}
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
}
