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
	_ datasource.DataSource              = (*securityComplianceFrameworksDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*securityComplianceFrameworksDataSource)(nil)
)

type securityComplianceFrameworksDataSource struct {
	configuredDataSourceClient
}

type securityComplianceFrameworkItemModel struct {
	Key                   types.String `tfsdk:"key"`
	Title                 types.String `tfsdk:"title"`
	ShortTitle            types.String `tfsdk:"short_title"`
	Version               types.String `tfsdk:"version"`
	Description           types.String `tfsdk:"description"`
	ControlCount          types.Int64  `tfsdk:"control_count"`
	AutomatedControlCount types.Int64  `tfsdk:"automated_control_count"`
	Enabled               types.Bool   `tfsdk:"enabled"`
	EnabledAt             types.String `tfsdk:"enabled_at"`
}

type securityComplianceFrameworksDataSourceModel struct {
	ID         types.String                           `tfsdk:"id"`
	Frameworks []securityComplianceFrameworkItemModel `tfsdk:"frameworks"`
}

// NewSecurityComplianceFrameworksDataSource returns the
// ankra_security_compliance_frameworks data source: the framework catalogue
// `ankra security compliance frameworks` lists.
func NewSecurityComplianceFrameworksDataSource() datasource.DataSource {
	return &securityComplianceFrameworksDataSource{}
}

func (frameworksDataSource *securityComplianceFrameworksDataSource) Metadata(_ context.Context, request datasource.MetadataRequest, response *datasource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_security_compliance_frameworks"
}

func (frameworksDataSource *securityComplianceFrameworksDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, response *datasource.SchemaResponse) {
	computedString := func(description string) schema.StringAttribute {
		return schema.StringAttribute{MarkdownDescription: description, Computed: true}
	}
	response.Schema = schema.Schema{
		MarkdownDescription: "Lists the compliance framework catalogue and which frameworks the organisation tracks.",
		Attributes: map[string]schema.Attribute{
			"id": computedString("Placeholder identifier for the data source."),
			"frameworks": schema.ListNestedAttribute{
				MarkdownDescription: "Frameworks in the catalogue.",
				Computed:            true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"key":                     computedString("Framework key, as `ankra_security_compliance_framework` takes it."),
						"title":                   computedString("Framework title."),
						"short_title":             computedString("Short framework title."),
						"version":                 computedString("Framework version."),
						"description":             computedString("Framework description."),
						"control_count":           schema.Int64Attribute{MarkdownDescription: "Controls the framework defines.", Computed: true},
						"automated_control_count": schema.Int64Attribute{MarkdownDescription: "Controls the platform evaluates automatically.", Computed: true},
						"enabled":                 schema.BoolAttribute{MarkdownDescription: "Whether the organisation tracks the framework.", Computed: true},
						"enabled_at":              computedString("When the framework was enabled."),
					},
				},
			},
		},
	}
}

func (frameworksDataSource *securityComplianceFrameworksDataSource) Read(ctx context.Context, _ datasource.ReadRequest, response *datasource.ReadResponse) {
	apiClient := frameworksDataSource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}

	frameworks, err := apiClient.ListSecurityComplianceFrameworks(ctx)
	if err != nil {
		response.Diagnostics.AddError("Unable to list compliance frameworks", err.Error())
		return
	}

	state := securityComplianceFrameworksDataSourceModel{
		ID:         types.StringValue("ankra_security_compliance_frameworks"),
		Frameworks: make([]securityComplianceFrameworkItemModel, 0, len(frameworks)),
	}
	for _, framework := range frameworks {
		state.Frameworks = append(state.Frameworks, securityComplianceFrameworkItemModel{
			Key:                   types.StringValue(framework.Key),
			Title:                 types.StringValue(framework.Title),
			ShortTitle:            types.StringValue(framework.ShortTitle),
			Version:               types.StringValue(framework.Version),
			Description:           types.StringValue(framework.Description),
			ControlCount:          types.Int64Value(framework.ControlCount),
			AutomatedControlCount: types.Int64Value(framework.AutomatedControlCount),
			Enabled:               types.BoolValue(framework.Enabled),
			EnabledAt:             types.StringPointerValue(framework.EnabledAt),
		})
	}
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
}
