// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework-jsontypes/jsontypes"
	"github.com/hashicorp/terraform-plugin-framework-timeouts/resource/timeouts"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
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

// managedStepErrorPatience is how long a managed create may keep reporting
// the same failed step before the wait gives up on it. The platform retries
// failed steps, so one failure is not final; one that persists this long
// names a cause the practitioner has to fix.
var managedStepErrorPatience = 10 * time.Minute

// managedWaitPollInterval paces the managed cluster wait; tests shorten it.
var managedWaitPollInterval = 15 * time.Second

type managedClusterResource struct {
	configuredClient
}

type managedClusterModel struct {
	ID                   types.String                     `tfsdk:"id"`
	ClusterID            types.String                     `tfsdk:"cluster_id"`
	State                types.String                     `tfsdk:"state"`
	Kind                 types.String                     `tfsdk:"kind"`
	Provider             types.String                     `tfsdk:"provider_name"`
	Name                 types.String                     `tfsdk:"name"`
	Description          types.String                     `tfsdk:"description"`
	CredentialID         types.String                     `tfsdk:"credential_id"`
	Location             types.String                     `tfsdk:"location"`
	KubernetesVersion    types.String                     `tfsdk:"kubernetes_version"`
	CNI                  types.String                     `tfsdk:"cni"`
	HA                   types.Bool                       `tfsdk:"ha"`
	ClusterPlan          types.String                     `tfsdk:"cluster_plan"`
	ProviderOptions      jsontypes.Normalized             `tfsdk:"provider_options"`
	NodePools            map[string]managedNodePoolFields `tfsdk:"node_pools"`
	GitopsBranch         types.String                     `tfsdk:"gitops_branch"`
	GitopsCredentialName types.String                     `tfsdk:"gitops_credential_name"`
	GitopsRepository     types.String                     `tfsdk:"gitops_repository"`
	DestroyMode          types.String                     `tfsdk:"destroy_mode"`
	RetentionPolicy      types.String                     `tfsdk:"retention_policy"`
	ForceDestroy         types.Bool                       `tfsdk:"force_destroy"`
	WaitForReady         types.Bool                       `tfsdk:"wait_for_ready"`
	WaitForAgent         types.Bool                       `tfsdk:"wait_for_agent"`
	Timeouts             timeouts.Value                   `tfsdk:"timeouts"`
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
		PlanModifiers:       []planmodifier.Bool{clusterReplaceBool()},
	}
	attributes["cluster_plan"] = optionalReplaceString("Control-plane plan or tier, where the provider sells several.")
	attributes["provider_options"] = schema.StringAttribute{
		MarkdownDescription: "The provider's own options as a JSON object, for example " +
			"`jsonencode({ resource_group = \"rg\", private_cluster = false })` for `aks` or " +
			"`jsonencode({ private_network_id = \"...\" })` for `kapsule`. Sent under the key named after " +
			"`provider_name` (`aks`, `eks`, `gke`, `ovh_mks`, `kapsule`, `ankracloud_k8s`, `uks`). Compared as JSON, " +
			"so formatting does not matter. Changing it forces replacement.",
		Optional:      true,
		CustomType:    jsontypes.NormalizedType{},
		PlanModifiers: []planmodifier.String{clusterReplaceString()},
		Validators:    []validator.String{managedProviderOptionsValidator{}},
	}
	attributes["node_pools"] = schema.MapNestedAttribute{
		MarkdownDescription: "The cluster's node pools, keyed by pool name; at least one. Adding a key adds a pool and " +
			"removing one deletes it, in place. `node_count` and `autoscaling` change in place. Any other change to an " +
			"existing pool is refused at plan time, because the provider cannot apply it: rename the key instead, which " +
			"adds the new pool before removing the old one. Pools managed by `ankra_managed_node_pool` must not be listed here.",
		Required:      true,
		NestedObject:  schema.NestedAttributeObject{Attributes: managedNodePoolAttributes(false)},
		PlanModifiers: []planmodifier.Map{managedNodePoolsShapeModifier{}},
	}
	attributes["gitops_branch"] = defaultedReplaceString("GitOps branch to commit the generated stack to.", "master")
	attributes["gitops_credential_name"] = optionalReplaceString(
		"GitOps credential name; commits the generated stack to Git when set with `gitops_repository`.")
	attributes["gitops_repository"] = optionalReplaceString("GitOps repository (`owner/name`) to commit the generated stack to.")
	attributes["destroy_mode"] = schema.StringAttribute{
		MarkdownDescription: "What a destroy does. `delete_provider_cluster` deletes the cluster at the provider. " +
			"`disconnect` removes it from Ankra and leaves it running. A cluster this resource creates defaults to " +
			"`delete_provider_cluster`, the usual meaning of destroy. An imported cluster defaults to `disconnect`, " +
			"because Terraform did not build it and it may be a customer's existing cluster. Even with " +
			"`delete_provider_cluster`, the platform refuses to delete a cluster it did not create unless " +
			"`force_destroy` is set. Only affects a future destroy.",
		Optional:      true,
		Computed:      true,
		PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
		Validators:    []validator.String{laneOneOf(client.ManagedDestroyModeDisconnect, client.ManagedDestroyModeDeleteProviderCluster)},
	}
	attributes["retention_policy"] = schema.StringAttribute{
		MarkdownDescription: "What a provider delete does with the cluster's retained provider resources: `delete` (default, " +
			"the platform's default) or `retain`. Only affects a future destroy, so it never replaces the cluster.",
		Optional:   true,
		Computed:   true,
		Default:    stringdefault.StaticString("delete"),
		Validators: []validator.String{laneOneOf(laneRetentionPolicy...)},
	}
	attributes["force_destroy"] = schema.BoolAttribute{
		MarkdownDescription: "Send `force=true` when destroying. **This overrides the platform's ownership check**: " +
			"with `destroy_mode = \"delete_provider_cluster\"` it deletes the provider cluster even when Ankra did " +
			"not create it (an imported, customer-owned cluster). Defaults to `false`. Only affects a future destroy.",
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
	attributes["wait_for_agent"] = schema.BoolAttribute{
		MarkdownDescription: "With `wait_for_ready`, also wait for the Ankra agent to connect (state `online`), which " +
			"anything deploying through Ankra needs. Defaults to `false`: the wait ends once the provider has built " +
			"the cluster (`provider_without_access`).",
		Optional: true,
		Computed: true,
		Default:  booldefault.StaticBool(false),
	}
	response.Schema = schema.Schema{
		MarkdownDescription: "A managed Kubernetes cluster that the provider (DigitalOcean DOKS, UpCloud UKS, GKE, AKS, " +
			"EKS, OVHcloud MKS, Scaleway Kapsule or Ankra Cloud Kubernetes) builds and Ankra connects. The " +
			"counterpart of `ankra cluster managed create` and `ankra cluster managed delete`.\n\n" +
			"Import by `<provider_name>/<cluster_id>`. The platform does not return the creation arguments, so the " +
			"first apply after an import adopts the configured values without replacing the cluster; node pools are " +
			"read back, and pools the configuration does not list are released from management, never deleted, by " +
			"that first apply.",
		Attributes: attributes,
		Blocks: map[string]schema.Block{
			"timeouts": timeouts.Block(ctx, timeouts.Opts{Create: true, Update: true, Delete: true}),
		},
	}
}

