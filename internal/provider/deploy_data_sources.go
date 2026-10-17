// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ datasource.DataSource              = (*deployHostTargetsDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*deployHostTargetsDataSource)(nil)
	_ datasource.DataSource              = (*deployEnvironmentsDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*deployEnvironmentsDataSource)(nil)
)

type deployHostTargetsDataSource struct {
	configuredDataSourceClient
}

type deployHostTargetItemModel struct {
	ID              types.String `tfsdk:"id"`
	Name            types.String `tfsdk:"name"`
	Environment     types.String `tfsdk:"environment"`
	EnvironmentID   types.String `tfsdk:"environment_id"`
	Labels          types.Map    `tfsdk:"labels"`
	Status          types.String `tfsdk:"status"`
	AgentVersion    types.String `tfsdk:"agent_version"`
	Hostname        types.String `tfsdk:"hostname"`
	OS              types.String `tfsdk:"os"`
	Arch            types.String `tfsdk:"arch"`
	LastHeartbeatAt types.String `tfsdk:"last_heartbeat_at"`
	RegisteredAt    types.String `tfsdk:"registered_at"`
	RevokedAt       types.String `tfsdk:"revoked_at"`
}

type deployHostTargetsDataSourceModel struct {
	ID          types.String                `tfsdk:"id"`
	Environment types.String                `tfsdk:"environment"`
	Truncated   types.Bool                  `tfsdk:"truncated"`
	HostTargets []deployHostTargetItemModel `tfsdk:"host_targets"`
}

// NewDeployHostTargetsDataSource returns the ankra_host_targets data source,
// the counterpart of `ankra targets list`.
func NewDeployHostTargetsDataSource() datasource.DataSource {
	return &deployHostTargetsDataSource{}
}

func (targetsDataSource *deployHostTargetsDataSource) Metadata(_ context.Context, request datasource.MetadataRequest, response *datasource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_host_targets"
}

func (targetsDataSource *deployHostTargetsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, response *datasource.SchemaResponse) {
	computed := func(description string) schema.StringAttribute {
		return schema.StringAttribute{MarkdownDescription: description, Computed: true}
	}
	response.Schema = schema.Schema{
		MarkdownDescription: "Lists the hosts registered as deploy targets in the provider's project, revoked ones included. " +
			"The counterpart of `ankra targets list` and `ankra targets get`.",
		Attributes: map[string]schema.Attribute{
			"id": computed("Placeholder identifier for the data source."),
			"environment": schema.StringAttribute{
				MarkdownDescription: "Only list targets in this environment. The platform answers an error for an environment that does not exist.",
				Optional:            true,
				Validators:          []validator.String{deployPatternValidator{pattern: deployEnvironmentNamePattern, subject: "environment name"}},
			},
			"truncated": schema.BoolAttribute{
				MarkdownDescription: "Whether the platform stopped the listing at its row cap.",
				Computed:            true,
			},
			"host_targets": schema.ListNestedAttribute{
				MarkdownDescription: "Host targets.",
				Computed:            true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id":             computed("Identifier of the host target."),
						"name":           computed("Name, unique in its environment."),
						"environment":    computed("Environment name."),
						"environment_id": computed("Environment identifier."),
						"labels": schema.MapAttribute{
							MarkdownDescription: "Labels deploy stages select targets by.",
							Computed:            true,
							ElementType:         types.StringType,
						},
						"status":            computed("`online`, `offline`, or `unknown` for a host that never checked in."),
						"agent_version":     computed("Agent version the host reported."),
						"hostname":          computed("Hostname the host reported."),
						"os":                computed("Operating system the host reported."),
						"arch":              computed("Architecture the host reported."),
						"last_heartbeat_at": computed("Last heartbeat."),
						"registered_at":     computed("When the host registered."),
						"revoked_at":        computed("When the target was revoked; null while active."),
					},
				},
			},
		},
	}
}

