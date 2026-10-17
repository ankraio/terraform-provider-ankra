// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64default"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/listplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/mapplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/objectplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"ankra.io/terraform-provider-ankra/internal/client"
)

// managedNodePoolSpotModel asks for spot or preemptible capacity.
type managedNodePoolSpotModel struct {
	Enabled  types.Bool    `tfsdk:"enabled"`
	MaxPrice types.Float64 `tfsdk:"max_price"`
}

// managedNodePoolFields are the pool attributes shared by the node_pools of
// ankra_managed_cluster and the ankra_managed_node_pool resource.
type managedNodePoolFields struct {
	Name             types.String               `tfsdk:"name"`
	Size             types.String               `tfsdk:"size"`
	Count            types.Int64                `tfsdk:"node_count"`
	Labels           types.Map                  `tfsdk:"labels"`
	Taints           []nodeGroupTaintModel      `tfsdk:"taints"`
	Autoscaling      *nodeGroupAutoscalingModel `tfsdk:"autoscaling"`
	Zone             types.String               `tfsdk:"zone"`
	RootVolumeType   types.String               `tfsdk:"root_volume_type"`
	RootVolumeSizeGB types.Int64                `tfsdk:"root_volume_size_gb"`
	Spot             *managedNodePoolSpotModel  `tfsdk:"spot"`
}

// managedNodePoolSpotAttribute is the spot object of a pool.
func managedNodePoolSpotAttribute(planModifiers ...planmodifier.Object) schema.SingleNestedAttribute {
	return schema.SingleNestedAttribute{
		MarkdownDescription: "Run the pool on spot or preemptible capacity, where the provider offers it.",
		Optional:            true,
		PlanModifiers:       planModifiers,
		Attributes: map[string]schema.Attribute{
			"enabled":   schema.BoolAttribute{MarkdownDescription: "Whether the pool runs on spot capacity.", Required: true},
			"max_price": schema.Float64Attribute{MarkdownDescription: "Highest hourly price to pay, where the provider takes one.", Optional: true},
		},
	}
}

// managedNodePoolCreateFromFields builds the add body of a pool.
func managedNodePoolCreateFromFields(ctx context.Context, fields managedNodePoolFields, diagnostics *diag.Diagnostics) client.ManagedNodePoolCreate {
	count := int64(1)
	if !fields.Count.IsNull() && !fields.Count.IsUnknown() {
		count = fields.Count.ValueInt64()
	}
	labels := nodeGroupLabelsToAPI(ctx, fields.Labels, diagnostics)
	if len(labels) == 0 {
		labels = nil
	}
	taints := nodeGroupTaintsToAPI(fields.Taints)
	if len(taints) == 0 {
		taints = nil
	}
	request := client.ManagedNodePoolCreate{
		Name:             fields.Name.ValueString(),
		Size:             fields.Size.ValueString(),
		Count:            count,
		Labels:           labels,
		Taints:           taints,
		Zone:             optionalStringPointer(fields.Zone),
		RootVolumeType:   optionalStringPointer(fields.RootVolumeType),
		RootVolumeSizeGB: int64Pointer(fields.RootVolumeSizeGB),
	}
	if fields.Autoscaling != nil {
		request.Autoscaling = &client.ManagedNodePoolAutoscaling{
			Enabled:  fields.Autoscaling.Enabled.ValueBool(),
			MinCount: fields.Autoscaling.MinCount.ValueInt64(),
			MaxCount: fields.Autoscaling.MaxCount.ValueInt64(),
		}
	}
	if fields.Spot != nil {
		request.Spot = &client.ManagedNodePoolSpot{Enabled: fields.Spot.Enabled.ValueBool()}
		if !fields.Spot.MaxPrice.IsNull() && !fields.Spot.MaxPrice.IsUnknown() {
			price := fields.Spot.MaxPrice.ValueFloat64()
			request.Spot.MaxPrice = &price
		}
	}
	return request
}

// managedNodePoolSizingUpdate is the PATCH an update of a pool's count or
// autoscaling needs, and whether one is needed. Removing autoscaling from
// configuration disables it.
func managedNodePoolSizingUpdate(planned, prior managedNodePoolFields) (client.ManagedNodePoolUpdate, bool) {
	var update client.ManagedNodePoolUpdate
	changed := false
	if settings, autoscalingChanged := nodeGroupAutoscalingChange(planned.Autoscaling, prior.Autoscaling); autoscalingChanged {
		enabled := settings.Enabled
		minimum := settings.MinCount
		maximum := settings.MaxCount
		update.AutoscalingEnabled = &enabled
		update.AutoscalingMin = &minimum
		update.AutoscalingMax = &maximum
		changed = true
	}
	if !planned.Count.IsNull() && !planned.Count.IsUnknown() && !planned.Count.Equal(prior.Count) {
		count := planned.Count.ValueInt64()
		update.Count = &count
		changed = true
	}
	return update, changed
}

