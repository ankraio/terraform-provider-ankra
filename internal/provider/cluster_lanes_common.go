// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"
	"time"

	"github.com/hashicorp/terraform-plugin-framework-timeouts/resource/timeouts"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"ankra.io/terraform-provider-ankra/internal/client"
)

// clusterGonePollInterval paces the wait for a deprovisioned cluster to
// leave the listing; tests shorten it.
var clusterGonePollInterval = 15 * time.Second

// clusterDeprovisioningState is the state of a cluster whose deprovision is
// already running.
const clusterDeprovisioningState = "deprovisioning"

// laneClusterCommonModel holds the attributes every lane in this file family
// shares. Each lane's model embeds it and adds its own placement and sizing
// vocabulary.
type laneClusterCommonModel struct {
	ID                types.String   `tfsdk:"id"`
	ClusterID         types.String   `tfsdk:"cluster_id"`
	State             types.String   `tfsdk:"state"`
	Kind              types.String   `tfsdk:"kind"`
	ProjectID         types.String   `tfsdk:"project_id"`
	Name              types.String   `tfsdk:"name"`
	Description       types.String   `tfsdk:"description"`
	Distribution      types.String   `tfsdk:"distribution"`
	CNI               types.String   `tfsdk:"cni"`
	KubernetesVersion types.String   `tfsdk:"kubernetes_version"`
	ControlPlaneCount types.Int64    `tfsdk:"control_plane_count"`
	WorkerCount       types.Int64    `tfsdk:"worker_count"`
	EtcdTopology      types.String   `tfsdk:"etcd_topology"`
	EtcdNodeCount     types.Int64    `tfsdk:"etcd_node_count"`
	IncludeNetworking types.Bool     `tfsdk:"include_networking"`
	IncludeDNS        types.Bool     `tfsdk:"include_dns"`
	ForceDestroy      types.Bool     `tfsdk:"force_destroy"`
	WaitForReady      types.Bool     `tfsdk:"wait_for_ready"`
	Timeouts          timeouts.Value `tfsdk:"timeouts"`
}

// laneClusterAttributes are the schema attributes behind
// laneClusterCommonModel. Every provisioning argument replaces the cluster
// when changed (except right after an import, when the configured value is
// adopted): the platform has no update for a cluster's build parameters, and
// day-2 sizing goes through ankra_node_group instead.
func laneClusterAttributes(laneDisplayName string) map[string]schema.Attribute {
	attributes := identityAttributes()
	attributes["name"] = withStringValidators(requiredReplaceString("Name of the cluster."), laneNameValidator{})
	attributes["description"] = optionalReplaceString("Description of the cluster.")
	attributes["distribution"] = withStringValidators(defaultedReplaceString(
		"Kubernetes distribution: `kubeadm` (default, vanilla upstream Kubernetes) or `k3s`.", "kubeadm"),
		laneOneOf(laneDistributions...))
	attributes["cni"] = withStringValidators(defaultedReplaceString(
		"Container network interface. Defaults to `cilium`, the only CNI kubeadm clusters run; set `flannel` or `calico` for k3s clusters.", "cilium"),
		laneOneOf(laneCNIs...))
	attributes["kubernetes_version"] = optionalReplaceString(
		"Kubernetes version. Defaults to the platform's stable version when unset; `data.ankra_kubernetes_versions` lists the supported ones.")
	attributes["control_plane_count"] = defaultedReplaceInt64("Number of control-plane nodes.", 1)
	attributes["worker_count"] = defaultedReplaceInt64(
		"Number of worker nodes in the cluster's default worker group. Further groups, and in-place resizing, are `ankra_node_group` resources.", 1)
	attributes["etcd_topology"] = withStringValidators(defaultedReplaceString(
		"etcd topology for kubeadm clusters: `stacked` or `external`.", "stacked"), laneOneOf(laneEtcdTopologies...))
	attributes["etcd_node_count"] = defaultedReplaceInt64("Number of dedicated etcd nodes when `etcd_topology` is `external`.", 3)
	attributes["include_networking"] = defaultedReplaceBool("Install the Ankra networking stack (Traefik and cert-manager) for ingress.", true)
	attributes["include_dns"] = defaultedReplaceBool("Delegate a DNS subzone to the cluster and wire external-dns into the networking stack.", true)
	attributes["force_destroy"] = laneForceDestroyAttribute(laneDisplayName)
	attributes["wait_for_ready"] = waitForReadyAttribute()
	return attributes
}

