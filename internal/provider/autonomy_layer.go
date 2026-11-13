// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"ankra.io/terraform-provider-ankra/internal/client"
)

var (
	autonomyLevels = []string{"you_run_it", "together", "ankra_runs_it"}
	autonomyAreas  = []string{"keep_it_up", "ship_changes", "keep_it_safe", "keep_costs_down", "keep_it_current", "fix_my_code"}
)

// autonomyLimitsModel is the limits block of a "who runs it" layer.
type autonomyLimitsModel struct {
	MonthlyCostIncreaseCents     types.Int64 `tfsdk:"monthly_cost_increase_cents"`
	ProductionDowntimeSeconds    types.Int64 `tfsdk:"production_downtime_seconds"`
	NonProductionDowntimeSeconds types.Int64 `tfsdk:"non_production_downtime_seconds"`
	MayChangeData                types.Bool  `tfsdk:"may_change_data"`
}

// autonomyLayerAttributes are the level, areas and limits attributes of a
// resource that manages one stored layer.
func autonomyLayerAttributes(subject string) map[string]schema.Attribute {
	nonNegative := []validator.Int64{alertDNSRangeValidator{minimum: 0, maximum: 1 << 53}}
	return map[string]schema.Attribute{
		"level": schema.StringAttribute{
			MarkdownDescription: "Who runs every area of " + subject + " unless `areas` says otherwise: `you_run_it`, `together` or `ankra_runs_it`. " +
				"Unset leaves it to the layer below.",
			Optional:   true,
			Validators: []validator.String{organisationStringOneOf{values: autonomyLevels}},
		},
		"areas": schema.MapAttribute{
			MarkdownDescription: "Per-area levels, keyed by area: `keep_it_up`, `ship_changes`, `keep_it_safe`, `keep_costs_down`, " +
				"`keep_it_current` or `fix_my_code`.",
			Optional:    true,
			ElementType: types.StringType,
			Validators:  []validator.Map{autonomyAreasValidator{}},
		},
		"limits": schema.SingleNestedAttribute{
			MarkdownDescription: "Limits on what Ankra may do unasked.",
			Optional:            true,
			Attributes: map[string]schema.Attribute{
				"monthly_cost_increase_cents": schema.Int64Attribute{
					MarkdownDescription: "Most a change may add to the monthly cost, in cents.", Optional: true, Validators: nonNegative,
				},
				"production_downtime_seconds": schema.Int64Attribute{
					MarkdownDescription: "Most downtime a change may cause in production, in seconds.", Optional: true, Validators: nonNegative,
				},
				"non_production_downtime_seconds": schema.Int64Attribute{
					MarkdownDescription: "Most downtime a change may cause outside production, in seconds.", Optional: true, Validators: nonNegative,
				},
				"may_change_data": schema.BoolAttribute{
					MarkdownDescription: "Whether a change may alter data.", Optional: true,
				},
			},
		},
	}
}

// autonomyLayerIsManaged reports whether configuration sets any part of the
// layer; an unmanaged layer is neither written nor read.
func autonomyLayerIsManaged(level types.String, areas types.Map, limits *autonomyLimitsModel) bool {
	return !level.IsNull() || !areas.IsNull() || limits != nil
}

// autonomyLayerChanged reports whether the planned layer differs from the
// one in state.
func autonomyLayerChanged(plannedLevel, stateLevel types.String, plannedAreas, stateAreas types.Map, plannedLimits, stateLimits *autonomyLimitsModel) bool {
	if !plannedLevel.Equal(stateLevel) || !plannedAreas.Equal(stateAreas) {
		return true
	}
	if (plannedLimits == nil) != (stateLimits == nil) {
		return true
	}
	return plannedLimits != nil && *plannedLimits != *stateLimits
}

// autonomyLayerWrite builds the full replacement of a layer from
// configuration, starting from the stored layer.
func autonomyLayerWrite(ctx context.Context, level types.String, areas types.Map, limits *autonomyLimitsModel, stored *client.AutonomyLayer,
	diagnostics *diag.Diagnostics) client.AutonomyLayerWrite {
	write := client.AutonomyLayerWrite{Level: stringPointer(level), Areas: map[string]string{}, Expected: stored}
	for key, value := range applicationStringMap(ctx, areas, diagnostics) {
		write.Areas[key] = value
	}
	if limits != nil {
		write.Limits = client.AutonomyLayerLimits{
			MonthlyCostIncreaseCents:     int64Pointer(limits.MonthlyCostIncreaseCents),
			ProductionDowntimeSeconds:    int64Pointer(limits.ProductionDowntimeSeconds),
			NonProductionDowntimeSeconds: int64Pointer(limits.NonProductionDowntimeSeconds),
			MayChangeData:                boolPointer(limits.MayChangeData),
		}
	}
	return write
}

