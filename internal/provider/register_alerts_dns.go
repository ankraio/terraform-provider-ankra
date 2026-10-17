// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/resource"
)

// alertAndDNSResources are alert destinations and routes, and organisation DNS.
func alertAndDNSResources() []func() resource.Resource {
	return nil
}

func alertAndDNSDataSources() []func() datasource.DataSource {
	return nil
}
