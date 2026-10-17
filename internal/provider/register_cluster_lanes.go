// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/resource"
)

// clusterLaneResources are the cluster lanes beyond the original five clouds, managed Kubernetes, node pools and node groups.
func clusterLaneResources() []func() resource.Resource {
	return []func() resource.Resource{
		NewAWSClusterResource,
		NewProxmoxClusterResource,
		NewMorpheusClusterResource,
		NewAnkraCloudClusterResource,
		NewManagedClusterResource,
		NewManagedNodePoolResource,
		NewNodeGroupResource,
		NewPlaygroundResource,
	}
}

func clusterLaneDataSources() []func() datasource.DataSource {
	return []func() datasource.DataSource{
		NewKubernetesVersionsDataSource,
		NewPlaygroundPlansDataSource,
		NewNodeGroupsDataSource,
	}
}