// ValidateConfig refuses a configuration the platform would refuse: a
// credential is required for every provider but ankracloud_k8s, and a
// cluster needs at least one node pool.
func (clusterResource *managedClusterResource) ValidateConfig(ctx context.Context, request resource.ValidateConfigRequest, response *resource.ValidateConfigResponse) {
	var provider, credentialID types.String
	var nodePools types.Map
	response.Diagnostics.Append(request.Config.GetAttribute(ctx, path.Root("provider_name"), &provider)...)
	response.Diagnostics.Append(request.Config.GetAttribute(ctx, path.Root("credential_id"), &credentialID)...)
	response.Diagnostics.Append(request.Config.GetAttribute(ctx, path.Root("node_pools"), &nodePools)...)
	if response.Diagnostics.HasError() {
		return
	}
	if !nodePools.IsNull() && !nodePools.IsUnknown() && len(nodePools.Elements()) == 0 {
		response.Diagnostics.AddAttributeError(path.Root("node_pools"), "No node pools",
			"A managed cluster needs at least one node pool.")
	}
	if provider.IsUnknown() || provider.IsNull() || !credentialID.IsNull() {
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
	if options := plan.ProviderOptions.ValueString(); !plan.ProviderOptions.IsNull() && strings.TrimSpace(options) != "" {
		createRequest.ProviderOptions = json.RawMessage(options)
	}
	poolCounts := map[string]int64{}
	for _, name := range sortedPoolNames(plan.NodePools) {
		pool := managedNodePoolCreateFromFields(ctx, name, plan.NodePools[name], &response.Diagnostics)
		poolCounts[name] = pool.Count
		createRequest.NodePools = append(createRequest.NodePools, pool)
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
	if plan.DestroyMode.IsUnknown() || plan.DestroyMode.IsNull() {
		plan.DestroyMode = types.StringValue(client.ManagedDestroyModeDeleteProviderCluster)
	}
	plannedPools := maps.Clone(plan.NodePools)
	for name, fields := range plan.NodePools {
		settleUnknownPoolFields(&fields, poolCounts[name])
		plan.NodePools[name] = fields
	}
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
	response.Diagnostics.Append(trackScope(ctx, apiClient, response.Private)...)
	if response.Diagnostics.HasError() {
		return
	}

	cluster := settleManagedCluster(ctx, apiClient, clusterID, plan.WaitForReady, plan.WaitForAgent, plan.Timeouts.Create, &response.Diagnostics)
	if cluster == nil {
		return
	}
	applyClusterIdentity(&plan.State, &plan.Kind, cluster)
	clusterResource.refreshPools(ctx, apiClient, &plan, plannedPools)
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
}

// refreshPools fills, after an apply, the pool values the plan left unknown
// from the listing, and keeps every value the plan knew. A pool the listing
// does not show yet keeps its planned values, with unknowns settled.
func (clusterResource *managedClusterResource) refreshPools(ctx context.Context, apiClient *client.Client, model *managedClusterModel, planned map[string]managedNodePoolFields) {
	listed := map[string]managedNodePoolFields{}
	if pools, err := apiClient.ListManagedNodePools(ctx, model.Provider.ValueString(), model.ClusterID.ValueString()); err == nil {
		listing := managedClusterModel{NodePools: model.NodePools}
		applyPoolListing(&listing, pools, false)
		listed = listing.NodePools
	}
	result := make(map[string]managedNodePoolFields, len(planned))
	for name, plannedFields := range planned {
		fields := plannedFields
		if refreshed, exists := listed[name]; exists {
			fields = keepPlannedPoolFields(refreshed, plannedFields)
		}
		settleUnknownPoolFields(&fields, model.NodePools[name].Count.ValueInt64())
		result[name] = fields
	}
	model.NodePools = result
}

// keepPlannedPoolFields returns the refreshed pool with every value the plan
// already knew restored: an apply must produce what it planned, and only the
// values the plan left unknown are taken from the listing. The next refresh
// records any difference.
func keepPlannedPoolFields(refreshed, planned managedNodePoolFields) managedNodePoolFields {
	result := planned
	if planned.Count.IsUnknown() {
		result.Count = refreshed.Count
	}
	if planned.Zone.IsUnknown() {
		result.Zone = refreshed.Zone
	}
	if planned.RootVolumeType.IsUnknown() {
		result.RootVolumeType = refreshed.RootVolumeType
	}
	if planned.RootVolumeSizeGB.IsUnknown() {
		result.RootVolumeSizeGB = refreshed.RootVolumeSizeGB
	}
	return result
}

// applyPoolListing refreshes the model's pools from a listing. A pool the
// listing no longer shows drops out, so the next plan re-adds it rather than
// replacing the cluster.
func applyPoolListing(model *managedClusterModel, pools []client.ManagedNodePool, adoptListed bool) {
	refreshed := make(map[string]managedNodePoolFields, len(pools))
	for _, pool := range pools {
		fields, tracked := model.NodePools[pool.Name]
		if !tracked && !adoptListed {
			continue
		}
		refreshManagedNodePoolFields(&fields, pool, adoptListed)
		refreshed[pool.Name] = fields
	}
	model.NodePools = refreshed
}

// settleManagedCluster waits for the provider to build the cluster, and for
// the agent too when asked, or reads it once when the wait is off.
func settleManagedCluster(ctx context.Context, apiClient *client.Client, clusterID string, waitForReady, waitForAgent types.Bool, createTimeout timeoutFunc, diagnostics *diag.Diagnostics) *client.Cluster {
	if !waitForReady.ValueBool() {
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

	readyStates := client.ManagedClusterProviderReadyStates
	if waitForAgent.ValueBool() {
		readyStates = client.ManagedClusterOnlineStates
	}
	var stepErrorSince time.Time
	var stepErrorKey string
	lastState := ""
	hasWarnedStepError := false
	for {
		cluster, err := apiClient.GetClusterByID(waitContext, clusterID)
		if err != nil && waitContext.Err() == nil {
			diagnostics.AddError("Unable to read cluster", err.Error())
			return nil
		}
		if cluster != nil {
			lastState = cluster.State
			switch {
			case slices.Contains(readyStates, cluster.State):
				return cluster
			case slices.Contains(client.ManagedClusterFailureStates, cluster.State):
				diagnostics.AddError("Cluster did not become ready",
					fmt.Sprintf("Managed cluster %s entered state %q while waiting for %v.", clusterID, cluster.State, readyStates))
				return nil
			case cluster.LatestStepError != nil:
				key := cluster.LatestStepError.StepName + "\x00" + cluster.LatestStepError.Message
				if key != stepErrorKey {
					stepErrorKey, stepErrorSince = key, time.Now()
				} else if time.Since(stepErrorSince) >= managedStepErrorPatience && !hasWarnedStepError {
					hasWarnedStepError = true
					diagnostics.AddWarning("Cluster build step is failing",
						fmt.Sprintf("Managed cluster %s has been failing step %q for %s: %s. The platform keeps retrying it, "+
							"and this apply keeps waiting until the create timeout. If the cause needs fixing on your side "+
							"(a quota, a permission), fix it now: the cluster then continues where it stopped.",
							clusterID, cluster.LatestStepError.StepName, managedStepErrorPatience, cluster.LatestStepError.Message))
				}
			default:
				stepErrorKey = ""
			}
		}
		select {
		case <-waitContext.Done():
			diagnostics.AddError("Cluster did not become ready",
				fmt.Sprintf("Timed out waiting for managed cluster %s to reach %v (last observed state %q).", clusterID, readyStates, lastState))
			return nil
		case <-time.After(managedWaitPollInterval):
		}
	}
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
		removeMissingCluster(ctx, apiClient, response)
		return
	}
	state.ID = types.StringValue(cluster.ID)
	if cluster.Name != "" {
		state.Name = types.StringValue(cluster.Name)
	}
	applyClusterIdentity(&state.State, &state.Kind, cluster)

	pools, err := apiClient.ListManagedNodePools(ctx, state.Provider.ValueString(), clusterID)
	if removeWhenNotFound(ctx, err, apiClient, response) {
		return
	}
	if err != nil {
		response.Diagnostics.AddError("Unable to read managed node pools", err.Error())
		return
	}
	marker, _ := request.Private.GetKey(ctx, clusterImportedKey)
	applyPoolListing(&state, pools, len(marker) > 0)
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
	response.Diagnostics.Append(trackScope(ctx, apiClient, response.Private)...)
}

// Update adds the pools a new key names, applies node_count and autoscaling
// changes, and deletes the pools whose key was removed, in that order so the
// cluster never drops to zero pools on a rename. Right after an import, the
// pools the configuration does not list are released rather than deleted.
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
	marker, _ := request.Private.GetKey(ctx, clusterImportedKey)
	isAdopting := len(marker) > 0
	plannedPools := maps.Clone(plan.NodePools)
	provider := state.Provider.ValueString()
	clusterID := state.ClusterID.ValueString()

	for _, name := range sortedPoolNames(plan.NodePools) {
		if _, exists := state.NodePools[name]; exists {
			continue
		}
		pool := managedNodePoolCreateFromFields(ctx, name, plan.NodePools[name], &response.Diagnostics)
		if response.Diagnostics.HasError() {
			return
		}
		if err := apiClient.AddManagedNodePool(ctx, provider, clusterID, pool); err != nil {
			response.Diagnostics.AddError("Unable to add managed node pool "+name, err.Error())
			return
		}
		fields := plan.NodePools[name]
		settleUnknownPoolFields(&fields, pool.Count)
		plan.NodePools[name] = fields
	}
	for _, name := range sortedPoolNames(plan.NodePools) {
		prior, exists := state.NodePools[name]
		if !exists {
			continue
		}
		fields := plan.NodePools[name]
		if update, changed := managedNodePoolSizingUpdate(fields, prior); changed {
			if err := apiClient.UpdateManagedNodePool(ctx, provider, clusterID, name, update); err != nil {
				response.Diagnostics.AddError("Unable to update managed node pool "+name, err.Error())
				return
			}
		}
		settleUnknownPoolFields(&fields, prior.Count.ValueInt64())
		plan.NodePools[name] = fields
	}
	var removedPools []string
	for _, name := range sortedPoolNames(state.NodePools) {
		if _, kept := plan.NodePools[name]; !kept {
			removedPools = append(removedPools, name)
		}
	}
	if !isAdopting && len(removedPools) > 0 {
		wait := resolveTimeout(ctx, plan.Timeouts.Update, defaultManagedPoolTimeout, &response.Diagnostics)
		if response.Diagnostics.HasError() {
			return
		}
		waitContext, cancel := context.WithTimeout(ctx, wait)
		defer cancel()
		for _, name := range sortedPoolNames(plan.NodePools) {
			if _, existed := state.NodePools[name]; existed {
				continue
			}
			if readinessError := awaitManagedPoolReady(waitContext, apiClient, provider, clusterID, name, plan.NodePools[name]); readinessError != nil {
				// The new pools exist and the removed ones are kept, so state
				// records both and the next apply retries the delete.
				keepRemovedPools(&plan, state, removedPools)
				plan.ID, plan.ClusterID, plan.State, plan.Kind = state.ID, state.ClusterID, state.State, state.Kind
				if plan.DestroyMode.IsUnknown() {
					plan.DestroyMode = state.DestroyMode
				}
				clusterResource.refreshPools(ctx, apiClient, &plan, plan.NodePools)
				response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
				response.Diagnostics.AddError("Removed node pools were kept", fmt.Sprintf(
					"Pool %q is not ready yet, so %s was not deleted: deleting it first would leave its workloads with "+
						"nowhere to run (%s). Apply again to delete it once the new pool is ready.",
					name, strings.Join(removedPools, ", "), readinessError.Error()))
				return
			}
		}
		for _, name := range removedPools {
			err := apiClient.DeleteManagedNodePool(ctx, provider, clusterID, name)
			if err != nil && !deletedWhenNotFound(ctx, err, apiClient, request.Private, &response.Diagnostics) {
				if !response.Diagnostics.HasError() {
					response.Diagnostics.AddError("Unable to delete managed node pool "+name, err.Error())
				}
				return
			}
		}
	}

	plan.ID = state.ID
	plan.ClusterID = state.ClusterID
	plan.State = state.State
	plan.Kind = state.Kind
	if plan.DestroyMode.IsUnknown() {
		plan.DestroyMode = state.DestroyMode
	}
	clusterResource.refreshPools(ctx, apiClient, &plan, plannedPools)
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
	response.Diagnostics.Append(clearClusterImported(ctx, response.Private)...)
}

// defaultManagedPoolTimeout bounds the wait for a new pool's nodes when the
// configuration sets no update timeout.
const defaultManagedPoolTimeout = 30 * time.Minute

// managedPoolPollInterval paces the wait for a new pool; tests shorten it.
var managedPoolPollInterval = 15 * time.Second

// awaitManagedPoolReady waits until the pool's Kubernetes nodes are Ready
// and as many as it asked for: the provider accepts a new pool long before
// its nodes can take workloads. The nodes are found by the provider's pool
// label through the cluster agent; a provider whose pool label Ankra does not
// know (UKS, OVH MKS) cannot be verified, and the caller then keeps the pools
// it would have deleted, as the platform's own blue/green replacement does.
func awaitManagedPoolReady(ctx context.Context, apiClient *client.Client, provider, clusterID, poolName string, fields managedNodePoolFields) error {
	labelKey, isKnown := client.ManagedNodePoolLabelKeys[provider]
	if !isKnown {
		return fmt.Errorf("ankra cannot find a %s pool's nodes, so it cannot tell when the new pool is ready", provider)
	}
	want := fields.Count.ValueInt64()
	if fields.Autoscaling != nil && fields.Autoscaling.Enabled.ValueBool() {
		want = fields.Autoscaling.MinCount.ValueInt64()
	}
	if want < 1 {
		want = 1
	}
	var ready int64
	var lastError error
	for {
		count, err := apiClient.CountReadyKubernetesNodes(ctx, clusterID, labelKey, poolName)
		if err == nil {
			ready, lastError = count, nil
			if ready >= want {
				return nil
			}
		} else if ctx.Err() == nil {
			lastError = err
		}
		select {
		case <-ctx.Done():
			if lastError != nil {
				return fmt.Errorf("its nodes could not be read before the update timeout: %w", lastError)
			}
			return fmt.Errorf("only %d of its %d nodes were Ready before the update timeout", ready, want)
		case <-time.After(managedPoolPollInterval):
		}
	}
}

// keepRemovedPools puts the pools an update would have deleted back into the
// model, with their prior state.
func keepRemovedPools(model *managedClusterModel, prior managedClusterModel, removedPools []string) {
	for _, name := range removedPools {
		model.NodePools[name] = prior.NodePools[name]
	}
}

// Delete disconnects or deletes the cluster (destroy_mode) and waits until
// the platform no longer lists it, so a replacement's create does not collide
// with the old cluster's name.
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
	if !requireUnmovedScope(ctx, apiClient, request.Private, &response.Diagnostics) {
		return
	}
	wait := resolveTimeout(ctx, state.Timeouts.Delete, defaultDeleteTimeout, &response.Diagnostics)
	if response.Diagnostics.HasError() {
		return
	}
	deleteContext, cancel := context.WithTimeout(ctx, wait)
	defer cancel()

	mode := state.DestroyMode.ValueString()
	if mode == "" {
		mode = client.ManagedDestroyModeDisconnect
	}
	clusterID := state.ClusterID.ValueString()
	if current, readError := apiClient.GetClusterByID(deleteContext, clusterID); readError == nil && current != nil &&
		current.State == clusterDeprovisioningState {
		// A delete is already running (an earlier destroy timed out):
		// re-sending it would cancel and re-dispatch the platform's
		// deprovision jobs, so only wait for it.
		awaitClusterGone(deleteContext, apiClient, clusterID, &response.Diagnostics)
		return
	}
	result, err := apiClient.DeleteManagedCluster(deleteContext, state.Provider.ValueString(), clusterID, mode,
		state.ForceDestroy.ValueBool(), state.RetentionPolicy.ValueString())
	if err != nil {
		if !deletedWhenNotFound(ctx, err, apiClient, request.Private, &response.Diagnostics) && !response.Diagnostics.HasError() {
			response.Diagnostics.AddError("Unable to destroy managed cluster", err.Error())
		}
		return
	}
	if len(result.Errors) > 0 {
		response.Diagnostics.AddError("Managed cluster destroy reported errors", strings.Join(result.Errors, "; "))
		return
	}
	if mode == client.ManagedDestroyModeDeleteProviderCluster && result.OperationID == nil && result.ResourcesMarked == 0 {
		// The platform answers success with nothing marked when it did not
		// recognise the cluster as one of this provider's. Only the listing
		// can tell whether anything happened.
		cluster, readError := apiClient.GetClusterByID(deleteContext, clusterID)
		if readError == nil && cluster != nil {
			response.Diagnostics.AddError("Managed cluster was not deleted", fmt.Sprintf(
				"The platform accepted the delete of cluster %s but marked nothing for deletion: it does not recognise "+
					"the cluster as a %s cluster. Check provider_name.", clusterID, state.Provider.ValueString()))
			return
		}
	}
	awaitClusterGone(deleteContext, apiClient, clusterID, &response.Diagnostics)
}

