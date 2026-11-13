// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/resource"
)

// applicationResources are applications, their autonomy, environments, backup and provenance settings, and Ankra Pipelines.
func applicationResources() []func() resource.Resource {
	return []func() resource.Resource{
		NewApplicationResource,
		NewApplicationSettingsResource,
		NewApplicationAutoDeployResource,
		NewApplicationAIConfigResource,
		NewApplicationDemoConfigResource,
		NewApplicationEnvSecretResource,
		NewApplicationDeploymentResource,
		NewPipelineRepositoryResource,
		NewPipelineDefinitionResource,
		NewPipelineScheduleResource,
		NewPipelineMergeTrainResource,
		NewApplicationAutonomyResource,
		NewApplicationEnvironmentResource,
		NewApplicationDatabaseBackupResource,
		NewApplicationProvenancePolicyResource,
		NewPipelineWorkspaceProfilesResource,
	}
}

func applicationDataSources() []func() datasource.DataSource {
	return []func() datasource.DataSource{
		NewApplicationsDataSource,
		NewApplicationDataSource,
		NewPipelineRepositoriesDataSource,
	}
}
