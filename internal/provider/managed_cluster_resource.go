// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-timeouts/resource/timeouts"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"ankra.io/terraform-provider-ankra/internal/client"
)

var (
	_ resource.Resource                   = (*managedClusterResource)(nil)
	_ resource.ResourceWithConfigure      = (*managedClusterResource)(nil)
	_ resource.ResourceWithImportState    = (*managedClusterResource)(nil)
	_ resource.ResourceWithValidateConfig = (*managedClusterResource)(nil)
)

type managedClusterResource struct {
	configuredClient
}

type managedClusterNodePoolModel struct {
	managedNodePoolFields
}

type managedClusterModel struct {
	ID                   types.String                  `tfsdk:"id"`
	ClusterID            types.String                  `tfsdk:"cluster_id"`
	State                types.String                  `tfsdk:"state"`
	Kind                 types.String                  `tfsdk:"kind"`
	Provider             types.String                  `tfsdk:"provider_name"`
	Name                 types.String                  `tfsdk:"name"`
	Description          types.String                  `tfsdk:"description"`
	CredentialID         types.String                  `tfsdk:"credential_id"`
	Location             types.String                  `tfsdk:"location"`
	KubernetesVersion    types.String                  `tfsdk:"kubernetes_version"`
	CNI                  types.String                  `tfsdk:"cni"`
	HA                   types.Bool                    `tfsdk:"ha"`
	ClusterPlan          types.String                  `tfsdk:"cluster_plan"`
	ProviderOptions      types.String                  `tfsdk:"provider_options"`
	NodePools            []managedClusterNodePoolModel `tfsdk:"node_pools"`
	GitopsBranch         types.String                  `tfsdk:"gitops_branch"`
	GitopsCredentialName types.String                  `tfsdk:"gitops_credential_name"`
	GitopsRepository     types.String                  `tfsdk:"gitops_repository"`
	RetentionPolicy      types.String                  `tfsdk:"retention_policy"`
	ForceDestroy         types.Bool                    `tfsdk:"force_destroy"`
	WaitForReady         types.Bool                    `tfsdk:"wait_for_ready"`
	Timeouts             timeouts.Value                `tfsdk:"timeouts"`
}

// NewManagedClusterResource returns the ankra_managed_cluster resource, the
// counterpart of `ankra cluster managed create` and `ankra cluster managed
// delete`.
func NewManagedClusterResource() resource.Resource {
	return &managedClusterResource{}
}

func (clusterResource *managedClusterResource) Metadata(_ context.Context, request resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_managed_cluster"
}

