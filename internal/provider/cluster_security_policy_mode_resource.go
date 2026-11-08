// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"ankra.io/terraform-provider-ankra/internal/client"
)

var clusterSecurityPolicyModes = []string{"audit", "enforce", "off"}

var (
	_ resource.Resource                = (*clusterSecurityPolicyModeResource)(nil)
	_ resource.ResourceWithConfigure   = (*clusterSecurityPolicyModeResource)(nil)
	_ resource.ResourceWithImportState = (*clusterSecurityPolicyModeResource)(nil)
)

type clusterSecurityPolicyModeResource struct {
	configuredClient
}

type clusterSecurityPolicyModeModel struct {
	ID                    types.String `tfsdk:"id"`
	ClusterID             types.String `tfsdk:"cluster_id"`
	Mode                  types.String `tfsdk:"mode"`
	PolicyMode            types.String `tfsdk:"policy_mode"`
	PolicyEngineInstalled types.Bool   `tfsdk:"policy_engine_installed"`
	CommitSHA             types.String `tfsdk:"commit_sha"`
}

// NewClusterSecurityPolicyModeResource returns the
// ankra_cluster_security_policy_mode resource, the counterpart of
// `ankra security policy-mode`.
func NewClusterSecurityPolicyModeResource() resource.Resource {
	return &clusterSecurityPolicyModeResource{}
}

func (policyMode *clusterSecurityPolicyModeResource) Metadata(_ context.Context, request resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_cluster_security_policy_mode"
}

func (policyMode *clusterSecurityPolicyModeResource) Schema(_ context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: "The mode of a cluster's security baseline (`ankra security policy-mode`): `audit` observes and reports, " +
			"`enforce` blocks non-compliant admissions, and `off` uninstalls the Kyverno policy engine and leaves native pod security " +
			"observing. Switching back to `audit` or `enforce` does not reinstall Kyverno. The change goes through the cluster's stack " +
			"update and, when the cluster has a GitOps repository, is committed there. The cluster must carry the security baseline; " +
			"one without it answers 404.\n\n" +
			"`mode` reads back as the mode last asked for, so a change made with the CLI or the portal shows as drift; `policy_mode` is " +
			"what the cluster stores now. Destroy leaves the mode as it is.",
		Attributes: map[string]schema.Attribute{
			"id":         clusterSettingsIDAttribute("The cluster id."),
			"cluster_id": clusterSettingsClusterIDAttribute(),
			"mode": schema.StringAttribute{
				MarkdownDescription: "`audit`, `enforce` or `off`.",
				Required:            true,
				Validators:          []validator.String{organisationStringOneOf{values: clusterSecurityPolicyModes}},
			},
			"policy_mode": schema.StringAttribute{
				MarkdownDescription: "The mode the cluster stores now: `enforce`, `audit`, or `off` when no policy engine is installed " +
					"(also after `audit` was asked for on a cluster whose engine was retired).",
				Computed: true,
			},
			"policy_engine_installed": schema.BoolAttribute{
				MarkdownDescription: "Whether a Kyverno or kyverno-policies add-on is installed on the cluster.",
				Computed:            true,
			},
			"commit_sha": schema.StringAttribute{
				MarkdownDescription: "The GitOps commit that carried the last change made by this resource; null when the cluster has no GitOps repository.",
				Computed:            true,
			},
		},
	}
}

func (policyMode *clusterSecurityPolicyModeResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	var plan clusterSecurityPolicyModeModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if policyMode.set(ctx, &plan, &response.Diagnostics) {
		response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
		response.Diagnostics.Append(trackScope(ctx, policyMode.client, response.Private)...)
	}
}

func (policyMode *clusterSecurityPolicyModeResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	var plan clusterSecurityPolicyModeModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if policyMode.set(ctx, &plan, &response.Diagnostics) {
		response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
	}
}

func (policyMode *clusterSecurityPolicyModeResource) set(ctx context.Context, plan *clusterSecurityPolicyModeModel, diagnostics *diag.Diagnostics) bool {
	apiClient := policyMode.requireClient(diagnostics)
	if apiClient == nil || diagnostics.HasError() {
		return false
	}
	clusterID := plan.ClusterID.ValueString()
	change, setError := apiClient.SetClusterSecurityPolicyMode(ctx, clusterID, plan.Mode.ValueString())
	if setError != nil {
		diagnostics.AddError("Unable to set the security policy mode", clusterSettingsErrorText(setError, clusterID))
		return false
	}
	view, readError := apiClient.GetClusterSecurityPolicyMode(ctx, clusterID)
	if readError != nil {
		diagnostics.AddError("Unable to read the security policy mode back", clusterSettingsErrorText(readError, clusterID))
		return false
	}
	plan.ID = plan.ClusterID
	plan.CommitSHA = types.StringPointerValue(change.CommitSHA)
	clusterSecurityPolicyModeApply(plan, view)
	return true
}

func (policyMode *clusterSecurityPolicyModeResource) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	apiClient := policyMode.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state clusterSecurityPolicyModeModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	view, readError := apiClient.GetClusterSecurityPolicyMode(ctx, state.ClusterID.ValueString())
	if clusterSettingsReadNotFound(ctx, readError, apiClient, state.ClusterID.ValueString(), response) {
		return
	}
	if readError != nil {
		response.Diagnostics.AddError("Unable to read the security policy mode", readError.Error())
		return
	}
	if view.PolicyMode == nil && view.RequestedMode == nil {
		response.State.RemoveResource(ctx)
		return
	}
	state.ID = state.ClusterID
	switch {
	case view.RequestedMode != nil:
		state.Mode = types.StringValue(*view.RequestedMode)
	default:
		state.Mode = types.StringPointerValue(view.PolicyMode)
	}
	clusterSecurityPolicyModeApply(&state, view)
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
	response.Diagnostics.Append(trackScope(ctx, apiClient, response.Private)...)
}

// Delete leaves the mode as it is: there is no platform default to return
// to, and switching a cluster to audit or off would loosen it.
func (policyMode *clusterSecurityPolicyModeResource) Delete(_ context.Context, _ resource.DeleteRequest, _ *resource.DeleteResponse) {
}

// ImportState imports by cluster id.
func (policyMode *clusterSecurityPolicyModeResource) ImportState(ctx context.Context, request resource.ImportStateRequest, response *resource.ImportStateResponse) {
	clusterSettingsImportClusterID(ctx, request, response)
}

func clusterSecurityPolicyModeApply(model *clusterSecurityPolicyModeModel, view *client.ClusterSecurityPolicyMode) {
	model.PolicyMode = types.StringPointerValue(view.PolicyMode)
	model.PolicyEngineInstalled = types.BoolValue(view.PolicyEngineInstalled)
	if model.CommitSHA.IsUnknown() {
		model.CommitSHA = types.StringNull()
	}
}
