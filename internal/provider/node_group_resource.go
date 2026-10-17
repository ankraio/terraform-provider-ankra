// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework-timeouts/resource/timeouts"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"ankra.io/terraform-provider-ankra/internal/client"
)

var (
	_ resource.Resource                = (*nodeGroupResource)(nil)
	_ resource.ResourceWithConfigure   = (*nodeGroupResource)(nil)
	_ resource.ResourceWithImportState = (*nodeGroupResource)(nil)
)

// defaultNodeGroupTimeout bounds each node-group wait: building or draining
// nodes is a minutes-long job.
const defaultNodeGroupTimeout = 30 * time.Minute

// nodeGroupPollInterval paces the node-group waits; tests shorten it.
var nodeGroupPollInterval = 15 * time.Second

type nodeGroupResource struct {
	configuredClient
}

type nodeGroupTaintModel struct {
	Key    types.String `tfsdk:"key"`
	Value  types.String `tfsdk:"value"`
	Effect types.String `tfsdk:"effect"`
}

type nodeGroupAutoscalingModel struct {
	Enabled  types.Bool  `tfsdk:"enabled"`
	MinCount types.Int64 `tfsdk:"min_count"`
	MaxCount types.Int64 `tfsdk:"max_count"`
}

type nodeGroupModel struct {
	ID               types.String               `tfsdk:"id"`
	ClusterID        types.String               `tfsdk:"cluster_id"`
	ClusterKind      types.String               `tfsdk:"cluster_kind"`
	Name             types.String               `tfsdk:"name"`
	InstanceType     types.String               `tfsdk:"instance_type"`
	Count            types.Int64                `tfsdk:"node_count"`
	Labels           types.Map                  `tfsdk:"labels"`
	Taints           []nodeGroupTaintModel      `tfsdk:"taints"`
	Autoscaling      *nodeGroupAutoscalingModel `tfsdk:"autoscaling"`
	AvailabilityZone types.String               `tfsdk:"availability_zone"`
	Zone             types.String               `tfsdk:"zone"`
	UserData         types.String               `tfsdk:"user_data"`
	ForceDrain       types.Bool                 `tfsdk:"force_drain"`
	Timeouts         timeouts.Value             `tfsdk:"timeouts"`
}

// NewNodeGroupResource returns the ankra_node_group resource, the counterpart
// of `ankra cluster node-group add|delete`, `scale`, `upgrade`, `labels`,
// `taints` and `autoscaling set`.
func NewNodeGroupResource() resource.Resource {
	return &nodeGroupResource{}
}

func (groupResource *nodeGroupResource) Metadata(_ context.Context, request resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_node_group"
}

// nodeGroupTaintsAttribute is the taint list shared by node groups and
// managed node pools.
func nodeGroupTaintsAttribute(description string, planModifiers ...planmodifier.List) schema.ListNestedAttribute {
	return schema.ListNestedAttribute{
		MarkdownDescription: description,
		Optional:            true,
		PlanModifiers:       planModifiers,
		NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
			"key": schema.StringAttribute{MarkdownDescription: "Taint key.", Required: true},
			"value": schema.StringAttribute{
				MarkdownDescription: "Taint value. Defaults to empty.",
				Optional:            true,
				Computed:            true,
				Default:             stringdefault.StaticString(""),
			},
			"effect": schema.StringAttribute{
				MarkdownDescription: "`NoSchedule`, `PreferNoSchedule` or `NoExecute`.",
				Required:            true,
				Validators:          []validator.String{laneOneOf(laneTaintEffects...)},
			},
		}},
	}
}

