// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/listplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"ankra.io/terraform-provider-ankra/internal/client"
)

var (
	_ resource.Resource                = (*laneClusterResource[awsClusterModel])(nil)
	_ resource.ResourceWithConfigure   = (*laneClusterResource[awsClusterModel])(nil)
	_ resource.ResourceWithImportState = (*laneClusterResource[awsClusterModel])(nil)
	_ resource.ResourceWithConfigure   = (*laneClusterResource[proxmoxClusterModel])(nil)
	_ resource.ResourceWithImportState = (*laneClusterResource[proxmoxClusterModel])(nil)
	_ resource.ResourceWithConfigure   = (*laneClusterResource[morpheusClusterModel])(nil)
	_ resource.ResourceWithImportState = (*laneClusterResource[morpheusClusterModel])(nil)
	_ resource.ResourceWithConfigure   = (*laneClusterResource[ankraCloudClusterModel])(nil)
	_ resource.ResourceWithImportState = (*laneClusterResource[ankraCloudClusterModel])(nil)
)

func requiredReplaceInt64(description string) schema.Int64Attribute {
	return schema.Int64Attribute{
		MarkdownDescription: description,
		Required:            true,
		PlanModifiers:       []planmodifier.Int64{int64planmodifier.RequiresReplace()},
		Validators:          []validator.Int64{laneAtLeastOne()},
	}
}

func optionalReplaceInt64(description string) schema.Int64Attribute {
	return schema.Int64Attribute{
		MarkdownDescription: description,
		Optional:            true,
		PlanModifiers:       []planmodifier.Int64{int64planmodifier.RequiresReplace()},
		Validators:          []validator.Int64{laneAtLeastOne()},
	}
}

func requiredReplaceStringList(description string) schema.ListAttribute {
	return schema.ListAttribute{
		MarkdownDescription: description,
		Required:            true,
		ElementType:         types.StringType,
		PlanModifiers:       []planmodifier.List{listplanmodifier.RequiresReplace()},
	}
}

// laneGitopsAttributes are the GitOps source arguments of the lanes whose
// create contract takes one.
func laneGitopsAttributes(attributes map[string]schema.Attribute) {
	attributes["gitops_branch"] = defaultedReplaceString("GitOps branch to commit the generated stack to.", "master")
	attributes["gitops_credential_name"] = optionalReplaceString(
		"GitOps credential name; commits the generated stack to Git when set with `gitops_repository`.")
	attributes["gitops_repository"] = optionalReplaceString("GitOps repository (`owner/name`) to commit the generated stack to.")
}

// laneRetentionAttribute is the retention policy of the lanes that keep or
// delete cloud resources on deprovision.
func laneRetentionAttribute() schema.Attribute {
	return withStringValidators(defaultedReplaceString(
		"What deprovisioning does with retained cloud resources such as volumes: `retain` (default) or `delete`.", "retain"),
		laneOneOf(laneRetentionPolicy...))
}

// ---------------------------------------------------------------------------
// AWS (self-managed EC2)

type awsClusterModel struct {
	laneClusterCommonModel
	CredentialID         types.String `tfsdk:"credential_id"`
	SSHKeyCredentialID   types.String `tfsdk:"ssh_key_credential_id"`
	Region               types.String `tfsdk:"region"`
	VpcID                types.String `tfsdk:"vpc_id"`
	NodeSubnetIDs        types.List   `tfsdk:"node_subnet_ids"`
	BastionSubnetID      types.String `tfsdk:"bastion_subnet_id"`
	NetworkIPRange       types.String `tfsdk:"network_ip_range"`
	AvailabilityZones    types.List   `tfsdk:"availability_zones"`
	NatGatewaySingleZone types.Bool   `tfsdk:"nat_gateway_single_zone"`
	EgressMode           types.String `tfsdk:"egress_mode"`
	BastionInstanceType  types.String `tfsdk:"bastion_instance_type"`
	BastionAllowedIPs    types.List   `tfsdk:"bastion_allowed_ips"`
	ControlPlaneType     types.String `tfsdk:"control_plane_type"`
	WorkerType           types.String `tfsdk:"worker_type"`
	EtcdType             types.String `tfsdk:"etcd_type"`
	UbuntuSeries         types.String `tfsdk:"ubuntu_series"`
	Architecture         types.String `tfsdk:"architecture"`
	RootVolumeGiB        types.Int64  `tfsdk:"root_volume_gib"`
	GitopsBranch         types.String `tfsdk:"gitops_branch"`
	GitopsCredentialName types.String `tfsdk:"gitops_credential_name"`
	GitopsRepository     types.String `tfsdk:"gitops_repository"`
	RetentionPolicy      types.String `tfsdk:"retention_policy"`
	AcceptVolumeDataLoss types.Bool   `tfsdk:"accept_volume_data_loss"`
}

