// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/resource"
)

// aiResources are the organisation's AI provider credentials, endpoints, model catalogue, lane models and board identity.
func aiResources() []func() resource.Resource {
	return []func() resource.Resource{
		NewAIProviderResource,
		NewAIAnthropicKeyResource,
		NewAIOpenRouterKeyResource,
		NewAIOpenAICompatibleResource,
		NewAIEndpointResource,
		NewAIModelResource,
		NewAILaneModelResource,
		NewAIBoardIdentityResource,
	}
}

func aiDataSources() []func() datasource.DataSource {
	return []func() datasource.DataSource{
		NewAIModelsDataSource,
		NewAIEndpointsDataSource,
		NewAILanesDataSource,
	}
}

// targetResources are host deploy targets' join tokens and the managed-service placement policies and consumer bindings.
func targetResources() []func() resource.Resource {
	return []func() resource.Resource{
		NewDeployHostJoinTokenResource,
		NewServicePlacementPolicyResource,
		NewServiceConsumerResource,
	}
}

func targetDataSources() []func() datasource.DataSource {
	return []func() datasource.DataSource{
		NewDeployHostTargetsDataSource,
		NewDeployEnvironmentsDataSource,
	}
}