// nodeGroupAutoscalingAttribute is the autoscaling object shared by node
// groups and managed node pools.
func nodeGroupAutoscalingAttribute(description string) schema.SingleNestedAttribute {
	return schema.SingleNestedAttribute{
		MarkdownDescription: description,
		Optional:            true,
		Attributes: map[string]schema.Attribute{
			"enabled":   schema.BoolAttribute{MarkdownDescription: "Whether the Cluster Autoscaler manages the node count.", Required: true},
			"min_count": schema.Int64Attribute{MarkdownDescription: "Lower bound of the node count (at least 1).", Required: true, Validators: []validator.Int64{laneAtLeastOne()}},
			"max_count": schema.Int64Attribute{MarkdownDescription: "Upper bound of the node count.", Required: true, Validators: []validator.Int64{laneAtLeastOne()}},
		},
	}
}

func (groupResource *nodeGroupResource) Schema(ctx context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: "A node group of a cluster Ankra provisioned (Hetzner, OVHcloud, UpCloud, DigitalOcean, " +
			"Scaleway, AWS, Proxmox VE, HPE Morpheus or Ankra Cloud). The counterpart of `ankra cluster node-group " +
			"add|delete`, and of `scale`, `upgrade`, `labels`, `taints` and `autoscaling set` for in-place changes: " +
			"`node_count`, `instance_type`, `labels`, `taints` and `autoscaling` update the running group without " +
			"replacing it. Each change is submitted and then waited for until the group's listing shows it.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "`<cluster_id>/<name>`.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"cluster_id": schema.StringAttribute{
				MarkdownDescription: "Cluster the group belongs to. Changing it replaces the group.",
				Required:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"cluster_kind": schema.StringAttribute{
				MarkdownDescription: "Kind of the cluster (`hetzner`, `aws`, ...), resolved from the platform; it selects the API lane.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				MarkdownDescription: "Group name, unique within the cluster. Changing it replaces the group.",
				Required:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
				Validators:          []validator.String{laneNameValidator{}},
			},
			"instance_type": schema.StringAttribute{
				MarkdownDescription: "Instance type (server type, size, plan or flavor, in the cloud's vocabulary; a plan id " +
					"for HPE Morpheus). Changing it resizes every node in place: the platform drains and power-cycles each one. " +
					"The apply waits, within the update timeout, until every node of the group is back up on the new type.",
				Required: true,
			},
			"node_count": schema.Int64Attribute{
				MarkdownDescription: "Number of nodes (the API's `count`; `count` itself is reserved by Terraform). The platform defaults it to 1. Changing it scales the group in place. " +
					"While `autoscaling` is enabled the Cluster Autoscaler owns the live count, so drift in it is not reported; " +
					"leave `node_count` unset then.",
				Optional:      true,
				Computed:      true,
				PlanModifiers: []planmodifier.Int64{int64planmodifier.UseStateForUnknown()},
				Validators:    []validator.Int64{laneAtLeastOne()},
			},
			"labels": schema.MapAttribute{
				MarkdownDescription: "Kubernetes labels of the group's nodes. Updated in place.",
				Optional:            true,
				ElementType:         types.StringType,
			},
			"taints": nodeGroupTaintsAttribute("Kubernetes taints of the group's nodes. Updated in place."),
			"autoscaling": nodeGroupAutoscalingAttribute(
				"Cluster Autoscaler bounds of the group. Updated in place; removing the block disables autoscaling."),
			"availability_zone": optionalReplaceString("Pin every node to one availability zone (OVHcloud 3-AZ regions)."),
			"zone":              optionalReplaceString("Pin every node to one zone of an UpCloud multi-zone cluster's pool."),
			"user_data": schema.StringAttribute{
				MarkdownDescription: "Cloud-init document applied at first boot of every node the group creates (OVHcloud only). " +
					"The platform does not return it, so a change made outside Terraform is not detected. Changing it replaces the group.",
				Optional:      true,
				Sensitive:     true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"force_drain": schema.BoolAttribute{
				MarkdownDescription: "Drain nodes a scale-down, resize or delete removes without honouring their pods' " +
					"PodDisruptionBudgets. Defaults to `false`. Affects future changes only.",
				Optional: true,
				Computed: true,
				Default:  booldefault.StaticBool(false),
			},
		},
		Blocks: map[string]schema.Block{
			"timeouts": timeouts.Block(ctx, timeouts.Opts{Create: true, Update: true, Delete: true}),
		},
	}
}

