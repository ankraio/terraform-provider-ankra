// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/resource"
)

// organisationResources are the organisation itself, its members, tokens, MCP servers and CI and AI review settings.
func organisationResources() []func() resource.Resource {
	return nil
}

func organisationDataSources() []func() datasource.DataSource {
	return nil
}
