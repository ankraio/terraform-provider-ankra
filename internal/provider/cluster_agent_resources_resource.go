// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"encoding/json"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"ankra.io/terraform-provider-ankra/internal/client"
)

var (
	_ resource.Resource                = (*clusterAgentResourcesResource)(nil)
	_ resource.ResourceWithConfigure   = (*clusterAgentResourcesResource)(nil)
	_ resource.ResourceWithImportState = (*clusterAgentResourcesResource)(nil)
)

// clusterAgentResourcesStoredKey is the private state key holding, per
// quantity Terraform wrote, the canonical spelling the platform stored. A
// read that finds the same canonical value keeps the configured spelling
// ("2048Mi" stays "2048Mi" while the platform reports "2Gi").
const clusterAgentResourcesStoredKey = "stored_quantities"

type clusterAgentResourcesResource struct {
	configuredClient
}

type clusterAgentResourcesModel struct {
	ID            types.String `tfsdk:"id"`
	ClusterID     types.String `tfsdk:"cluster_id"`
	MemoryLimit   types.String `tfsdk:"memory_limit"`
	MemoryRequest types.String `tfsdk:"memory_request"`
	CPULimit      types.String `tfsdk:"cpu_limit"`
	CPURequest    types.String `tfsdk:"cpu_request"`
	ApplyState    types.String `tfsdk:"apply_state"`
}

// clusterAgentResourcesQuantities pairs each quantity's wire name with its
// attribute in the model and its value in the API shapes.
type clusterAgentResourcesQuantity struct {
	name      string
	attribute func(*clusterAgentResourcesModel) *types.String
	stored    func(*client.ClusterAgentResources) *string
	update    func(*client.ClusterAgentResourcesUpdate) **string
}

var clusterAgentResourcesQuantities = []clusterAgentResourcesQuantity{
	{"memory_limit", func(model *clusterAgentResourcesModel) *types.String { return &model.MemoryLimit },
		func(resources *client.ClusterAgentResources) *string { return resources.MemoryLimit },
		func(update *client.ClusterAgentResourcesUpdate) **string { return &update.MemoryLimit }},
	{"memory_request", func(model *clusterAgentResourcesModel) *types.String { return &model.MemoryRequest },
		func(resources *client.ClusterAgentResources) *string { return resources.MemoryRequest },
		func(update *client.ClusterAgentResourcesUpdate) **string { return &update.MemoryRequest }},
	{"cpu_limit", func(model *clusterAgentResourcesModel) *types.String { return &model.CPULimit },
		func(resources *client.ClusterAgentResources) *string { return resources.CPULimit },
		func(update *client.ClusterAgentResourcesUpdate) **string { return &update.CPULimit }},
	{"cpu_request", func(model *clusterAgentResourcesModel) *types.String { return &model.CPURequest },
		func(resources *client.ClusterAgentResources) *string { return resources.CPURequest },
		func(update *client.ClusterAgentResourcesUpdate) **string { return &update.CPURequest }},
}

// NewClusterAgentResourcesResource returns the ankra_cluster_agent_resources
// resource, the counterpart of `ankra cluster agent resources set`.
func NewClusterAgentResourcesResource() resource.Resource {
	return &clusterAgentResourcesResource{}
}

func (resourcesResource *clusterAgentResourcesResource) Metadata(_ context.Context, request resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_cluster_agent_resources"
}

func (resourcesResource *clusterAgentResourcesResource) Schema(_ context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	quantity := func(description string) schema.StringAttribute {
		return schema.StringAttribute{
			MarkdownDescription: description + " A Kubernetes quantity; an empty string uses the agent chart's default. " +
				"Unset, the platform's value is kept and reported.",
			Optional:      true,
			Computed:      true,
			PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
		}
	}
	response.Schema = schema.Schema{
		MarkdownDescription: "The CPU and memory the cluster's Ankra agent requests and is limited to. The counterpart of " +
			"`ankra cluster agent resources set`. Only the attributes set in configuration are written. The platform stores each " +
			"quantity in its canonical spelling; the configured spelling is kept while it means the same quantity. Destroying the " +
			"resource returns each quantity the configuration has set to the chart default.",
		Attributes: map[string]schema.Attribute{
			"id":             clusterSettingsIDAttribute("The cluster id."),
			"cluster_id":     clusterSettingsClusterIDAttribute(),
			"memory_limit":   quantity("Memory limit, for example `1Gi`."),
			"memory_request": quantity("Memory request, for example `512Mi`."),
			"cpu_limit":      quantity("CPU limit, for example `1` or `500m`."),
			"cpu_request":    quantity("CPU request, for example `100m`."),
			"apply_state": schema.StringAttribute{
				MarkdownDescription: "How the last write reached the agent: `applied`, `pending_upgrade`, `agent_offline` or `not_set`.",
				Computed:            true,
			},
		},
	}
}

func (resourcesResource *clusterAgentResourcesResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	var plan, config clusterAgentResourcesModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	response.Diagnostics.Append(request.Config.Get(ctx, &config)...)
	if response.Diagnostics.HasError() {
		return
	}
	stored, ok := resourcesResource.write(ctx, &plan, config, nil, &response.Diagnostics)
	if !ok {
		return
	}
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
	response.Diagnostics.Append(response.Private.SetKey(ctx, clusterAgentResourcesStoredKey, stored)...)
}

