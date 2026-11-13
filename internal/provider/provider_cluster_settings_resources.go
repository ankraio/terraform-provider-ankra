// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"ankra.io/terraform-provider-ankra/internal/client"
)

var ovhGatewayModels = []string{"s", "m", "l", "xl", "2xl", "3xl"}

// providerSettingsImportedKey marks, in private state, settings imported
// but not applied yet. The platform cannot read the UpCloud gateway plan,
// the zone pool or the OVH gateway model back, so the first apply after an
// import records the configured values without sending them: sending would
// rebuild a gateway that may already run them.
const providerSettingsImportedKey = "provider_settings_imported"

const upcloudGatewayPlanOutage = "Changing the UpCloud NAT gateway plan interrupts the cluster's outbound internet access for several " +
	"minutes while UpCloud rebuilds the gateway. Apply it at a quiet time."

const ovhGatewayModelChange = "Changing the OVH gateway model replaces the cluster's managed gateway, which can interrupt its outbound access."

var (
	_ resource.ResourceWithImportState = (*upcloudClusterSettingsResource)(nil)
	_ resource.ResourceWithImportState = (*ovhClusterSettingsResource)(nil)
	_ resource.ResourceWithModifyPlan  = (*upcloudClusterSettingsResource)(nil)
	_ resource.ResourceWithConfigure   = (*upcloudClusterSettingsResource)(nil)
	_ resource.ResourceWithConfigure   = (*ovhClusterSettingsResource)(nil)
	_ resource.ResourceWithModifyPlan  = (*ovhClusterSettingsResource)(nil)
)

type upcloudClusterSettingsResource struct {
	configuredClient
}

type upcloudClusterSettingsModel struct {
	ID               types.String `tfsdk:"id"`
	ClusterID        types.String `tfsdk:"cluster_id"`
	LoadBalancerPlan types.String `tfsdk:"load_balancer_plan"`
	GatewayPlan      types.String `tfsdk:"gateway_plan"`
	Zones            types.Set    `tfsdk:"zones"`
}

// NewUpcloudClusterSettingsResource returns the ankra_upcloud_cluster_settings
// resource, the counterpart of `ankra cluster upcloud load-balancer-plan set`,
// `ankra cluster upcloud gateway-plan` and the UpCloud zone pool route.
func NewUpcloudClusterSettingsResource() resource.Resource {
	return &upcloudClusterSettingsResource{}
}

func (settings *upcloudClusterSettingsResource) Metadata(_ context.Context, request resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_upcloud_cluster_settings"
}

func (settings *upcloudClusterSettingsResource) Schema(_ context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: "Day-2 network settings of an UpCloud cluster Ankra provisions: the plan of its ingress load balancer, the " +
			"plan of its NAT gateways and its zone pool. Each is written only when it changes, and the plan warns before a " +
			"`gateway_plan` is sent: that interrupts the cluster's outbound internet access for several minutes while UpCloud " +
			"rebuilds the gateway. The platform cannot read the gateway plan or the zone pool back, so both keep the configured " +
			"value, and the first apply after an import records them without sending them. Zones can only be added (the cluster " +
			"must run the `wireguard_mesh` network mode and keep at least 3 zones), so removing one fails the plan. Destroy " +
			"leaves every setting as it is.",
		Attributes: map[string]schema.Attribute{
			"id":         clusterSettingsIDAttribute("The cluster id."),
			"cluster_id": clusterSettingsClusterIDAttribute(),
			"load_balancer_plan": schema.StringAttribute{
				MarkdownDescription: "UpCloud plan of the cluster's ingress load balancer, checked against UpCloud's catalogue. Unset leaves it to the platform.",
				Optional:            true,
			},
			"gateway_plan": schema.StringAttribute{
				MarkdownDescription: "UpCloud plan of the cluster's NAT gateways. A stopped cluster's gateways take it at the next start.",
				Optional:            true,
			},
			"zones": schema.SetAttribute{
				MarkdownDescription: "Zones the cluster's pool must hold. Zones can only be added; every new zone gets its own router, private network and NAT gateway.",
				Optional:            true,
				ElementType:         types.StringType,
			},
		},
	}
}