func (clusterResource *managedClusterResource) Schema(ctx context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	attributes := identityAttributes()
	attributes["provider_name"] = managedProviderAttribute("Managed Kubernetes provider.")
	attributes["name"] = withStringValidators(requiredReplaceString("Name of the cluster."), laneNameValidator{})
	attributes["description"] = optionalReplaceString("Description of the cluster.")
	attributes["credential_id"] = optionalReplaceString(
		"Cloud credential id used to create the cluster. Required for every provider except `ankracloud_k8s`, " +
			"which uses the organisation's built-in Ankra Cloud credential when unset.")
	attributes["location"] = requiredReplaceString("Region or location of the cluster, in the provider's vocabulary.")
	attributes["kubernetes_version"] = optionalReplaceString(
		"Kubernetes version. Unset takes the provider's default. Upgrading is `ankra cluster managed upgrade`, which this resource does not do.")
	attributes["cni"] = optionalReplaceString("Container network interface, where the provider offers a choice.")
	attributes["ha"] = schema.BoolAttribute{
		MarkdownDescription: "Ask for a highly available control plane, where the provider offers one.",
		Optional:            true,
		PlanModifiers:       []planmodifier.Bool{boolplanmodifier.RequiresReplace()},
	}
	attributes["cluster_plan"] = optionalReplaceString("Control-plane plan or tier, where the provider sells several.")
	attributes["provider_options"] = schema.StringAttribute{
		MarkdownDescription: "The provider's own options as a JSON object, for example " +
			"`jsonencode({ resource_group = \"rg\", private_cluster = false })` for `aks` or " +
			"`jsonencode({ private_network_id = \"...\" })` for `kapsule`. Sent under the key named after " +
			"`provider_name` (`aks`, `eks`, `gke`, `ovh_mks`, `kapsule`, `ankracloud_k8s`, `uks`). Changing it forces replacement.",
		Optional:      true,
		PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
		Validators:    []validator.String{managedProviderOptionsValidator{}},
	}
	attributes["node_pools"] = schema.ListNestedAttribute{
		MarkdownDescription: "The cluster's initial node pools; at least one. `node_count` and `autoscaling` change in " +
			"place. Adding, removing or reordering pools, or changing any other pool argument, replaces the cluster: " +
			"manage further pools with `ankra_managed_node_pool` instead.",
		Required:      true,
		NestedObject:  schema.NestedAttributeObject{Attributes: managedNodePoolAttributes(false)},
		PlanModifiers: []planmodifier.List{managedNodePoolsReplaceModifier{}},
	}
	attributes["gitops_branch"] = defaultedReplaceString("GitOps branch to commit the generated stack to.", "master")
	attributes["gitops_credential_name"] = optionalReplaceString(
		"GitOps credential name; commits the generated stack to Git when set with `gitops_repository`.")
	attributes["gitops_repository"] = optionalReplaceString("GitOps repository (`owner/name`) to commit the generated stack to.")
	attributes["retention_policy"] = schema.StringAttribute{
		MarkdownDescription: "What a destroy does with the cluster's retained provider resources: `delete` (default, " +
			"the platform's default) or `retain`. Only affects a future destroy, so it never replaces the cluster.",
		Optional:   true,
		Computed:   true,
		Default:    stringdefault.StaticString("delete"),
		Validators: []validator.String{laneOneOf(laneRetentionPolicy...)},
	}
	attributes["force_destroy"] = schema.BoolAttribute{
		MarkdownDescription: "Send `force=true` when deleting, skipping the platform's teardown guards. Defaults to " +
			"`false`. Only affects a future destroy.",
		Optional: true,
		Computed: true,
		Default:  booldefault.StaticBool(false),
	}
	attributes["wait_for_ready"] = schema.BoolAttribute{
		MarkdownDescription: "Wait until the provider has built the cluster (the platform reports it past " +
			"`creating`) before the resource is considered created. Defaults to `true`.",
		Optional: true,
		Computed: true,
		Default:  booldefault.StaticBool(true),
	}
	response.Schema = schema.Schema{
		MarkdownDescription: "A managed Kubernetes cluster that the provider (DigitalOcean DOKS, UpCloud UKS, GKE, AKS, " +
			"EKS, OVHcloud MKS, Scaleway Kapsule or Ankra Cloud Kubernetes) builds and Ankra connects. The " +
			"counterpart of `ankra cluster managed create` and `ankra cluster managed delete`.",
		Attributes: attributes,
		Blocks:     commonProvisionedBlocks(ctx),
	}
}

// ValidateConfig refuses a configuration the platform would refuse: a
// credential is required for every provider but ankracloud_k8s.
func (clusterResource *managedClusterResource) ValidateConfig(ctx context.Context, request resource.ValidateConfigRequest, response *resource.ValidateConfigResponse) {
	var provider, credentialID types.String
	response.Diagnostics.Append(request.Config.GetAttribute(ctx, path.Root("provider_name"), &provider)...)
	response.Diagnostics.Append(request.Config.GetAttribute(ctx, path.Root("credential_id"), &credentialID)...)
	if response.Diagnostics.HasError() || provider.IsUnknown() || provider.IsNull() || !credentialID.IsNull() {
		return
	}
	if provider.ValueString() != client.ManagedProviderAnkraCloudK8s {
		response.Diagnostics.AddAttributeError(path.Root("credential_id"), "Missing credential_id",
			fmt.Sprintf("Provider %q needs a cloud credential; only %q falls back to the built-in credential.",
				provider.ValueString(), client.ManagedProviderAnkraCloudK8s))
	}
}