// NewAWSClusterResource returns the ankra_aws_cluster resource, the
// counterpart of `ankra cluster aws create` and `ankra cluster aws
// deprovision`.
func NewAWSClusterResource() resource.Resource {
	return &laneClusterResource[awsClusterModel]{definition: laneClusterDefinition[awsClusterModel]{
		typeSuffix:  "_aws_cluster",
		lane:        client.LaneAWS,
		displayName: "AWS",
		description: "Provisions a self-managed Kubernetes cluster on AWS EC2 through the Ankra platform (not EKS; " +
			"see `ankra_managed_cluster` for that). The counterpart of `ankra cluster aws create` and `ankra cluster aws " +
			"deprovision`. Provisioning arguments are immutable, so changing one forces replacement; add capacity " +
			"in place with `ankra_node_group`.",
		attributes: func() map[string]schema.Attribute {
			attributes := laneClusterAttributes("AWS")
			attributes["credential_id"] = requiredReplaceString("AWS credential id used to provision the cluster.")
			attributes["ssh_key_credential_id"] = requiredReplaceString("SSH key credential id to install on the nodes.")
			attributes["region"] = requiredReplaceString("AWS region (e.g. `eu-central-1`).")
			attributes["vpc_id"] = optionalReplaceString(
				"Existing VPC to adopt. When set, `node_subnet_ids` and `bastion_subnet_id` are required and " +
					"`network_ip_range`, `availability_zones` and `nat_gateway_single_zone` must be left unset. " +
					"Unset, Ankra creates the VPC.")
			attributes["node_subnet_ids"] = optionalReplaceStringList("Private subnets of the adopted VPC the nodes run in.")
			attributes["bastion_subnet_id"] = optionalReplaceString("Public subnet of the adopted VPC the bastion runs in.")
			attributes["network_ip_range"] = optionalReplaceString(
				"CIDR of the VPC Ankra creates. The platform uses `10.0.0.0/16` when unset.")
			attributes["availability_zones"] = optionalReplaceStringList("Availability zones of the VPC Ankra creates.")
			attributes["nat_gateway_single_zone"] = defaultedReplaceBool(
				"Run one NAT gateway for the created VPC instead of one per zone.", false)
			attributes["egress_mode"] = optionalReplaceString(
				"Node egress mode. A created VPC uses `nat_gateway`; an adopted VPC is resolved by the platform when unset.")
			attributes["bastion_instance_type"] = defaultedReplaceString("EC2 instance type of the bastion host.", "t3.small")
			attributes["bastion_allowed_ips"] = requiredReplaceStringList(
				"CIDRs allowed to reach the bastion over SSH. The platform requires at least one.")
			attributes["control_plane_type"] = defaultedReplaceString("EC2 instance type of control-plane nodes.", "t3.medium")
			attributes["worker_type"] = defaultedReplaceString("EC2 instance type of the default worker group.", "t3.medium")
			attributes["etcd_type"] = defaultedReplaceString("EC2 instance type of dedicated etcd nodes.", "t3.medium")
			attributes["ubuntu_series"] = defaultedReplaceString("Ubuntu release of the node image.", "24.04")
			attributes["architecture"] = withStringValidators(
				defaultedReplaceString("CPU architecture of the nodes: `amd64` or `arm64`.", "amd64"),
				laneOneOf("amd64", "arm64"))
			attributes["root_volume_gib"] = defaultedReplaceInt64("Root volume size of each node, in GiB.", 40)
			laneGitopsAttributes(attributes)
			attributes["retention_policy"] = laneRetentionAttribute()
			attributes["accept_volume_data_loss"] = acceptVolumeDataLossAttribute()
			return attributes
		},
		common: func(model *awsClusterModel) *laneClusterCommonModel { return &model.laneClusterCommonModel },
		request: func(ctx context.Context, model *awsClusterModel, diagnostics *diag.Diagnostics) any {
			allowedIPs := stringsFromList(ctx, model.BastionAllowedIPs, diagnostics)
			if allowedIPs == nil {
				allowedIPs = []string{}
			}
			return client.AWSClusterRequest{
				Name:                 model.Name.ValueString(),
				Description:          optionalStringPointer(model.Description),
				CredentialID:         model.CredentialID.ValueString(),
				SSHKeyCredentialID:   model.SSHKeyCredentialID.ValueString(),
				Region:               model.Region.ValueString(),
				VpcID:                model.VpcID.ValueString(),
				NodeSubnetIDs:        stringsFromList(ctx, model.NodeSubnetIDs, diagnostics),
				BastionSubnetID:      model.BastionSubnetID.ValueString(),
				NetworkIPRange:       model.NetworkIPRange.ValueString(),
				AvailabilityZones:    stringsFromList(ctx, model.AvailabilityZones, diagnostics),
				NatGatewaySingleZone: model.NatGatewaySingleZone.ValueBool(),
				EgressMode:           model.EgressMode.ValueString(),
				BastionInstanceType:  model.BastionInstanceType.ValueString(),
				BastionAllowedIPs:    allowedIPs,
				ControlPlaneCount:    model.ControlPlaneCount.ValueInt64(),
				ControlPlaneType:     model.ControlPlaneType.ValueString(),
				WorkerCount:          model.WorkerCount.ValueInt64(),
				WorkerType:           model.WorkerType.ValueString(),
				Distribution:         model.Distribution.ValueString(),
				KubernetesVersion:    optionalStringPointer(model.KubernetesVersion),
				EtcdTopology:         model.EtcdTopology.ValueString(),
				EtcdNodeCount:        model.EtcdNodeCount.ValueInt64(),
				EtcdType:             model.EtcdType.ValueString(),
				CNI:                  model.CNI.ValueString(),
				UbuntuSeries:         model.UbuntuSeries.ValueString(),
				Architecture:         model.Architecture.ValueString(),
				RootVolumeGiB:        model.RootVolumeGiB.ValueInt64(),
				GitopsCredentialName: optionalStringPointer(model.GitopsCredentialName),
				GitopsRepository:     optionalStringPointer(model.GitopsRepository),
				GitopsBranch:         model.GitopsBranch.ValueString(),
				IncludeNetworking:    model.IncludeNetworking.ValueBool(),
				IncludeDNS:           model.IncludeDNS.ValueBool(),
				RetentionPolicy:      model.RetentionPolicy.ValueString(),
			}
		},
		acceptVolumeDataLoss: func(model *awsClusterModel) bool { return model.AcceptVolumeDataLoss.ValueBool() },
	}}
}

