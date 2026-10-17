// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"
	"sync"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"ankra.io/terraform-provider-ankra/internal/client"
)

var (
	_ resource.Resource                = (*clusterStackVariableResource)(nil)
	_ resource.ResourceWithConfigure   = (*clusterStackVariableResource)(nil)
	_ resource.ResourceWithImportState = (*clusterStackVariableResource)(nil)
)

// configurationStackLocks serialises the read-modify-write of one stack's
// variables map: every ankra_cluster_stack_variable on a stack rewrites the
// whole map, so two of them applied in parallel would drop each other's
// change.
var configurationStackLocks sync.Map

func configurationLockStack(clusterID, stackName string) func() {
	value, _ := configurationStackLocks.LoadOrStore(clusterID+"/"+stackName, &sync.Mutex{})
	mutex, ok := value.(*sync.Mutex)
	if !ok {
		mutex = &sync.Mutex{}
	}
	mutex.Lock()
	return mutex.Unlock
}

// clusterStackVariableOperation is the change write makes to a stack's
// variables map.
type clusterStackVariableOperation int

const (
	clusterStackVariableCreate clusterStackVariableOperation = iota
	clusterStackVariableUpdate
	clusterStackVariableDelete
)

type clusterStackVariableResource struct {
	configuredClient
}

type clusterStackVariableModel struct {
	ID        types.String `tfsdk:"id"`
	ClusterID types.String `tfsdk:"cluster_id"`
	StackName types.String `tfsdk:"stack_name"`
	Name      types.String `tfsdk:"name"`
	Value     types.String `tfsdk:"value"`
}

// NewClusterStackVariableResource returns the ankra_cluster_stack_variable
// resource, the counterpart of `ankra cluster stacks variables set|delete`.
func NewClusterStackVariableResource() resource.Resource {
	return &clusterStackVariableResource{}
}

func (variableResource *clusterStackVariableResource) Metadata(_ context.Context, request resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_cluster_stack_variable"
}

func (variableResource *clusterStackVariableResource) Schema(_ context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: "A variable on one stack of a cluster, the most specific scope: it shadows cluster and organisation variables " +
			"of the same name when that stack's manifests and add-ons render. Stack variables live in the stack's specification, " +
			"so every change is a stack update the platform commits to the cluster's GitOps repository and redeploys. " +
			"The stack must already exist (created through `ankra_cluster`, the CLI or the portal); an `ankra_cluster` apply leaves " +
			"stack variables alone. The counterpart of `ankra cluster stacks variables set` and `ankra cluster stacks variables delete`.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "`<cluster_id>/<stack_name>/<name>`.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"cluster_id": schema.StringAttribute{
				MarkdownDescription: "Identifier of the cluster. Changing it replaces the variable.",
				Required:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"stack_name": schema.StringAttribute{
				MarkdownDescription: "Name of the stack. Changing it replaces the variable.",
				Required:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
				Validators:          []validator.String{configurationPathNameValidator{}},
			},
			"name": schema.StringAttribute{
				MarkdownDescription: "Variable name. Changing it replaces the variable.",
				Required:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
				Validators:          []validator.String{configurationPathNameValidator{}},
			},
			"value": schema.StringAttribute{
				MarkdownDescription: "Variable value. It is stored in plain text in the stack specification and the GitOps repository; " +
					"marked sensitive so plans do not print it.",
				Required:  true,
				Sensitive: true,
			},
		},
	}
}

func (variableResource *clusterStackVariableResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	apiClient := variableResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var plan clusterStackVariableModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}

	if !variableResource.write(ctx, apiClient, plan, clusterStackVariableCreate, &response.Diagnostics) {
		return
	}
	plan.ID = types.StringValue(clusterStackVariableID(plan))
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
}

