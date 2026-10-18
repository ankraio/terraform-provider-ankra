// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"
	"regexp"

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

// stackVariableNameValidator mirrors the platform's stack variable name rule
// (`^\w+$`): the `${{ ankra.<name> }}` grammar takes word characters only.
var stackVariableNameValidator = catalogueStringValidator{
	description: "a stack variable name is one or more ASCII letters, digits and underscores",
	pattern:     regexp.MustCompile(`^[A-Za-z0-9_]+$`),
}

// clusterStackVariableOwnershipNote is the part of the resource and data
// source documentation about who else writes a stack's variables.
const clusterStackVariableOwnershipNote = "**Manage each key in one place only.** `ankra_cluster` does not send stack variables, so its applies keep " +
	"the keys this resource sets. Anything that writes a stack's whole variable map replaces them, though: an `ankra cluster apply` " +
	"whose stack carries a `variables` map, or publishing a stack draft (from the portal, say) that edits the stack's variables or other " +
	"stack-level fields, because the draft does not pick up writes made after it was opened. After such a write the next plan shows the key " +
	"missing or changed and sets it again."

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
// resource: one key of a stack's variables.
func NewClusterStackVariableResource() resource.Resource {
	return &clusterStackVariableResource{}
}

func (variableResource *clusterStackVariableResource) Metadata(_ context.Context, request resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_cluster_stack_variable"
}

func (variableResource *clusterStackVariableResource) Schema(_ context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: "One variable of a stack on a cluster, referenced from the stack's manifests and add-on values as " +
			"`${{ ankra.<name> }}`. It shadows a cluster or organisation variable of the same name. Each write changes only this key " +
			"of the deployed stack and leaves the stack's other keys, and any pending draft, as they are; a changed value redeploys " +
			"the stack members that reference it.\n\n" + clusterStackVariableOwnershipNote + "\n\n" +
			"Creating the resource for a key the stack already holds fails; import the key instead.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "`<cluster_id>/<stack_name>/<name>`.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"cluster_id": clusterSettingsClusterIDAttribute(),
			"stack_name": schema.StringAttribute{
				MarkdownDescription: "Name of the stack. Changing it replaces the resource.",
				Required:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
				Validators:          []validator.String{configurationPathNameValidator{}},
			},
			"name": schema.StringAttribute{
				MarkdownDescription: "Variable name: ASCII letters, digits and underscores. Changing it replaces the resource.",
				Required:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
				Validators:          []validator.String{stackVariableNameValidator},
			},
			"value": schema.StringAttribute{
				MarkdownDescription: "Variable value. Stack variables have no secret mode: the platform stores the value in plain text and " +
					"commits it to the cluster's GitOps repository, so keep secrets in a secret organisation variable instead. " +
					"Marked sensitive so plans do not print it.",
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
	clusterID, stackName, name := plan.ClusterID.ValueString(), plan.StackName.ValueString(), plan.Name.ValueString()

	_, lookupError := apiClient.GetStackVariable(ctx, clusterID, stackName, name)
	switch {
	case lookupError == nil:
		response.Diagnostics.AddError("Stack variable already set",
			fmt.Sprintf("Stack %q on cluster %s already holds the variable %q. Import it instead of creating it, so Terraform does not "+
				"take over a value something else manages: terraform import <resource address> %s",
				stackName, clusterID, name, clusterStackVariableID(clusterID, stackName, name)))
		return
	case !client.IsNotFound(lookupError):
		response.Diagnostics.AddError("Unable to read stack variables", clusterVariableErrorDetail(lookupError))
		return
	}

	if _, err := apiClient.SetStackVariable(ctx, clusterID, stackName, name, plan.Value.ValueString()); err != nil {
		response.Diagnostics.AddError("Unable to set stack variable", clusterVariableErrorDetail(err))
		return
	}

	plan.ID = types.StringValue(clusterStackVariableID(clusterID, stackName, name))
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
	response.Diagnostics.Append(trackScope(ctx, apiClient, response.Private)...)
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

	value, err := apiClient.GetStackVariable(ctx, state.ClusterID.ValueString(), state.StackName.ValueString(), state.Name.ValueString())
	if removeWhenNotFound(ctx, err, apiClient, response) {
		return
	}
	if err != nil {
		response.Diagnostics.AddError("Unable to read stack variable", clusterVariableErrorDetail(err))
		return
	}

	state.ID = types.StringValue(clusterStackVariableID(state.ClusterID.ValueString(), state.StackName.ValueString(), state.Name.ValueString()))
	state.Value = types.StringValue(value)
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
	response.Diagnostics.Append(trackScope(ctx, apiClient, response.Private)...)
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

	if _, err := apiClient.SetStackVariable(ctx, plan.ClusterID.ValueString(), plan.StackName.ValueString(), plan.Name.ValueString(),
		plan.Value.ValueString()); err != nil {
		response.Diagnostics.AddError("Unable to set stack variable", clusterVariableErrorDetail(err))
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

	err := apiClient.DeleteStackVariable(ctx, state.ClusterID.ValueString(), state.StackName.ValueString(), state.Name.ValueString())
	if err == nil || deletedWhenNotFound(ctx, err, apiClient, request.Private, &response.Diagnostics) || response.Diagnostics.HasError() {
		return
	}
	response.Diagnostics.AddError("Unable to remove stack variable", clusterVariableErrorDetail(err))
}

// ImportState imports by `<cluster_id>/<stack_name>/<name>`.
func (variableResource *clusterStackVariableResource) ImportState(ctx context.Context, request resource.ImportStateRequest, response *resource.ImportStateResponse) {
	parts, err := configurationSplitImportID(request.ID, "cluster_id", "stack_name", "name")
	if err != nil {
		response.Diagnostics.AddError("Invalid import ID", err.Error())
		return
	}
	response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("id"), request.ID)...)
	response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("cluster_id"), parts[0])...)
	response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("stack_name"), parts[1])...)
	response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("name"), parts[2])...)
}

func clusterStackVariableID(clusterID, stackName, name string) string {
	return clusterID + "/" + stackName + "/" + name
}
