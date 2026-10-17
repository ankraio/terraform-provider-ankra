// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/resource"
)

// configurationResources are Helm registries, their credentials, and organisation, cluster and stack variables.
func configurationResources() []func() resource.Resource {
	return []func() resource.Resource{
		NewOrganisationVariableResource,
		NewHelmRegistryResource,
		NewHelmRegistryCredentialResource,
		NewClusterVariableResource,
	}
}

func configurationDataSources() []func() datasource.DataSource {
	return []func() datasource.DataSource{
		NewOrganisationVariablesDataSource,
		NewHelmRegistriesDataSource,
		NewHelmRegistryCredentialsDataSource,
		NewHelmChartDataSource,
		NewClusterVariablesDataSource,
	}
}