// laneForceDestroyAttribute is the deprovisioning force flag of the lanes in
// this family. Unlike the original five lanes, which kept `true` for
// backwards compatibility, it defaults to the guarded path.
func laneForceDestroyAttribute(laneDisplayName string) schema.BoolAttribute {
	return schema.BoolAttribute{
		MarkdownDescription: "Send `force=true` when deprovisioning, which makes the platform tear down the cluster's " +
			laneDisplayName + " resources without its usual guards. Defaults to `false`, the guarded delete path. " +
			"Changing this only affects a future destroy, so it never replaces the cluster.",
		Optional: true,
		Computed: true,
		Default:  booldefault.StaticBool(false),
	}
}

// laneClusterDefinition describes one lane to the shared lifecycle.
type laneClusterDefinition[Model any] struct {
	typeSuffix  string
	lane        string
	displayName string
	description string
	attributes  func() map[string]schema.Attribute
	common      func(model *Model) *laneClusterCommonModel
	request     func(ctx context.Context, model *Model, diagnostics *diag.Diagnostics) any
	// acceptVolumeDataLoss reads the volume-loss consent for a lane whose
	// deprovision asks for it; nil for lanes that never do.
	acceptVolumeDataLoss func(model *Model) bool
}

// laneClusterResource is the shared lifecycle of a provisioned cluster lane.
// It reuses the provisioned-cluster machinery of the original five clouds:
// the id is persisted before the readiness wait, state and kind are refreshed
// from the platform, and a cluster gone from the listing is removed from
// state so the next plan recreates it, unless the provider's scope moved.
type laneClusterResource[Model any] struct {
	configuredClient
	definition laneClusterDefinition[Model]
}

func (laneResource *laneClusterResource[Model]) Metadata(_ context.Context, request resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + laneResource.definition.typeSuffix
}

func (laneResource *laneClusterResource[Model]) Schema(ctx context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: laneResource.definition.description + laneImportNote,
		Attributes:          laneResource.definition.attributes(),
		Blocks:              commonProvisionedBlocks(ctx),
	}
}

// laneImportNote documents the import contract every lane shares.
const laneImportNote = "\n\nImport by cluster id. The platform does not return the build arguments, so the first apply after an " +
	"import adopts the configured values in place, without replacing the cluster: they must describe the cluster as it was built."

func (laneResource *laneClusterResource[Model]) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	apiClient := laneResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var plan Model
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}
	common := laneResource.definition.common(&plan)

	createRequest := laneResource.definition.request(ctx, &plan, &response.Diagnostics)
	if response.Diagnostics.HasError() {
		return
	}
	created, err := apiClient.CreateLaneCluster(ctx, laneResource.definition.lane, createRequest)
	if err != nil {
		response.Diagnostics.AddError("Unable to create "+laneResource.definition.displayName+" cluster", err.Error())
		return
	}
	if created.ClusterID == "" {
		response.Diagnostics.AddError("Invalid create response", "The Ankra platform did not return a cluster_id.")
		return
	}

	common.ID = types.StringValue(created.ClusterID)
	common.ClusterID = types.StringValue(created.ClusterID)
	common.State = types.StringUnknown()
	common.Kind = types.StringUnknown()
	common.ProjectID = optionalString(apiClient.ScopeProjectID())

	// Persist the id before waiting. A wait that times out must not lose the
	// cluster the platform already started building.
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
	response.Diagnostics.Append(trackScope(ctx, apiClient, response.Private)...)
	if response.Diagnostics.HasError() {
		return
	}

	cluster := settleProvisioned(ctx, apiClient, created.ClusterID, common.WaitForReady, common.Timeouts.Create, &response.Diagnostics)
	if cluster == nil {
		return
	}
	applyClusterIdentity(&common.State, &common.Kind, &common.ProjectID, apiClient, cluster)
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
}

