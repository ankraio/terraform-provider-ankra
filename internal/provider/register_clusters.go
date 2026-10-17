// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/resource"
)

// clusterResources are the imported cluster and the original cloud lanes.
func clusterResources() []func() resource.Resource {
	return []func() resource.Resource{
		NewClusterResource,
		NewHetznerClusterResource,
		NewDigitalOceanClusterResource,
		NewOVHClusterResource,
		NewScalewayClusterResource,
		NewUpCloudClusterResource,
	}
}

func clusterDataSources() []func() datasource.DataSource {
	return []func() datasource.DataSource{
		NewClustersDataSource,
	}
}
