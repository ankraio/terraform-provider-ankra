// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"ankra.io/terraform-provider-ankra/internal/client"
)

var (
	_ resource.Resource                = (*pipelineRepositoryResource)(nil)
	_ resource.ResourceWithConfigure   = (*pipelineRepositoryResource)(nil)
	_ resource.ResourceWithImportState = (*pipelineRepositoryResource)(nil)
)

// pipelineRepositoryProviders are the source-code providers a repository can
// be connected from (enginekit/pipelinerun).
var pipelineRepositoryProviders = []string{"github", "gitlab", "bitbucket_cloud"}

type pipelineRepositoryResource struct {
	configuredClient
}

type pipelineRepositoryModel struct {
	ID             types.String `tfsdk:"id"`
	Provider       types.String `tfsdk:"provider_name"`
	Owner          types.String `tfsdk:"owner"`
	Name           types.String `tfsdk:"name"`
	CredentialName types.String `tfsdk:"credential_name"`
	DefaultBranch  types.String `tfsdk:"default_branch"`
	ApplicationID  types.String `tfsdk:"application_id"`
	ClusterID      types.String `tfsdk:"cluster_id"`
	WorkspacesOnly types.Bool   `tfsdk:"workspaces_only"`
}

// NewPipelineRepositoryResource returns the ankra_pipeline_repository
// resource, the counterpart of `ankra pipeline repositories connect`,
// `disconnect`, `set-cluster` and `set-mode`.
func NewPipelineRepositoryResource() resource.Resource {
	return &pipelineRepositoryResource{}
}

func (repositoryResource *pipelineRepositoryResource) Metadata(_ context.Context, request resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_pipeline_repository"
}

func (repositoryResource *pipelineRepositoryResource) Schema(_ context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	replace := []planmodifier.String{stringplanmodifier.RequiresReplace()}
	response.Schema = schema.Schema{
		MarkdownDescription: "A Git repository connected to Ankra Pipelines, so pushes, pull requests and tags run its committed " +
			"`.ankra/pipeline.yaml`; it needs no application. The counterpart of `ankra pipeline repositories connect` and `disconnect`, " +
			"with `set-cluster` and `set-mode` changing it in place. Disconnecting is refused while a run is queued or running.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Identifier of the connected repository.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"provider_name": schema.StringAttribute{
				MarkdownDescription: "Source-code provider: `github`, `gitlab` or `bitbucket_cloud`. Changing it replaces the connection.",
				Required:            true,
				PlanModifiers:       replace,
				Validators:          []validator.String{applicationStringOneOf{values: pipelineRepositoryProviders}},
			},
			"owner": schema.StringAttribute{
				MarkdownDescription: "Repository owner or organisation. Changing it replaces the connection.",
				Required:            true,
				PlanModifiers:       replace,
			},
			"name": schema.StringAttribute{
				MarkdownDescription: "Repository name. Changing it replaces the connection.",
				Required:            true,
				PlanModifiers:       replace,
			},
			"credential_name": schema.StringAttribute{
				MarkdownDescription: "Organisation credential the repository is reached through. Changing it replaces the connection.",
				Optional:            true,
				PlanModifiers:       replace,
			},
			"default_branch": schema.StringAttribute{
				MarkdownDescription: "Branch the pipeline file is read from. Defaults to `main`. Changing it replaces the connection.",
				Optional:            true,
				Computed:            true,
				Default:             stringdefault.StaticString("main"),
				PlanModifiers:       replace,
			},
			"application_id": schema.StringAttribute{
				MarkdownDescription: "Application to link the repository to. Changing it replaces the connection.",
				Optional:            true,
				PlanModifiers:       replace,
			},
			"cluster_id": schema.StringAttribute{
				MarkdownDescription: "Cluster the repository's runs go to, overriding the organisation's CI cluster. Changes in place; unset uses the organisation's.",
				Optional:            true,
			},
			"workspaces_only": schema.BoolAttribute{
				MarkdownDescription: "Connect the repository for AI workspaces only: Ankra records no pipeline and starts no runs for it. Defaults to `false`. Changes in place.",
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(false),
			},
		},
	}
}

func pipelineRepositoryToModel(repository *client.PipelineRepository, model *pipelineRepositoryModel) {
	model.ID = types.StringValue(repository.ID)
	model.Provider = types.StringValue(repository.Provider)
	model.Owner = types.StringValue(repository.Owner)
	model.Name = types.StringValue(repository.Name)
	model.CredentialName = optionalString(repository.CredentialName)
	model.DefaultBranch = types.StringValue(repository.DefaultBranch)
	model.ApplicationID = types.StringPointerValue(repository.ApplicationID)
	model.ClusterID = types.StringPointerValue(repository.ClusterID)
	model.WorkspacesOnly = types.BoolValue(repository.WorkspacesOnly)
}

func (repositoryResource *pipelineRepositoryResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	apiClient := repositoryResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var plan pipelineRepositoryModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}
	repository, err := apiClient.ConnectPipelineRepository(ctx, client.PipelineRepositoryConnect{
		Provider:       plan.Provider.ValueString(),
		Owner:          plan.Owner.ValueString(),
		Name:           plan.Name.ValueString(),
		CredentialName: plan.CredentialName.ValueString(),
		DefaultBranch:  plan.DefaultBranch.ValueString(),
		ApplicationID:  plan.ApplicationID.ValueString(),
		ClusterID:      plan.ClusterID.ValueString(),
		WorkspacesOnly: plan.WorkspacesOnly.ValueBool(),
	})
	if err != nil {
		response.Diagnostics.AddError("Unable to connect pipeline repository",
			err.Error()+"\n\nA repository the organisation already connected can be adopted with `terraform import`.")
		return
	}
	pipelineRepositoryToModel(repository, &plan)
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
}

func (repositoryResource *pipelineRepositoryResource) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	apiClient := repositoryResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state pipelineRepositoryModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	repository, err := apiClient.GetPipelineRepository(ctx, state.ID.ValueString())
	if removeWhenNotFound(ctx, err, response) {
		return
	}
	if err != nil {
		response.Diagnostics.AddError("Unable to read pipeline repository", err.Error())
		return
	}
	pipelineRepositoryToModel(repository, &state)
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
}

func (repositoryResource *pipelineRepositoryResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	apiClient := repositoryResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var plan, state pipelineRepositoryModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	repository, err := apiClient.UpdatePipelineRepository(ctx, state.ID.ValueString(), client.PipelineRepositoryUpdate{
		ClusterID:      stringPointer(plan.ClusterID),
		WorkspacesOnly: plan.WorkspacesOnly.ValueBool(),
	})
	if err != nil {
		response.Diagnostics.AddError("Unable to update pipeline repository", err.Error())
		return
	}
	pipelineRepositoryToModel(repository, &plan)
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
}

func (repositoryResource *pipelineRepositoryResource) Delete(ctx context.Context, request resource.DeleteRequest, response *resource.DeleteResponse) {
	apiClient := repositoryResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state pipelineRepositoryModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	if err := apiClient.DisconnectPipelineRepository(ctx, state.ID.ValueString()); err != nil {
		response.Diagnostics.AddError("Unable to disconnect pipeline repository", err.Error())
	}
}

// ImportState imports by repository id.
func (repositoryResource *pipelineRepositoryResource) ImportState(ctx context.Context, request resource.ImportStateRequest, response *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), request, response)
}
