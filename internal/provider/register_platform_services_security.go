// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/resource"
)

// securityResources are finding dispositions, compliance frameworks and
// per-cluster security report schedules.
func securityResources() []func() resource.Resource {
	return []func() resource.Resource{
		NewSecurityDispositionResource,
		NewSecurityComplianceFrameworkResource,
		NewSecurityReportScheduleResource,
	}
}

func securityDataSources() []func() datasource.DataSource {
	return []func() datasource.DataSource{
		NewSecurityComplianceFrameworksDataSource,
	}
}
