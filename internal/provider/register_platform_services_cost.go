// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/resource"
)

// costResources are cost budgets, the autopilot policy and its per-cluster
// overrides, and the pricing settings.
func costResources() []func() resource.Resource {
	return []func() resource.Resource{
		NewCostBudgetResource,
		NewCostAutopilotResource,
		NewCostAutopilotOverrideResource,
		NewCostSettingsResource,
	}
}

func costDataSources() []func() datasource.DataSource {
	return []func() datasource.DataSource{
		NewCostBudgetsDataSource,
	}
}