func (resourcesResource *clusterAgentResourcesResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	var plan, config clusterAgentResourcesModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	response.Diagnostics.Append(request.Config.Get(ctx, &config)...)
	previous, diagnostics := request.Private.GetKey(ctx, clusterAgentResourcesStoredKey)
	response.Diagnostics.Append(diagnostics...)
	if response.Diagnostics.HasError() {
		return
	}
	stored, ok := resourcesResource.write(ctx, &plan, config, previous, &response.Diagnostics)
	if !ok {
		return
	}
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
	response.Diagnostics.Append(response.Private.SetKey(ctx, clusterAgentResourcesStoredKey, stored)...)
}

// write sends the configured quantities and records the canonical spelling
// the platform stored for each, merged with the ones recorded before (a
// quantity the configuration stops naming is still reset on destroy).
func (resourcesResource *clusterAgentResourcesResource) write(ctx context.Context, plan *clusterAgentResourcesModel, config clusterAgentResourcesModel, previous []byte, diagnostics *diag.Diagnostics) ([]byte, bool) {
	apiClient := resourcesResource.requireClient(diagnostics)
	if apiClient == nil {
		return nil, false
	}
	recorded := clusterAgentResourcesRecorded(previous)
	update := client.ClusterAgentResourcesUpdate{}
	for _, quantity := range clusterAgentResourcesQuantities {
		*quantity.update(&update) = stringPointer(*quantity.attribute(&config))
	}
	var resources *client.ClusterAgentResources
	var err error
	if update.IsEmpty() {
		resources, err = apiClient.GetClusterAgentResources(ctx, plan.ClusterID.ValueString())
	} else {
		resources, err = apiClient.UpdateClusterAgentResources(ctx, plan.ClusterID.ValueString(), update)
	}
	if err != nil {
		diagnostics.AddError("Unable to set cluster agent resources", clusterSettingsErrorText(err, plan.ClusterID.ValueString()))
		return nil, false
	}
	for _, quantity := range clusterAgentResourcesQuantities {
		if !quantity.attribute(&config).IsNull() {
			recorded[quantity.name] = clusterSettingsValueOr(quantity.stored(resources), "")
		}
	}
	applyClusterAgentResources(plan, resources, recorded)
	encoded, _ := json.Marshal(recorded)
	return encoded, true
}

func (resourcesResource *clusterAgentResourcesResource) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	apiClient := resourcesResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state clusterAgentResourcesModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	previous, diagnostics := request.Private.GetKey(ctx, clusterAgentResourcesStoredKey)
	response.Diagnostics.Append(diagnostics...)
	if response.Diagnostics.HasError() {
		return
	}
	resources, err := apiClient.GetClusterAgentResources(ctx, state.ClusterID.ValueString())
	if clusterSettingsReadNotFound(ctx, err, apiClient, state.ClusterID.ValueString(), response) {
		return
	}
	if err != nil {
		response.Diagnostics.AddError("Unable to read cluster agent resources", err.Error())
		return
	}
	applyClusterAgentResources(&state, resources, clusterAgentResourcesRecorded(previous))
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
}

// Delete returns each quantity Terraform wrote to the chart default; a
// quantity only reported is left as it is.
func (resourcesResource *clusterAgentResourcesResource) Delete(ctx context.Context, request resource.DeleteRequest, response *resource.DeleteResponse) {
	apiClient := resourcesResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state clusterAgentResourcesModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	previous, diagnostics := request.Private.GetKey(ctx, clusterAgentResourcesStoredKey)
	response.Diagnostics.Append(diagnostics...)
	if response.Diagnostics.HasError() {
		return
	}
	recorded := clusterAgentResourcesRecorded(previous)
	update := client.ClusterAgentResourcesUpdate{}
	for _, quantity := range clusterAgentResourcesQuantities {
		if _, written := recorded[quantity.name]; written {
			empty := ""
			*quantity.update(&update) = &empty
		}
	}
	if update.IsEmpty() {
		return
	}
	_, err := apiClient.UpdateClusterAgentResources(ctx, state.ClusterID.ValueString(), update)
	if deleteError := clusterSettingsDeleteError(err, state.ClusterID.ValueString()); deleteError != nil {
		response.Diagnostics.AddError("Unable to restore cluster agent resources", deleteError.Error())
	}
}

// ImportState imports by cluster id.
func (resourcesResource *clusterAgentResourcesResource) ImportState(ctx context.Context, request resource.ImportStateRequest, response *resource.ImportStateResponse) {
	clusterSettingsImportClusterID(ctx, request, response)
}

func clusterAgentResourcesRecorded(encoded []byte) map[string]string {
	recorded := map[string]string{}
	if len(encoded) > 0 {
		_ = json.Unmarshal(encoded, &recorded)
	}
	return recorded
}

// applyClusterAgentResources copies the stored quantities into the model,
// keeping a known spelling while the platform still stores the canonical
// value recorded for it.
func applyClusterAgentResources(model *clusterAgentResourcesModel, resources *client.ClusterAgentResources, recorded map[string]string) {
	model.ID = model.ClusterID
	for _, quantity := range clusterAgentResourcesQuantities {
		attribute := quantity.attribute(model)
		stored := clusterSettingsValueOr(quantity.stored(resources), "")
		canonical, written := recorded[quantity.name]
		if written && canonical == stored && !attribute.IsNull() && !attribute.IsUnknown() {
			continue
		}
		*attribute = types.StringValue(stored)
	}
	model.ApplyState = types.StringValue(resources.ApplyState)
}
