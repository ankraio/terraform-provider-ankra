// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"ankra.io/terraform-provider-ankra/internal/client"
)

var (
	_ resource.Resource                = (*clusterCustomDNSZoneResource)(nil)
	_ resource.ResourceWithConfigure   = (*clusterCustomDNSZoneResource)(nil)
	_ resource.ResourceWithImportState = (*clusterCustomDNSZoneResource)(nil)
)

type clusterCustomDNSZoneResource struct {
	configuredClient
}

type clusterCustomDNSZoneModel struct {
	ID             types.String `tfsdk:"id"`
	ClusterID      types.String `tfsdk:"cluster_id"`
	Zone           types.String `tfsdk:"zone"`
	CredentialName types.String `tfsdk:"credential_name"`
	Provider       types.String `tfsdk:"credential_provider"`
	Policy         types.String `tfsdk:"policy"`
}

// NewClusterCustomDNSZoneResource returns the ankra_cluster_custom_dns_zone
// resource, the counterpart of `ankra cluster custom-dns-zones add|remove`.
func NewClusterCustomDNSZoneResource() resource.Resource {
	return &clusterCustomDNSZoneResource{}
}

func (zoneResource *clusterCustomDNSZoneResource) Metadata(_ context.Context, request resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_cluster_custom_dns_zone"
}

func (zoneResource *clusterCustomDNSZoneResource) Schema(_ context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: "A DNS zone a cluster also serves with one of the organisation's own DNS credentials (an external-dns " +
			"webhook, Avura or Cloudflare credential), alongside the generated domain Ankra serves. Ankra renders an external-dns " +
			"scoped to exactly this zone. The counterpart of `ankra cluster custom-dns-zones add` and `remove`. " +
			"Withdrawing the zone removes the controller; the zone's records are left untouched.",
		Attributes: map[string]schema.Attribute{
			"id":         clusterSettingsIDAttribute("`<cluster_id>/<zone>`."),
			"cluster_id": clusterSettingsClusterIDAttribute(),
			"zone": schema.StringAttribute{
				MarkdownDescription: "Zone to serve, e.g. `launch.example.com`. The platform stores it lower case without a trailing dot. Changing it replaces the resource.",
				Required:            true,
				Validators:          []validator.String{clusterSettingsNoSlashValidator{}},
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"credential_name": schema.StringAttribute{
				MarkdownDescription: "Name of the organisation DNS credential that publishes into the zone. Changing it re-points the zone in place.",
				Required:            true,
			},
			"credential_provider": schema.StringAttribute{
				MarkdownDescription: "Kind the credential must be: `cloudflare`, `webhook` or `avura`. Unset accepts whichever kind the credential is, " +
					"and the attribute then reports that kind.",
				Optional:   true,
				Computed:   true,
				Validators: []validator.String{clusterSettingsOneOf("cloudflare", "webhook", "avura")},
			},
			"policy": schema.StringAttribute{
				MarkdownDescription: "The external-dns policy the zone's controller runs: `upsert-only` for Cloudflare, `sync` otherwise.",
				Computed:            true,
			},
		},
	}
}

func (zoneResource *clusterCustomDNSZoneResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	zoneResource.declare(ctx, request.Plan, request.Config, &response.State, &response.Diagnostics)
}

func (zoneResource *clusterCustomDNSZoneResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	zoneResource.declare(ctx, request.Plan, request.Config, &response.State, &response.Diagnostics)
}

func (zoneResource *clusterCustomDNSZoneResource) declare(ctx context.Context, plan tfsdk.Plan, config tfsdk.Config, state *tfsdk.State, diagnostics *diag.Diagnostics) {
	apiClient := zoneResource.requireClient(diagnostics)
	if apiClient == nil {
		return
	}
	var model clusterCustomDNSZoneModel
	diagnostics.Append(plan.Get(ctx, &model)...)
	var configuredProvider types.String
	diagnostics.Append(config.GetAttribute(ctx, path.Root("credential_provider"), &configuredProvider)...)
	if diagnostics.HasError() {
		return
	}
	binding, err := apiClient.DeclareClusterCustomDNSZone(ctx, model.ClusterID.ValueString(), client.ClusterCustomDNSZoneDeclaration{
		Zone:           model.Zone.ValueString(),
		CredentialName: model.CredentialName.ValueString(),
		Provider:       stringPointer(configuredProvider),
	})
	if err != nil {
		diagnostics.AddError("Unable to declare cluster custom DNS zone", err.Error())
		return
	}
	applyClusterCustomDNSZone(&model, binding)
	diagnostics.Append(state.Set(ctx, &model)...)
}

func (zoneResource *clusterCustomDNSZoneResource) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	apiClient := zoneResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state clusterCustomDNSZoneModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	zones, err := apiClient.ListClusterCustomDNSZones(ctx, state.ClusterID.ValueString())
	if clusterSettingsReadNotFound(ctx, err, apiClient, state.ClusterID.ValueString(), response) {
		return
	}
	if err != nil {
		response.Diagnostics.AddError("Unable to read cluster custom DNS zone", err.Error())
		return
	}
	wanted := clusterSettingsNormaliseZone(state.Zone.ValueString())
	for index := range zones {
		isClusterBinding := zones[index].Source == "" || zones[index].Source == "cluster"
		if isClusterBinding && clusterSettingsNormaliseZone(zones[index].Zone) == wanted {
			applyClusterCustomDNSZone(&state, &zones[index])
			response.Diagnostics.Append(response.State.Set(ctx, &state)...)
			return
		}
	}
	response.State.RemoveResource(ctx)
}

func (zoneResource *clusterCustomDNSZoneResource) Delete(ctx context.Context, request resource.DeleteRequest, response *resource.DeleteResponse) {
	apiClient := zoneResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state clusterCustomDNSZoneModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	err := apiClient.RemoveClusterCustomDNSZone(ctx, state.ClusterID.ValueString(), clusterSettingsNormaliseZone(state.Zone.ValueString()))
	if deleteError := clusterSettingsDeleteError(err, state.ClusterID.ValueString()); deleteError != nil {
		response.Diagnostics.AddError("Unable to withdraw cluster custom DNS zone", deleteError.Error())
	}
}

// ImportState imports by "<cluster_id>/<zone>".
func (zoneResource *clusterCustomDNSZoneResource) ImportState(ctx context.Context, request resource.ImportStateRequest, response *resource.ImportStateResponse) {
	clusterSettingsImportComposite(ctx, request, response, "zone")
}

func applyClusterCustomDNSZone(model *clusterCustomDNSZoneModel, binding *client.ClusterCustomDNSZone) {
	if clusterSettingsNormaliseZone(model.Zone.ValueString()) != binding.Zone {
		model.Zone = types.StringValue(binding.Zone)
	}
	model.ID = types.StringValue(model.ClusterID.ValueString() + "/" + binding.Zone)
	model.CredentialName = types.StringValue(binding.CredentialName)
	model.Provider = types.StringPointerValue(binding.Provider)
	model.Policy = types.StringPointerValue(binding.Policy)
}