func (clusterResource *managedClusterResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	apiClient := clusterResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var plan managedClusterModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}

	createRequest := client.ManagedClusterCreate{
		Provider:             plan.Provider.ValueString(),
		Name:                 plan.Name.ValueString(),
		Description:          optionalStringPointer(plan.Description),
		CredentialID:         optionalStringPointer(plan.CredentialID),
		Location:             plan.Location.ValueString(),
		KubernetesVersion:    optionalStringPointer(plan.KubernetesVersion),
		CNI:                  optionalStringPointer(plan.CNI),
		HA:                   boolPointer(plan.HA),
		ClusterPlan:          optionalStringPointer(plan.ClusterPlan),
		GitopsCredentialName: optionalStringPointer(plan.GitopsCredentialName),
		GitopsRepository:     optionalStringPointer(plan.GitopsRepository),
		GitopsBranch:         plan.GitopsBranch.ValueString(),
	}
	if options := plan.ProviderOptions.ValueString(); strings.TrimSpace(options) != "" {
		createRequest.ProviderOptions = json.RawMessage(options)
	}
	for _, pool := range plan.NodePools {
		createRequest.NodePools = append(createRequest.NodePools,
			managedNodePoolCreateFromFields(ctx, pool.managedNodePoolFields, &response.Diagnostics))
	}
	if response.Diagnostics.HasError() {
		return
	}

	clusterID, err := apiClient.CreateManagedCluster(ctx, createRequest)
	if err != nil {
		response.Diagnostics.AddError("Unable to create managed cluster", err.Error())
		return
	}
	plan.ID = types.StringValue(clusterID)
	plan.ClusterID = types.StringValue(clusterID)
	plan.State = types.StringUnknown()
	plan.Kind = types.StringUnknown()
	for index := range plan.NodePools {
		if plan.NodePools[index].Count.IsUnknown() {
			plan.NodePools[index].Count = types.Int64Value(createRequest.NodePools[index].Count)
		}
	}
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}

	cluster := settleManagedCluster(ctx, apiClient, clusterID, plan.WaitForReady, plan.Timeouts.Create, &response.Diagnostics)
	if cluster == nil {
		return
	}
	applyClusterIdentity(&plan.State, &plan.Kind, cluster)
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
}

// settleManagedCluster waits for the provider to build the cluster, or reads
// it once when the wait is off.
func settleManagedCluster(ctx context.Context, apiClient *client.Client, clusterID string, waitForReady types.Bool, createTimeout timeoutFunc, diagnostics *diag.Diagnostics) *client.Cluster {
	if !waitForReady.IsNull() && !waitForReady.IsUnknown() && !waitForReady.ValueBool() {
		cluster, err := apiClient.GetClusterByID(ctx, clusterID)
		if err != nil {
			diagnostics.AddError("Unable to read cluster", err.Error())
			return nil
		}
		if cluster == nil {
			return &client.Cluster{ID: clusterID}
		}
		return cluster
	}
	wait := resolveTimeout(ctx, createTimeout, defaultCreateTimeout, diagnostics)
	if diagnostics.HasError() {
		return nil
	}
	waitContext, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	cluster, err := apiClient.WaitForClusterState(waitContext, clusterID, client.WaitOptions{
		ReadyStates:   client.ManagedClusterReadyStates,
		FailureStates: []string{"deprovisioning", "stopped"},
	})
	if err != nil {
		diagnostics.AddError("Cluster did not become ready", err.Error())
		return nil
	}
	return cluster
}