// ---------------------------------------------------------------------------
// Proxmox VE

type proxmoxClusterModel struct {
	laneClusterCommonModel
	CredentialID             types.String `tfsdk:"credential_id"`
	SSHKeyCredentialID       types.String `tfsdk:"ssh_key_credential_id"`
	Node                     types.String `tfsdk:"node"`
	PlacementNodes           types.List   `tfsdk:"placement_nodes"`
	Bridge                   types.String `tfsdk:"bridge"`
	Storage                  types.String `tfsdk:"storage"`
	Template                 types.String `tfsdk:"template"`
	BastionInstanceType      types.String `tfsdk:"bastion_instance_type"`
	ControlPlaneInstanceType types.String `tfsdk:"control_plane_instance_type"`
	WorkerInstanceType       types.String `tfsdk:"worker_instance_type"`
	EtcdInstanceType         types.String `tfsdk:"etcd_instance_type"`
	NetworkMode              types.String `tfsdk:"network_mode"`
	SitePublicIP             types.String `tfsdk:"site_public_ip"`
}

// NewProxmoxClusterResource returns the ankra_proxmox_cluster resource, the
// counterpart of `ankra cluster proxmox create`.
func NewProxmoxClusterResource() resource.Resource {
	return &laneClusterResource[proxmoxClusterModel]{definition: laneClusterDefinition[proxmoxClusterModel]{
		typeSuffix:  "_proxmox_cluster",
		lane:        client.LaneProxmox,
		displayName: "Proxmox VE",
		description: "Provisions a Kubernetes cluster on Proxmox VE virtual machines through the Ankra platform. " +
			"The counterpart of `ankra cluster proxmox create`. Proxmox has no cloud controller, so the cluster " +
			"carries no `external_cloud_provider` and no GitOps source at create. Provisioning arguments are " +
			"immutable, so changing one forces replacement; add capacity in place with `ankra_node_group`.",
		attributes: func() map[string]schema.Attribute {
			attributes := laneClusterAttributes("Proxmox VE")
			attributes["credential_id"] = requiredReplaceString("Proxmox VE credential id used to provision the cluster.")
			attributes["ssh_key_credential_id"] = requiredReplaceString("SSH key credential id to install on the VMs.")
			attributes["node"] = requiredReplaceString("Proxmox host the cluster's VMs are created on.")
			attributes["placement_nodes"] = optionalReplaceStringList(
				"Two or more Proxmox hosts to spread the cluster's VMs across. Unset keeps every VM on `node`.")
			attributes["bridge"] = requiredReplaceString("Network bridge the VMs attach to (e.g. `vmbr0`).")
			attributes["storage"] = defaultedReplaceString("Storage the VM disks are created on.", "local-lvm")
			attributes["template"] = optionalReplaceString(
				"VM template to clone. Unset lets the platform pick the first available template on the node.")
			attributes["bastion_instance_type"] = defaultedReplaceString("Size of the bastion VM.", "px-small")
			attributes["control_plane_instance_type"] = defaultedReplaceString("Size of control-plane VMs.", "px-medium")
			attributes["worker_instance_type"] = defaultedReplaceString("Size of the default worker group's VMs.", "px-medium")
			attributes["etcd_instance_type"] = defaultedReplaceString("Size of dedicated etcd VMs.", "px-medium")
			attributes["network_mode"] = withStringValidators(optionalReplaceString(
				"Put the cluster on the platform WireGuard overlay behind its bastion site gateway: "+
					"`private_network` or `wireguard_mesh`. kubeadm only; requires `site_public_ip`."),
				laneOneOf("private_network", "wireguard_mesh"))
			attributes["site_public_ip"] = optionalReplaceString("Public address of the site gateway for `network_mode`.")
			return attributes
		},
		common: func(model *proxmoxClusterModel) *laneClusterCommonModel { return &model.laneClusterCommonModel },
		request: func(ctx context.Context, model *proxmoxClusterModel, diagnostics *diag.Diagnostics) any {
			return client.ProxmoxClusterRequest{
				Name:                     model.Name.ValueString(),
				Description:              optionalStringPointer(model.Description),
				CredentialID:             model.CredentialID.ValueString(),
				SSHKeyCredentialID:       model.SSHKeyCredentialID.ValueString(),
				Node:                     model.Node.ValueString(),
				PlacementNodes:           stringsFromList(ctx, model.PlacementNodes, diagnostics),
				Bridge:                   model.Bridge.ValueString(),
				Storage:                  model.Storage.ValueString(),
				Template:                 model.Template.ValueString(),
				BastionInstanceType:      model.BastionInstanceType.ValueString(),
				ControlPlaneCount:        model.ControlPlaneCount.ValueInt64(),
				ControlPlaneInstanceType: model.ControlPlaneInstanceType.ValueString(),
				WorkerCount:              model.WorkerCount.ValueInt64(),
				WorkerInstanceType:       model.WorkerInstanceType.ValueString(),
				Distribution:             model.Distribution.ValueString(),
				KubernetesVersion:        optionalStringPointer(model.KubernetesVersion),
				EtcdTopology:             model.EtcdTopology.ValueString(),
				EtcdNodeCount:            model.EtcdNodeCount.ValueInt64(),
				EtcdInstanceType:         model.EtcdInstanceType.ValueString(),
				CNI:                      model.CNI.ValueString(),
				NetworkMode:              model.NetworkMode.ValueString(),
				SitePublicIP:             model.SitePublicIP.ValueString(),
				IncludeNetworking:        model.IncludeNetworking.ValueBool(),
				IncludeDNS:               model.IncludeDNS.ValueBool(),
			}
		},
	}}
}

