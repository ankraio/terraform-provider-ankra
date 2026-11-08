// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/resource"
)

// organisationResources are the organisation itself, its members, tokens, secret slots, projects, OIDC trust policies, MCP servers and CI and AI review settings.
func organisationResources() []func() resource.Resource {
	return []func() resource.Resource{
		NewOrganisationResource,
		NewOrganisationMemberResource,
		NewAPITokenResource,
		NewServiceTokenResource,
		NewMCPServerResource,
		NewMCPToolGrantResource,
		NewCISettingsResource,
		NewCIPoolMemberResource,
		NewAIReviewRepositoryResource,
		NewAIReviewRelatedRepositoryResource,
		NewAISpendCapResource,
		NewAIEnvironmentResource,
		NewAIReviewBindingResource,
		NewSecretSlotResource,
		NewProjectResource,
		NewOIDCTrustPolicyResource,
	}
}

func organisationDataSources() []func() datasource.DataSource {
	return []func() datasource.DataSource{
		NewOrganisationDataSource,
		NewOrganisationsDataSource,
		NewMCPServersDataSource,
		NewOrganisationRolesDataSource,
		NewAPITokensDataSource,
		NewServiceTokensDataSource,
		NewProjectDataSource,
		NewProjectsDataSource,
		NewOIDCTrustPoliciesDataSource,
		NewClusterAccessPolicyDataSource,
	}
}