func (clusterResource *managedClusterResource) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	apiClient := clusterResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state managedClusterModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	clusterID := state.ClusterID.ValueString()
	cluster, err := apiClient.GetClusterByID(ctx, clusterID)
	if err != nil {
		response.Diagnostics.AddError("Unable to read cluster", err.Error())
		return
	}
	if cluster == nil {
		response.State.RemoveResource(ctx)
		return
	}
	state.ID = types.StringValue(cluster.ID)
	if cluster.Name != "" {
		state.Name = types.StringValue(cluster.Name)
	}
	applyClusterIdentity(&state.State, &state.Kind, cluster)

	pools, err := apiClient.ListManagedNodePools(ctx, state.Provider.ValueString(), clusterID)
	if removeWhenNotFound(ctx, err, response) {
		return
	}
	if err != nil {
		response.Diagnostics.AddError("Unable to read managed node pools", err.Error())
		return
	}
	poolsByName := make(map[string]client.ManagedNodePool, len(pools))
	for _, pool := range pools {
		poolsByName[pool.Name] = pool
	}
	refreshed := make([]managedClusterNodePoolModel, 0, len(state.NodePools))
	for _, statePool := range state.NodePools {
		pool, found := poolsByName[statePool.Name.ValueString()]
		if !found {
			// A pool removed outside Terraform drops out of state, so the
			// plan shows the cluster's node_pools no longer match.
			continue
		}
		refreshManagedNodePoolFields(&statePool.managedNodePoolFields, pool)
		refreshed = append(refreshed, statePool)
	}
	state.NodePools = refreshed
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
}

// Update changes the initial pools' count and autoscaling in place. Every
// other change replaces the cluster (RequiresReplace, or the node_pools
// modifier), so nothing else reaches here except the destroy-time flags.
func (clusterResource *managedClusterResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	apiClient := clusterResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var plan, state managedClusterModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	for index := range plan.NodePools {
		if index >= len(state.NodePools) {
			break
		}
		update, changed := managedNodePoolSizingUpdate(plan.NodePools[index].managedNodePoolFields, state.NodePools[index].managedNodePoolFields)
		if !changed {
			continue
		}
		poolName := plan.NodePools[index].Name.ValueString()
		if err := apiClient.UpdateManagedNodePool(ctx, state.Provider.ValueString(), state.ClusterID.ValueString(), poolName, update); err != nil {
			response.Diagnostics.AddError("Unable to update managed node pool "+poolName, err.Error())
			return
		}
	}
	plan.ID = state.ID
	plan.ClusterID = state.ClusterID
	plan.State = state.State
	plan.Kind = state.Kind
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
}

func (clusterResource *managedClusterResource) Delete(ctx context.Context, request resource.DeleteRequest, response *resource.DeleteResponse) {
	apiClient := clusterResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state managedClusterModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	wait := resolveTimeout(ctx, state.Timeouts.Delete, defaultDeleteTimeout, &response.Diagnostics)
	if response.Diagnostics.HasError() {
		return
	}
	deleteContext, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	retentionPolicy := state.RetentionPolicy.ValueString()
	if retentionPolicy == "" {
		retentionPolicy = "delete"
	}
	if err := apiClient.DeleteManagedCluster(deleteContext, state.Provider.ValueString(), state.ClusterID.ValueString(),
		state.ForceDestroy.ValueBool(), retentionPolicy); err != nil {
		response.Diagnostics.AddError("Unable to delete managed cluster", err.Error())
	}
}

