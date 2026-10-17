// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ resource.Resource                = (*applicationAutoDeployResource)(nil)
	_ resource.ResourceWithConfigure   = (*applicationAutoDeployResource)(nil)
	_ resource.ResourceWithImportState = (*applicationAutoDeployResource)(nil)
)

type applicationAutoDeployResource struct {
	configuredClient
}

type applicationAutoDeployModel struct {
	ApplicationID types.String `tfsdk:"application_id"`
	Enabled       types.Bool   `tfsdk:"enabled"`
	Branch        types.String `tfsdk:"branch"`
}

// NewApplicationAutoDeployResource returns the ankra_application_auto_deploy
// resource, the counterpart of `ankra application auto-deploy set`.
func NewApplicationAutoDeployResource() resource.Resource {
	return &applicationAutoDeployResource{}
}

func (autoDeployResource *applicationAutoDeployResource) Metadata(_ context.Context, request resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_application_auto_deploy"
}

func (autoDeployResource *applicationAutoDeployResource) Schema(_ context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: "Push-to-deploy for an application: whether a successful build of the tracked branch redeploys every deployment of it. " +
			"The counterpart of `ankra application auto-deploy set`; it needs permission to deploy the application. " +
			"A newly registered application starts with it on. Destroying this resource leaves the switch as it is and only " +
			"removes it from Terraform state, because the platform has no single default to return an existing application to.",
		Attributes: map[string]schema.Attribute{
			"application_id": schema.StringAttribute{
				MarkdownDescription: "Identifier of the application. Changing it replaces the resource.",
				Required:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"enabled": schema.BoolAttribute{
				MarkdownDescription: "Whether builds of the tracked branch deploy without asking.",
				Required:            true,
			},
			"branch": schema.StringAttribute{
				MarkdownDescription: "Branch push-to-deploy watches.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
		},
	}
}

func (autoDeployResource *applicationAutoDeployResource) write(ctx context.Context, plan *applicationAutoDeployModel) error {
	autoDeploy, err := autoDeployResource.client.SetApplicationAutoDeploy(ctx, plan.ApplicationID.ValueString(), plan.Enabled.ValueBool())
	if err != nil {
		return err
	}
	plan.Enabled = types.BoolValue(autoDeploy.Enabled)
	plan.Branch = types.StringValue(autoDeploy.Branch)
	return nil
}

func (autoDeployResource *applicationAutoDeployResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	if autoDeployResource.requireClient(&response.Diagnostics) == nil {
		return
	}
	var plan applicationAutoDeployModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}
	if err := autoDeployResource.write(ctx, &plan); err != nil {
		response.Diagnostics.AddError("Unable to set application auto-deploy", err.Error())
		return
	}
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
}

func (autoDeployResource *applicationAutoDeployResource) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	apiClient := autoDeployResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state applicationAutoDeployModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	autoDeploy, err := apiClient.GetApplicationAutoDeploy(ctx, state.ApplicationID.ValueString())
	if removeWhenNotFound(ctx, err, response) {
		return
	}
	if err != nil {
		response.Diagnostics.AddError("Unable to read application auto-deploy", err.Error())
		return
	}
	state.Enabled = types.BoolValue(autoDeploy.Enabled)
	state.Branch = types.StringValue(autoDeploy.Branch)
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
}

func (autoDeployResource *applicationAutoDeployResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	if autoDeployResource.requireClient(&response.Diagnostics) == nil {
		return
	}
	var plan applicationAutoDeployModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}
	if err := autoDeployResource.write(ctx, &plan); err != nil {
		response.Diagnostics.AddError("Unable to update application auto-deploy", err.Error())
		return
	}
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
}

// Delete only forgets the resource: see the schema description.
func (autoDeployResource *applicationAutoDeployResource) Delete(_ context.Context, _ resource.DeleteRequest, response *resource.DeleteResponse) {
	autoDeployResource.requireClient(&response.Diagnostics)
}

// ImportState imports by application id.
func (autoDeployResource *applicationAutoDeployResource) ImportState(ctx context.Context, request resource.ImportStateRequest, response *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("application_id"), request, response)
}
