// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/objectplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"

	"ankra.io/terraform-provider-ankra/internal/client"
)

// clusterAgentCIPlacementModel is the ci_placement object as configured.
type clusterAgentCIPlacementModel struct {
	NodeGroup    types.String                             `tfsdk:"node_group"`
	NodeSelector types.Map                                `tfsdk:"node_selector"`
	Tolerations  []clusterAgentCIPlacementTolerationModel `tfsdk:"tolerations"`
	Mode         types.String                             `tfsdk:"mode"`
}

type clusterAgentCIPlacementTolerationModel struct {
	Key      types.String `tfsdk:"key"`
	Operator types.String `tfsdk:"operator"`
	Value    types.String `tfsdk:"value"`
	Effect   types.String `tfsdk:"effect"`
}

var clusterAgentCIPlacementTolerationAttributeTypes = map[string]attr.Type{
	"key":      types.StringType,
	"operator": types.StringType,
	"value":    types.StringType,
	"effect":   types.StringType,
}

var clusterAgentCIPlacementAttributeTypes = map[string]attr.Type{
	"node_group":    types.StringType,
	"node_selector": types.MapType{ElemType: types.StringType},
	"tolerations":   types.ListType{ElemType: types.ObjectType{AttrTypes: clusterAgentCIPlacementTolerationAttributeTypes}},
	"mode":          types.StringType,
}

func clusterAgentCIPlacementAttribute() schema.SingleNestedAttribute {
	return schema.SingleNestedAttribute{
		MarkdownDescription: "Where every pipeline pod of the cluster is placed (step pods, their helpers and image builds), " +
			"the counterpart of `--node-group`, `--node-selector` and `--toleration`. The platform resolves `node_group` into the " +
			"group's node selector and tolerations and stores them merged with the ones named here; the configured values are kept " +
			"in state while the stored placement still contains them, so that resolution plans no diff. Destroying the resource " +
			"clears a placement the configuration has set.",
		Optional:      true,
		Computed:      true,
		PlanModifiers: []planmodifier.Object{objectplanmodifier.UseStateForUnknown()},
		Attributes: map[string]schema.Attribute{
			"node_group": schema.StringAttribute{
				MarkdownDescription: "Ankra node group of the cluster whose nodes run pipeline pods.",
				Optional:            true,
			},
			"node_selector": schema.MapAttribute{
				MarkdownDescription: "Node labels a pipeline pod's node must carry.",
				Optional:            true,
				ElementType:         types.StringType,
			},
			"tolerations": schema.ListNestedAttribute{
				MarkdownDescription: "Taints every pipeline pod tolerates.",
				Optional:            true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"key": schema.StringAttribute{
							MarkdownDescription: "Taint key.",
							Required:            true,
						},
						"operator": schema.StringAttribute{
							MarkdownDescription: "`Equal` or `Exists`. Defaults to `Equal` with a value and `Exists` without.",
							Optional:            true,
							Validators:          []validator.String{clusterSettingsOneOf("Equal", "Exists")},
						},
						"value": schema.StringAttribute{
							MarkdownDescription: "Taint value.",
							Optional:            true,
						},
						"effect": schema.StringAttribute{
							MarkdownDescription: "`NoSchedule`, `PreferNoSchedule` or `NoExecute`.",
							Optional:            true,
							Validators:          []validator.String{clusterSettingsOneOf("NoSchedule", "PreferNoSchedule", "NoExecute")},
						},
					},
				},
			},
			"mode": schema.StringAttribute{
				MarkdownDescription: "`required` (the default) pins pipeline pods to the selected nodes; `preferred` favours them " +
					"and lets pods run elsewhere when they have no room.",
				Optional:   true,
				Validators: []validator.String{clusterSettingsOneOf("required", "preferred")},
			},
		},
	}
}

// clusterAgentCIPlacementFromObject reads a configured placement into the
// API shape; null or unknown is nil.
func clusterAgentCIPlacementFromObject(ctx context.Context, object types.Object, diagnostics *diag.Diagnostics) *client.ClusterAgentCIPlacement {
	model := clusterAgentCIPlacementModelFrom(ctx, object, diagnostics)
	if model == nil {
		return nil
	}
	placement := &client.ClusterAgentCIPlacement{
		NodeGroup: model.NodeGroup.ValueString(),
		Mode:      model.Mode.ValueString(),
	}
	if !model.NodeSelector.IsNull() && !model.NodeSelector.IsUnknown() {
		selector := map[string]string{}
		diagnostics.Append(model.NodeSelector.ElementsAs(ctx, &selector, false)...)
		placement.NodeSelector = selector
	}
	for _, toleration := range model.Tolerations {
		placement.Tolerations = append(placement.Tolerations, client.ClusterAgentCIPlacementToleration{
			Key:      toleration.Key.ValueString(),
			Operator: toleration.Operator.ValueString(),
			Value:    toleration.Value.ValueString(),
			Effect:   toleration.Effect.ValueString(),
		})
	}
	return placement
}

