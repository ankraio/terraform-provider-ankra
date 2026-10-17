// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/resource"
)

// credentialResources are cloud provider and SSH key credentials.
func credentialResources() []func() resource.Resource {
	return nil
}

func credentialDataSources() []func() datasource.DataSource {
	return nil
}