// autonomyLayerState maps a stored layer to state; a nil layer clears every
// part, which plans the configured layer again.
func autonomyLayerState(stored *client.AutonomyLayer, stateAreas types.Map, stateLimits *autonomyLimitsModel) (types.String, types.Map, *autonomyLimitsModel) {
	if stored == nil {
		return types.StringNull(), types.MapNull(types.StringType), nil
	}
	areas := types.MapNull(types.StringType)
	if len(stored.Areas) > 0 || !stateAreas.IsNull() {
		elements := make(map[string]attr.Value, len(stored.Areas))
		for key, value := range stored.Areas {
			elements[key] = types.StringValue(value)
		}
		areas = types.MapValueMust(types.StringType, elements)
	}
	limits := &autonomyLimitsModel{
		MonthlyCostIncreaseCents:     types.Int64PointerValue(stored.Limits.MonthlyCostIncreaseCents),
		ProductionDowntimeSeconds:    types.Int64PointerValue(stored.Limits.ProductionDowntimeSeconds),
		NonProductionDowntimeSeconds: types.Int64PointerValue(stored.Limits.NonProductionDowntimeSeconds),
		MayChangeData:                types.BoolPointerValue(stored.Limits.MayChangeData),
	}
	if stateLimits == nil && *limits == (autonomyLimitsModel{
		MonthlyCostIncreaseCents: types.Int64Null(), ProductionDowntimeSeconds: types.Int64Null(),
		NonProductionDowntimeSeconds: types.Int64Null(), MayChangeData: types.BoolNull(),
	}) {
		limits = nil
	}
	return types.StringPointerValue(stored.Level), areas, limits
}

// autonomyAreasValidator refuses an unknown area or level at plan time.
type autonomyAreasValidator struct{}

func (autonomyAreasValidator) Description(_ context.Context) string {
	return "keys must be areas (" + strings.Join(autonomyAreas, ", ") + ") and values levels (" + strings.Join(autonomyLevels, ", ") + ")"
}

func (areasValidator autonomyAreasValidator) MarkdownDescription(ctx context.Context) string {
	return areasValidator.Description(ctx)
}

func (autonomyAreasValidator) ValidateMap(_ context.Context, request validator.MapRequest, response *validator.MapResponse) {
	if request.ConfigValue.IsNull() || request.ConfigValue.IsUnknown() {
		return
	}
	for key, element := range request.ConfigValue.Elements() {
		if !slices.Contains(autonomyAreas, key) {
			response.Diagnostics.AddAttributeError(request.Path, "Invalid area",
				fmt.Sprintf("%q is not an area: use one of %s.", key, strings.Join(autonomyAreas, ", ")))
		}
		level, isString := element.(types.String)
		if isString && !level.IsNull() && !level.IsUnknown() && !slices.Contains(autonomyLevels, level.ValueString()) {
			response.Diagnostics.AddAttributeError(request.Path, "Invalid level",
				fmt.Sprintf("%q is not a level: use one of %s.", level.ValueString(), strings.Join(autonomyLevels, ", ")))
		}
	}
}

// resolveProvisionedLane reads the cluster's kind, which names the API lane
// of a provisioned cluster, and refuses a kind outside allowed.
func resolveProvisionedLane(ctx context.Context, apiClient *client.Client, clusterID string, allowed []string, feature string,
	diagnostics *diag.Diagnostics) (string, bool) {
	cluster, clusterError := apiClient.GetClusterByID(ctx, clusterID)
	if clusterError != nil {
		diagnostics.AddError("Unable to read cluster", clusterError.Error())
		return "", false
	}
	if cluster == nil {
		diagnostics.AddError("Cluster not found", fmt.Sprintf("No cluster %s is visible to this token.", clusterID))
		return "", false
	}
	if !slices.Contains(allowed, cluster.Kind) {
		diagnostics.AddError("Cluster kind not served",
			fmt.Sprintf("Cluster %s is of kind %q. %s is served on clusters Ankra provisions on %s.", clusterID, cluster.Kind, feature, strings.Join(allowed, ", ")))
		return "", false
	}
	return cluster.Kind, true
}