// ModifyPlan warns before a gateway plan change, which interrupts egress,
// and refuses removing a zone: the platform can only grow the pool.
func (settings *upcloudClusterSettingsResource) ModifyPlan(ctx context.Context, request resource.ModifyPlanRequest, response *resource.ModifyPlanResponse) {
	if request.Plan.Raw.IsNull() {
		return
	}
	var plan upcloudClusterSettingsModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}
	if request.State.Raw.IsNull() {
		if !plan.GatewayPlan.IsNull() {
			response.Diagnostics.AddAttributeWarning(path.Root("gateway_plan"), "NAT gateway plan will be sent", upcloudGatewayPlanOutage)
		}
		return
	}
	var state upcloudClusterSettingsModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	imported, diagnostics := request.Private.GetKey(ctx, providerSettingsImportedKey)
	response.Diagnostics.Append(diagnostics...)
	if len(imported) == 0 && !plan.GatewayPlan.IsNull() && !plan.GatewayPlan.Equal(state.GatewayPlan) {
		response.Diagnostics.AddAttributeWarning(path.Root("gateway_plan"), "NAT gateway plan will change", upcloudGatewayPlanOutage)
	}
	if plan.Zones.IsUnknown() || state.Zones.IsNull() {
		return
	}
	planned := organisationStringsFromSet(ctx, plan.Zones, &response.Diagnostics)
	var removed []string
	for _, zone := range organisationStringsFromSet(ctx, state.Zones, &response.Diagnostics) {
		if !slices.Contains(planned, zone) {
			removed = append(removed, zone)
		}
	}
	if len(removed) > 0 {
		response.Diagnostics.AddAttributeError(path.Root("zones"), "Zones cannot be removed",
			fmt.Sprintf("The UpCloud zone pool can only grow; %s would be removed. Keep the zones in configuration.", strings.Join(removed, ", ")))
	}
}

func (settings *upcloudClusterSettingsResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	var plan upcloudClusterSettingsModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if settings.write(ctx, &plan, nil, &response.Diagnostics) {
		response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
		response.Diagnostics.Append(trackScope(ctx, settings.client, response.Private)...)
	}
}

func (settings *upcloudClusterSettingsResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	var plan, state upcloudClusterSettingsModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	imported, diagnostics := request.Private.GetKey(ctx, providerSettingsImportedKey)
	response.Diagnostics.Append(diagnostics...)
	if len(imported) > 0 {
		state.GatewayPlan = plan.GatewayPlan
		state.Zones = plan.Zones
		response.Diagnostics.AddWarning("Imported settings recorded",
			"The platform cannot read the NAT gateway plan or the zone pool back, so the configured values were recorded without being "+
				"sent or checked. Make sure they match what the cluster runs.")
	}
	if settings.write(ctx, &plan, &state, &response.Diagnostics) {
		response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
		response.Diagnostics.Append(response.Private.SetKey(ctx, providerSettingsImportedKey, nil)...)
	}
}

func (settings *upcloudClusterSettingsResource) write(ctx context.Context, plan, state *upcloudClusterSettingsModel, diagnostics *diag.Diagnostics) bool {
	apiClient := settings.requireClient(diagnostics)
	if apiClient == nil || diagnostics.HasError() {
		return false
	}
	clusterID := plan.ClusterID.ValueString()
	if state == nil {
		if _, ok := resolveProvisionedLane(ctx, apiClient, clusterID, []string{client.LaneUpCloud}, "This setting", diagnostics); !ok {
			return false
		}
	}
	isChanged := func(planned, stored types.String) bool {
		return !planned.IsNull() && (state == nil || !planned.Equal(stored))
	}
	var storedLoadBalancerPlan, storedGatewayPlan types.String
	if state != nil {
		storedLoadBalancerPlan, storedGatewayPlan = state.LoadBalancerPlan, state.GatewayPlan
	}
	if isChanged(plan.LoadBalancerPlan, storedLoadBalancerPlan) {
		if setError := apiClient.SetUpcloudLoadBalancerPlan(ctx, clusterID, plan.LoadBalancerPlan.ValueString()); setError != nil {
			diagnostics.AddError("Unable to change the load balancer plan", clusterSettingsErrorText(setError, clusterID))
			return false
		}
	}
	if isChanged(plan.GatewayPlan, storedGatewayPlan) {
		if setError := apiClient.SetUpcloudGatewayPlan(ctx, clusterID, plan.GatewayPlan.ValueString()); setError != nil {
			diagnostics.AddError("Unable to change the NAT gateway plan", clusterSettingsErrorText(setError, clusterID))
			return false
		}
	}
	if !plan.Zones.IsNull() && (state == nil || !plan.Zones.Equal(state.Zones)) {
		zones := organisationStringsFromSet(ctx, plan.Zones, diagnostics)
		if diagnostics.HasError() {
			return false
		}
		if _, extendError := apiClient.ExtendUpcloudZonePool(ctx, clusterID, zones); extendError != nil {
			diagnostics.AddError("Unable to extend the zone pool", clusterSettingsErrorText(extendError, clusterID))
			return false
		}
	}
	plan.ID = plan.ClusterID
	return true
}

