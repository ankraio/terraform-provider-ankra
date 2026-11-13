// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/mapplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/objectplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"

	"ankra.io/terraform-provider-ankra/internal/client"
)

var (
	autonomyLevels = []string{"you_run_it", "together", "ankra_runs_it"}
	autonomyAreas  = []string{"keep_it_up", "ship_changes", "keep_it_safe", "keep_costs_down", "keep_it_current", "fix_my_code"}
)

// autonomyLayerPrivateKey holds, in private state, the stored layer as the
// last refresh or write saw it. A write sends it as the expected layer, so a
// layer changed between plan and apply is refused instead of overwritten.
const autonomyLayerPrivateKey = "autonomy_layer"

var autonomyLimitsAttributeTypes = map[string]attr.Type{
	"monthly_cost_increase_cents":     types.Int64Type,
	"production_downtime_seconds":     types.Int64Type,
	"non_production_downtime_seconds": types.Int64Type,
	"may_change_data":                 types.BoolType,
}

// autonomyLimitsModel is the limits block of a "who runs it" layer.
type autonomyLimitsModel struct {
	MonthlyCostIncreaseCents     types.Int64 `tfsdk:"monthly_cost_increase_cents"`
	ProductionDowntimeSeconds    types.Int64 `tfsdk:"production_downtime_seconds"`
	NonProductionDowntimeSeconds types.Int64 `tfsdk:"non_production_downtime_seconds"`
	MayChangeData                types.Bool  `tfsdk:"may_change_data"`
}

// autonomyLayerParts are the level, areas and limits of one stored layer as
// a resource holds them.
type autonomyLayerParts struct {
	Level  *types.String
	Areas  *types.Map
	Limits *types.Object
}

// autonomyLayerStore reads and replaces one stored layer: an application's
// or one of its environments'.
type autonomyLayerStore struct {
	read  func(ctx context.Context) (*client.AutonomyLayer, error)
	write func(ctx context.Context, write client.AutonomyLayerWrite) (*client.AutonomyLayer, error)
}

