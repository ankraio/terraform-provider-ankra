// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/resource"
)

// organisationResources are the organisation itself, its members, roles, tokens, secret slots, projects, OIDC trust policies, MCP servers,
// CI and AI review settings, the AI ticket desk and its sync connections, and the pipeline and provenance policies.
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
		NewOrganisationRoleResource,
		NewAITicketSettingsResource,
		NewAITicketProjectChannelsResource,
		NewAITicketSyncConnectionResource,
		NewPipelinePolicyResource,
		NewProvenancePolicyResource,
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