func (settings *upcloudClusterSettingsResource) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	apiClient := settings.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state upcloudClusterSettingsModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	clusterID := state.ClusterID.ValueString()
	loadBalancerPlan, readError := apiClient.GetUpcloudLoadBalancerPlan(ctx, clusterID)
	if clusterSettingsReadNotFound(ctx, readError, apiClient, clusterID, response) {
		return
	}
	if readError != nil {
		response.Diagnostics.AddError("Unable to read the load balancer plan", readError.Error())
		return
	}
	state.ID = state.ClusterID
	if !state.LoadBalancerPlan.IsNull() || loadBalancerPlan != nil {
		state.LoadBalancerPlan = types.StringPointerValue(loadBalancerPlan)
	}
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
	response.Diagnostics.Append(trackScope(ctx, apiClient, response.Private)...)
}

// Delete leaves every setting as it is: there is no plan to return to, and
// zones cannot be removed.
func (settings *upcloudClusterSettingsResource) Delete(_ context.Context, _ resource.DeleteRequest, _ *resource.DeleteResponse) {
}

// ImportState imports by cluster id. The first apply records the configured
// gateway plan and zones without sending them.
func (settings *upcloudClusterSettingsResource) ImportState(ctx context.Context, request resource.ImportStateRequest, response *resource.ImportStateResponse) {
	clusterSettingsImportClusterID(ctx, request, response)
	response.Diagnostics.Append(response.Private.SetKey(ctx, providerSettingsImportedKey, []byte("true"))...)
}

type ovhClusterSettingsResource struct {
	configuredClient
}

type ovhClusterSettingsModel struct {
	ID           types.String `tfsdk:"id"`
	ClusterID    types.String `tfsdk:"cluster_id"`
	GatewayModel types.String `tfsdk:"gateway_model"`
}

// NewOVHClusterSettingsResource returns the ankra_ovh_cluster_settings
// resource, the counterpart of `ankra cluster ovh gateway-model`.
func NewOVHClusterSettingsResource() resource.Resource {
	return &ovhClusterSettingsResource{}
}

func (settings *ovhClusterSettingsResource) Metadata(_ context.Context, request resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_ovh_cluster_settings"
}

func (settings *ovhClusterSettingsResource) Schema(_ context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: "Day-2 settings of an OVHcloud cluster Ankra provisions (`ankra cluster ovh gateway-model`): the model of " +
			"its managed gateway. The plan warns before the model is sent. The platform cannot read the model back, so " +
			"`gateway_model` keeps the configured value, and the first apply after an import records it without sending it. " +
			"Destroy leaves the gateway as it is.",
		Attributes: map[string]schema.Attribute{
			"id":         clusterSettingsIDAttribute("The cluster id."),
			"cluster_id": clusterSettingsClusterIDAttribute(),
			"gateway_model": schema.StringAttribute{
				MarkdownDescription: "Model of the managed gateway: `s`, `m`, `l`, `xl`, `2xl` or `3xl`.",
				Required:            true,
				Validators:          []validator.String{organisationStringOneOf{values: ovhGatewayModels}},
			},
		},
	}
}

