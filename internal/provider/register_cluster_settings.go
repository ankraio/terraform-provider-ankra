// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/resource"
)

// clusterSettingResources are settings and attachments on an existing cluster.
func clusterSettingResources() []func() resource.Resource {
	return nil
}

func clusterSettingDataSources() []func() datasource.DataSource {
	return nil
}