func (groupResource *nodeGroupResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	apiClient := groupResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var plan nodeGroupModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}

	clusterID := plan.ClusterID.ValueString()
	lane, ok := resolveNodeGroupLane(ctx, apiClient, clusterID, &response.Diagnostics)
	if !ok {
		return
	}
	labels := nodeGroupLabelsToAPI(ctx, plan.Labels, &response.Diagnostics)
	if response.Diagnostics.HasError() {
		return
	}
	createRequest := client.NodeGroupCreate{
		Name:             plan.Name.ValueString(),
		InstanceType:     plan.InstanceType.ValueString(),
		Count:            int64Pointer(plan.Count),
		Labels:           labels,
		Taints:           nodeGroupTaintsToAPI(plan.Taints),
		AvailabilityZone: plan.AvailabilityZone.ValueString(),
		Zone:             plan.Zone.ValueString(),
		UserData:         plan.UserData.ValueString(),
	}
	if plan.Autoscaling != nil {
		createRequest.Autoscaling = &client.NodeGroupAutoscaling{
			Enabled:  plan.Autoscaling.Enabled.ValueBool(),
			MinCount: plan.Autoscaling.MinCount.ValueInt64(),
			MaxCount: plan.Autoscaling.MaxCount.ValueInt64(),
		}
	}

	wait := resolveTimeout(ctx, plan.Timeouts.Create, defaultNodeGroupTimeout, &response.Diagnostics)
	if response.Diagnostics.HasError() {
		return
	}
	waitContext, cancel := context.WithTimeout(ctx, wait)
	defer cancel()

	if err := apiClient.AddNodeGroup(waitContext, lane, clusterID, createRequest); err != nil {
		response.Diagnostics.AddError("Unable to add node group", err.Error())
		return
	}
	plan.ID = types.StringValue(clusterID + "/" + plan.Name.ValueString())
	plan.ClusterKind = types.StringValue(lane)

	wantCount := int64(1)
	if !plan.Count.IsNull() && !plan.Count.IsUnknown() {
		wantCount = plan.Count.ValueInt64()
	}
	isAutoscaled := plan.Autoscaling != nil && plan.Autoscaling.Enabled.ValueBool()
	group, err := apiClient.WaitForNodeGroup(waitContext, lane, clusterID, plan.Name.ValueString(), nodeGroupPollInterval,
		func(group *client.NodeGroup) bool {
			return group != nil && group.InstanceType == createRequest.InstanceType &&
				(isAutoscaled || group.Count == wantCount) && nodeGroupNodesJoined(group)
		})
	if err != nil {
		// The group was accepted; keep it in state so it is not orphaned.
		plan.Count = types.Int64Value(wantCount)
		response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
		response.Diagnostics.Append(trackScope(ctx, apiClient, response.Private)...)
		response.Diagnostics.AddError("Node group did not become ready", err.Error())
		return
	}
	plan.Count = types.Int64Value(group.Count)
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
	response.Diagnostics.Append(trackScope(ctx, apiClient, response.Private)...)
}

// nodeGroupNodesJoined reports whether every counted worker of a group is a
// Kubernetes node. The listing records a write's target (count, instance
// type) as soon as the platform accepts it, so the target alone says nothing
// about the nodes; joined_count and unregistered_workers are the listing's
// only view of them. When the platform cannot tell (it omits joined_count,
// for example on AWS or a stopped cluster), the wait ends at the accepted
// target.
func nodeGroupNodesJoined(group *client.NodeGroup) bool {
	if len(group.UnregisteredWorkers) > 0 {
		return false
	}
	return group.JoinedCount == nil || *group.JoinedCount == group.Count
}

