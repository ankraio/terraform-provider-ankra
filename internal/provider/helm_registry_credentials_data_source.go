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
	_ datasource.DataSource              = (*helmRegistryCredentialsDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*helmRegistryCredentialsDataSource)(nil)
)

type helmRegistryCredentialsDataSource struct {
	configuredDataSourceClient
}

type helmRegistryCredentialItemModel struct {
	ID        types.String `tfsdk:"id"`
	Name      types.String `tfsdk:"name"`
	CreatedAt types.String `tfsdk:"created_at"`
}

type helmRegistryCredentialsDataSourceModel struct {
	ID          types.String                      `tfsdk:"id"`
	Credentials []helmRegistryCredentialItemModel `tfsdk:"credentials"`
}

// NewHelmRegistryCredentialsDataSource returns the
// ankra_helm_registry_credentials data source, the counterpart of
// `ankra helm credentials list`.
func NewHelmRegistryCredentialsDataSource() datasource.DataSource {
	return &helmRegistryCredentialsDataSource{}
}

func (credentialsDataSource *helmRegistryCredentialsDataSource) Metadata(_ context.Context, request datasource.MetadataRequest, response *datasource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_helm_registry_credentials"
}

func (credentialsDataSource *helmRegistryCredentialsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, response *datasource.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: "Lists the organisation's Helm registry credentials by name, for an addon's `registry_credential_name`. " +
			"Usernames and passwords are not listed. The counterpart of `ankra helm credentials list`.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Placeholder identifier for the data source.",
				Computed:            true,
			},
			"credentials": schema.ListNestedAttribute{
				MarkdownDescription: "Registry credentials.",
				Computed:            true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id":         schema.StringAttribute{MarkdownDescription: "Identifier of the credential.", Computed: true},
						"name":       schema.StringAttribute{MarkdownDescription: "Credential name.", Computed: true},
						"created_at": schema.StringAttribute{MarkdownDescription: "When the credential was created.", Computed: true},
					},
				},
			},
		},
	}
}

func (credentialsDataSource *helmRegistryCredentialsDataSource) Read(ctx context.Context, _ datasource.ReadRequest, response *datasource.ReadResponse) {
	apiClient := credentialsDataSource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}

	credentials, err := apiClient.ListHelmRegistryCredentials(ctx)
	if err != nil {
		response.Diagnostics.AddError("Unable to list Helm registry credentials", err.Error())
		return
	}

	state := helmRegistryCredentialsDataSourceModel{
		ID:          types.StringValue("ankra_helm_registry_credentials"),
		Credentials: make([]helmRegistryCredentialItemModel, 0, len(credentials)),
	}
	for _, credential := range credentials {
		state.Credentials = append(state.Credentials, helmRegistryCredentialItemModel{
			ID:        types.StringValue(credential.ID),
			Name:      types.StringValue(credential.Name),
			CreatedAt: optionalString(credential.CreatedAt),
		})
	}
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
}
