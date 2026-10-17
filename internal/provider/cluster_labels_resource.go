// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"ankra.io/terraform-provider-ankra/internal/client"
)

var (
	_ resource.Resource                = (*clusterLabelsResource)(nil)
	_ resource.ResourceWithConfigure   = (*clusterLabelsResource)(nil)
	_ resource.ResourceWithImportState = (*clusterLabelsResource)(nil)
)

// The platform's label rules: lower-case keys, the
// environment key reserved for the cluster's own environment, at most 32
// labels of at most 63 characters a part.
const (
	clusterLabelsReservedKey     = "environment"
	clusterLabelsMaximumCount    = 32
	clusterLabelsMaximumPartSize = 63
)

var (
	clusterLabelsKeyPattern   = regexp.MustCompile(`^[a-z0-9]([a-z0-9._/-]*[a-z0-9])?$`)
	clusterLabelsValuePattern = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9._-]*[A-Za-z0-9])?$`)
)

type clusterLabelsResource struct {
	configuredClient
}

type clusterLabelsModel struct {
	ID        types.String `tfsdk:"id"`
	ClusterID types.String `tfsdk:"cluster_id"`
	Labels    types.Map    `tfsdk:"labels"`
}

// NewClusterLabelsResource returns the ankra_cluster_labels resource.
func NewClusterLabelsResource() resource.Resource {
	return &clusterLabelsResource{}
}

func (labelsResource *clusterLabelsResource) Metadata(_ context.Context, request resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_cluster_labels"
}

func (labelsResource *clusterLabelsResource) Schema(_ context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: "The free-form labels on a cluster, which dynamic cluster groups select on. The resource owns the " +
			"cluster's whole label set: a label that is not in `labels`, including one added in the portal, is removed on the next " +
			"apply. Declare at most one `ankra_cluster_labels` per cluster. Creating it on a cluster that already carries labels the " +
			"configuration does not name fails; add them to `labels` or `terraform import` the cluster's labels first. Destroying the " +
			"resource removes every label it manages. The cluster's environment is not a label; it is set on the cluster.",
		Attributes: map[string]schema.Attribute{
			"id":         clusterSettingsIDAttribute("The cluster id."),
			"cluster_id": clusterSettingsClusterIDAttribute(),
			"labels": schema.MapAttribute{
				MarkdownDescription: fmt.Sprintf("Labels by key, at most %d. Keys are 1-%d lower-case letters, digits, `.`, `_`, `-` or `/`; "+
					"values are 1-%d letters, digits, `.`, `_` or `-`; both start and end with a letter or digit. The key `%s` is reserved.",
					clusterLabelsMaximumCount, clusterLabelsMaximumPartSize, clusterLabelsMaximumPartSize, clusterLabelsReservedKey),
				Required:    true,
				ElementType: types.StringType,
				Validators:  []validator.Map{clusterLabelsValidator{}},
			},
		},
	}
}

func (labelsResource *clusterLabelsResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	var plan clusterLabelsModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if labelsResource.converge(ctx, &plan, true, &response.Diagnostics) {
		response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
		response.Diagnostics.Append(trackScope(ctx, labelsResource.client, response.Private)...)
	}
}

func (labelsResource *clusterLabelsResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	var plan clusterLabelsModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if labelsResource.converge(ctx, &plan, false, &response.Diagnostics) {
		response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
	}
}

// converge makes the cluster carry exactly the planned labels: it sets
// every planned label and removes every other key the cluster carries.
func (labelsResource *clusterLabelsResource) converge(ctx context.Context, plan *clusterLabelsModel, isCreate bool, diagnostics *diag.Diagnostics) bool {
	apiClient := labelsResource.requireClient(diagnostics)
	if apiClient == nil || diagnostics.HasError() {
		return false
	}
	clusterID := plan.ClusterID.ValueString()
	desired := map[string]string{}
	diagnostics.Append(plan.Labels.ElementsAs(ctx, &desired, false)...)
	if diagnostics.HasError() {
		return false
	}
	current, err := apiClient.GetClusterLabels(ctx, clusterID)
	if err != nil {
		diagnostics.AddError("Unable to read cluster labels", clusterSettingsErrorText(err, clusterID))
		return false
	}
	change := clusterLabelsChange(current.Labels, desired)
	if isCreate && len(change.Remove) > 0 {
		diagnostics.AddError("Cluster already has labels",
			fmt.Sprintf("Cluster %s carries labels this configuration does not name (%s). Creating the resource would remove them "+
				"without the plan showing it. Add them to `labels`, or adopt the cluster's labels with "+
				"`terraform import <address> %s` and review the next plan.", clusterID, strings.Join(change.Remove, ", "), clusterID))
		return false
	}
	result := current
	if len(change.Set) > 0 || len(change.Remove) > 0 {
		result, err = apiClient.ChangeClusterLabels(ctx, clusterID, change)
		if err != nil {
			diagnostics.AddError("Unable to set cluster labels", clusterSettingsErrorText(err, clusterID))
			return false
		}
	}
	plan.ID = plan.ClusterID
	plan.Labels = clusterLabelsMap(result.Labels)
	return true
}

func (labelsResource *clusterLabelsResource) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	apiClient := labelsResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state clusterLabelsModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	labels, err := apiClient.GetClusterLabels(ctx, state.ClusterID.ValueString())
	if clusterSettingsReadNotFound(ctx, err, apiClient, state.ClusterID.ValueString(), response) {
		return
	}
	if err != nil {
		response.Diagnostics.AddError("Unable to read cluster labels", err.Error())
		return
	}
	state.ID = state.ClusterID
	state.Labels = clusterLabelsMap(labels.Labels)
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
	response.Diagnostics.Append(trackScope(ctx, apiClient, response.Private)...)
}

// Delete removes the labels in state. A label added outside Terraform since
// the last refresh is left.
func (labelsResource *clusterLabelsResource) Delete(ctx context.Context, request resource.DeleteRequest, response *resource.DeleteResponse) {
	apiClient := labelsResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state clusterLabelsModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	managed := map[string]string{}
	response.Diagnostics.Append(state.Labels.ElementsAs(ctx, &managed, false)...)
	if response.Diagnostics.HasError() || len(managed) == 0 {
		return
	}
	keys := make([]string, 0, len(managed))
	for key := range managed {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	_, err := apiClient.ChangeClusterLabels(ctx, state.ClusterID.ValueString(), client.ClusterLabelsChange{Remove: keys})
	if deleteError := clusterSettingsDeleteError(err, state.ClusterID.ValueString()); deleteError != nil {
		response.Diagnostics.AddError("Unable to remove cluster labels", deleteError.Error())
	}
}

// ImportState imports by cluster id.
func (labelsResource *clusterLabelsResource) ImportState(ctx context.Context, request resource.ImportStateRequest, response *resource.ImportStateResponse) {
	clusterSettingsImportClusterID(ctx, request, response)
}

// clusterLabelsChange is the change that turns current into desired: set
// every label whose value differs or is missing, remove every other key.
func clusterLabelsChange(current, desired map[string]string) client.ClusterLabelsChange {
	change := client.ClusterLabelsChange{Set: map[string]string{}}
	for key, value := range desired {
		if currentValue, present := current[key]; !present || currentValue != value {
			change.Set[key] = value
		}
	}
	for key := range current {
		if _, wanted := desired[key]; !wanted {
			change.Remove = append(change.Remove, key)
		}
	}
	sort.Strings(change.Remove)
	return change
}

func clusterLabelsMap(labels map[string]string) types.Map {
	elements := make(map[string]attr.Value, len(labels))
	for key, value := range labels {
		elements[key] = types.StringValue(value)
	}
	return types.MapValueMust(types.StringType, elements)
}

// clusterLabelsValidator refuses at plan time a label set the platform would
// refuse at apply.
type clusterLabelsValidator struct{}

func (clusterLabelsValidator) Description(_ context.Context) string {
	return "labels must follow the platform's label rules"
}

func (labelsValidator clusterLabelsValidator) MarkdownDescription(ctx context.Context) string {
	return labelsValidator.Description(ctx)
}

func (clusterLabelsValidator) ValidateMap(_ context.Context, request validator.MapRequest, response *validator.MapResponse) {
	if request.ConfigValue.IsNull() || request.ConfigValue.IsUnknown() {
		return
	}
	elements := request.ConfigValue.Elements()
	if len(elements) > clusterLabelsMaximumCount {
		response.Diagnostics.AddAttributeError(request.Path, "Too many labels",
			fmt.Sprintf("A cluster carries at most %d labels; this configuration sets %d.", clusterLabelsMaximumCount, len(elements)))
	}
	keys := make([]string, 0, len(elements))
	for key := range elements {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if key == clusterLabelsReservedKey {
			response.Diagnostics.AddAttributeError(request.Path, "Reserved label key",
				fmt.Sprintf("The %q label is reserved: it is the cluster's environment, which is set on the cluster itself.", key))
			continue
		}
		if len(key) > clusterLabelsMaximumPartSize || !clusterLabelsKeyPattern.MatchString(key) {
			response.Diagnostics.AddAttributeError(request.Path, "Invalid label key",
				fmt.Sprintf("Label key %q is not valid: use 1-%d lowercase letters, digits, '.', '_', '-' or '/', starting and ending with a letter or digit.",
					key, clusterLabelsMaximumPartSize))
		}
		value, isString := elements[key].(types.String)
		if !isString || value.IsUnknown() || value.IsNull() {
			continue
		}
		text := value.ValueString()
		if len(text) > clusterLabelsMaximumPartSize || !clusterLabelsValuePattern.MatchString(text) {
			response.Diagnostics.AddAttributeError(request.Path, "Invalid label value",
				fmt.Sprintf("Label value %q for %q is not valid: use 1-%d letters, digits, '.', '_' or '-', starting and ending with a letter or digit.",
					text, key, clusterLabelsMaximumPartSize))
		}
	}
}