// awaitNodeGroupRoll waits for an instance-type resize to finish on every
// node. The group listing shows the new instance type as soon as the resize
// is accepted, and joined_count stays equal to the count while nodes are
// power-cycled one lane at a time, so neither says the roll is done. The
// platform moves every worker of the group to "updating" in the same write
// that accepts the resize and back to "up" once its part of the roll is
// done, which the node listing shows per machine.
func awaitNodeGroupRoll(ctx context.Context, apiClient *client.Client, lane, clusterID, groupName, instanceType string) error {
	var pending []string
	for {
		nodes, err := apiClient.ListClusterNodes(ctx, lane, clusterID)
		if err != nil {
			if ctx.Err() != nil {
				break
			}
			return fmt.Errorf("unable to read the cluster's nodes to follow the resize: %w", err)
		}
		pending = pending[:0]
		seen := 0
		for _, node := range nodes {
			if node.IsDeleted || node.NodeGroup == nil || *node.NodeGroup != groupName {
				continue
			}
			seen++
			if node.State != client.ClusterNodeUpState || node.InstanceType != instanceType {
				pending = append(pending, fmt.Sprintf("%s (%s, %s)", node.Name, node.State, node.InstanceType))
			}
		}
		if seen > 0 && len(pending) == 0 {
			return nil
		}
		select {
		case <-ctx.Done():
		case <-time.After(nodeGroupPollInterval):
			continue
		}
		break
	}
	return fmt.Errorf("the resize of group %s to %s was accepted but had not finished on every node before the update "+
		"timeout; still rolling: %s. A node whose drain is refused stays here: see `ankra cluster operations list`, or set "+
		"force_drain = true", groupName, instanceType, strings.Join(pending, ", "))
}

// nodeGroupReadNotFound handles a 404 from the node-group routes. They
// answer "Cluster not found" both for a cluster outside the provider's scope
// and for a cluster that was deleted: when the provider's scope moved since
// the group was tracked it is reported, otherwise the cluster, and the group
// with it, is gone.
func nodeGroupReadNotFound(ctx context.Context, err error, apiClient *client.Client, response *resource.ReadResponse) bool {
	return removeWhenNotFound(ctx, err, apiClient, response)
}

func (groupResource *nodeGroupResource) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	apiClient := groupResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state nodeGroupModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}

	clusterID := state.ClusterID.ValueString()
	lane := state.ClusterKind.ValueString()
	if lane == "" {
		cluster, err := apiClient.GetClusterByID(ctx, clusterID)
		if err != nil {
			response.Diagnostics.AddError("Unable to read cluster", err.Error())
			return
		}
		if cluster == nil {
			removeMissingCluster(ctx, apiClient, response)
			return
		}
		lane = cluster.Kind
	}

	group, err := apiClient.GetNodeGroup(ctx, lane, clusterID, state.Name.ValueString())
	if nodeGroupReadNotFound(ctx, err, apiClient, response) {
		return
	}
	if err != nil {
		response.Diagnostics.AddError("Unable to read node group", err.Error())
		return
	}

	state.ID = types.StringValue(clusterID + "/" + group.Name)
	state.ClusterKind = types.StringValue(lane)
	state.InstanceType = types.StringValue(group.InstanceType)
	isAutoscaled := group.AutoscalingEnabled
	if !isAutoscaled || state.Count.IsNull() || state.Count.IsUnknown() {
		state.Count = types.Int64Value(group.Count)
	}
	state.Labels = nodeGroupLabelsFromAPI(group.Labels, state.Labels)
	state.Taints = nodeGroupTaintsFromAPI(group.Taints, state.Taints)
	if state.Autoscaling != nil || group.AutoscalingEnabled {
		state.Autoscaling = &nodeGroupAutoscalingModel{
			Enabled:  types.BoolValue(group.AutoscalingEnabled),
			MinCount: types.Int64Value(group.Min),
			MaxCount: types.Int64Value(group.Max),
		}
	}
	if state.ForceDrain.IsNull() {
		state.ForceDrain = types.BoolValue(false)
	}
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
	response.Diagnostics.Append(trackScope(ctx, apiClient, response.Private)...)
}

