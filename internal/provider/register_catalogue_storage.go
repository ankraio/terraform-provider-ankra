// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/resource"
)

// catalogueAndStorageResources are stack profiles, backup vaults, buckets and the image registry.
func catalogueAndStorageResources() []func() resource.Resource {
	return []func() resource.Resource{
		NewStackProfileResource,
		NewStackProfileVersionResource,
		NewStackProfileShareResource,
		NewStackProfileLogoResource,
		NewBackupVaultResource,
		NewObjectStorageBucketResource,
		NewRegistryProjectResource,
		NewRegistryRobotResource,
		NewRegistryRetentionResource,
	}
}

func catalogueAndStorageDataSources() []func() datasource.DataSource {
	return []func() datasource.DataSource{
		NewStackProfilesDataSource,
		NewBackupVaultsDataSource,
		NewObjectStorageBucketsDataSource,
		NewRegistryProjectsDataSource,
		NewRegistryRobotsDataSource,
	}
}
