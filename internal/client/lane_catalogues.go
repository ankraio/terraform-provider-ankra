// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package client

import (
	"context"
	"net/http"
	"net/url"
)

// The catalogue reads below list what a lane's credential can build with:
// regions, sizes and networks on AWS, hosts and their bridges, storages and
// templates on Proxmox VE, groups, clouds, plans, layouts and networks on HPE
// Morpheus, and zones and plans on Ankra Cloud. Each is a live call through
// the named credential (cluster go/internal/providerapi aws.go, ankracloud.go
// and clusters.go), so the answer is whatever the cloud reports right now.

// AWSRegion is one enabled region of the AWS account.
type AWSRegion struct {
	Slug string `json:"slug"`
	Name string `json:"name"`
}

// AWSInstanceType is one EC2 instance type offered in a region. A nil price
// is unknown, never free.
type AWSInstanceType struct {
	Name              string   `json:"name"`
	VCPUs             int64    `json:"vcpus"`
	MemoryGiB         float64  `json:"memory_gib"`
	Architecture      string   `json:"architecture"`
	Category          string   `json:"category"`
	HourlyPriceUSD    *float64 `json:"hourly_price_usd"`
	MonthlyPriceUSD   *float64 `json:"monthly_price_usd"`
	CurrentGeneration bool     `json:"current_generation"`
}

// AWSVPC is one VPC of a region.
type AWSVPC struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	CIDR      string `json:"cidr"`
	IsDefault bool   `json:"is_default"`
}

// AWSSubnetEgress classifies where a subnet's IPv4 default route leads.
type AWSSubnetEgress struct {
	Kind string `json:"kind"`
}

// AWSSubnet is one subnet of a VPC.
type AWSSubnet struct {
	ID                  string          `json:"id"`
	Name                string          `json:"name"`
	CIDR                string          `json:"cidr"`
	AvailabilityZone    string          `json:"availability_zone"`
	MapPublicIPOnLaunch bool            `json:"map_public_ip_on_launch"`
	Egress              AWSSubnetEgress `json:"egress"`
}

// ProxmoxHost is one node of the Proxmox VE cluster behind a credential.
type ProxmoxHost struct {
	Node        string `json:"node"`
	Status      string `json:"status"`
	CPUCount    int64  `json:"cpu_count"`
	MemoryBytes int64  `json:"memory_bytes"`
}

// ProxmoxBridge is a host Linux bridge (source "bridge") or an
// Ankra-managed SDN vnet (source "sdn") on a Proxmox VE node.
type ProxmoxBridge struct {
	Name     string  `json:"name"`
	Active   bool    `json:"active"`
	CIDR     *string `json:"cidr"`
	Comments *string `json:"comments"`
	Source   string  `json:"source"`
}

// ProxmoxStorage is one storage of a Proxmox VE node.
type ProxmoxStorage struct {
	Storage        string   `json:"storage"`
	Type           string   `json:"type"`
	Content        []string `json:"content"`
	Active         bool     `json:"active"`
	Shared         bool     `json:"shared"`
	AvailableBytes int64    `json:"available_bytes"`
	TotalBytes     int64    `json:"total_bytes"`
}

// ProxmoxTemplate is one VM template on a Proxmox VE node.
type ProxmoxTemplate struct {
	VMID int64  `json:"vmid"`
	Name string `json:"name"`
	Node string `json:"node"`
}

// MorpheusGroup is one HPE Morpheus group.
type MorpheusGroup struct {
	ID       int64   `json:"id"`
	Name     string  `json:"name"`
	Location *string `json:"location"`
}

// MorpheusCloud is one HPE Morpheus cloud.
type MorpheusCloud struct {
	ID        int64   `json:"id"`
	Name      string  `json:"name"`
	CloudType *string `json:"cloud_type"`
}

// MorpheusPlan is one HPE Morpheus service plan.
type MorpheusPlan struct {
	ID             int64   `json:"id"`
	Name           string  `json:"name"`
	Code           *string `json:"code"`
	MaxCores       *int64  `json:"max_cores"`
	MaxMemoryBytes *int64  `json:"max_memory_bytes"`
}

// MorpheusLayout is one HPE Morpheus instance-type layout.
type MorpheusLayout struct {
	ID               int64   `json:"id"`
	Name             string  `json:"name"`
	InstanceTypeID   int64   `json:"instance_type_id"`
	InstanceTypeName string  `json:"instance_type_name"`
	ProvisionType    *string `json:"provision_type"`
}