func (variableResource *clusterStackVariableResource) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	apiClient := variableResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state clusterStackVariableModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}

	stack, err := apiClient.GetClusterStackVariables(ctx, state.ClusterID.ValueString(), state.StackName.ValueString())
	if removeWhenNotFound(ctx, err, response) {
		return
	}
	if err != nil {
		response.Diagnostics.AddError("Unable to read stack variable", err.Error())
		return
	}
	value, exists := stack.Variables[state.Name.ValueString()]
	if !exists {
		response.State.RemoveResource(ctx)
		return
	}

	state.ID = types.StringValue(clusterStackVariableID(state))
	state.Value = types.StringValue(value)
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
}

func (variableResource *clusterStackVariableResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	apiClient := variableResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var plan clusterStackVariableModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}

	if !variableResource.write(ctx, apiClient, plan, clusterStackVariableUpdate, &response.Diagnostics) {
		return
	}
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
}

func (variableResource *clusterStackVariableResource) Delete(ctx context.Context, request resource.DeleteRequest, response *resource.DeleteResponse) {
	apiClient := variableResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state clusterStackVariableModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}

	variableResource.write(ctx, apiClient, state, clusterStackVariableDelete, &response.Diagnostics)
}

// write applies operation to the stack: it reads the stack's current
// variables map and sends it back with this one change. Create refuses a
// variable the stack already has, so an existing one is imported rather than
// silently taken over. Deleting from a stack that is gone, or that no longer
// has the variable, sends nothing; so does an update that changes nothing.
func (variableResource *clusterStackVariableResource) write(ctx context.Context, apiClient *client.Client, model clusterStackVariableModel, operation clusterStackVariableOperation, diagnostics *diag.Diagnostics) bool {
	clusterID := model.ClusterID.ValueString()
	stackName := model.StackName.ValueString()
	name := model.Name.ValueString()
	value := model.Value.ValueString()
	unlock := configurationLockStack(clusterID, stackName)
	defer unlock()

	stack, err := apiClient.GetClusterStackVariables(ctx, clusterID, stackName)
	if operation == clusterStackVariableDelete && client.IsNotFound(err) {
		return true
	}
	if client.IsNotFound(err) {
		diagnostics.AddError("Stack not found",
			fmt.Sprintf("Stack %q does not exist on cluster %s; create the stack before setting its variables.", stackName, clusterID))
		return false
	}
	if err != nil {
		diagnostics.AddError("Unable to read stack variables", err.Error())
		return false
	}

	current, exists := stack.Variables[name]
	switch operation {
	case clusterStackVariableCreate:
		if exists {
			diagnostics.AddError("Stack variable already exists",
				fmt.Sprintf("Stack %q on cluster %s already has a variable %q. Import it with "+
					"`terraform import <address> %s/%s/%s` to manage it.", stackName, clusterID, name, clusterID, stackName, name))
			return false
		}
		stack.Variables[name] = value
	case clusterStackVariableUpdate:
		if exists && current == value {
			return true
		}
		stack.Variables[name] = value
	case clusterStackVariableDelete:
		if !exists {
			return true
		}
		delete(stack.Variables, name)
	}

	err = apiClient.UpdateClusterStackVariables(ctx, clusterID, *stack)
	if err == nil || (operation == clusterStackVariableDelete && client.IsNotFound(err)) {
		return true
	}
	summary := "Unable to set stack variable"
	if operation == clusterStackVariableDelete {
		summary = "Unable to delete stack variable"
	}
	diagnostics.AddError(summary, err.Error())
	return false
}

// ImportState imports by `<cluster_id>/<stack_name>/<name>`.
func (variableResource *clusterStackVariableResource) ImportState(ctx context.Context, request resource.ImportStateRequest, response *resource.ImportStateResponse) {
	parts, err := configurationSplitImportID(request.ID, "cluster_id", "stack_name", "name")
	if err != nil {
		response.Diagnostics.AddError("Invalid import ID", err.Error())
		return
	}
	response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("cluster_id"), parts[0])...)
	response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("stack_name"), parts[1])...)
	response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("name"), parts[2])...)
}

func clusterStackVariableID(model clusterStackVariableModel) string {
	return model.ClusterID.ValueString() + "/" + model.StackName.ValueString() + "/" + model.Name.ValueString()
}
