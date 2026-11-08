// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/resource"
)

// alertAndDNSResources are alert definitions, destinations and routes,
// organisation DNS, and the access gateway policies of Ankra Cloud DNS zones.
func alertAndDNSResources() []func() resource.Resource {
	return []func() resource.Resource{
		NewAlertDestinationResource,
		NewAlertRouteResource,
		NewAlertRuleResource,
		NewOrganisationDNSRecordResource,
		NewOrganisationCustomDNSZoneResource,
		NewOrganisationDomainResource,
		NewDNSCredentialResource,
		NewCloudflareCredentialResource,
		NewCloudflareDNSRecordResource,
		NewCloudDNSZoneAccessResource,
	}
}

func alertAndDNSDataSources() []func() datasource.DataSource {
	return []func() datasource.DataSource{
		NewAlertDestinationsDataSource,
		NewAlertRoutesDataSource,
		NewOrganisationDNSZoneDataSource,
		NewOrganisationDNSRecordsDataSource,
		NewOrganisationCustomDNSZonesDataSource,
		NewDNSCredentialsDataSource,
		NewCloudflareCredentialsDataSource,
		NewCloudflareDomainsDataSource,
		NewCloudflareDNSRecordsDataSource,
		NewSlackChannelsDataSource,
		NewTeamsChannelsDataSource,
		NewClusterDNSZonesDataSource,
		NewAlertIngestCredentialsDataSource,
		NewCloudDNSZonesDataSource,
	}
}