// MorpheusNetwork is one HPE Morpheus network.
type MorpheusNetwork struct {
	ID      int64   `json:"id"`
	Name    string  `json:"name"`
	CIDR    *string `json:"cidr"`
	CloudID *int64  `json:"cloud_id"`
}

// AnkraCloudZone is one Ankra Cloud zone a cluster can be deployed in.
type AnkraCloudZone struct {
	Name        string `json:"name"`
	Region      string `json:"region"`
	DisplayName string `json:"display_name"`
	Country     string `json:"country"`
}

// AnkraCloudPlan is one Ankra Cloud server plan.
type AnkraCloudPlan struct {
	Name              string `json:"name"`
	Family            string `json:"family"`
	Category          string `json:"category"`
	Cores             int64  `json:"cores"`
	MemoryMebibytes   int64  `json:"memory_mebibytes"`
	StorageGibibytes  int64  `json:"storage_gibibytes"`
	PriceMonthlyCents int64  `json:"price_monthly_cents"`
	Available         bool   `json:"available"`
}

// CataloguePricing is the completeness report the AWS and Ankra Cloud
// catalogues carry next to their list: Complete is false when some prices
// (or, for Ankra Cloud zones, some zone capabilities) could not be read,
// and IncompleteReasons says which.
type CataloguePricing struct {
	Complete          bool     `json:"pricing_complete"`
	IncompleteReasons []string `json:"incomplete_reasons"`
}

// laneCatalogueQuery builds a catalogue query from name/value pairs,
// leaving out empty values.
func laneCatalogueQuery(pairs ...string) string {
	query := url.Values{}
	for index := 0; index+1 < len(pairs); index += 2 {
		if pairs[index+1] != "" {
			query.Set(pairs[index], pairs[index+1])
		}
	}
	if len(query) == 0 {
		return ""
	}
	return "?" + query.Encode()
}

func (client *Client) getLaneCatalogue(ctx context.Context, lane, catalogue, query string, target any) error {
	return client.doRequest(ctx, http.MethodGet, "/api/v1/clusters/"+url.PathEscape(lane)+"/"+catalogue+query, nil, target)
}

// ListAWSRegions lists the regions enabled for an AWS credential.
func (client *Client) ListAWSRegions(ctx context.Context, credentialID string) ([]AWSRegion, CataloguePricing, error) {
	var envelope struct {
		Regions []AWSRegion `json:"regions"`
		CataloguePricing
	}
	err := client.getLaneCatalogue(ctx, LaneAWS, "regions", laneCatalogueQuery("credential_id", credentialID), &envelope)
	return envelope.Regions, envelope.CataloguePricing, err
}

// ListAWSInstanceTypes lists the EC2 instance types offered in a region.
func (client *Client) ListAWSInstanceTypes(ctx context.Context, credentialID, region string) ([]AWSInstanceType, CataloguePricing, error) {
	var envelope struct {
		InstanceTypes []AWSInstanceType `json:"instance_types"`
		CataloguePricing
	}
	err := client.getLaneCatalogue(ctx, LaneAWS, "instance-types", laneCatalogueQuery("credential_id", credentialID, "region", region), &envelope)
	return envelope.InstanceTypes, envelope.CataloguePricing, err
}

// ListAWSVPCs lists the VPCs of a region.
func (client *Client) ListAWSVPCs(ctx context.Context, credentialID, region string) ([]AWSVPC, error) {
	var envelope struct {
		VPCs []AWSVPC `json:"vpcs"`
	}
	err := client.getLaneCatalogue(ctx, LaneAWS, "vpcs", laneCatalogueQuery("credential_id", credentialID, "region", region), &envelope)
	return envelope.VPCs, err
}

// ListAWSSubnets lists the subnets of a VPC.
func (client *Client) ListAWSSubnets(ctx context.Context, credentialID, region, vpcID string) ([]AWSSubnet, error) {
	var envelope struct {
		Subnets []AWSSubnet `json:"subnets"`
	}
	err := client.getLaneCatalogue(ctx, LaneAWS, "subnets",
		laneCatalogueQuery("credential_id", credentialID, "region", region, "vpc_id", vpcID), &envelope)
	return envelope.Subnets, err
}

// ListProxmoxHosts lists the nodes of the Proxmox VE cluster behind a
// credential.
func (client *Client) ListProxmoxHosts(ctx context.Context, credentialID string) ([]ProxmoxHost, error) {
	var hosts []ProxmoxHost
	err := client.getLaneCatalogue(ctx, LaneProxmox, "nodes", laneCatalogueQuery("credential_id", credentialID), &hosts)
	return hosts, err
}