func (settings *ovhClusterSettingsResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	var plan ovhClusterSettingsModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if settings.write(ctx, &plan, true, &response.Diagnostics) {
		response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
		response.Diagnostics.Append(trackScope(ctx, settings.client, response.Private)...)
	}
}

func (settings *ovhClusterSettingsResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	var plan ovhClusterSettingsModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	imported, diagnostics := request.Private.GetKey(ctx, providerSettingsImportedKey)
	response.Diagnostics.Append(diagnostics...)
	if len(imported) > 0 {
		plan.ID = plan.ClusterID
		response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
		response.Diagnostics.Append(response.Private.SetKey(ctx, providerSettingsImportedKey, nil)...)
		response.Diagnostics.AddWarning("Imported gateway model recorded",
			"The platform cannot read the gateway model back, so the configured model was recorded without being sent or checked. "+
				"Make sure it matches what the cluster runs.")
		return
	}
	if settings.write(ctx, &plan, false, &response.Diagnostics) {
		response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
	}
}

// ModifyPlan warns before the gateway model is sent, which replaces the
// managed gateway; the first apply after an import sends nothing.
func (settings *ovhClusterSettingsResource) ModifyPlan(ctx context.Context, request resource.ModifyPlanRequest, response *resource.ModifyPlanResponse) {
	if request.Plan.Raw.IsNull() {
		return
	}
	var plan ovhClusterSettingsModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}
	if request.State.Raw.IsNull() {
		response.Diagnostics.AddAttributeWarning(path.Root("gateway_model"), "Gateway model will be sent", ovhGatewayModelChange)
		return
	}
	var state ovhClusterSettingsModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	imported, diagnostics := request.Private.GetKey(ctx, providerSettingsImportedKey)
	response.Diagnostics.Append(diagnostics...)
	if len(imported) == 0 && !plan.GatewayModel.Equal(state.GatewayModel) {
		response.Diagnostics.AddAttributeWarning(path.Root("gateway_model"), "Gateway model will change", ovhGatewayModelChange)
	}
}

func (settings *ovhClusterSettingsResource) write(ctx context.Context, plan *ovhClusterSettingsModel, isCreate bool, diagnostics *diag.Diagnostics) bool {
	apiClient := settings.requireClient(diagnostics)
	if apiClient == nil || diagnostics.HasError() {
		return false
	}
	clusterID := plan.ClusterID.ValueString()
	if isCreate {
		if _, ok := resolveProvisionedLane(ctx, apiClient, clusterID, []string{client.LaneOVH}, "The gateway model", diagnostics); !ok {
			return false
		}
	}
	if setError := apiClient.SetOVHGatewayModel(ctx, clusterID, plan.GatewayModel.ValueString()); setError != nil {
		diagnostics.AddError("Unable to change the gateway model", clusterSettingsErrorText(setError, clusterID))
		return false
	}
	plan.ID = plan.ClusterID
	return true
}

func (settings *ovhClusterSettingsResource) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	apiClient := settings.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state ovhClusterSettingsModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	cluster, clusterError := apiClient.GetClusterByID(ctx, state.ClusterID.ValueString())
	if clusterError != nil {
		response.Diagnostics.AddError("Unable to read cluster", clusterError.Error())
		return
	}
	if cluster == nil {
		removeMissingCluster(ctx, apiClient, response)
		return
	}
	state.ID = state.ClusterID
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
	response.Diagnostics.Append(trackScope(ctx, apiClient, response.Private)...)
}

// Delete leaves the gateway as it is.
func (settings *ovhClusterSettingsResource) Delete(_ context.Context, _ resource.DeleteRequest, _ *resource.DeleteResponse) {
}

// ImportState imports by cluster id; the first apply records the configured
// model without sending it.
func (settings *ovhClusterSettingsResource) ImportState(ctx context.Context, request resource.ImportStateRequest, response *resource.ImportStateResponse) {
	clusterSettingsImportClusterID(ctx, request, response)
	response.Diagnostics.Append(response.Private.SetKey(ctx, providerSettingsImportedKey, []byte("true"))...)
}