// ---------------------------------------------------------------------------
// HPE Morpheus

type morpheusClusterModel struct {
	laneClusterCommonModel
	CredentialID       types.String `tfsdk:"credential_id"`
	SSHKeyCredentialID types.String `tfsdk:"ssh_key_credential_id"`
	GroupID            types.Int64  `tfsdk:"group_id"`
	CloudID            types.Int64  `tfsdk:"cloud_id"`
	NetworkID          types.Int64  `tfsdk:"network_id"`
	LayoutID           types.Int64  `tfsdk:"layout_id"`
	VirtualImageID     types.Int64  `tfsdk:"virtual_image_id"`
	BastionPlanID      types.Int64  `tfsdk:"bastion_plan_id"`
	ControlPlanePlanID types.Int64  `tfsdk:"control_plane_plan_id"`
	WorkerPlanID       types.Int64  `tfsdk:"worker_plan_id"`
	EtcdPlanID         types.Int64  `tfsdk:"etcd_plan_id"`
}

// NewMorpheusClusterResource returns the ankra_morpheus_cluster resource, the
// counterpart of `ankra cluster morpheus create`.
func NewMorpheusClusterResource() resource.Resource {
	return &laneClusterResource[morpheusClusterModel]{definition: laneClusterDefinition[morpheusClusterModel]{
		typeSuffix:  "_morpheus_cluster",
		lane:        client.LaneMorpheus,
		displayName: "HPE Morpheus",
		description: "Provisions a Kubernetes cluster on HPE Morpheus through the Ankra platform. The counterpart of " +
			"`ankra cluster morpheus create`. Morpheus sizes nodes by numeric plan id; `ankra cluster morpheus plans`, " +
			"`groups`, `clouds`, `layouts` and `networks` list the ids. Provisioning arguments are immutable, so " +
			"changing one forces replacement; add capacity in place with `ankra_node_group`.",
		attributes: func() map[string]schema.Attribute {
			attributes := laneClusterAttributes("HPE Morpheus")
			attributes["credential_id"] = requiredReplaceString("HPE Morpheus credential id used to provision the cluster.")
			attributes["ssh_key_credential_id"] = requiredReplaceString("SSH key credential id to install on the instances.")
			attributes["group_id"] = requiredReplaceInt64("Morpheus group id the instances are created in.")
			attributes["cloud_id"] = requiredReplaceInt64("Morpheus cloud id the instances are created in.")
			attributes["network_id"] = requiredReplaceInt64("Morpheus network id the instances attach to.")
			attributes["layout_id"] = requiredReplaceInt64("Morpheus instance layout id.")
			attributes["virtual_image_id"] = optionalReplaceInt64("Morpheus virtual image id. Unset uses the layout's image.")
			attributes["bastion_plan_id"] = requiredReplaceInt64("Morpheus plan id of the bastion instance.")
			attributes["control_plane_plan_id"] = requiredReplaceInt64("Morpheus plan id of control-plane instances.")
			attributes["worker_plan_id"] = requiredReplaceInt64("Morpheus plan id of the default worker group's instances.")
			attributes["etcd_plan_id"] = optionalReplaceInt64("Morpheus plan id of dedicated etcd instances.")
			return attributes
		},
		common: func(model *morpheusClusterModel) *laneClusterCommonModel { return &model.laneClusterCommonModel },
		request: func(_ context.Context, model *morpheusClusterModel, _ *diag.Diagnostics) any {
			return client.MorpheusClusterRequest{
				Name:               model.Name.ValueString(),
				Description:        optionalStringPointer(model.Description),
				CredentialID:       model.CredentialID.ValueString(),
				SSHKeyCredentialID: model.SSHKeyCredentialID.ValueString(),
				GroupID:            model.GroupID.ValueInt64(),
				CloudID:            model.CloudID.ValueInt64(),
				NetworkID:          model.NetworkID.ValueInt64(),
				LayoutID:           model.LayoutID.ValueInt64(),
				VirtualImageID:     int64Pointer(model.VirtualImageID),
				BastionPlanID:      model.BastionPlanID.ValueInt64(),
				ControlPlaneCount:  model.ControlPlaneCount.ValueInt64(),
				ControlPlanePlanID: model.ControlPlanePlanID.ValueInt64(),
				WorkerCount:        model.WorkerCount.ValueInt64(),
				WorkerPlanID:       model.WorkerPlanID.ValueInt64(),
				Distribution:       model.Distribution.ValueString(),
				KubernetesVersion:  optionalStringPointer(model.KubernetesVersion),
				EtcdTopology:       model.EtcdTopology.ValueString(),
				EtcdNodeCount:      model.EtcdNodeCount.ValueInt64(),
				EtcdPlanID:         int64Pointer(model.EtcdPlanID),
				CNI:                model.CNI.ValueString(),
				IncludeNetworking:  model.IncludeNetworking.ValueBool(),
				IncludeDNS:         model.IncludeDNS.ValueBool(),
			}
		},
	}}
}