// ImportState imports by `<provider_name>/<cluster_id>`. The creation
// arguments are adopted from the configuration on the first apply, and the
// destroy mode defaults to disconnect: Terraform did not build the cluster.
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
	response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("destroy_mode"), client.ManagedDestroyModeDisconnect)...)
	response.Diagnostics.Append(markClusterImported(ctx, response.Private)...)
}

func sortedPoolNames(pools map[string]managedNodePoolFields) []string {
	names := make([]string, 0, len(pools))
	for name := range pools {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
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

// managedNodePoolsShapeModifier refuses, at plan time, a change to an
// existing pool that the provider cannot apply in place. Replacing the whole
// cluster for it would destroy every workload, so the plan stops and names
// the way out: a new key adds a pool, and removing the old key deletes it.
type managedNodePoolsShapeModifier struct{}

func (managedNodePoolsShapeModifier) Description(_ context.Context) string {
	return "Refuses changes to an existing pool other than node_count and autoscaling."
}

func (modifier managedNodePoolsShapeModifier) MarkdownDescription(ctx context.Context) string {
	return modifier.Description(ctx)
}

func (managedNodePoolsShapeModifier) PlanModifyMap(ctx context.Context, request planmodifier.MapRequest, response *planmodifier.MapResponse) {
	if request.State.Raw.IsNull() || request.Plan.Raw.IsNull() || request.PlanValue.IsUnknown() || request.StateValue.IsNull() {
		return
	}
	var planned, prior map[string]managedNodePoolFields
	response.Diagnostics.Append(request.PlanValue.ElementsAs(ctx, &planned, false)...)
	response.Diagnostics.Append(request.StateValue.ElementsAs(ctx, &prior, false)...)
	if response.Diagnostics.HasError() {
		return
	}
	marker, _ := request.Private.GetKey(ctx, clusterImportedKey)
	isAdopting := len(marker) > 0
	for _, name := range sortedPoolNames(prior) {
		if _, kept := planned[name]; kept || isAdopting {
			continue
		}
		response.Diagnostics.AddAttributeWarning(request.Path.AtMapKey(name), "Node pool will be deleted",
			fmt.Sprintf("Pool %q and its nodes will be deleted. The apply first adds any new pools and waits until "+
				"their nodes are Ready, so workloads can move; it keeps %q if it cannot confirm that.", name, name))
	}
	for _, name := range sortedPoolNames(planned) {
		priorFields, exists := prior[name]
		if !exists {
			continue
		}
		if changes := managedPoolShapeChanges(planned[name], priorFields, isAdopting); len(changes) > 0 {
			response.Diagnostics.AddAttributeError(request.Path.AtMapKey(name), "Node pool cannot change in place",
				fmt.Sprintf("The provider cannot change %s of the existing pool %q. Give the pool a new key "+
					"(for example %q): the apply adds the new pool, then deletes this one, without replacing the cluster.",
					strings.Join(changes, ", "), name, name+"-2"))
		}
	}
}