// Update applies each in-place change in turn and waits for the listing to
// show it before the next, because the platform refuses a second write on a
// group while one is still running.
func (groupResource *nodeGroupResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	apiClient := groupResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var plan, state nodeGroupModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}

	wait := resolveTimeout(ctx, plan.Timeouts.Update, defaultNodeGroupTimeout, &response.Diagnostics)
	if response.Diagnostics.HasError() {
		return
	}
	waitContext, cancel := context.WithTimeout(ctx, wait)
	defer cancel()

	lane := state.ClusterKind.ValueString()
	clusterID := state.ClusterID.ValueString()
	name := state.Name.ValueString()
	forceDrain := plan.ForceDrain.ValueBool()
	apply := func(summary string, write func() error, condition client.NodeGroupCondition) bool {
		if err := write(); err != nil {
			response.Diagnostics.AddError("Unable to update node group "+summary, err.Error())
			return false
		}
		if _, err := apiClient.WaitForNodeGroup(waitContext, lane, clusterID, name, nodeGroupPollInterval, condition); err != nil {
			response.Diagnostics.AddError("Node group "+summary+" did not converge", err.Error())
			return false
		}
		return true
	}

	if !plan.InstanceType.Equal(state.InstanceType) {
		instanceType := plan.InstanceType.ValueString()
		if !apply("instance type", func() error {
			return apiClient.UpdateNodeGroupInstanceType(waitContext, lane, clusterID, name, instanceType, forceDrain)
		}, func(group *client.NodeGroup) bool {
			return group != nil && group.InstanceType == instanceType && nodeGroupNodesJoined(group)
		}) {
			return
		}
		if rollError := awaitNodeGroupRoll(waitContext, apiClient, lane, clusterID, name, instanceType); rollError != nil {
			response.Diagnostics.AddError("Node group resize did not finish", rollError.Error())
			return
		}
	}

	if settings, changed := nodeGroupAutoscalingChange(plan.Autoscaling, state.Autoscaling); changed {
		if !apply("autoscaling", func() error {
			return apiClient.UpdateNodeGroupAutoscaling(waitContext, lane, clusterID, name, settings)
		}, func(group *client.NodeGroup) bool {
			return group != nil && group.AutoscalingEnabled == settings.Enabled &&
				group.Min == settings.MinCount && group.Max == settings.MaxCount
		}) {
			return
		}
	}

	isAutoscaled := plan.Autoscaling != nil && plan.Autoscaling.Enabled.ValueBool()
	if !plan.Count.IsUnknown() && !plan.Count.IsNull() && !plan.Count.Equal(state.Count) {
		count := plan.Count.ValueInt64()
		if !apply("count", func() error {
			return apiClient.ScaleNodeGroup(waitContext, lane, clusterID, name, count, forceDrain)
		}, func(group *client.NodeGroup) bool {
			return group != nil && (isAutoscaled || group.Count == count) && nodeGroupNodesJoined(group)
		}) {
			return
		}
	}

	if !plan.Labels.Equal(state.Labels) {
		labels := nodeGroupLabelsToAPI(ctx, plan.Labels, &response.Diagnostics)
		if response.Diagnostics.HasError() {
			return
		}
		if !apply("labels", func() error {
			return apiClient.UpdateNodeGroupLabels(waitContext, lane, clusterID, name, labels)
		}, func(group *client.NodeGroup) bool {
			return group != nil && maps.Equal(stringifyLabels(group.Labels), labels)
		}) {
			return
		}
	}

	if !nodeGroupTaintsEqual(plan.Taints, state.Taints) {
		taints := nodeGroupTaintsToAPI(plan.Taints)
		if !apply("taints", func() error {
			return apiClient.UpdateNodeGroupTaints(waitContext, lane, clusterID, name, taints)
		}, func(group *client.NodeGroup) bool { return group != nil && slices.Equal(group.Taints, taints) }) {
			return
		}
	}

	group, err := apiClient.GetNodeGroup(waitContext, lane, clusterID, name)
	if err != nil {
		response.Diagnostics.AddError("Unable to read node group", err.Error())
		return
	}
	plan.ID = state.ID
	plan.ClusterKind = state.ClusterKind
	if plan.Count.IsUnknown() || plan.Count.IsNull() || !isAutoscaled {
		plan.Count = types.Int64Value(group.Count)
	}
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
}

