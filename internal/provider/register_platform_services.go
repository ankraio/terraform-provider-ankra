// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/resource"
)

// platformServiceResources are AI providers and models, cost, security, services and targets.
func platformServiceResources() []func() resource.Resource {
	return nil
}

func platformServiceDataSources() []func() datasource.DataSource {
	return nil
}
