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
	_ datasource.DataSource              = (*clusterVariablesDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*clusterVariablesDataSource)(nil)
	_ datasource.DataSource              = (*clusterStackVariablesDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*clusterStackVariablesDataSource)(nil)
)

type clusterVariablesDataSource struct {
	configuredDataSourceClient
}

type clusterVariableItemModel struct {
	ID          types.String `tfsdk:"id"`
	Name        types.String `tfsdk:"name"`
	Value       types.String `tfsdk:"value"`
	Description types.String `tfsdk:"description"`
}

type clusterVariablesDataSourceModel struct {
	ID        types.String               `tfsdk:"id"`
	ClusterID types.String               `tfsdk:"cluster_id"`
	Variables []clusterVariableItemModel `tfsdk:"variables"`
}

// NewClusterVariablesDataSource returns the ankra_cluster_variables data
// source, the counterpart of `ankra cluster variables list`.
func NewClusterVariablesDataSource() datasource.DataSource {
	return &clusterVariablesDataSource{}
}

func (variablesDataSource *clusterVariablesDataSource) Metadata(_ context.Context, request datasource.MetadataRequest, response *datasource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_cluster_variables"
}

func (variablesDataSource *clusterVariablesDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, response *datasource.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: "Lists a cluster's variables. The counterpart of `ankra cluster variables list`.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Same as `cluster_id`.",
				Computed:            true,
			},
			"cluster_id": schema.StringAttribute{
				MarkdownDescription: "Identifier of the cluster.",
				Required:            true,
			},
			"variables": schema.ListNestedAttribute{
				MarkdownDescription: "Variables on the cluster, sorted by name.",
				Computed:            true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id":          schema.StringAttribute{MarkdownDescription: "Identifier of the variable.", Computed: true},
						"name":        schema.StringAttribute{MarkdownDescription: "Variable name.", Computed: true},
						"value":       schema.StringAttribute{MarkdownDescription: "Variable value.", Computed: true, Sensitive: true},
						"description": schema.StringAttribute{MarkdownDescription: "Free-text description.", Computed: true},
					},
				},
			},
		},
	}
}

func (variablesDataSource *clusterVariablesDataSource) Read(ctx context.Context, request datasource.ReadRequest, response *datasource.ReadResponse) {
	apiClient := variablesDataSource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var config clusterVariablesDataSourceModel
	response.Diagnostics.Append(request.Config.Get(ctx, &config)...)
	if response.Diagnostics.HasError() {
		return
	}

	variables, err := apiClient.ListClusterVariables(ctx, config.ClusterID.ValueString())
	if err != nil {
		response.Diagnostics.AddError("Unable to list cluster variables", err.Error())
		return
	}

	config.ID = config.ClusterID
	config.Variables = make([]clusterVariableItemModel, 0, len(variables))
	for _, variable := range variables {
		config.Variables = append(config.Variables, clusterVariableItemModel{
			ID:          types.StringValue(variable.ID),
			Name:        types.StringValue(variable.Name),
			Value:       types.StringValue(variable.Value),
			Description: types.StringPointerValue(variable.Description),
		})
	}
	response.Diagnostics.Append(response.State.Set(ctx, &config)...)
}

type clusterStackVariablesDataSource struct {
	configuredDataSourceClient
}

type clusterStackVariablesDataSourceModel struct {
	ID        types.String `tfsdk:"id"`
	ClusterID types.String `tfsdk:"cluster_id"`
	StackName types.String `tfsdk:"stack_name"`
	Variables types.Map    `tfsdk:"variables"`
}

// NewClusterStackVariablesDataSource returns the
// ankra_cluster_stack_variables data source, the counterpart of
// `ankra cluster stacks variables list`.
func NewClusterStackVariablesDataSource() datasource.DataSource {
	return &clusterStackVariablesDataSource{}
}

func (variablesDataSource *clusterStackVariablesDataSource) Metadata(_ context.Context, request datasource.MetadataRequest, response *datasource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_cluster_stack_variables"
}

func (variablesDataSource *clusterStackVariablesDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, response *datasource.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: "Reads the variables of one stack on a cluster. While the stack has an unpublished draft, " +
			"the platform reports the draft's variables. The counterpart of `ankra cluster stacks variables list`.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "`<cluster_id>/<stack_name>`.",
				Computed:            true,
			},
			"cluster_id": schema.StringAttribute{
				MarkdownDescription: "Identifier of the cluster.",
				Required:            true,
			},
			"stack_name": schema.StringAttribute{
				MarkdownDescription: "Name of the stack.",
				Required:            true,
			},
			"variables": schema.MapAttribute{
				MarkdownDescription: "Variable values by name.",
				Computed:            true,
				Sensitive:           true,
				ElementType:         types.StringType,
			},
		},
	}
}

func (variablesDataSource *clusterStackVariablesDataSource) Read(ctx context.Context, request datasource.ReadRequest, response *datasource.ReadResponse) {
	apiClient := variablesDataSource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var config clusterStackVariablesDataSourceModel
	response.Diagnostics.Append(request.Config.Get(ctx, &config)...)
	if response.Diagnostics.HasError() {
		return
	}

	stack, err := apiClient.GetClusterStackVariables(ctx, config.ClusterID.ValueString(), config.StackName.ValueString())
	if err != nil {
		response.Diagnostics.AddError("Unable to read stack variables", err.Error())
		return
	}

	variables, diagnostics := types.MapValueFrom(ctx, types.StringType, stack.Variables)
	response.Diagnostics.Append(diagnostics...)
	if response.Diagnostics.HasError() {
		return
	}
	config.ID = types.StringValue(config.ClusterID.ValueString() + "/" + config.StackName.ValueString())
	config.Variables = variables
	response.Diagnostics.Append(response.State.Set(ctx, &config)...)
}
