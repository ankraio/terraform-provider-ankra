// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/resource"
)

// catalogueAndStorageResources are stack profiles, backup vaults, buckets and the image registry.
func catalogueAndStorageResources() []func() resource.Resource {
	return nil
}

func catalogueAndStorageDataSources() []func() datasource.DataSource {
	return nil
}