func (laneResource *laneClusterResource[Model]) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	apiClient := laneResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state Model
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	common := laneResource.definition.common(&state)

	cluster, err := apiClient.GetClusterByID(ctx, laneClusterID(common))
	if err != nil {
		response.Diagnostics.AddError("Unable to read cluster", err.Error())
		return
	}
	if cluster == nil {
		removeMissingCluster(ctx, apiClient, response)
		return
	}

	common.ID = types.StringValue(cluster.ID)
	common.ClusterID = types.StringValue(cluster.ID)
	if cluster.Name != "" {
		common.Name = types.StringValue(cluster.Name)
	}
	applyClusterIdentity(&common.State, &common.Kind, &common.ProjectID, apiClient, cluster)
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
	response.Diagnostics.Append(trackScope(ctx, apiClient, response.Private)...)
}

// Update carries through the arguments that can change in place
// (force_destroy, wait_for_ready, accept_volume_data_loss: they affect future
// operations rather than the running cluster), and adopts the configured
// creation arguments right after an import. Any other change to a creation
// argument replaces the cluster, so it never reaches here.
func (laneResource *laneClusterResource[Model]) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	if laneResource.requireClient(&response.Diagnostics) == nil {
		return
	}
	var plan Model
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
	response.Diagnostics.Append(clearClusterImported(ctx, response.Private)...)
}

// Delete deprovisions the cluster and waits until the platform no longer
// lists it, so a replacement's create does not collide with the old
// cluster's name while it is still being torn down.
func (laneResource *laneClusterResource[Model]) Delete(ctx context.Context, request resource.DeleteRequest, response *resource.DeleteResponse) {
	apiClient := laneResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state Model
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	common := laneResource.definition.common(&state)
	options := client.LaneDeprovisionOptions{Force: common.ForceDestroy.ValueBool()}
	if laneResource.definition.acceptVolumeDataLoss != nil {
		options.AcceptVolumeDataLoss = laneResource.definition.acceptVolumeDataLoss(&state)
	}
	deprovisionLaneClusterAndWait(ctx, apiClient, laneDeprovision{
		lane:          laneResource.definition.lane,
		displayName:   laneResource.definition.displayName,
		clusterID:     laneClusterID(common),
		options:       options,
		deleteTimeout: common.Timeouts.Delete,
	}, request.Private, &response.Diagnostics)
}

// laneDeprovision is one cluster destroy on a provisioned lane.
type laneDeprovision struct {
	lane          string
	displayName   string
	clusterID     string
	options       client.LaneDeprovisionOptions
	deleteTimeout timeoutFunc
}

// deprovisionLaneClusterAndWait is the destroy every provisioned lane
// shares: it refuses when the provider's scope moved since the cluster was
// tracked, deprovisions with the lane's flags, and waits until the platform
// no longer lists the cluster, so a replacement's create does not collide
// with the old cluster's name while it is still being torn down.
func deprovisionLaneClusterAndWait(ctx context.Context, apiClient *client.Client, deprovision laneDeprovision, private privateStateReader, diagnostics *diag.Diagnostics) {
	if deprovision.clusterID == "" {
		return
	}
	// The scope check compares against the resolved project ID, so a slug
	// must be resolved first even when nothing else has sent a request in
	// this run (a destroy without refresh).
	if err := apiClient.ResolveProject(ctx); err != nil {
		diagnostics.AddError("Unable to resolve the provider's project_id", err.Error())
		return
	}
	if !requireUnmovedScope(ctx, apiClient, private, diagnostics) {
		return
	}

	wait := resolveTimeout(ctx, deprovision.deleteTimeout, defaultDeleteTimeout, diagnostics)
	if diagnostics.HasError() {
		return
	}
	deleteContext, cancel := context.WithTimeout(ctx, wait)
	defer cancel()

	if current, readError := apiClient.GetClusterByID(deleteContext, deprovision.clusterID); readError == nil && current != nil &&
		current.State == clusterDeprovisioningState {
		// A deprovision is already running (an earlier destroy timed out):
		// re-sending it would cancel and re-dispatch the platform's jobs, so
		// only wait for it.
		awaitClusterGone(deleteContext, apiClient, deprovision.clusterID, diagnostics)
		return
	}
	err := apiClient.DeprovisionLaneCluster(deleteContext, deprovision.lane, deprovision.clusterID, deprovision.options)
	if err != nil {
		if !deletedWhenNotFound(ctx, err, apiClient, private, diagnostics) && !diagnostics.HasError() {
			diagnostics.AddError("Unable to deprovision "+deprovision.displayName+" cluster", err.Error())
		}
		return
	}
	awaitClusterGone(deleteContext, apiClient, deprovision.clusterID, diagnostics)
}

