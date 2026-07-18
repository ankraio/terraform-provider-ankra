// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"ankra.io/terraform-provider-ankra/internal/client"
)

const (
	tokenDeprecationMessage = "Configure the token on the provider block (or the ANKRA_TOKEN environment variable) instead of per resource."
	missingTokenDetail      = "Set an API token on the provider block, the ANKRA_TOKEN environment variable, or the deprecated per-resource ankra_token attribute."
)

var (
	_ resource.Resource                = (*clusterResource)(nil)
	_ resource.ResourceWithConfigure   = (*clusterResource)(nil)
	_ resource.ResourceWithImportState = (*clusterResource)(nil)
)

type clusterResource struct {
	client *client.Client
}

type manifestModel struct {
	Name           types.String   `tfsdk:"name"`
	Namespace      types.String   `tfsdk:"namespace"`
	ManifestBase64 types.String   `tfsdk:"manifest_base64"`
	Parents        []types.String `tfsdk:"parents"`
	FromFile       types.String   `tfsdk:"from_file"`
}

type addonModel struct {
	Name              types.String   `tfsdk:"name"`
	ChartName         types.String   `tfsdk:"chart_name"`
	ChartVersion      types.String   `tfsdk:"chart_version"`
	RepositoryURL     types.String   `tfsdk:"repository_url"`
	Namespace         types.String   `tfsdk:"namespace"`
	ConfigurationType types.String   `tfsdk:"configuration_type"`
	Configuration     types.String   `tfsdk:"configuration"`
	Parents           []types.String `tfsdk:"parents"`
	JobConfiguration  types.String   `tfsdk:"job_configuration"`
}

type stackModel struct {
	Name        types.String    `tfsdk:"name"`
	Description types.String    `tfsdk:"description"`
	Manifests   []manifestModel `tfsdk:"manifests"`
	Addons      []addonModel    `tfsdk:"addons"`
}

type clusterResourceModel struct {
	ID                   types.String `tfsdk:"id"`
	ClusterName          types.String `tfsdk:"cluster_name"`
	GithubCredentialName types.String `tfsdk:"github_credential_name"`
	GithubBranch         types.String `tfsdk:"github_branch"`
	GithubRepository     types.String `tfsdk:"github_repository"`
	AnkraToken           types.String `tfsdk:"ankra_token"`
	Stacks               []stackModel `tfsdk:"stacks"`
	ClusterID            types.String `tfsdk:"cluster_id"`
	HelmCommand          types.String `tfsdk:"helm_command"`
}

// NewClusterResource returns a new ankra_cluster resource.
func NewClusterResource() resource.Resource {
	return &clusterResource{}
}

func (clusterResourceInstance *clusterResource) Metadata(_ context.Context, request resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_cluster"
}

