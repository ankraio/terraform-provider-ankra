// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/setplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"ankra.io/terraform-provider-ankra/internal/client"
)

// osPatchWindowDays are the day names the platform stores, Monday first.
var osPatchWindowDays = []string{"mon", "tue", "wed", "thu", "fri", "sat", "sun"}

var (
	_ resource.Resource                = (*clusterOSPatchPolicyResource)(nil)
	_ resource.ResourceWithConfigure   = (*clusterOSPatchPolicyResource)(nil)
	_ resource.ResourceWithImportState = (*clusterOSPatchPolicyResource)(nil)
)

type clusterOSPatchPolicyResource struct {
	configuredClient
}

type clusterOSPatchPolicyModel struct {
	ID                   types.String `tfsdk:"id"`
	ClusterID            types.String `tfsdk:"cluster_id"`
	ClusterKind          types.String `tfsdk:"cluster_kind"`
	Enabled              types.Bool   `tfsdk:"enabled"`
	WindowDays           types.Set    `tfsdk:"window_days"`
	WindowStartHourUTC   types.Int64  `tfsdk:"window_start_hour_utc"`
	WindowDurationHours  types.Int64  `tfsdk:"window_duration_hours"`
	CadenceDays          types.Int64  `tfsdk:"cadence_days"`
	IncludeStatefulNodes types.Bool   `tfsdk:"include_stateful_nodes"`
	SecurityLagDays      types.Int64  `tfsdk:"security_lag_days"`
	WindowDescription    types.String `tfsdk:"window_description"`
}

// NewClusterOSPatchPolicyResource returns the ankra_cluster_os_patch_policy
// resource, the counterpart of `ankra cluster os-patches policy`.
func NewClusterOSPatchPolicyResource() resource.Resource {
	return &clusterOSPatchPolicyResource{}
}

func (policyResource *clusterOSPatchPolicyResource) Metadata(_ context.Context, request resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_cluster_os_patch_policy"
}

func (policyResource *clusterOSPatchPolicyResource) Schema(_ context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: "The rolling OS security-patch policy of a cluster Ankra provisions on Ankra Cloud, AWS, DigitalOcean, " +
			"Hetzner, OVHcloud, Scaleway or UpCloud (`ankra cluster os-patches policy`): whether Ankra patches the cluster's hosts " +
			"on its own, one host at a time, inside a weekly maintenance window in UTC, how often, whether it may drain nodes that " +
			"carry single-replica stateful workloads, and after how many days held-back security updates count as a security lag. " +
			"Only the attributes set in configuration are written; an unset attribute keeps the stored value (or the platform " +
			"default: disabled, Monday to Thursday 02:00 UTC for 4 hours, weekly, stateful nodes skipped, 14 days) and reports it. " +
			"Turning the policy off while a patch run is in flight is refused. Destroy leaves the policy as it is, so an enabled " +
			"policy keeps patching: set `enabled = false` and apply before destroying the resource to stop it. " +
			"Needs an Ankra platform from 2026-11-08 or later.",
		Attributes: map[string]schema.Attribute{
			"id":         clusterSettingsIDAttribute("The cluster id."),
			"cluster_id": clusterSettingsClusterIDAttribute(),
			"cluster_kind": schema.StringAttribute{
				MarkdownDescription: "Kind of the cluster, resolved from the platform; it selects the API lane.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"enabled": schema.BoolAttribute{
				MarkdownDescription: "Whether Ankra starts a patch run on its own inside the window, at most once per `cadence_days`. " +
					"A run drains, patches and can reboot every host. A run can be started by hand either way.",
				Optional:      true,
				Computed:      true,
				PlanModifiers: []planmodifier.Bool{boolplanmodifier.UseStateForUnknown()},
			},
			"window_days": schema.SetAttribute{
				MarkdownDescription: "Days the window opens on: `mon`, `tue`, `wed`, `thu`, `fri`, `sat` or `sun`. A window that runs past midnight belongs to the day it opened on.",
				Optional:            true,
				Computed:            true,
				ElementType:         types.StringType,
				Validators:          []validator.Set{osPatchWindowDaysValidator{}},
				PlanModifiers:       []planmodifier.Set{setplanmodifier.UseStateForUnknown()},
			},
			"window_start_hour_utc": schema.Int64Attribute{
				MarkdownDescription: "Hour the window opens at, in UTC, 0 to 23.",
				Optional:            true,
				Computed:            true,
				Validators:          []validator.Int64{alertDNSRangeValidator{minimum: 0, maximum: 23}},
				PlanModifiers:       []planmodifier.Int64{int64planmodifier.UseStateForUnknown()},
			},
			"window_duration_hours": schema.Int64Attribute{
				MarkdownDescription: "How long the window stays open, 1 to 12 hours.",
				Optional:            true,
				Computed:            true,
				Validators:          []validator.Int64{alertDNSRangeValidator{minimum: 1, maximum: 12}},
				PlanModifiers:       []planmodifier.Int64{int64planmodifier.UseStateForUnknown()},
			},
			"cadence_days": schema.Int64Attribute{
				MarkdownDescription: "Minimum number of days between two automatic runs, 1 to 90.",
				Optional:            true,
				Computed:            true,
				Validators:          []validator.Int64{alertDNSRangeValidator{minimum: 1, maximum: 90}},
				PlanModifiers:       []planmodifier.Int64{int64planmodifier.UseStateForUnknown()},
			},
			"include_stateful_nodes": schema.BoolAttribute{
				MarkdownDescription: "Whether a run may drain nodes that carry single-replica stateful workloads and database node pools. " +
					"Off, they are skipped and reported.",
				Optional:      true,
				Computed:      true,
				PlanModifiers: []planmodifier.Bool{boolplanmodifier.UseStateForUnknown()},
			},
			"security_lag_days": schema.Int64Attribute{
				MarkdownDescription: "Days held-back security updates may stay pending before the cluster reports a security lag, 1 to 90.",
				Optional:            true,
				Computed:            true,
				Validators:          []validator.Int64{alertDNSRangeValidator{minimum: 1, maximum: 90}},
				PlanModifiers:       []planmodifier.Int64{int64planmodifier.UseStateForUnknown()},
			},
			"window_description": schema.StringAttribute{
				MarkdownDescription: "The window in words, as the platform describes it.",
				Computed:            true,
			},
		},
	}
}

func (policyResource *clusterOSPatchPolicyResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	apiClient := policyResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var plan clusterOSPatchPolicyModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}
	lane, ok := resolveOSPatchPolicyLane(ctx, apiClient, plan.ClusterID.ValueString(), &response.Diagnostics)
	if !ok {
		return
	}
	plan.ClusterKind = types.StringValue(lane)
	if policyResource.write(ctx, apiClient, &plan, &response.Diagnostics) {
		response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
		response.Diagnostics.Append(trackScope(ctx, apiClient, response.Private)...)
	}
}

func (policyResource *clusterOSPatchPolicyResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	apiClient := policyResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var plan, state clusterOSPatchPolicyModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	plan.ClusterKind = state.ClusterKind
	if policyResource.write(ctx, apiClient, &plan, &response.Diagnostics) {
		response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
	}
}

func (policyResource *clusterOSPatchPolicyResource) write(ctx context.Context, apiClient *client.Client, plan *clusterOSPatchPolicyModel, diagnostics *diag.Diagnostics) bool {
	clusterID := plan.ClusterID.ValueString()
	update := client.OSPatchPolicyUpdate{
		Enabled:              boolPointer(plan.Enabled),
		WindowStartHourUTC:   int64Pointer(plan.WindowStartHourUTC),
		WindowDurationHours:  int64Pointer(plan.WindowDurationHours),
		CadenceDays:          int64Pointer(plan.CadenceDays),
		IncludeStatefulNodes: boolPointer(plan.IncludeStatefulNodes),
		SecurityLagDays:      int64Pointer(plan.SecurityLagDays),
	}
	if !plan.WindowDays.IsNull() && !plan.WindowDays.IsUnknown() {
		update.WindowDays = clusterSettingsStringsFromSet(ctx, plan.WindowDays, diagnostics)
		if diagnostics.HasError() {
			return false
		}
	}
	policy, updateError := apiClient.UpdateOSPatchPolicy(ctx, plan.ClusterKind.ValueString(), clusterID, update)
	if updateError != nil {
		diagnostics.AddError("Unable to set the OS patch policy", osPatchPolicyErrorText(updateError, clusterID))
		return false
	}
	plan.ID = plan.ClusterID
	clusterOSPatchPolicyApply(plan, policy)
	return true
}

func (policyResource *clusterOSPatchPolicyResource) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	apiClient := policyResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state clusterOSPatchPolicyModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	clusterID := state.ClusterID.ValueString()
	lane := state.ClusterKind.ValueString()
	if lane == "" {
		cluster, clusterError := apiClient.GetClusterByID(ctx, clusterID)
		if clusterError != nil {
			response.Diagnostics.AddError("Unable to read cluster", clusterError.Error())
			return
		}
		if cluster == nil {
			removeMissingCluster(ctx, apiClient, response)
			return
		}
		if !slices.Contains(client.OSPatchPolicyLanes, cluster.Kind) {
			response.Diagnostics.AddError("Cluster has no OS patch policy", osPatchPolicyLaneDetail(clusterID, cluster.Kind))
			return
		}
		lane = cluster.Kind
	}
	policy, readError := apiClient.GetOSPatchPolicy(ctx, lane, clusterID)
	if clusterSettingsReadNotFound(ctx, readError, apiClient, clusterID, response) {
		return
	}
	if readError != nil {
		response.Diagnostics.AddError("Unable to read the OS patch policy", osPatchPolicyErrorText(readError, clusterID))
		return
	}
	state.ID = state.ClusterID
	state.ClusterKind = types.StringValue(lane)
	clusterOSPatchPolicyApply(&state, policy)
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
	response.Diagnostics.Append(trackScope(ctx, apiClient, response.Private)...)
}

