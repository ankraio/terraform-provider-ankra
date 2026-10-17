// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/resource"
)

// credentialResources are cloud provider and SSH key credentials.
func credentialResources() []func() resource.Resource {
	resources := []func() resource.Resource{NewSSHKeyCredentialResource}
	for _, kind := range providerCredentialKinds {
		resources = append(resources, newProviderCredentialResource(kind))
	}
	return resources
}

func credentialDataSources() []func() datasource.DataSource {
	return []func() datasource.DataSource{
		NewCredentialsDataSource,
		NewCredentialDataSource,
	}
}