func (clusterResourceInstance *clusterResource) Schema(_ context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: "Imports and manages a cluster on the Ankra platform.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Identifier of the cluster (mirrors `cluster_id`).",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"cluster_name": schema.StringAttribute{
				MarkdownDescription: "Name of the cluster. Changing this forces a new resource.",
				Required:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"github_credential_name": schema.StringAttribute{
				MarkdownDescription: "Name of the stored GitHub credential used to access the repository.",
				Required:            true,
			},
			"github_branch": schema.StringAttribute{
				MarkdownDescription: "Git branch that holds the cluster configuration.",
				Required:            true,
			},
			"github_repository": schema.StringAttribute{
				MarkdownDescription: "GitHub repository (`owner/name`) that holds the cluster configuration.",
				Required:            true,
			},
			"ankra_token": schema.StringAttribute{
				MarkdownDescription: "Deprecated per-resource API token. " + tokenDeprecationMessage,
				Optional:            true,
				Sensitive:           true,
				DeprecationMessage:  tokenDeprecationMessage,
			},
			"cluster_id": schema.StringAttribute{
				MarkdownDescription: "Identifier assigned to the cluster by the Ankra platform.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"helm_command": schema.StringAttribute{
				MarkdownDescription: "Helm command emitted by the platform to bootstrap the cluster agent.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
		},
		Blocks: map[string]schema.Block{
			"stacks": schema.ListNestedBlock{
				MarkdownDescription: "Stacks of manifests and addons to apply to the cluster.",
				NestedObject: schema.NestedBlockObject{
					Attributes: map[string]schema.Attribute{
						"name": schema.StringAttribute{
							MarkdownDescription: "Name of the stack.",
							Required:            true,
						},
						"description": schema.StringAttribute{
							MarkdownDescription: "Description of the stack.",
							Optional:            true,
						},
					},
					Blocks: map[string]schema.Block{
						"manifests": schema.ListNestedBlock{
							MarkdownDescription: "Raw Kubernetes manifests deployed as part of the stack.",
							NestedObject: schema.NestedBlockObject{
								Attributes: map[string]schema.Attribute{
									"name": schema.StringAttribute{
										MarkdownDescription: "Name of the manifest.",
										Required:            true,
									},
									"namespace": schema.StringAttribute{
										MarkdownDescription: "Namespace the manifest is applied to.",
										Optional:            true,
									},
									"manifest_base64": schema.StringAttribute{
										MarkdownDescription: "Base64-encoded manifest content.",
										Required:            true,
									},
									"parents": schema.ListAttribute{
										MarkdownDescription: "Names of resources this manifest depends on.",
										Optional:            true,
										ElementType:         types.StringType,
									},
									"from_file": schema.StringAttribute{
										MarkdownDescription: "Source file the manifest was generated from.",
										Optional:            true,
									},
								},
							},
						},
						"addons": schema.ListNestedBlock{
							MarkdownDescription: "Helm-chart addons deployed as part of the stack.",
							NestedObject: schema.NestedBlockObject{
								Attributes: map[string]schema.Attribute{
									"name": schema.StringAttribute{
										MarkdownDescription: "Name of the addon.",
										Required:            true,
									},
									"chart_name": schema.StringAttribute{
										MarkdownDescription: "Helm chart name.",
										Required:            true,
									},
									"chart_version": schema.StringAttribute{
										MarkdownDescription: "Helm chart version.",
										Required:            true,
									},
									"repository_url": schema.StringAttribute{
										MarkdownDescription: "Helm chart repository URL.",
										Required:            true,
									},
									"namespace": schema.StringAttribute{
										MarkdownDescription: "Namespace the addon is installed into.",
										Required:            true,
									},
									"configuration_type": schema.StringAttribute{
										MarkdownDescription: "Type of the supplied configuration.",
										Optional:            true,
									},
									"configuration": schema.StringAttribute{
										MarkdownDescription: "Addon configuration payload.",
										Optional:            true,
									},
									"parents": schema.ListAttribute{
										MarkdownDescription: "Names of resources this addon depends on.",
										Optional:            true,
										ElementType:         types.StringType,
									},
									"job_configuration": schema.StringAttribute{
										MarkdownDescription: "Job configuration payload for the addon.",
										Optional:            true,
									},
								},
							},
						},
					},
				},
			},
		},
	}
}

func (clusterResourceInstance *clusterResource) Configure(_ context.Context, request resource.ConfigureRequest, response *resource.ConfigureResponse) {
	if request.ProviderData == nil {
		return
	}
	apiClient, ok := request.ProviderData.(*client.Client)
	if !ok {
		response.Diagnostics.AddError(
			"Unexpected provider data type",
			fmt.Sprintf("Expected *client.Client, got %T. This is a bug in the provider.", request.ProviderData),
		)
		return
	}
	clusterResourceInstance.client = apiClient
}

func (clusterResourceInstance *clusterResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	var plan clusterResourceModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}
	clusterResourceInstance.importCluster(ctx, &plan, &response.Diagnostics)
	if response.Diagnostics.HasError() {
		return
	}
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
}

func (clusterResourceInstance *clusterResource) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	var state clusterResourceModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}

	apiClient := clusterResourceInstance.clientForToken(state.AnkraToken)
	if apiClient.Token == "" {
		return
	}

	clusterID := state.ClusterID.ValueString()
	if clusterID == "" {
		clusterID = state.ID.ValueString()
	}

	cluster, err := apiClient.GetClusterByID(ctx, clusterID)
	if err != nil {
		response.Diagnostics.AddError("Unable to read cluster", err.Error())
		return
	}
	if cluster == nil {
		response.State.RemoveResource(ctx)
		return
	}

	state.ID = types.StringValue(cluster.ID)
	state.ClusterID = types.StringValue(cluster.ID)
	if cluster.Name != "" {
		state.ClusterName = types.StringValue(cluster.Name)
	}
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
}

func (clusterResourceInstance *clusterResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	var plan clusterResourceModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}
	clusterResourceInstance.importCluster(ctx, &plan, &response.Diagnostics)
	if response.Diagnostics.HasError() {
		return
	}
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
}

