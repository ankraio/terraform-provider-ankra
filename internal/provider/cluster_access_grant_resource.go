// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"ankra.io/terraform-provider-ankra/internal/client"
)

var (
	_ resource.Resource                = (*clusterAccessGrantResource)(nil)
	_ resource.ResourceWithConfigure   = (*clusterAccessGrantResource)(nil)
	_ resource.ResourceWithImportState = (*clusterAccessGrantResource)(nil)
)

type clusterAccessGrantResource struct {
	configuredClient
}

type clusterAccessGrantModel struct {
	ID              types.String `tfsdk:"id"`
	ClusterID       types.String `tfsdk:"cluster_id"`
	UserEmail       types.String `tfsdk:"user_email"`
	Scope           types.String `tfsdk:"scope"`
	Namespace       types.String `tfsdk:"namespace"`
	Role            types.String `tfsdk:"role"`
	ExpiresAt       types.String `tfsdk:"expires_at"`
	Reason          types.String `tfsdk:"reason"`
	AnkraUserID     types.String `tfsdk:"ankra_user_id"`
	ReconcileStatus types.String `tfsdk:"reconcile_status"`
	Source          types.String `tfsdk:"source"`
}

// NewClusterAccessGrantResource returns the ankra_cluster_access_grant
// resource, the counterpart of `ankra cluster access grant` and `revoke`.
func NewClusterAccessGrantResource() resource.Resource {
	return &clusterAccessGrantResource{}
}

func (grantResource *clusterAccessGrantResource) Metadata(_ context.Context, request resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_cluster_access_grant"
}

// clusterAccessGrantReplaceWhenStanding replaces the grant when the planned grant has no
// expiry: the platform re-times a matching grant in place only when the
// request carries an expiry, and answers a conflict otherwise.
func clusterAccessGrantReplaceWhenStanding(ctx context.Context, request planmodifier.StringRequest, response *stringplanmodifier.RequiresReplaceIfFuncResponse) {
	var expiresAt types.String
	response.Diagnostics.Append(request.Plan.GetAttribute(ctx, path.Root("expires_at"), &expiresAt)...)
	response.RequiresReplace = expiresAt.IsNull()
}

func (grantResource *clusterAccessGrantResource) Schema(_ context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	retimeDescription := "A change is applied in place while `expires_at` is set (the platform re-times the grant); otherwise it replaces the grant."
	response.Schema = schema.Schema{
		MarkdownDescription: "Kubernetes access to a cluster through the Ankra gateway for one organisation member. " +
			"The counterpart of `ankra cluster access grant` and `ankra cluster access revoke`.\n\n" +
			"Once a grant's expiry has passed the platform retires it, and the next plan proposes creating it again; " +
			"move `expires_at` forward or remove the resource.",
		Attributes: map[string]schema.Attribute{
			"id":         clusterSettingsIDAttribute("Identifier of the grant."),
			"cluster_id": clusterSettingsClusterIDAttribute(),
			"user_email": schema.StringAttribute{
				MarkdownDescription: "Email of the organisation member the grant is for. Changing it replaces the grant.",
				Required:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"scope": schema.StringAttribute{
				MarkdownDescription: "`cluster` or `namespace`. Defaults to `cluster`. Changing it replaces the grant.",
				Optional:            true,
				Computed:            true,
				Default:             stringdefault.StaticString("cluster"),
				Validators:          []validator.String{clusterSettingsOneOf("cluster", "namespace")},
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"namespace": schema.StringAttribute{
				MarkdownDescription: "Namespace the grant is bound to; required when `scope` is `namespace` and refused otherwise. Changing it replaces the grant.",
				Optional:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"role": schema.StringAttribute{
				MarkdownDescription: "`view`, `edit`, `admin` or `cluster-admin`. Changing it replaces the grant.",
				Required:            true,
				Validators:          []validator.String{clusterSettingsOneOf("view", "edit", "admin", "cluster-admin")},
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"expires_at": schema.StringAttribute{
				MarkdownDescription: "RFC 3339 time the grant ends. Unset is a standing grant. " + retimeDescription,
				Optional:            true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplaceIf(clusterAccessGrantReplaceWhenStanding,
					"Removing the expiry replaces the grant.", "Removing the expiry replaces the grant.")},
			},
			"reason": schema.StringAttribute{
				MarkdownDescription: "Justification recorded with the grant; the organisation access policy may require one. " + retimeDescription,
				Optional:            true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplaceIf(clusterAccessGrantReplaceWhenStanding,
					"Changing the reason of a standing grant replaces it.", "Changing the reason of a standing grant replaces it.")},
			},
			"ankra_user_id": schema.StringAttribute{
				MarkdownDescription: "Identifier of the member the grant is for.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"reconcile_status": schema.StringAttribute{
				MarkdownDescription: "Whether the grant's binding has been applied on the cluster.",
				Computed:            true,
			},
			"source": schema.StringAttribute{
				MarkdownDescription: "Where the grant came from: `manual`, `creator`, `role` or `elevated`; null when the platform cannot tell.",
				Computed:            true,
			},
		},
	}
}

func (grantResource *clusterAccessGrantResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	apiClient := grantResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var plan clusterAccessGrantModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}
	grant, err := apiClient.CreateClusterAccessGrant(ctx, plan.ClusterID.ValueString(), clusterAccessGrantRequest(plan))
	if err != nil {
		response.Diagnostics.AddError("Unable to create cluster access grant", err.Error())
		return
	}
	applyClusterAccessGrant(&plan, grant)
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
}

func (grantResource *clusterAccessGrantResource) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	apiClient := grantResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state clusterAccessGrantModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	grant, err := apiClient.GetClusterAccessGrant(ctx, state.ClusterID.ValueString(), state.ID.ValueString())
	if removeWhenNotFound(ctx, err, response) {
		return
	}
	if err != nil {
		response.Diagnostics.AddError("Unable to read cluster access grant", err.Error())
		return
	}
	if clusterAccessGrantExpired(grant) {
		response.State.RemoveResource(ctx)
		return
	}
	applyClusterAccessGrant(&state, grant)
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
}