func (targetsDataSource *deployHostTargetsDataSource) Read(ctx context.Context, request datasource.ReadRequest, response *datasource.ReadResponse) {
	apiClient := targetsDataSource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var configuration deployHostTargetsDataSourceModel
	response.Diagnostics.Append(request.Config.Get(ctx, &configuration)...)
	if response.Diagnostics.HasError() {
		return
	}
	targets, truncated, err := apiClient.ListDeployHostTargets(ctx, configuration.Environment.ValueString())
	if err != nil {
		response.Diagnostics.AddError("Unable to list host targets", err.Error())
		return
	}
	state := deployHostTargetsDataSourceModel{
		ID:          types.StringValue("ankra_host_targets"),
		Environment: configuration.Environment,
		Truncated:   types.BoolValue(truncated),
		HostTargets: make([]deployHostTargetItemModel, 0, len(targets)),
	}
	for _, target := range targets {
		labels, diagnostics := types.MapValueFrom(ctx, types.StringType, target.Labels)
		response.Diagnostics.Append(diagnostics...)
		if target.Labels == nil {
			labels = types.MapValueMust(types.StringType, nil)
		}
		state.HostTargets = append(state.HostTargets, deployHostTargetItemModel{
			ID:              types.StringValue(target.ID),
			Name:            types.StringValue(target.Name),
			Environment:     types.StringValue(target.Environment),
			EnvironmentID:   types.StringValue(target.EnvironmentID),
			Labels:          labels,
			Status:          types.StringValue(target.Status),
			AgentVersion:    types.StringPointerValue(target.AgentVersion),
			Hostname:        types.StringPointerValue(target.Hostname),
			OS:              types.StringPointerValue(target.OS),
			Arch:            types.StringPointerValue(target.Arch),
			LastHeartbeatAt: types.StringPointerValue(target.LastHeartbeatAt),
			RegisteredAt:    types.StringValue(target.RegisteredAt),
			RevokedAt:       types.StringPointerValue(target.RevokedAt),
		})
	}
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
}

type deployEnvironmentsDataSource struct {
	configuredDataSourceClient
}

type deployEnvironmentItemModel struct {
	ID        types.String `tfsdk:"id"`
	Name      types.String `tfsdk:"name"`
	CreatedAt types.String `tfsdk:"created_at"`
}

type deployEnvironmentsDataSourceModel struct {
	ID           types.String                 `tfsdk:"id"`
	Truncated    types.Bool                   `tfsdk:"truncated"`
	Environments []deployEnvironmentItemModel `tfsdk:"environments"`
}

// NewDeployEnvironmentsDataSource returns the ankra_deploy_environments data
// source: the environments host targets register into.
func NewDeployEnvironmentsDataSource() datasource.DataSource {
	return &deployEnvironmentsDataSource{}
}

func (environmentsDataSource *deployEnvironmentsDataSource) Metadata(_ context.Context, request datasource.MetadataRequest, response *datasource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_deploy_environments"
}

func (environmentsDataSource *deployEnvironmentsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, response *datasource.SchemaResponse) {
	computed := func(description string) schema.StringAttribute {
		return schema.StringAttribute{MarkdownDescription: description, Computed: true}
	}
	response.Schema = schema.Schema{
		MarkdownDescription: "Lists the deploy environments of the provider's project: the environments host targets register into and deploy stages name. " +
			"An environment is created by the first `ankra_host_join_token` minted for it.",
		Attributes: map[string]schema.Attribute{
			"id": computed("Placeholder identifier for the data source."),
			"truncated": schema.BoolAttribute{
				MarkdownDescription: "Whether the platform stopped the listing at its row cap.",
				Computed:            true,
			},
			"environments": schema.ListNestedAttribute{
				MarkdownDescription: "Deploy environments.",
				Computed:            true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id":         computed("Identifier of the environment."),
						"name":       computed("Environment name."),
						"created_at": computed("When the environment was created."),
					},
				},
			},
		},
	}
}

func (environmentsDataSource *deployEnvironmentsDataSource) Read(ctx context.Context, _ datasource.ReadRequest, response *datasource.ReadResponse) {
	apiClient := environmentsDataSource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	environments, truncated, err := apiClient.ListDeployEnvironments(ctx)
	if err != nil {
		response.Diagnostics.AddError("Unable to list deploy environments", err.Error())
		return
	}
	state := deployEnvironmentsDataSourceModel{
		ID:           types.StringValue("ankra_deploy_environments"),
		Truncated:    types.BoolValue(truncated),
		Environments: make([]deployEnvironmentItemModel, 0, len(environments)),
	}
	for _, environment := range environments {
		state.Environments = append(state.Environments, deployEnvironmentItemModel{
			ID:        types.StringValue(environment.ID),
			Name:      types.StringValue(environment.Name),
			CreatedAt: types.StringValue(environment.CreatedAt),
		})
	}
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
}