// refreshManagedNodePoolFields copies what the listing reports onto a pool's
// state. While autoscaling is enabled the provider owns the live count, so
// the configured count is kept rather than reported as drift.
func refreshManagedNodePoolFields(fields *managedNodePoolFields, pool client.ManagedNodePool) {
	fields.Name = types.StringValue(pool.Name)
	fields.Size = types.StringValue(pool.Size)
	isAutoscaled := pool.Autoscaling != nil && pool.Autoscaling.Enabled
	if !isAutoscaled || fields.Count.IsNull() || fields.Count.IsUnknown() {
		fields.Count = types.Int64Value(pool.Count)
	}
	labels := make(map[string]any, len(pool.Labels))
	for key, value := range pool.Labels {
		labels[key] = value
	}
	fields.Labels = nodeGroupLabelsFromAPI(labels, fields.Labels)
	fields.Taints = nodeGroupTaintsFromAPI(managedPoolTaints(pool.Taints), fields.Taints)
	if fields.Autoscaling != nil || isAutoscaled {
		autoscaling := &nodeGroupAutoscalingModel{Enabled: types.BoolValue(isAutoscaled)}
		autoscaling.MinCount = types.Int64Null()
		autoscaling.MaxCount = types.Int64Null()
		if pool.Autoscaling != nil && pool.Autoscaling.MinCount != nil {
			autoscaling.MinCount = types.Int64Value(*pool.Autoscaling.MinCount)
		}
		if pool.Autoscaling != nil && pool.Autoscaling.MaxCount != nil {
			autoscaling.MaxCount = types.Int64Value(*pool.Autoscaling.MaxCount)
		}
		if fields.Autoscaling != nil && autoscaling.MinCount.IsNull() {
			autoscaling.MinCount = fields.Autoscaling.MinCount
		}
		if fields.Autoscaling != nil && autoscaling.MaxCount.IsNull() {
			autoscaling.MaxCount = fields.Autoscaling.MaxCount
		}
		fields.Autoscaling = autoscaling
	}
	if pool.Zone != nil && *pool.Zone != "" {
		fields.Zone = types.StringValue(*pool.Zone)
	} else if !fields.Zone.IsNull() {
		fields.Zone = types.StringNull()
	}
	if pool.RootVolumeType != nil && *pool.RootVolumeType != "" {
		fields.RootVolumeType = types.StringValue(*pool.RootVolumeType)
	} else if !fields.RootVolumeType.IsNull() {
		fields.RootVolumeType = types.StringNull()
	}
	if pool.RootVolumeSizeGB != nil {
		fields.RootVolumeSizeGB = types.Int64Value(*pool.RootVolumeSizeGB)
	} else if !fields.RootVolumeSizeGB.IsNull() {
		fields.RootVolumeSizeGB = types.Int64Null()
	}
}

// managedPoolTaints reads the listing's loosely typed taints, ordered as the
// platform stores them.
func managedPoolTaints(taints []map[string]string) []client.NodeGroupTaint {
	result := make([]client.NodeGroupTaint, 0, len(taints))
	for _, taint := range taints {
		result = append(result, client.NodeGroupTaint{Key: taint["key"], Value: taint["value"], Effect: taint["effect"]})
	}
	return result
}

// managedNodePoolAttributes are the pool attributes. A standalone pool
// (ankra_managed_node_pool) carries RequiresReplace on everything the
// platform cannot change in place; inside ankra_managed_cluster the
// node_pools list's own plan modifier makes that decision.
func managedNodePoolAttributes(isStandalone bool) map[string]schema.Attribute {
	var replaceString []planmodifier.String
	var replaceInt64 []planmodifier.Int64
	var replaceMap []planmodifier.Map
	var replaceList []planmodifier.List
	var replaceObject []planmodifier.Object
	if isStandalone {
		replaceString = []planmodifier.String{stringplanmodifier.RequiresReplace()}
		replaceInt64 = []planmodifier.Int64{int64planmodifier.RequiresReplace()}
		replaceMap = []planmodifier.Map{mapplanmodifier.RequiresReplace()}
		replaceList = []planmodifier.List{listplanmodifier.RequiresReplace()}
		replaceObject = []planmodifier.Object{objectplanmodifier.RequiresReplace()}
	}
	count := schema.Int64Attribute{
		MarkdownDescription: "Number of nodes (the API's `count`); the platform defaults it to 1. Changed in place. While `autoscaling` is " +
			"enabled the provider's autoscaler owns the live count, so drift in it is not reported.",
		Optional:   true,
		Computed:   true,
		Validators: []validator.Int64{laneAtLeastOne()},
	}
	if isStandalone {
		count.PlanModifiers = []planmodifier.Int64{int64planmodifier.UseStateForUnknown()}
	} else {
		count.Default = int64default.StaticInt64(1)
	}
	return map[string]schema.Attribute{
		"name": schema.StringAttribute{
			MarkdownDescription: "Pool name, unique within the cluster.",
			Required:            true,
			PlanModifiers:       replaceString,
			Validators:          []validator.String{laneNameValidator{}},
		},
		"size": schema.StringAttribute{
			MarkdownDescription: "Node size in the provider's vocabulary (droplet size, machine type, VM size, plan).",
			Required:            true,
			PlanModifiers:       replaceString,
		},
		"node_count": count,
		"labels": schema.MapAttribute{
			MarkdownDescription: "Kubernetes labels of the pool's nodes.",
			Optional:            true,
			ElementType:         types.StringType,
			PlanModifiers:       replaceMap,
		},
		"taints":      nodeGroupTaintsAttribute("Kubernetes taints of the pool's nodes.", replaceList...),
		"autoscaling": nodeGroupAutoscalingAttribute("Autoscaling bounds of the pool. Changed in place; removing the block disables autoscaling."),
		"zone": schema.StringAttribute{
			MarkdownDescription: "Zone the pool's nodes run in, where the provider places pools per zone.",
			Optional:            true,
			PlanModifiers:       replaceString,
		},
		"root_volume_type": schema.StringAttribute{
			MarkdownDescription: "Root volume type of the pool's nodes, where the provider offers a choice.",
			Optional:            true,
			PlanModifiers:       replaceString,
		},
		"root_volume_size_gb": schema.Int64Attribute{
			MarkdownDescription: "Root volume size of the pool's nodes, in GB.",
			Optional:            true,
			PlanModifiers:       replaceInt64,
			Validators:          []validator.Int64{laneAtLeastOne()},
		},
		"spot": managedNodePoolSpotAttribute(replaceObject...),
	}
}
