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
	_ datasource.DataSource              = (*helmRegistriesDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*helmRegistriesDataSource)(nil)
)

type helmRegistriesDataSource struct {
	configuredDataSourceClient
}

type helmRegistryItemModel struct {
	Name           types.String `tfsdk:"name"`
	URL            types.String `tfsdk:"url"`
	Kind           types.String `tfsdk:"kind"`
	CredentialName types.String `tfsdk:"credential_name"`
	ExcludeCharts  types.List   `tfsdk:"exclude_charts"`
	IsGlobal       types.Bool   `tfsdk:"is_global"`
	ChartCount     types.Int64  `tfsdk:"chart_count"`
	LastSyncError  types.String `tfsdk:"last_sync_error"`
}

type helmRegistriesDataSourceModel struct {
	ID         types.String            `tfsdk:"id"`
	Registries []helmRegistryItemModel `tfsdk:"registries"`
}

// NewHelmRegistriesDataSource returns the ankra_helm_registries data source,
// the counterpart of `ankra helm registries list`.
func NewHelmRegistriesDataSource() datasource.DataSource {
	return &helmRegistriesDataSource{}
}

func (registriesDataSource *helmRegistriesDataSource) Metadata(_ context.Context, request datasource.MetadataRequest, response *datasource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_helm_registries"
}

func (registriesDataSource *helmRegistriesDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, response *datasource.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: "Lists the Helm registries the organisation sees, the platform's global registries included. " +
			"The counterpart of `ankra helm registries list`.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Placeholder identifier for the data source.",
				Computed:            true,
			},
			"registries": schema.ListNestedAttribute{
				MarkdownDescription: "Helm registries.",
				Computed:            true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"name":            schema.StringAttribute{MarkdownDescription: "Registry name, as an addon's `registry_name`.", Computed: true},
						"url":             schema.StringAttribute{MarkdownDescription: "Registry URL, as an addon's `registry_url`.", Computed: true},
						"kind":            schema.StringAttribute{MarkdownDescription: "`oci` or `http`.", Computed: true},
						"credential_name": schema.StringAttribute{MarkdownDescription: "Attached registry credential, if any.", Computed: true},
						"exclude_charts": schema.ListAttribute{
							MarkdownDescription: "Charts left out of the catalogue (HTTP registries).",
							Computed:            true,
							ElementType:         types.StringType,
						},
						"is_global":       schema.BoolAttribute{MarkdownDescription: "Whether the platform provides the registry to every organisation.", Computed: true},
						"chart_count":     schema.Int64Attribute{MarkdownDescription: "Charts indexed from the registry.", Computed: true},
						"last_sync_error": schema.StringAttribute{MarkdownDescription: "Why the most recent index sync failed, when it did.", Computed: true},
					},
				},
			},
		},
	}
}

func (registriesDataSource *helmRegistriesDataSource) Read(ctx context.Context, _ datasource.ReadRequest, response *datasource.ReadResponse) {
	apiClient := registriesDataSource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}

	registries, err := apiClient.ListHelmRegistries(ctx)
	if err != nil {
		response.Diagnostics.AddError("Unable to list Helm registries", err.Error())
		return
	}

	state := helmRegistriesDataSourceModel{
		ID:         types.StringValue("ankra_helm_registries"),
		Registries: make([]helmRegistryItemModel, 0, len(registries)),
	}
	for _, registry := range registries {
		state.Registries = append(state.Registries, helmRegistryItemModel{
			Name:           types.StringValue(registry.Name),
			URL:            types.StringValue(registry.URL),
			Kind:           types.StringValue(client.HelmRegistryKind(registry.URL)),
			CredentialName: optionalString(helmStringValueOrEmpty(registry.CredentialName)),
			ExcludeCharts:  listFromStrings(registry.ExcludeCharts),
			IsGlobal:       types.BoolValue(registry.IsGlobal),
			ChartCount:     types.Int64Value(registry.ChartCount),
			LastSyncError:  types.StringPointerValue(registry.LastSyncError),
		})
	}
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
}

func helmStringValueOrEmpty(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