// Update re-times the grant: the plan modifiers only allow an in-place
// change of expires_at or reason while an expiry is set, and posting the
// matching grant with an expiry replaces its expiry and reason.
func (grantResource *clusterAccessGrantResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	apiClient := grantResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var plan clusterAccessGrantModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}
	grant, err := apiClient.CreateClusterAccessGrant(ctx, plan.ClusterID.ValueString(), clusterAccessGrantRequest(plan))
	if err != nil {
		response.Diagnostics.AddError("Unable to re-time cluster access grant", err.Error())
		return
	}
	applyClusterAccessGrant(&plan, grant)
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
}

func (grantResource *clusterAccessGrantResource) Delete(ctx context.Context, request resource.DeleteRequest, response *resource.DeleteResponse) {
	apiClient := grantResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state clusterAccessGrantModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	if err := apiClient.DeleteClusterAccessGrant(ctx, state.ClusterID.ValueString(), state.ID.ValueString()); err != nil {
		response.Diagnostics.AddError("Unable to revoke cluster access grant", err.Error())
	}
}

// ImportState imports by "<cluster_id>/<grant_id>".
func (grantResource *clusterAccessGrantResource) ImportState(ctx context.Context, request resource.ImportStateRequest, response *resource.ImportStateResponse) {
	clusterSettingsImportComposite(ctx, request, response, "id")
}

func clusterAccessGrantRequest(plan clusterAccessGrantModel) client.ClusterAccessGrantCreate {
	return client.ClusterAccessGrantCreate{
		UserEmail: plan.UserEmail.ValueString(),
		Scope:     plan.Scope.ValueString(),
		Namespace: stringPointer(plan.Namespace),
		Role:      plan.Role.ValueString(),
		ExpiresAt: stringPointer(plan.ExpiresAt),
		Reason:    stringPointer(plan.Reason),
	}
}

func applyClusterAccessGrant(model *clusterAccessGrantModel, grant *client.ClusterAccessGrant) {
	model.ID = types.StringValue(grant.ID)
	if grant.ClusterID != "" {
		model.ClusterID = types.StringValue(grant.ClusterID)
	}
	if grant.UserEmail != nil && !strings.EqualFold(*grant.UserEmail, model.UserEmail.ValueString()) {
		model.UserEmail = types.StringValue(*grant.UserEmail)
	}
	model.Scope = types.StringValue(grant.Scope)
	model.Namespace = types.StringPointerValue(grant.Namespace)
	model.Role = types.StringValue(grant.Role)
	model.ExpiresAt = clusterSettingsTimestamp(model.ExpiresAt, grant.ExpiresAt)
	model.Reason = types.StringPointerValue(grant.Reason)
	model.AnkraUserID = types.StringValue(grant.AnkraUserID)
	model.ReconcileStatus = types.StringValue(grant.ReconcileStatus)
	model.Source = types.StringPointerValue(grant.Source)
}

func clusterAccessGrantExpired(grant *client.ClusterAccessGrant) bool {
	if grant.ExpiresAt == nil {
		return false
	}
	expiresAt, err := time.Parse(time.RFC3339, *grant.ExpiresAt)
	return err == nil && !expiresAt.After(time.Now())
}