// originalLaneForceDestroy reads force_destroy on the original five lanes,
// whose default stays true for backwards compatibility.
func originalLaneForceDestroy(value types.Bool) bool {
	if value.IsNull() || value.IsUnknown() {
		return true
	}
	return value.ValueBool()
}

// originalLaneClusterID is the cluster id of an original-lane resource,
// falling back to id for state written before cluster_id existed.
func originalLaneClusterID(clusterID types.String, id types.String) string {
	if value := clusterID.ValueString(); value != "" {
		return value
	}
	return id.ValueString()
}

// ImportState imports by cluster id and opens the adoption window: the
// creation arguments the platform does not return are taken from the
// configuration on the first apply instead of replacing the cluster.
func (laneResource *laneClusterResource[Model]) ImportState(ctx context.Context, request resource.ImportStateRequest, response *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("cluster_id"), request, response)
	response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("id"), request.ID)...)
	response.Diagnostics.Append(markClusterImported(ctx, response.Private)...)
}

func laneClusterID(common *laneClusterCommonModel) string {
	if clusterID := common.ClusterID.ValueString(); clusterID != "" {
		return clusterID
	}
	return common.ID.ValueString()
}

// removeMissingCluster handles a cluster the listing no longer shows. The
// listing is project-scoped, so when the provider's scope moved since the
// cluster was tracked the cluster is most likely still there: it is kept,
// with an error, rather than planned for a duplicate.
func removeMissingCluster(ctx context.Context, apiClient *client.Client, response *resource.ReadResponse) {
	if moved := scopeMoved(ctx, apiClient, response.Private); moved != "" {
		response.Diagnostics.AddError("Cluster not visible in the provider's current scope", moved+". "+scopeMovedDetail)
		return
	}
	response.State.RemoveResource(ctx)
}

// requireUnmovedScope refuses a destroy when the provider's scope moved since
// the object was tracked: the platform would answer 404, and the listing
// would not show the object, for something that still exists in the old
// scope.
func requireUnmovedScope(ctx context.Context, apiClient *client.Client, private privateStateReader, diagnostics *diag.Diagnostics) bool {
	if moved := scopeMoved(ctx, apiClient, private); moved != "" {
		diagnostics.AddError("Object not visible in the provider's current scope", moved+". "+scopeMovedDetail)
		return false
	}
	return true
}

// awaitClusterGone polls the cluster listing until the cluster leaves it.
func awaitClusterGone(ctx context.Context, apiClient *client.Client, clusterID string, diagnostics *diag.Diagnostics) {
	lastState := ""
	for {
		cluster, err := apiClient.GetClusterByID(ctx, clusterID)
		if err != nil && ctx.Err() == nil {
			diagnostics.AddError("Unable to confirm the cluster was deprovisioned", err.Error())
			return
		}
		if err == nil && cluster == nil {
			return
		}
		if cluster != nil {
			lastState = cluster.State
		}
		select {
		case <-ctx.Done():
			diagnostics.AddError("Cluster was not deprovisioned in time", fmt.Sprintf(
				"The platform accepted the deprovision of cluster %s, but it is still listed (last state %q). Raise the "+
					"delete timeout, or check the cluster's operations with `ankra cluster operations list`.", clusterID, lastState))
			return
		case <-time.After(clusterGonePollInterval):
		}
	}
}