// ImportState imports by `<provider_name>/<cluster_id>`. The creation
// arguments are not returned by the platform and must match the
// configuration; node_pools is read back for the pools the configuration
// declares.
func (clusterResource *managedClusterResource) ImportState(ctx context.Context, request resource.ImportStateRequest, response *resource.ImportStateResponse) {
	provider, clusterID, ok := splitClusterScopedID(request.ID)
	if !ok || !slices.Contains(client.ManagedProviders, provider) {
		response.Diagnostics.AddError("Invalid import id",
			fmt.Sprintf("Expected <provider_name>/<cluster_id> with provider_name one of %s, got %q.",
				strings.Join(client.ManagedProviders, ", "), request.ID))
		return
	}
	response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("id"), clusterID)...)
	response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("cluster_id"), clusterID)...)
	response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("provider_name"), provider)...)
	response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("retention_policy"), "delete")...)
	response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("force_destroy"), false)...)
	response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("wait_for_ready"), true)...)
}

// managedProviderOptionsValidator requires provider_options to be a JSON
// object.
type managedProviderOptionsValidator struct{}

func (managedProviderOptionsValidator) Description(_ context.Context) string {
	return "value must be a JSON object"
}

func (optionsValidator managedProviderOptionsValidator) MarkdownDescription(ctx context.Context) string {
	return optionsValidator.Description(ctx)
}

func (managedProviderOptionsValidator) ValidateString(_ context.Context, request validator.StringRequest, response *validator.StringResponse) {
	if request.ConfigValue.IsNull() || request.ConfigValue.IsUnknown() {
		return
	}
	var object map[string]any
	if err := json.Unmarshal([]byte(request.ConfigValue.ValueString()), &object); err != nil || object == nil {
		response.Diagnostics.AddAttributeError(request.Path, "Invalid provider_options",
			"provider_options must be a JSON object; build it with jsonencode({ ... }).")
	}
}

// managedNodePoolsReplaceModifier replaces the cluster when node_pools
// changes in any way the platform cannot apply in place: a pool added,
// removed or reordered, or any pool argument other than count and
// autoscaling changed.
type managedNodePoolsReplaceModifier struct{}

func (managedNodePoolsReplaceModifier) Description(_ context.Context) string {
	return "Replaces the cluster unless only the pools' count or autoscaling changed."
}

func (modifier managedNodePoolsReplaceModifier) MarkdownDescription(ctx context.Context) string {
	return modifier.Description(ctx)
}

func (managedNodePoolsReplaceModifier) PlanModifyList(ctx context.Context, request planmodifier.ListRequest, response *planmodifier.ListResponse) {
	if request.State.Raw.IsNull() || request.Plan.Raw.IsNull() || request.PlanValue.IsUnknown() || request.StateValue.IsNull() {
		return
	}
	var planned, prior []managedClusterNodePoolModel
	response.Diagnostics.Append(request.PlanValue.ElementsAs(ctx, &planned, false)...)
	response.Diagnostics.Append(request.StateValue.ElementsAs(ctx, &prior, false)...)
	if response.Diagnostics.HasError() {
		return
	}
	if len(planned) != len(prior) {
		response.RequiresReplace = true
		return
	}
	for index := range planned {
		if !managedPoolShapeEqual(planned[index].managedNodePoolFields, prior[index].managedNodePoolFields) {
			response.RequiresReplace = true
			return
		}
	}
}

// managedPoolShapeEqual compares every pool argument except count and
// autoscaling. Unknown planned values compare as changed.
func managedPoolShapeEqual(planned, prior managedNodePoolFields) bool {
	spotEqual := (planned.Spot == nil) == (prior.Spot == nil)
	if spotEqual && planned.Spot != nil {
		spotEqual = planned.Spot.Enabled.Equal(prior.Spot.Enabled) && planned.Spot.MaxPrice.Equal(prior.Spot.MaxPrice)
	}
	return spotEqual &&
		planned.Name.Equal(prior.Name) &&
		planned.Size.Equal(prior.Size) &&
		planned.Labels.Equal(prior.Labels) &&
		nodeGroupTaintsEqual(planned.Taints, prior.Taints) &&
		planned.Zone.Equal(prior.Zone) &&
		planned.RootVolumeType.Equal(prior.RootVolumeType) &&
		planned.RootVolumeSizeGB.Equal(prior.RootVolumeSizeGB)
}