func (groupResource *nodeGroupResource) Delete(ctx context.Context, request resource.DeleteRequest, response *resource.DeleteResponse) {
	apiClient := groupResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state nodeGroupModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	wait := resolveTimeout(ctx, state.Timeouts.Delete, defaultNodeGroupTimeout, &response.Diagnostics)
	if response.Diagnostics.HasError() {
		return
	}
	waitContext, cancel := context.WithTimeout(ctx, wait)
	defer cancel()

	lane := state.ClusterKind.ValueString()
	clusterID := state.ClusterID.ValueString()
	name := state.Name.ValueString()
	if !requireUnmovedScope(ctx, apiClient, request.Private, &response.Diagnostics) {
		return
	}
	// The scope has not moved (checked above), so a "Cluster not found" here
	// means the cluster, and the group with it, is gone.
	if err := apiClient.DeleteNodeGroup(waitContext, lane, clusterID, name, state.ForceDrain.ValueBool()); err != nil {
		if !deletedWhenNotFound(ctx, err, apiClient, request.Private, &response.Diagnostics) && !response.Diagnostics.HasError() {
			response.Diagnostics.AddError("Unable to delete node group", err.Error())
		}
		return
	}
	var lastSeen *client.NodeGroup
	_, err := apiClient.WaitForNodeGroup(waitContext, lane, clusterID, name, nodeGroupPollInterval,
		func(group *client.NodeGroup) bool {
			lastSeen = group
			return group == nil
		})
	if err == nil || client.IsNotFound(err) {
		return
	}
	detail := err.Error()
	if lastSeen != nil {
		detail = fmt.Sprintf("The platform accepted the delete, but group %q still lists %d node(s). A node whose pods' "+
			"PodDisruptionBudgets refuse the drain keeps the group alive: the refusal is on the cluster's operations "+
			"(`ankra cluster operations list`). Set force_drain = true to drain without honouring them. (%s)",
			name, lastSeen.Count, err.Error())
	}
	response.Diagnostics.AddError("Node group was not removed", detail)
}

// ImportState imports by `<cluster_id>/<name>`.
func (groupResource *nodeGroupResource) ImportState(ctx context.Context, request resource.ImportStateRequest, response *resource.ImportStateResponse) {
	clusterID, name, ok := splitClusterScopedID(request.ID)
	if !ok {
		response.Diagnostics.AddError("Invalid import id",
			fmt.Sprintf("Expected <cluster_id>/<name>, got %q.", request.ID))
		return
	}
	response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("id"), request.ID)...)
	response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("cluster_id"), clusterID)...)
	response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("name"), name)...)
	response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("force_drain"), false)...)
}