func (clusterResourceInstance *clusterResource) Delete(ctx context.Context, request resource.DeleteRequest, response *resource.DeleteResponse) {
	var state clusterResourceModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}

	clusterName := state.ClusterName.ValueString()
	if clusterName == "" {
		return
	}

	apiClient := clusterResourceInstance.clientForToken(state.AnkraToken)
	if apiClient.Token == "" {
		response.Diagnostics.AddError("Missing API token", missingTokenDetail)
		return
	}
	if err := apiClient.DeleteCluster(ctx, clusterName); err != nil {
		response.Diagnostics.AddError("Unable to delete cluster", err.Error())
	}
}

func (clusterResourceInstance *clusterResource) ImportState(ctx context.Context, request resource.ImportStateRequest, response *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("cluster_id"), request, response)
}

// importCluster builds the import payload from the plan, calls the API, and
// writes the computed attributes back into the plan.
func (clusterResourceInstance *clusterResource) importCluster(ctx context.Context, plan *clusterResourceModel, diagnostics *diag.Diagnostics) {
	apiClient := clusterResourceInstance.clientForToken(plan.AnkraToken)
	if apiClient.Token == "" {
		diagnostics.AddError("Missing API token", missingTokenDetail)
		return
	}

	request := client.ImportClusterRequest{
		Name:        plan.ClusterName.ValueString(),
		Description: "Managed by Terraform",
		Spec: client.ImportClusterSpec{
			GitRepository: client.GitRepository{
				Provider:       "github",
				CredentialName: plan.GithubCredentialName.ValueString(),
				Branch:         plan.GithubBranch.ValueString(),
				Repository:     plan.GithubRepository.ValueString(),
			},
			Stacks: stacksToAPI(plan.Stacks),
		},
	}

	response, err := apiClient.ImportCluster(ctx, request)
	if err != nil {
		diagnostics.AddError("Unable to import cluster", err.Error())
		return
	}
	if response.ClusterID == "" {
		diagnostics.AddError("Invalid import response", "The Ankra platform did not return a cluster_id.")
		return
	}

	plan.ID = types.StringValue(response.ClusterID)
	plan.ClusterID = types.StringValue(response.ClusterID)
	plan.HelmCommand = types.StringValue(response.ImportCommand)
}

// clientForToken returns the configured client, or a copy overridden with the
// deprecated per-resource token when one is supplied.
func (clusterResourceInstance *clusterResource) clientForToken(token types.String) *client.Client {
	if token.IsNull() || token.IsUnknown() || token.ValueString() == "" {
		return clusterResourceInstance.client
	}
	override := *clusterResourceInstance.client
	override.Token = token.ValueString()
	return &override
}

func stacksToAPI(stacks []stackModel) []client.Stack {
	result := make([]client.Stack, 0, len(stacks))
	for _, stack := range stacks {
		result = append(result, client.Stack{
			Name:        stack.Name.ValueString(),
			Description: stack.Description.ValueString(),
			Manifests:   manifestsToAPI(stack.Manifests),
			Addons:      addonsToAPI(stack.Addons),
		})
	}
	return result
}

func manifestsToAPI(manifests []manifestModel) []client.Manifest {
	result := make([]client.Manifest, 0, len(manifests))
	for _, manifest := range manifests {
		result = append(result, client.Manifest{
			Name:           manifest.Name.ValueString(),
			Namespace:      manifest.Namespace.ValueString(),
			ManifestBase64: manifest.ManifestBase64.ValueString(),
			Parents:        stringsToAPI(manifest.Parents),
			FromFile:       manifest.FromFile.ValueString(),
		})
	}
	return result
}

func addonsToAPI(addons []addonModel) []client.Addon {
	result := make([]client.Addon, 0, len(addons))
	for _, addon := range addons {
		result = append(result, client.Addon{
			Name:              addon.Name.ValueString(),
			ChartName:         addon.ChartName.ValueString(),
			ChartVersion:      addon.ChartVersion.ValueString(),
			RepositoryURL:     addon.RepositoryURL.ValueString(),
			Namespace:         addon.Namespace.ValueString(),
			ConfigurationType: addon.ConfigurationType.ValueString(),
			Configuration:     addon.Configuration.ValueString(),
			Parents:           stringsToAPI(addon.Parents),
			JobConfiguration:  addon.JobConfiguration.ValueString(),
		})
	}
	return result
}

func stringsToAPI(values []types.String) []string {
	if len(values) == 0 {
		return nil
	}
	result := make([]string, 0, len(values))
	for _, value := range values {
		result = append(result, value.ValueString())
	}
	return result
}
