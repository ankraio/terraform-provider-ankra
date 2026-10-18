// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
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
// ankra_managed_cluster (keyed by pool name) and the ankra_managed_node_pool
// resource.
type managedNodePoolFields struct {
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
func managedNodePoolCreateFromFields(ctx context.Context, name string, fields managedNodePoolFields, diagnostics *diag.Diagnostics) client.ManagedNodePoolCreate {
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
		Name:             name,
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
// the configured count is kept rather than reported as drift. The placement
// fields (zone, root volume) are computed: the provider fills them in when
// the configuration leaves them unset (AKS reports its OS disk, Ankra Cloud
// its zone), so they are recorded rather than diffed against null.
//
// Providers report sizes in their own case (AKS answers Standard_D4s_v5 for
// standard_d4s_v5) and add labels of their own to a pool, so the size keeps
// the configured spelling when it differs only in case, and only the label
// keys the state already tracks are refreshed. keepAllLabels records every
// listed label, for a pool just imported, whose state tracks none yet.
func refreshManagedNodePoolFields(fields *managedNodePoolFields, pool client.ManagedNodePool, keepAllLabels bool) {
	if fields.Size.IsNull() || fields.Size.IsUnknown() || !strings.EqualFold(fields.Size.ValueString(), pool.Size) {
		fields.Size = types.StringValue(pool.Size)
	}
	isAutoscaled := pool.Autoscaling != nil && pool.Autoscaling.Enabled
	if !isAutoscaled || fields.Count.IsNull() || fields.Count.IsUnknown() {
		fields.Count = types.Int64Value(pool.Count)
	}
	fields.Labels = managedPoolLabelsFromAPI(pool.Labels, fields.Labels, keepAllLabels)
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
	fields.Zone = types.StringNull()
	if pool.Zone != nil && *pool.Zone != "" {
		fields.Zone = types.StringValue(*pool.Zone)
	}
	fields.RootVolumeType = types.StringNull()
	if pool.RootVolumeType != nil && *pool.RootVolumeType != "" {
		fields.RootVolumeType = types.StringValue(*pool.RootVolumeType)
	}
	fields.RootVolumeSizeGB = types.Int64PointerValue(pool.RootVolumeSizeGB)
	if pool.Spot != nil && (fields.Spot != nil || pool.Spot.Enabled) {
		fields.Spot = &managedNodePoolSpotModel{
			Enabled:  types.BoolValue(pool.Spot.Enabled),
			MaxPrice: types.Float64PointerValue(pool.Spot.MaxPrice),
		}
	}
}

// managedPoolLabelsFromAPI refreshes the label keys the state tracks and
// ignores the labels a provider adds by itself. A tracked label that changed
// or disappeared is reported; a label the configuration never set is not.
func managedPoolLabelsFromAPI(listed map[string]string, prior types.Map, keepAll bool) types.Map {
	if keepAll {
		values := make(map[string]any, len(listed))
		for key, value := range listed {
			values[key] = value
		}
		return nodeGroupLabelsFromAPI(values, types.MapValueMust(types.StringType, nil))
	}
	if prior.IsNull() || prior.IsUnknown() {
		return prior
	}
	elements := make(map[string]attr.Value, len(prior.Elements()))
	for key := range prior.Elements() {
		if value, present := listed[key]; present {
			elements[key] = types.StringValue(value)
		}
	}
	return types.MapValueMust(types.StringType, elements)
}

// managedPoolLabelsCovered reports whether every planned label is present,
// with the same value, among the state's labels. Extra labels in the state
// (those a provider added, or an import read back) are not a change.
func managedPoolLabelsCovered(planned, prior types.Map) bool {
	priorElements := prior.Elements()
	for key, value := range planned.Elements() {
		if priorValue, present := priorElements[key]; !present || !priorValue.Equal(value) {
			return false
		}
	}
	return true
}

// settleUnknownPoolFields gives a pool that was just added, and that the
// listing does not show yet, known values for its computed fields.
func settleUnknownPoolFields(fields *managedNodePoolFields, count int64) {
	if fields.Count.IsUnknown() {
		fields.Count = types.Int64Value(count)
	}
	if fields.Zone.IsUnknown() {
		fields.Zone = types.StringNull()
	}
	if fields.RootVolumeType.IsUnknown() {
		fields.RootVolumeType = types.StringNull()
	}
	if fields.RootVolumeSizeGB.IsUnknown() {
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

// managedNodePoolAttributes are the pool attributes, without the name. A
// standalone pool (ankra_managed_node_pool) replaces itself when an argument
// the platform cannot change in place changes, except the size, which the
// resource changes by a tracked blue/green replacement; inside ankra_managed_cluster
// the node_pools map refuses such a change at plan time instead, because
// replacing the cluster for it would destroy every workload.
func managedNodePoolAttributes(isStandalone bool) map[string]schema.Attribute {
	var replaceString []planmodifier.String
	var replaceInt64 []planmodifier.Int64
	var replaceMap []planmodifier.Map
	var replaceList []planmodifier.List
	var replaceObject []planmodifier.Object
	if isStandalone {
		replaceString = []planmodifier.String{stringplanmodifier.RequiresReplace()}
		replaceInt64 = []planmodifier.Int64{int64planmodifier.RequiresReplace()}
		replaceMap = []planmodifier.Map{mapplanmodifier.RequiresReplaceIf(
			func(ctx context.Context, request planmodifier.MapRequest, response *mapplanmodifier.RequiresReplaceIfFuncResponse) {
				marker, _ := request.Private.GetKey(ctx, clusterImportedKey)
				isAdopting := len(marker) > 0
				response.RequiresReplace = !managedPoolLabelsCovered(request.PlanValue, request.StateValue) ||
					(!isAdopting && len(request.StateValue.Elements()) > len(request.PlanValue.Elements()))
			},
			"Changing a label replaces the pool; labels the provider added by itself are ignored.",
			"Changing a label replaces the pool; labels the provider added by itself are ignored.",
		)}
		replaceList = []planmodifier.List{listplanmodifier.RequiresReplace()}
		replaceObject = []planmodifier.Object{objectplanmodifier.RequiresReplace()}
	}
	computedString := func(description string) schema.StringAttribute {
		return schema.StringAttribute{
			MarkdownDescription: description,
			Optional:            true,
			Computed:            true,
			PlanModifiers:       append([]planmodifier.String{stringplanmodifier.UseStateForUnknown()}, replaceString...),
		}
	}
	return map[string]schema.Attribute{
		"size": schema.StringAttribute{
			MarkdownDescription: "Node size in the provider's vocabulary (droplet size, machine type, VM size, plan). " +
				"Compared case-insensitively, since providers report sizes in their own case.",
			Required: true,
		},
		"node_count": schema.Int64Attribute{
			MarkdownDescription: "Number of nodes (the API's `count`); the platform defaults it to 1. Changed in place. While " +
				"`autoscaling` is enabled the provider's autoscaler owns the live count, so drift in it is not reported.",
			Optional:      true,
			Computed:      true,
			PlanModifiers: []planmodifier.Int64{int64planmodifier.UseStateForUnknown()},
			Validators:    []validator.Int64{laneAtLeastOne()},
		},
		"labels": schema.MapAttribute{
			MarkdownDescription: "Kubernetes labels of the pool's nodes.",
			Optional:            true,
			ElementType:         types.StringType,
			PlanModifiers:       replaceMap,
		},
		"taints":      nodeGroupTaintsAttribute("Kubernetes taints of the pool's nodes.", replaceList...),
		"autoscaling": nodeGroupAutoscalingAttribute("Autoscaling bounds of the pool. Changed in place; removing the block disables autoscaling."),
		"zone": computedString("Zone the pool's nodes run in, where the provider places pools per zone. " +
			"When unset the provider's choice is recorded."),
		"root_volume_type": computedString("Root volume type of the pool's nodes, where the provider offers a choice. " +
			"When unset the provider's default is recorded."),
		"root_volume_size_gb": schema.Int64Attribute{
			MarkdownDescription: "Root volume size of the pool's nodes, in GB. When unset the provider's default is recorded.",
			Optional:            true,
			Computed:            true,
			PlanModifiers:       append([]planmodifier.Int64{int64planmodifier.UseStateForUnknown()}, replaceInt64...),
			Validators:          []validator.Int64{laneAtLeastOne()},
		},
		"spot": managedNodePoolSpotAttribute(replaceObject...),
	}
}

// managedPoolShapeChanges lists the pool arguments that differ between plan
// and state other than node_count and autoscaling. Unknown or null planned
// placement fields are not changes: they take the provider's value.
//
// Sizes compare case-insensitively. Labels must all be present in the state;
// a label the state holds and the plan drops is a change too, except while an
// import is adopted, when the state holds every label the provider reported.
func managedPoolShapeChanges(planned, prior managedNodePoolFields, isAdopting bool) []string {
	var changes []string
	if !planned.Size.IsUnknown() && !strings.EqualFold(planned.Size.ValueString(), prior.Size.ValueString()) {
		changes = append(changes, "size")
	}
	if !planned.Labels.IsUnknown() {
		labelsChanged := !managedPoolLabelsCovered(planned.Labels, prior.Labels)
		if !isAdopting && len(prior.Labels.Elements()) > len(planned.Labels.Elements()) {
			labelsChanged = true
		}
		if labelsChanged {
			changes = append(changes, "labels")
		}
	}
	if !nodeGroupTaintsEqual(planned.Taints, prior.Taints) {
		changes = append(changes, "taints")
	}
	if !planned.Zone.IsUnknown() && !planned.Zone.IsNull() && !planned.Zone.Equal(prior.Zone) {
		changes = append(changes, "zone")
	}
	if !planned.RootVolumeType.IsUnknown() && !planned.RootVolumeType.IsNull() && !planned.RootVolumeType.Equal(prior.RootVolumeType) {
		changes = append(changes, "root_volume_type")
	}
	if !planned.RootVolumeSizeGB.IsUnknown() && !planned.RootVolumeSizeGB.IsNull() && !planned.RootVolumeSizeGB.Equal(prior.RootVolumeSizeGB) {
		changes = append(changes, "root_volume_size_gb")
	}
	spotEqual := (planned.Spot == nil) == (prior.Spot == nil)
	if spotEqual && planned.Spot != nil {
		spotEqual = planned.Spot.Enabled.Equal(prior.Spot.Enabled) && planned.Spot.MaxPrice.Equal(prior.Spot.MaxPrice)
	}
	if !spotEqual {
		changes = append(changes, "spot")
	}
	return changes
}