// ListProxmoxBridges lists the bridges and SDN vnets of a Proxmox VE node.
func (client *Client) ListProxmoxBridges(ctx context.Context, credentialID, node string) ([]ProxmoxBridge, error) {
	var bridges []ProxmoxBridge
	err := client.getLaneCatalogue(ctx, LaneProxmox, "bridges", laneCatalogueQuery("credential_id", credentialID, "node", node), &bridges)
	return bridges, err
}

// ListProxmoxStorages lists the storages of a Proxmox VE node.
func (client *Client) ListProxmoxStorages(ctx context.Context, credentialID, node string) ([]ProxmoxStorage, error) {
	var storages []ProxmoxStorage
	err := client.getLaneCatalogue(ctx, LaneProxmox, "storages", laneCatalogueQuery("credential_id", credentialID, "node", node), &storages)
	return storages, err
}

// ListProxmoxTemplates lists the VM templates of a Proxmox VE node.
func (client *Client) ListProxmoxTemplates(ctx context.Context, credentialID, node string) ([]ProxmoxTemplate, error) {
	var templates []ProxmoxTemplate
	err := client.getLaneCatalogue(ctx, LaneProxmox, "templates", laneCatalogueQuery("credential_id", credentialID, "node", node), &templates)
	return templates, err
}

// ListMorpheusGroups lists the HPE Morpheus groups behind a credential.
func (client *Client) ListMorpheusGroups(ctx context.Context, credentialID string) ([]MorpheusGroup, error) {
	var groups []MorpheusGroup
	err := client.getLaneCatalogue(ctx, LaneMorpheus, "groups", laneCatalogueQuery("credential_id", credentialID), &groups)
	return groups, err
}

// ListMorpheusClouds lists the HPE Morpheus clouds behind a credential.
func (client *Client) ListMorpheusClouds(ctx context.Context, credentialID string) ([]MorpheusCloud, error) {
	var clouds []MorpheusCloud
	err := client.getLaneCatalogue(ctx, LaneMorpheus, "clouds", laneCatalogueQuery("credential_id", credentialID), &clouds)
	return clouds, err
}

// ListMorpheusPlans lists the HPE Morpheus service plans behind a credential.
func (client *Client) ListMorpheusPlans(ctx context.Context, credentialID string) ([]MorpheusPlan, error) {
	var plans []MorpheusPlan
	err := client.getLaneCatalogue(ctx, LaneMorpheus, "plans", laneCatalogueQuery("credential_id", credentialID), &plans)
	return plans, err
}

// ListMorpheusLayouts lists the HPE Morpheus instance-type layouts behind a
// credential.
func (client *Client) ListMorpheusLayouts(ctx context.Context, credentialID string) ([]MorpheusLayout, error) {
	var layouts []MorpheusLayout
	err := client.getLaneCatalogue(ctx, LaneMorpheus, "layouts", laneCatalogueQuery("credential_id", credentialID), &layouts)
	return layouts, err
}

// ListMorpheusNetworks lists the HPE Morpheus networks behind a credential.
func (client *Client) ListMorpheusNetworks(ctx context.Context, credentialID string) ([]MorpheusNetwork, error) {
	var networks []MorpheusNetwork
	err := client.getLaneCatalogue(ctx, LaneMorpheus, "networks", laneCatalogueQuery("credential_id", credentialID), &networks)
	return networks, err
}

// ListAnkraCloudZones lists the Ankra Cloud zones a cluster can be deployed
// in. An empty credentialID reads through the organisation's built-in Ankra
// Cloud credential.
func (client *Client) ListAnkraCloudZones(ctx context.Context, credentialID string) ([]AnkraCloudZone, CataloguePricing, error) {
	var envelope struct {
		Zones []AnkraCloudZone `json:"zones"`
		CataloguePricing
	}
	err := client.getLaneCatalogue(ctx, LaneAnkraCloud, "zones", laneCatalogueQuery("credential_id", credentialID), &envelope)
	return envelope.Zones, envelope.CataloguePricing, err
}

// ListAnkraCloudPlans lists the Ankra Cloud server plans, cheapest first. An
// empty credentialID reads through the organisation's built-in credential.
func (client *Client) ListAnkraCloudPlans(ctx context.Context, credentialID string) ([]AnkraCloudPlan, CataloguePricing, error) {
	var envelope struct {
		Plans []AnkraCloudPlan `json:"plans"`
		CataloguePricing
	}
	err := client.getLaneCatalogue(ctx, LaneAnkraCloud, "plans", laneCatalogueQuery("credential_id", credentialID), &envelope)
	return envelope.Plans, envelope.CataloguePricing, err
}