// Delete leaves the policy as it is: the platform has no route that removes
// a stored policy, and writing the defaults back would silently stop or
// reshape patching the cluster's owners may rely on.
func (policyResource *clusterOSPatchPolicyResource) Delete(_ context.Context, _ resource.DeleteRequest, _ *resource.DeleteResponse) {
}

// ImportState imports by cluster id; the read resolves the cluster's kind.
func (policyResource *clusterOSPatchPolicyResource) ImportState(ctx context.Context, request resource.ImportStateRequest, response *resource.ImportStateResponse) {
	clusterSettingsImportClusterID(ctx, request, response)
}

func clusterOSPatchPolicyApply(model *clusterOSPatchPolicyModel, policy *client.OSPatchPolicy) {
	days := make([]attr.Value, 0, len(policy.Window.Days))
	for _, day := range policy.Window.Days {
		days = append(days, types.StringValue(day))
	}
	model.Enabled = types.BoolValue(policy.Enabled)
	model.WindowDays = types.SetValueMust(types.StringType, days)
	model.WindowStartHourUTC = types.Int64Value(policy.Window.StartHourUTC)
	model.WindowDurationHours = types.Int64Value(policy.Window.DurationHours)
	model.CadenceDays = types.Int64Value(policy.CadenceDays)
	model.IncludeStatefulNodes = types.BoolValue(policy.IncludeStatefulNodes)
	model.SecurityLagDays = types.Int64Value(policy.SecurityLagDays)
	model.WindowDescription = optionalString(policy.WindowDescription)
}

// resolveOSPatchPolicyLane reads the cluster's kind, which names the API
// lane, and refuses a cluster the OS patch lane does not serve.
func resolveOSPatchPolicyLane(ctx context.Context, apiClient *client.Client, clusterID string, diagnostics *diag.Diagnostics) (string, bool) {
	cluster, clusterError := apiClient.GetClusterByID(ctx, clusterID)
	if clusterError != nil {
		diagnostics.AddError("Unable to read cluster", clusterError.Error())
		return "", false
	}
	if cluster == nil {
		diagnostics.AddAttributeError(path.Root("cluster_id"), "Cluster not found",
			fmt.Sprintf("No cluster %s is visible to this token.", clusterID))
		return "", false
	}
	if !slices.Contains(client.OSPatchPolicyLanes, cluster.Kind) {
		diagnostics.AddAttributeError(path.Root("cluster_id"), "Cluster has no OS patch policy", osPatchPolicyLaneDetail(clusterID, cluster.Kind))
		return "", false
	}
	return cluster.Kind, true
}

func osPatchPolicyLaneDetail(clusterID, kind string) string {
	return fmt.Sprintf("Cluster %s is of kind %q. The rolling OS patch lane serves clusters Ankra provisions on %s; "+
		"imported and managed Kubernetes clusters patch their nodes by other means.", clusterID, kind, strings.Join(client.OSPatchPolicyLanes, ", "))
}

// osPatchPolicyErrorText explains the errors the policy routes answer: a
// platform without the lane's routes answers 404 or 405 on them.
func osPatchPolicyErrorText(requestError error, clusterID string) string {
	var apiError *client.APIError
	if errors.As(requestError, &apiError) && apiError.StatusCode == http.StatusMethodNotAllowed {
		return requestError.Error() + "\n\nThe OS patch policy routes need an Ankra platform from 2026-11-08 or later."
	}
	return clusterSettingsErrorText(requestError, clusterID)
}

// osPatchWindowDaysValidator requires at least one day, each in the short
// lower-case form the platform stores, so the stored set matches the
// configured one.
type osPatchWindowDaysValidator struct{}

func (osPatchWindowDaysValidator) Description(_ context.Context) string {
	return "at least one of " + strings.Join(osPatchWindowDays, ", ")
}

func (daysValidator osPatchWindowDaysValidator) MarkdownDescription(ctx context.Context) string {
	return daysValidator.Description(ctx)
}

func (daysValidator osPatchWindowDaysValidator) ValidateSet(ctx context.Context, request validator.SetRequest, response *validator.SetResponse) {
	if request.ConfigValue.IsNull() || request.ConfigValue.IsUnknown() {
		return
	}
	elements := request.ConfigValue.Elements()
	if len(elements) == 0 {
		response.Diagnostics.AddAttributeError(request.Path, "No window days", "The window must open on at least one day: "+daysValidator.Description(ctx)+".")
		return
	}
	for _, element := range elements {
		day, isString := element.(types.String)
		if !isString || day.IsNull() || day.IsUnknown() {
			continue
		}
		if !slices.Contains(osPatchWindowDays, day.ValueString()) {
			response.Diagnostics.AddAttributeError(request.Path, "Invalid window day",
				fmt.Sprintf("%q is not accepted: write the day as one of %s.", day.ValueString(), strings.Join(osPatchWindowDays, ", ")))
		}
	}
}