// ---------------------------------------------------------------------------
// Ankra Cloud

type ankraCloudClusterModel struct {
	laneClusterCommonModel
	CredentialID         types.String `tfsdk:"credential_id"`
	RuntimeCredentialID  types.String `tfsdk:"runtime_credential_id"`
	SSHKeyCredentialID   types.String `tfsdk:"ssh_key_credential_id"`
	Zone                 types.String `tfsdk:"zone"`
	Template             types.String `tfsdk:"template"`
	PrivateNetworkID     types.String `tfsdk:"private_network_id"`
	NetworkIPRange       types.String `tfsdk:"network_ip_range"`
	NetworkMode          types.String `tfsdk:"network_mode"`
	BastionPlan          types.String `tfsdk:"bastion_plan"`
	BastionAllowedIPs    types.List   `tfsdk:"bastion_allowed_ips"`
	ControlPlanePlan     types.String `tfsdk:"control_plane_plan"`
	WorkerPlan           types.String `tfsdk:"worker_plan"`
	EtcdPlan             types.String `tfsdk:"etcd_plan"`
	GitopsBranch         types.String `tfsdk:"gitops_branch"`
	GitopsCredentialName types.String `tfsdk:"gitops_credential_name"`
	GitopsRepository     types.String `tfsdk:"gitops_repository"`
	RetentionPolicy      types.String `tfsdk:"retention_policy"`
	AcceptVolumeDataLoss types.Bool   `tfsdk:"accept_volume_data_loss"`
}

