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
	_ datasource.DataSource              = (*organisationVariablesDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*organisationVariablesDataSource)(nil)
)

type organisationVariablesDataSource struct {
	configuredDataSourceClient
}

type organisationVariableItemModel struct {
	ID          types.String `tfsdk:"id"`
	Name        types.String `tfsdk:"name"`
	Value       types.String `tfsdk:"value"`
	Description types.String `tfsdk:"description"`
	IsSecret    types.Bool   `tfsdk:"is_secret"`
}

type organisationVariablesDataSourceModel struct {
	ID        types.String                    `tfsdk:"id"`
	Variables []organisationVariableItemModel `tfsdk:"variables"`
}

// NewOrganisationVariablesDataSource returns the ankra_organisation_variables
// data source, the counterpart of `ankra org variables list`.
func NewOrganisationVariablesDataSource() datasource.DataSource {
	return &organisationVariablesDataSource{}
}

func (variablesDataSource *organisationVariablesDataSource) Metadata(_ context.Context, request datasource.MetadataRequest, response *datasource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_organisation_variables"
}

func (variablesDataSource *organisationVariablesDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, response *datasource.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: "Lists the organisation's variables. The counterpart of `ankra org variables list`.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Placeholder identifier for the data source.",
				Computed:            true,
			},
			"variables": schema.ListNestedAttribute{
				MarkdownDescription: "Variables in the organisation.",
				Computed:            true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id": schema.StringAttribute{
							MarkdownDescription: "Identifier of the variable.",
							Computed:            true,
						},
						"name": schema.StringAttribute{
							MarkdownDescription: "Variable name.",
							Computed:            true,
						},
						"value": schema.StringAttribute{
							MarkdownDescription: "Variable value; null for a secret variable.",
							Computed:            true,
							Sensitive:           true,
						},
						"description": schema.StringAttribute{
							MarkdownDescription: "Free-text description.",
							Computed:            true,
						},
						"is_secret": schema.BoolAttribute{
							MarkdownDescription: "Whether the value is a secret.",
							Computed:            true,
						},
					},
				},
			},
		},
	}
}

func (variablesDataSource *organisationVariablesDataSource) Read(ctx context.Context, _ datasource.ReadRequest, response *datasource.ReadResponse) {
	apiClient := variablesDataSource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}

	variables, err := apiClient.ListOrganisationVariables(ctx)
	if err != nil {
		response.Diagnostics.AddError("Unable to list organisation variables", err.Error())
		return
	}

	state := organisationVariablesDataSourceModel{
		ID:        types.StringValue("ankra_organisation_variables"),
		Variables: make([]organisationVariableItemModel, 0, len(variables)),
	}
	for _, variable := range variables {
		state.Variables = append(state.Variables, organisationVariableItemModel{
			ID:          types.StringValue(variable.ID),
			Name:        types.StringValue(variable.Name),
			Value:       types.StringPointerValue(variable.Value),
			Description: types.StringPointerValue(variable.Description),
			IsSecret:    types.BoolValue(variable.IsSecret),
		})
	}
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
}
