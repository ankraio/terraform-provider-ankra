// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/resource"
)

// platformServiceResources are AI providers and models, cost, security, services and targets.
func platformServiceResources() []func() resource.Resource {
	resources := aiResources()
	resources = append(resources, costResources()...)
	resources = append(resources, securityResources()...)
	resources = append(resources, targetResources()...)
	return resources
}

func platformServiceDataSources() []func() datasource.DataSource {
	dataSources := aiDataSources()
	dataSources = append(dataSources, costDataSources()...)
	dataSources = append(dataSources, securityDataSources()...)
	dataSources = append(dataSources, targetDataSources()...)
	return dataSources
}