func clusterAgentCIPlacementModelFrom(ctx context.Context, object types.Object, diagnostics *diag.Diagnostics) *clusterAgentCIPlacementModel {
	if object.IsNull() || object.IsUnknown() {
		return nil
	}
	var model clusterAgentCIPlacementModel
	diagnostics.Append(object.As(ctx, &model, basetypes.ObjectAsOptions{})...)
	return &model
}

// clusterAgentCIPlacementObject renders a stored placement as state, null
// when the cluster has none.
func clusterAgentCIPlacementObject(placement *client.ClusterAgentCIPlacement) types.Object {
	if placement == nil {
		return types.ObjectNull(clusterAgentCIPlacementAttributeTypes)
	}
	selector := types.MapNull(types.StringType)
	if len(placement.NodeSelector) > 0 {
		elements := map[string]attr.Value{}
		for key, value := range placement.NodeSelector {
			elements[key] = types.StringValue(value)
		}
		selector = types.MapValueMust(types.StringType, elements)
	}
	tolerationType := types.ObjectType{AttrTypes: clusterAgentCIPlacementTolerationAttributeTypes}
	tolerations := types.ListNull(tolerationType)
	if len(placement.Tolerations) > 0 {
		elements := make([]attr.Value, 0, len(placement.Tolerations))
		for _, toleration := range placement.Tolerations {
			elements = append(elements, types.ObjectValueMust(clusterAgentCIPlacementTolerationAttributeTypes, map[string]attr.Value{
				"key":      types.StringValue(toleration.Key),
				"operator": optionalString(toleration.Operator),
				"value":    optionalString(toleration.Value),
				"effect":   optionalString(toleration.Effect),
			}))
		}
		tolerations = types.ListValueMust(tolerationType, elements)
	}
	return types.ObjectValueMust(clusterAgentCIPlacementAttributeTypes, map[string]attr.Value{
		"node_group":    optionalString(placement.NodeGroup),
		"node_selector": selector,
		"tolerations":   tolerations,
		"mode":          optionalString(placement.Mode),
	})
}

// clusterAgentCIPlacementState decides what state holds for the placement:
// the prior (planned or stored) value while the stored placement still
// contains everything it names, otherwise the stored placement itself. The
// platform merges a node group's selector and tolerations into what was
// written and fills in defaults (mode, toleration operators), so comparing
// literally would plan a diff after every apply.
func clusterAgentCIPlacementState(ctx context.Context, prior types.Object, stored *client.ClusterAgentCIPlacement, diagnostics *diag.Diagnostics) types.Object {
	if stored == nil {
		return types.ObjectNull(clusterAgentCIPlacementAttributeTypes)
	}
	requested := clusterAgentCIPlacementFromObject(ctx, prior, diagnostics)
	if requested != nil && clusterAgentCIPlacementCovers(*stored, *requested) {
		return prior
	}
	return clusterAgentCIPlacementObject(stored)
}

// clusterAgentCIPlacementCovers reports whether the stored placement carries
// everything the requested one names.
func clusterAgentCIPlacementCovers(stored client.ClusterAgentCIPlacement, requested client.ClusterAgentCIPlacement) bool {
	if requested.NodeGroup != stored.NodeGroup {
		return false
	}
	requestedMode := requested.Mode
	if requestedMode == "" {
		requestedMode = "required"
	}
	storedMode := stored.Mode
	if storedMode == "" {
		storedMode = "required"
	}
	if requestedMode != storedMode {
		return false
	}
	for key, value := range requested.NodeSelector {
		if storedValue, present := stored.NodeSelector[key]; !present || storedValue != value {
			return false
		}
	}
	storedTolerations := map[client.ClusterAgentCIPlacementToleration]bool{}
	for _, toleration := range stored.Tolerations {
		storedTolerations[clusterAgentCIPlacementNormaliseToleration(toleration)] = true
	}
	for _, toleration := range requested.Tolerations {
		if !storedTolerations[clusterAgentCIPlacementNormaliseToleration(toleration)] {
			return false
		}
	}
	return true
}

// clusterAgentCIPlacementNormaliseToleration applies the platform's default
// operator: Equal with a value, Exists without.
func clusterAgentCIPlacementNormaliseToleration(toleration client.ClusterAgentCIPlacementToleration) client.ClusterAgentCIPlacementToleration {
	if toleration.Operator == "" {
		toleration.Operator = "Equal"
		if toleration.Value == "" {
			toleration.Operator = "Exists"
		}
	}
	return toleration
}
