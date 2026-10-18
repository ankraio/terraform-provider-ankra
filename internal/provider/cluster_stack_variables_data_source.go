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
	_ datasource.DataSource              = (*clusterStackVariablesDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*clusterStackVariablesDataSource)(nil)
)

type clusterStackVariablesDataSource struct {
	configuredDataSourceClient
}

type clusterStackVariablesDataSourceModel struct {
	ID              types.String `tfsdk:"id"`
	ClusterID       types.String `tfsdk:"cluster_id"`
	StackName       types.String `tfsdk:"stack_name"`
	Variables       types.Map    `tfsdk:"variables"`
	DeployWave      types.Int64  `tfsdk:"deploy_wave"`
	Description     types.String `tfsdk:"description"`
	HasPendingDraft types.Bool   `tfsdk:"has_pending_draft"`
	DeletePending   types.Bool   `tfsdk:"delete_pending"`
}

// NewClusterStackVariablesDataSource returns the
// ankra_cluster_stack_variables data source: a stack's deployed variables.
func NewClusterStackVariablesDataSource() datasource.DataSource {
	return &clusterStackVariablesDataSource{}
}

func (variablesDataSource *clusterStackVariablesDataSource) Metadata(_ context.Context, request datasource.MetadataRequest, response *datasource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_cluster_stack_variables"
}

func (variablesDataSource *clusterStackVariablesDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, response *datasource.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: "Reads the variables a stack on a cluster is deployed with, whoever set them. A pending draft's values are " +
			"never included; `has_pending_draft` says whether one exists. Set individual keys with `ankra_cluster_stack_variable`.",
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
				MarkdownDescription: "The deployed variables, by name. Marked sensitive, like the resource's `value`.",
				ElementType:         types.StringType,
				Computed:            true,
				Sensitive:           true,
			},
			"deploy_wave": schema.Int64Attribute{
				MarkdownDescription: "The stack's deploy wave; null when it has none.",
				Computed:            true,
			},
			"description": schema.StringAttribute{
				MarkdownDescription: "The stack's description; null when it has none.",
				Computed:            true,
			},
			"has_pending_draft": schema.BoolAttribute{
				MarkdownDescription: "Whether a draft of the stack is waiting to be published. A draft that edits stack-level fields " +
					"replaces these variables when it is published.",
				Computed: true,
			},
			"delete_pending": schema.BoolAttribute{
				MarkdownDescription: "Whether the stack is being deleted.",
				Computed:            true,
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

	deployed, err := apiClient.GetStackVariables(ctx, config.ClusterID.ValueString(), config.StackName.ValueString())
	if err != nil {
		response.Diagnostics.AddError("Unable to read stack variables", clusterVariableErrorDetail(err))
		return
	}

	elements := make(map[string]attr.Value, len(deployed.Variables))
	for name, value := range deployed.Variables {
		elements[name] = types.StringValue(value)
	}
	config.ID = types.StringValue(config.ClusterID.ValueString() + "/" + config.StackName.ValueString())
	config.Variables = types.MapValueMust(types.StringType, elements)
	config.DeployWave = types.Int64PointerValue(deployed.DeployWave)
	config.Description = types.StringPointerValue(deployed.Description)
	config.HasPendingDraft = types.BoolValue(deployed.HasPendingDraft)
	config.DeletePending = types.BoolValue(deployed.DeletePending)
	response.Diagnostics.Append(response.State.Set(ctx, &config)...)
}
