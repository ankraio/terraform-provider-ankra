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
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"ankra.io/terraform-provider-ankra/internal/client"
)

var (
	_ resource.Resource                = (*registryProjectResource)(nil)
	_ resource.ResourceWithConfigure   = (*registryProjectResource)(nil)
	_ resource.ResourceWithImportState = (*registryProjectResource)(nil)
)

type registryProjectResource struct {
	configuredClient
}

type registryProjectModel struct {
	ID        types.String `tfsdk:"id"`
	Name      types.String `tfsdk:"name"`
	Project   types.String `tfsdk:"project"`
	Host      types.String `tfsdk:"host"`
	CreatedAt types.String `tfsdk:"created_at"`
}

// NewRegistryProjectResource returns the ankra_registry_project resource,
// the counterpart of `ankra registry projects create|delete`.
func NewRegistryProjectResource() resource.Resource {
	return &registryProjectResource{}
}

func (projectResource *registryProjectResource) Metadata(_ context.Context, request resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_registry_project"
}

func (projectResource *registryProjectResource) Schema(_ context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: "An extra project on the Ankra image registry. A robot account is bound to one project, so a project is " +
			"how a login is limited to part of what the organisation publishes. The counterpart of `ankra registry projects create` " +
			"and `ankra registry projects delete`. The platform refuses to delete a project that still holds repositories or has " +
			"robots bound to it.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "The project name.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				MarkdownDescription: "Name the project is addressed by. Changing it replaces the project.",
				Required:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
				Validators:          []validator.String{catalogueRegistryProjectNameValidator},
			},
			"project": schema.StringAttribute{
				MarkdownDescription: "The project's name on the registry: the path segment images are pushed under.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"host": schema.StringAttribute{
				MarkdownDescription: "Registry host images are pushed to.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"created_at": schema.StringAttribute{
				MarkdownDescription: "When the project was created. A project of the same name created later is a different " +
					"project: Terraform then treats this one as gone instead of adopting it.",
				Computed:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
		},
	}
}

func (projectResource *registryProjectResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	apiClient := projectResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var plan registryProjectModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}

	created, err := apiClient.CreateRegistryProject(ctx, plan.Name.ValueString())
	if err != nil {
		response.Diagnostics.AddError("Unable to create registry project", err.Error())
		return
	}
	registryProjectToState(created, &plan)
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
	response.Diagnostics.Append(trackScope(ctx, apiClient, response.Private)...)
}

func (projectResource *registryProjectResource) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	apiClient := projectResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state registryProjectModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}

	project, err := apiClient.GetRegistryProject(ctx, state.Name.ValueString())
	if removeWhenNotFound(ctx, err, apiClient, response) {
		return
	}
	if err != nil {
		response.Diagnostics.AddError("Unable to read registry project", err.Error())
		return
	}
	if readTargetChanged(state.CreatedAt, project.CreatedAt) {
		removeWhenTargetChanged(ctx, apiClient, response)
		return
	}
	registryProjectToState(project, &state)
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
	response.Diagnostics.Append(trackScope(ctx, apiClient, response.Private)...)
}

// Update is never called with a change: every configurable attribute
// replaces the project.
func (projectResource *registryProjectResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	var plan registryProjectModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
}

func (projectResource *registryProjectResource) Delete(ctx context.Context, request resource.DeleteRequest, response *resource.DeleteResponse) {
	apiClient := projectResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state registryProjectModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	if err := apiClient.DeleteRegistryProject(ctx, state.Name.ValueString()); err != nil {
		response.Diagnostics.AddError("Unable to delete registry project", err.Error())
	}
}

// ImportState imports by project name.
func (projectResource *registryProjectResource) ImportState(ctx context.Context, request resource.ImportStateRequest, response *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("name"), request, response)
}

func registryProjectToState(project *client.RegistryProject, state *registryProjectModel) {
	state.ID = types.StringValue(project.Name)
	state.Name = types.StringValue(project.Name)
	state.Project = types.StringValue(project.Project)
	state.Host = types.StringValue(project.Host)
	state.CreatedAt = types.StringValue(project.CreatedAt)
}