// autonomyLayerAttributes are the level, areas and limits attributes of a
// resource that manages one stored layer. Unset, each reports what is stored.
func autonomyLayerAttributes(subject string) map[string]schema.Attribute {
	nonNegative := []validator.Int64{alertDNSRangeValidator{minimum: 0, maximum: 1 << 53}}
	return map[string]schema.Attribute{
		"level": schema.StringAttribute{
			MarkdownDescription: "Who runs every area of " + subject + " unless `areas` says otherwise: `you_run_it`, `together` or `ankra_runs_it`. " +
				"Unset reports what is stored; a layer with no level leaves it to the layer below.",
			Optional:      true,
			Computed:      true,
			Validators:    []validator.String{organisationStringOneOf{values: autonomyLevels}},
			PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
		},
		"areas": schema.MapAttribute{
			MarkdownDescription: "Per-area levels, keyed by area: `keep_it_up`, `ship_changes`, `keep_it_safe`, `keep_costs_down`, " +
				"`keep_it_current` or `fix_my_code`.",
			Optional:      true,
			Computed:      true,
			ElementType:   types.StringType,
			Validators:    []validator.Map{autonomyAreasValidator{}},
			PlanModifiers: []planmodifier.Map{mapplanmodifier.UseStateForUnknown()},
		},
		"limits": schema.SingleNestedAttribute{
			MarkdownDescription: "Limits on what Ankra may do unasked.",
			Optional:            true,
			Computed:            true,
			PlanModifiers:       []planmodifier.Object{objectplanmodifier.UseStateForUnknown()},
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

// autonomyLayerCreate adopts or writes the layer on create. Unset parts are
// unknown in the plan. A stored layer that holds a part the configuration
// does not set is refused, because writing the configured layer would
// silently drop that part: the user imports it instead.
func autonomyLayerCreate(ctx context.Context, store autonomyLayerStore, parts autonomyLayerParts, importCommand string, private privateStateWriter,
	diagnostics *diag.Diagnostics) bool {
	stored, readError := store.read(ctx)
	if readError != nil {
		diagnostics.AddError("Unable to read the stored autonomy layer", readError.Error())
		return false
	}
	isConfigured := !parts.Level.IsUnknown() || !parts.Areas.IsUnknown() || !parts.Limits.IsUnknown()
	if stored != nil && autonomyLayerHasUnsetParts(parts, stored) {
		diagnostics.AddError("A stored autonomy layer already exists",
			"The platform already stores a \"who runs it\" layer here with parts this configuration does not set, and creating the "+
				"resource would replace the whole layer and drop them. Import it first ("+importCommand+") so the plan shows every "+
				"part, or set level, areas and limits explicitly.")
		return false
	}
	result := stored
	if isConfigured {
		write := autonomyLayerWrite(ctx, parts, stored, diagnostics)
		if diagnostics.HasError() {
			return false
		}
		written, writeError := store.write(ctx, write)
		if writeError != nil {
			diagnostics.AddError("Unable to set who runs it", writeError.Error())
			return false
		}
		result = written
	}
	autonomyLayerApply(ctx, parts, result, diagnostics)
	autonomyLayerRemember(ctx, private, result, diagnostics)
	return !diagnostics.HasError()
}

// autonomyLayerUpdate replaces the layer when a part changed, expecting the
// layer the last refresh saw.
func autonomyLayerUpdate(ctx context.Context, store autonomyLayerStore, plan, state autonomyLayerParts, seen privateStateReader,
	private privateStateWriter, diagnostics *diag.Diagnostics) bool {
	if plan.Level.Equal(*state.Level) && plan.Areas.Equal(*state.Areas) && plan.Limits.Equal(*state.Limits) {
		return true
	}
	expected, isRemembered := autonomyLayerRemembered(ctx, seen)
	if !isRemembered {
		stored, readError := store.read(ctx)
		if readError != nil {
			diagnostics.AddError("Unable to read the stored autonomy layer", readError.Error())
			return false
		}
		expected = stored
	}
	write := autonomyLayerWrite(ctx, plan, expected, diagnostics)
	if diagnostics.HasError() {
		return false
	}
	written, writeError := store.write(ctx, write)
	if writeError != nil {
		diagnostics.AddError("Unable to set who runs it", writeError.Error())
		return false
	}
	autonomyLayerApply(ctx, plan, written, diagnostics)
	autonomyLayerRemember(ctx, private, written, diagnostics)
	return !diagnostics.HasError()
}

// autonomyLayerRefresh reads the stored layer into parts and remembers it.
func autonomyLayerRefresh(ctx context.Context, store autonomyLayerStore, parts autonomyLayerParts, private privateStateWriter, diagnostics *diag.Diagnostics) bool {
	stored, readError := store.read(ctx)
	if readError != nil {
		diagnostics.AddError("Unable to read the stored autonomy layer", readError.Error())
		return false
	}
	autonomyLayerApply(ctx, parts, stored, diagnostics)
	autonomyLayerRemember(ctx, private, stored, diagnostics)
	return !diagnostics.HasError()
}

func autonomyLayerHasUnsetParts(parts autonomyLayerParts, stored *client.AutonomyLayer) bool {
	return (parts.Level.IsUnknown() && stored.Level != nil) || (parts.Areas.IsUnknown() && len(stored.Areas) > 0) ||
		(parts.Limits.IsUnknown() && stored.Limits != (client.AutonomyLayerLimits{}))
}

// autonomyLayerWrite builds the full replacement of a layer; an unset part
// is written empty.
func autonomyLayerWrite(ctx context.Context, parts autonomyLayerParts, expected *client.AutonomyLayer, diagnostics *diag.Diagnostics) client.AutonomyLayerWrite {
	write := client.AutonomyLayerWrite{Level: stringPointer(*parts.Level), Areas: map[string]string{}, Expected: expected}
	for key, value := range applicationStringMap(ctx, *parts.Areas, diagnostics) {
		write.Areas[key] = value
	}
	if !parts.Limits.IsNull() && !parts.Limits.IsUnknown() {
		var limits autonomyLimitsModel
		diagnostics.Append(parts.Limits.As(ctx, &limits, basetypes.ObjectAsOptions{})...)
		write.Limits = client.AutonomyLayerLimits{
			MonthlyCostIncreaseCents:     int64Pointer(limits.MonthlyCostIncreaseCents),
			ProductionDowntimeSeconds:    int64Pointer(limits.ProductionDowntimeSeconds),
			NonProductionDowntimeSeconds: int64Pointer(limits.NonProductionDowntimeSeconds),
			MayChangeData:                boolPointer(limits.MayChangeData),
		}
	}
	return write
}

// autonomyLayerApply sets parts from a stored layer: an empty part is null.
func autonomyLayerApply(ctx context.Context, parts autonomyLayerParts, stored *client.AutonomyLayer, diagnostics *diag.Diagnostics) {
	*parts.Level = types.StringNull()
	*parts.Areas = types.MapNull(types.StringType)
	*parts.Limits = types.ObjectNull(autonomyLimitsAttributeTypes)
	if stored == nil {
		return
	}
	*parts.Level = types.StringPointerValue(stored.Level)
	if len(stored.Areas) > 0 {
		elements := make(map[string]attr.Value, len(stored.Areas))
		for key, value := range stored.Areas {
			elements[key] = types.StringValue(value)
		}
		*parts.Areas = types.MapValueMust(types.StringType, elements)
	}
	if stored.Limits != (client.AutonomyLayerLimits{}) {
		limits, limitsDiagnostics := types.ObjectValueFrom(ctx, autonomyLimitsAttributeTypes, autonomyLimitsModel{
			MonthlyCostIncreaseCents:     types.Int64PointerValue(stored.Limits.MonthlyCostIncreaseCents),
			ProductionDowntimeSeconds:    types.Int64PointerValue(stored.Limits.ProductionDowntimeSeconds),
			NonProductionDowntimeSeconds: types.Int64PointerValue(stored.Limits.NonProductionDowntimeSeconds),
			MayChangeData:                types.BoolPointerValue(stored.Limits.MayChangeData),
		})
		diagnostics.Append(limitsDiagnostics...)
		*parts.Limits = limits
	}
}

func autonomyLayerRemember(ctx context.Context, private privateStateWriter, stored *client.AutonomyLayer, diagnostics *diag.Diagnostics) {
	encoded, encodeError := json.Marshal(stored)
	if encodeError != nil {
		diagnostics.AddError("Unable to remember the stored autonomy layer", encodeError.Error())
		return
	}
	diagnostics.Append(private.SetKey(ctx, autonomyLayerPrivateKey, encoded)...)
}

// autonomyLayerRemembered returns the layer the last refresh or write saw;
// the second result is false when none was remembered.
func autonomyLayerRemembered(ctx context.Context, private privateStateReader) (*client.AutonomyLayer, bool) {
	encoded, diagnostics := private.GetKey(ctx, autonomyLayerPrivateKey)
	if diagnostics.HasError() || len(encoded) == 0 {
		return nil, false
	}
	var stored *client.AutonomyLayer
	if json.Unmarshal(encoded, &stored) != nil {
		return nil, false
	}
	return stored, true
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