// splitClusterScopedID parses `<cluster_id>/<name>` strictly: exactly two
// non-empty parts.
func splitClusterScopedID(identifier string) (string, string, bool) {
	parts := strings.Split(identifier, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}

// resolveNodeGroupLane reads the cluster's kind, which names the API lane its
// node groups live under.
func resolveNodeGroupLane(ctx context.Context, apiClient *client.Client, clusterID string, diagnostics *diag.Diagnostics) (string, bool) {
	cluster, err := apiClient.GetClusterByID(ctx, clusterID)
	if err != nil {
		diagnostics.AddError("Unable to read cluster", err.Error())
		return "", false
	}
	if cluster == nil {
		diagnostics.AddAttributeError(path.Root("cluster_id"), "Cluster not found",
			fmt.Sprintf("No cluster %s is visible to this token.", clusterID))
		return "", false
	}
	if !slices.Contains(client.NodeGroupLanes, cluster.Kind) {
		diagnostics.AddAttributeError(path.Root("cluster_id"), "Cluster has no node groups",
			fmt.Sprintf("Cluster %s is of kind %q. Node groups exist on clusters Ankra provisions: %s. "+
				"Managed Kubernetes clusters use ankra_managed_node_pool.", clusterID, cluster.Kind, strings.Join(client.NodeGroupLanes, ", ")))
		return "", false
	}
	return cluster.Kind, true
}

func nodeGroupLabelsToAPI(ctx context.Context, labels types.Map, diagnostics *diag.Diagnostics) map[string]string {
	if labels.IsNull() || labels.IsUnknown() {
		return map[string]string{}
	}
	values := map[string]string{}
	diagnostics.Append(labels.ElementsAs(ctx, &values, false)...)
	return values
}

func stringifyLabels(labels map[string]any) map[string]string {
	result := make(map[string]string, len(labels))
	for key, value := range labels {
		result[key] = fmt.Sprint(value)
	}
	return result
}

// nodeGroupLabelsFromAPI maps listed labels to state. No labels stay null
// when the configuration set none, so an unset attribute plans no diff.
func nodeGroupLabelsFromAPI(labels map[string]any, prior types.Map) types.Map {
	if len(labels) == 0 && prior.IsNull() {
		return types.MapNull(types.StringType)
	}
	elements := make(map[string]attr.Value, len(labels))
	for key, value := range stringifyLabels(labels) {
		elements[key] = types.StringValue(value)
	}
	return types.MapValueMust(types.StringType, elements)
}

func nodeGroupTaintsToAPI(taints []nodeGroupTaintModel) []client.NodeGroupTaint {
	result := make([]client.NodeGroupTaint, 0, len(taints))
	for _, taint := range taints {
		result = append(result, client.NodeGroupTaint{
			Key: taint.Key.ValueString(), Value: taint.Value.ValueString(), Effect: taint.Effect.ValueString(),
		})
	}
	return result
}

func nodeGroupTaintsFromAPI(taints []client.NodeGroupTaint, prior []nodeGroupTaintModel) []nodeGroupTaintModel {
	if len(taints) == 0 && prior == nil {
		return nil
	}
	result := make([]nodeGroupTaintModel, 0, len(taints))
	for _, taint := range taints {
		result = append(result, nodeGroupTaintModel{
			Key: types.StringValue(taint.Key), Value: types.StringValue(taint.Value), Effect: types.StringValue(taint.Effect),
		})
	}
	return result
}

func nodeGroupTaintsEqual(left, right []nodeGroupTaintModel) bool {
	return slices.Equal(nodeGroupTaintsToAPI(left), nodeGroupTaintsToAPI(right))
}

func nodeGroupAutoscalingEqual(left, right *nodeGroupAutoscalingModel) bool {
	if left == nil || right == nil {
		return left == right
	}
	return left.Enabled.Equal(right.Enabled) && left.MinCount.Equal(right.MinCount) && left.MaxCount.Equal(right.MaxCount)
}

// nodeGroupAutoscalingChange reports the autoscaling write an update needs.
// Removing the block from configuration disables autoscaling, keeping the
// last bounds, so the listing and the configuration agree afterwards.
func nodeGroupAutoscalingChange(planned, prior *nodeGroupAutoscalingModel) (client.NodeGroupAutoscaling, bool) {
	if nodeGroupAutoscalingEqual(planned, prior) {
		return client.NodeGroupAutoscaling{}, false
	}
	if planned == nil {
		if !prior.Enabled.ValueBool() {
			return client.NodeGroupAutoscaling{}, false
		}
		return client.NodeGroupAutoscaling{
			Enabled: false, MinCount: prior.MinCount.ValueInt64(), MaxCount: prior.MaxCount.ValueInt64(),
		}, true
	}
	return client.NodeGroupAutoscaling{
		Enabled:  planned.Enabled.ValueBool(),
		MinCount: planned.MinCount.ValueInt64(),
		MaxCount: planned.MaxCount.ValueInt64(),
	}, true
}