// acceptVolumeDataLossAttribute is the deprovision consent of the lanes
// whose teardown deletes captured CSI volumes.
func acceptVolumeDataLossAttribute() schema.BoolAttribute {
	return schema.BoolAttribute{
		MarkdownDescription: "Send `accept_volume_data_loss=true` when deprovisioning, consenting to the deletion of the " +
			"cluster's persistent volumes. Without it the platform refuses to destroy a cluster that still holds " +
			"volumes it would delete, and `terraform destroy` fails with that refusal. Defaults to `false`. Changing " +
			"this only affects a future destroy, so it never replaces the cluster.",
		Optional: true,
		Computed: true,
		Default:  booldefault.StaticBool(false),
	}
}

// originalLaneAcceptVolumeDataLossAttribute is acceptVolumeDataLossAttribute
// for the original five lanes. It has no computed default, so state written
// by an earlier provider version plans no change after the upgrade; unset
// reads as false.
func originalLaneAcceptVolumeDataLossAttribute() schema.BoolAttribute {
	return schema.BoolAttribute{
		MarkdownDescription: "Send `accept_volume_data_loss=true` when deprovisioning, consenting to the deletion of the " +
			"cluster's persistent volumes. Without it the platform refuses to destroy a cluster that still holds " +
			"volumes it would delete, and `terraform destroy` fails with that refusal; `force_destroy` does not " +
			"replace this consent. Unset means `false`. Changing this only affects a future destroy, so it never " +
			"replaces the cluster.",
		Optional: true,
	}
}

// laneRetentionAttribute is the retention policy of the lanes that keep or
// delete cloud resources on deprovision. The platform records it at create
// and offers no update, so a change after creation is refused at plan time
// rather than replacing the cluster or being silently ignored.
func laneRetentionAttribute() schema.StringAttribute {
	return schema.StringAttribute{
		MarkdownDescription: "What deprovisioning does with retained cloud resources such as volumes: `retain` (default) " +
			"or `delete`. Recorded when the cluster is created; the platform cannot change it afterwards, so a change is " +
			"refused at plan time.",
		Optional:      true,
		Computed:      true,
		Default:       stringdefault.StaticString("retain"),
		Validators:    []validator.String{laneOneOf(laneRetentionPolicy...)},
		PlanModifiers: []planmodifier.String{laneFixedAfterCreate{}},
	}
}

// laneFixedAfterCreate refuses a change to a value the platform records at
// create and cannot update, except while an import is being adopted.
type laneFixedAfterCreate struct{}

func (laneFixedAfterCreate) Description(_ context.Context) string {
	return "Refuses a change after the cluster was created."
}

func (modifier laneFixedAfterCreate) MarkdownDescription(ctx context.Context) string {
	return modifier.Description(ctx)
}

func (laneFixedAfterCreate) PlanModifyString(ctx context.Context, request planmodifier.StringRequest, response *planmodifier.StringResponse) {
	if request.State.Raw.IsNull() || request.Plan.Raw.IsNull() || request.PlanValue.IsUnknown() {
		return
	}
	if request.StateValue.IsNull() || request.PlanValue.Equal(request.StateValue) {
		return
	}
	response.Diagnostics.AddAttributeError(request.Path, "Cannot change after creation",
		fmt.Sprintf("The platform records %s when the cluster is created and cannot change it (%q to %q). "+
			"Keep the original value, or recreate the cluster with `terraform apply -replace`.",
			request.Path, request.StateValue.ValueString(), request.PlanValue.ValueString()))
}

// withStringValidators adds validators to a string attribute built by one of
// the shared constructors.
func withStringValidators(attribute schema.StringAttribute, validators ...validator.String) schema.StringAttribute {
	attribute.Validators = append(attribute.Validators, validators...)
	return attribute
}