// NewAnkraCloudClusterResource returns the ankra_ankracloud_cluster resource,
// the counterpart of `ankra cluster ankracloud create` and `ankra cluster
// ankracloud deprovision`.
func NewAnkraCloudClusterResource() resource.Resource {
	return &laneClusterResource[ankraCloudClusterModel]{definition: laneClusterDefinition[ankraCloudClusterModel]{
		typeSuffix:  "_ankracloud_cluster",
		lane:        client.LaneAnkraCloud,
		displayName: "Ankra Cloud",
		description: "Provisions a self-managed Kubernetes cluster on Ankra Cloud. The counterpart of `ankra cluster " +
			"ankracloud create` and `ankra cluster ankracloud deprovision`. `ankra cluster ankracloud zones` and `plans` " +
			"list the zones and plans. Provisioning arguments are immutable, so changing one forces replacement; " +
			"add capacity in place with `ankra_node_group`.",
		attributes: func() map[string]schema.Attribute {
			attributes := laneClusterAttributes("Ankra Cloud")
			attributes["credential_id"] = optionalReplaceString(
				"Ankra Cloud credential id. Unset uses the organisation's built-in Ankra Cloud credential.")
			attributes["runtime_credential_id"] = optionalReplaceString("Credential the cluster's cloud controller runs with.")
			attributes["ssh_key_credential_id"] = optionalReplaceString("SSH key credential id to install on the nodes.")
			attributes["zone"] = requiredReplaceString("Ankra Cloud zone.")
			attributes["template"] = defaultedReplaceString("OS template of the nodes.", "debian-13")
			attributes["private_network_id"] = optionalReplaceString(
				"Existing private network to adopt. Leave `network_ip_range` unset when adopting.")
			attributes["network_ip_range"] = optionalReplaceString("CIDR of the private network Ankra creates.")
			attributes["network_mode"] = withStringValidators(
				defaultedReplaceString("Node addressing: `ipv4_nat` (default) or `ipv6_only`.", "ipv4_nat"),
				laneOneOf("ipv4_nat", "ipv6_only"))
			attributes["bastion_plan"] = requiredReplaceString("Plan of the bastion host.")
			attributes["bastion_allowed_ips"] = optionalReplaceStringList("CIDRs allowed to reach the bastion over SSH.")
			attributes["control_plane_plan"] = requiredReplaceString("Plan of control-plane nodes.")
			attributes["worker_plan"] = optionalReplaceString(
				"Plan of the default worker group. Required when `worker_count` is above zero.")
			attributes["etcd_plan"] = optionalReplaceString("Plan of dedicated etcd nodes. Required when `etcd_topology` is `external`.")
			laneGitopsAttributes(attributes)
			attributes["retention_policy"] = laneRetentionAttribute()
			attributes["accept_volume_data_loss"] = acceptVolumeDataLossAttribute()
			return attributes
		},
		common: func(model *ankraCloudClusterModel) *laneClusterCommonModel { return &model.laneClusterCommonModel },
		request: func(ctx context.Context, model *ankraCloudClusterModel, diagnostics *diag.Diagnostics) any {
			return client.AnkraCloudClusterRequest{
				Name:                 model.Name.ValueString(),
				Description:          optionalStringPointer(model.Description),
				CredentialID:         optionalStringPointer(model.CredentialID),
				RuntimeCredentialID:  optionalStringPointer(model.RuntimeCredentialID),
				SSHKeyCredentialID:   optionalStringPointer(model.SSHKeyCredentialID),
				Zone:                 model.Zone.ValueString(),
				Template:             model.Template.ValueString(),
				PrivateNetworkID:     optionalStringPointer(model.PrivateNetworkID),
				NetworkIPRange:       optionalStringPointer(model.NetworkIPRange),
				NetworkMode:          model.NetworkMode.ValueString(),
				BastionPlan:          model.BastionPlan.ValueString(),
				BastionAllowedIPs:    stringsFromList(ctx, model.BastionAllowedIPs, diagnostics),
				ControlPlaneCount:    model.ControlPlaneCount.ValueInt64(),
				ControlPlanePlan:     model.ControlPlanePlan.ValueString(),
				WorkerCount:          model.WorkerCount.ValueInt64(),
				WorkerPlan:           model.WorkerPlan.ValueString(),
				Distribution:         model.Distribution.ValueString(),
				KubernetesVersion:    optionalStringPointer(model.KubernetesVersion),
				EtcdTopology:         model.EtcdTopology.ValueString(),
				EtcdNodeCount:        model.EtcdNodeCount.ValueInt64(),
				EtcdPlan:             model.EtcdPlan.ValueString(),
				CNI:                  model.CNI.ValueString(),
				GitopsCredentialName: optionalStringPointer(model.GitopsCredentialName),
				GitopsRepository:     optionalStringPointer(model.GitopsRepository),
				GitopsBranch:         model.GitopsBranch.ValueString(),
				IncludeNetworking:    model.IncludeNetworking.ValueBool(),
				IncludeDNS:           model.IncludeDNS.ValueBool(),
				RetentionPolicy:      model.RetentionPolicy.ValueString(),
			}
		},
		acceptVolumeDataLoss: func(model *ankraCloudClusterModel) bool { return model.AcceptVolumeDataLoss.ValueBool() },
	}}
}
