// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/hashicorp/terraform-plugin-framework-timeouts/resource/timeouts"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"ankra.io/terraform-provider-ankra/internal/client"
)

const (
	tokenDeprecationMessage = "Configure the token on the provider block (or the ANKRA_TOKEN environment variable) instead of per resource."
	missingTokenDetail      = "Set an API token on the provider block, the ANKRA_TOKEN environment variable, or the deprecated per-resource ankra_token attribute."
)

// errMissingToken signals that no usable API token reached the resource. Its
// text is never surfaced: callers pair it with missingTokenDetail, which is
// the sentence the operator actually reads in the diagnostic.
var errMissingToken = errors.New("no ankra api token configured")

var (
	_ resource.Resource                = (*clusterResource)(nil)
	_ resource.ResourceWithConfigure   = (*clusterResource)(nil)
	_ resource.ResourceWithImportState = (*clusterResource)(nil)

	_ resource.ResourceWithValidateConfig = (*clusterResource)(nil)
)

// defaultUpdateTimeout bounds a re-import. The platform commits the spec and
// pushes it to the GitOps repository before it answers.
const defaultUpdateTimeout = 20 * time.Minute

// parentsDescription documents the one parent syntax both member kinds share.
const parentsDescription = "Stack members that must deploy before this one. Write each as " +
	"`\"manifest:<name>\"` or `\"addon:<name>\"`; a bare `\"<name>\"` is accepted when exactly one " +
	"manifest or addon of that name is declared in this resource's stacks, and its kind is inferred. " +
	"Leaving `parents` unset keeps the dependencies stored on the platform; an empty list removes them."

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
	Name                   types.String   `tfsdk:"name"`
	ChartName              types.String   `tfsdk:"chart_name"`
	ChartVersion           types.String   `tfsdk:"chart_version"`
	RegistryName           types.String   `tfsdk:"registry_name"`
	RegistryURL            types.String   `tfsdk:"registry_url"`
	RegistryCredentialName types.String   `tfsdk:"registry_credential_name"`
	RepositoryURL          types.String   `tfsdk:"repository_url"`
	Namespace              types.String   `tfsdk:"namespace"`
	ConfigurationType      types.String   `tfsdk:"configuration_type"`
	Configuration          types.String   `tfsdk:"configuration"`
	Parents                []types.String `tfsdk:"parents"`
	JobConfiguration       types.String   `tfsdk:"job_configuration"`
}

type stackModel struct {
	Name        types.String    `tfsdk:"name"`
	Description types.String    `tfsdk:"description"`
	Manifests   []manifestModel `tfsdk:"manifests"`
	Addons      []addonModel    `tfsdk:"addons"`
}

type clusterResourceModel struct {
	ID                   types.String   `tfsdk:"id"`
	ClusterName          types.String   `tfsdk:"cluster_name"`
	GithubCredentialName types.String   `tfsdk:"github_credential_name"`
	GithubBranch         types.String   `tfsdk:"github_branch"`
	GithubRepository     types.String   `tfsdk:"github_repository"`
	AnkraToken           types.String   `tfsdk:"ankra_token"`
	Stacks               []stackModel   `tfsdk:"stacks"`
	ClusterID            types.String   `tfsdk:"cluster_id"`
	HelmCommand          types.String   `tfsdk:"helm_command"`
	State                types.String   `tfsdk:"state"`
	Kind                 types.String   `tfsdk:"kind"`
	WaitForOnline        types.Bool     `tfsdk:"wait_for_online"`
	Timeouts             timeouts.Value `tfsdk:"timeouts"`
}

// NewClusterResource returns a new ankra_cluster resource.
func NewClusterResource() resource.Resource {
	return &clusterResource{}
}

func (clusterResourceInstance *clusterResource) Metadata(_ context.Context, request resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_cluster"
}

func (clusterResourceInstance *clusterResource) Schema(ctx context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: "Imports and manages a cluster on the Ankra platform. Creating the resource " +
			"registers the cluster and returns `helm_command`, which installs the Ankra agent; the platform " +
			"issues that command only once, when the cluster is first registered. Updates re-apply the " +
			"stacks through the same import route.",
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
				MarkdownDescription: "Deprecated per-resource API token. " + tokenDeprecationMessage + " " +
					"Changing it (for example rotating the token) updates the resource in place and never " +
					"replaces the cluster.",
				Optional:           true,
				Sensitive:          true,
				DeprecationMessage: tokenDeprecationMessage,
			},
			"cluster_id": schema.StringAttribute{
				MarkdownDescription: "Identifier assigned to the cluster by the Ankra platform.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"state": schema.StringAttribute{
				MarkdownDescription: "Lifecycle state the platform reports for the cluster, refreshed on every " +
					"read (`offline` until the agent checks in, then `online`).",
				Computed:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"kind": schema.StringAttribute{
				MarkdownDescription: "Cluster kind the platform reports.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"wait_for_online": schema.BoolAttribute{
				MarkdownDescription: "Wait for the cluster agent to check in before the resource is considered " +
					"created. Defaults to `false`, because importing a cluster only registers it - somebody still " +
					"has to run `helm_command` against the cluster, which Terraform cannot do. Set it to `true` " +
					"when that happens out of band and dependent resources need a live cluster.",
				Optional: true,
				Computed: true,
				Default:  booldefault.StaticBool(false),
			},
			"helm_command": schema.StringAttribute{
				MarkdownDescription: "Helm command emitted by the platform to bootstrap the cluster agent. " +
					"Contains a live cluster agent token, so the value is sensitive.",
				Computed:      true,
				Sensitive:     true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
		},
		Blocks: map[string]schema.Block{
			"timeouts": timeouts.Block(ctx, timeouts.Opts{Create: true, Update: true}),
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
										MarkdownDescription: parentsDescription,
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
									"registry_url": schema.StringAttribute{
										MarkdownDescription: "URL of the Helm registry that serves the chart " +
											"(`https://...` or `oci://...`). Required unless the deprecated " +
											"`repository_url` is set.",
										Optional: true,
									},
									"registry_name": schema.StringAttribute{
										MarkdownDescription: "Name of the Helm registry as connected to the Ankra " +
											"organisation. Defaults to a name derived from `registry_url`; the " +
											"platform accepts any name when the URL matches a registry already " +
											"connected, and otherwise asks for the registry to be connected first.",
										Optional: true,
									},
									"registry_credential_name": schema.StringAttribute{
										MarkdownDescription: "Helm registry credential for a private registry. " +
											"When unset the platform uses the credential connected for the URL, if any.",
										Optional: true,
									},
									"repository_url": schema.StringAttribute{
										MarkdownDescription: "Deprecated alias of `registry_url`.",
										Optional:            true,
										DeprecationMessage:  "Use registry_url (and optionally registry_name) instead.",
									},
									"namespace": schema.StringAttribute{
										MarkdownDescription: "Namespace the addon is installed into.",
										Required:            true,
									},
									"configuration_type": schema.StringAttribute{
										MarkdownDescription: "Ignored; the platform only accepts standalone values.",
										Optional:            true,
										DeprecationMessage:  "configuration_type is ignored and will be removed; set configuration only.",
									},
									"configuration": schema.StringAttribute{
										MarkdownDescription: "Helm values for the addon, as YAML or base64-encoded YAML. " +
											"Treated as sensitive because Helm values commonly carry credentials.",
										Optional:  true,
										Sensitive: true,
									},
									"parents": schema.ListAttribute{
										MarkdownDescription: parentsDescription,
										Optional:            true,
										ElementType:         types.StringType,
									},
									"job_configuration": schema.StringAttribute{
										MarkdownDescription: "Per-addon job timeouts in seconds, as a JSON object, e.g. " +
											"`jsonencode({ create_job_timeout = 600, update_job_timeout = 600 })`. Accepted keys: " +
											"`create_job_timeout`, `read_job_timeout`, `update_job_timeout`, `delete_job_timeout`.",
										Optional: true,
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
	apiClient, clientError := clusterResourceInstance.clientForToken(plan.AnkraToken)
	if clientError != nil {
		response.Diagnostics.AddError("Missing API token", missingTokenDetail)
		return
	}
	importRequest, specDiagnostics := importRequestFromModel(&plan)
	response.Diagnostics.Append(specDiagnostics...)
	if response.Diagnostics.HasError() {
		return
	}

	// One deadline bounds the whole create: the import, the by-name
	// resolution of an asynchronous import, and the optional agent wait.
	createTimeout := resolveTimeout(ctx, plan.Timeouts.Create, defaultCreateTimeout, &response.Diagnostics)
	if response.Diagnostics.HasError() {
		return
	}
	createContext, cancel := context.WithTimeout(ctx, createTimeout)
	defer cancel()

	clusterName := plan.ClusterName.ValueString()
	preexisting, lookupError := apiClient.GetClusterByName(createContext, clusterName)
	if lookupError != nil {
		response.Diagnostics.AddError("Unable to check for an existing cluster", lookupError.Error())
		return
	}

	importResponse, importError := apiClient.ImportCluster(createContext, importRequest)
	if importError != nil {
		recoverFailedCreate(ctx, apiClient, &plan, preexisting, importError, response)
		return
	}

	clusterID := importResponse.ClusterID
	if clusterID == "" {
		// The platform queued the import instead of running it. The cluster is
		// registered in the background, so resolve it by name rather than
		// failing and leaving it orphaned outside Terraform.
		registered, waitError := apiClient.WaitForClusterByName(createContext, clusterName, 0)
		if waitError != nil {
			response.Diagnostics.AddError("Imported cluster was not registered",
				fmt.Sprintf("The platform accepted the import without returning a result, and no cluster "+
					"named %q appeared before the create timeout: %s", clusterName, waitError.Error()))
			return
		}
		clusterID = registered.ID
		response.Diagnostics.AddWarning("Cluster imported asynchronously",
			"The platform queued the import instead of running it, so it returned neither the outcome nor "+
				"the agent install command. The cluster was found by name and recorded in state, and "+
				"helm_command is empty: copy the agent install command from the Ankra UI.")
	} else if preexisting != nil && importResponse.ImportCommand == "" {
		response.Diagnostics.AddWarning("Existing cluster adopted",
			fmt.Sprintf("A cluster named %q already existed, so the import updated it and Terraform now "+
				"manages it. The platform only issues the agent install command when a cluster is first "+
				"registered, so helm_command is empty.", clusterName))
	}
	addImportWarnings(&response.Diagnostics, importResponse.Warnings)

	plan.ID = types.StringValue(clusterID)
	plan.ClusterID = types.StringValue(clusterID)
	plan.HelmCommand = types.StringValue(importResponse.ImportCommand)
	plan.State = types.StringNull()
	plan.Kind = types.StringNull()

	// Persist the import result before any wait, so a timeout cannot lose the
	// cluster the platform has already registered.
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}

	cluster := clusterResourceInstance.settleImported(createContext, apiClient, &plan, &response.Diagnostics)
	if cluster == nil {
		return
	}
	applyClusterIdentity(&plan.State, &plan.Kind, cluster)
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
}

// orphanLookupTimeout bounds the by-name lookup that runs after a failed
// import, on a context detached from the (possibly expired) create deadline.
const orphanLookupTimeout = 30 * time.Second

// recoverFailedCreate reports a failed import without leaving an orphan. A
// rejected specification is rolled back by the platform, so it is reported as
// is. Any other failure is ambiguous - the connection may have dropped after
// the platform registered the cluster - so a cluster of that name that did
// not exist before this create is recorded in state alongside the error.
// Terraform then marks it tainted, and the next apply replaces it.
func recoverFailedCreate(ctx context.Context, apiClient *client.Client, plan *clusterResourceModel, preexisting *client.Cluster, importError error, response *resource.CreateResponse) {
	var rejected *client.ImportRejectedError
	if errors.As(importError, &rejected) {
		response.Diagnostics.AddError("Cluster specification rejected", importError.Error())
		return
	}
	if preexisting == nil {
		lookupContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), orphanLookupTimeout)
		defer cancel()
		registered, lookupError := apiClient.GetClusterByName(lookupContext, plan.ClusterName.ValueString())
		if lookupError == nil && registered != nil {
			plan.ID = types.StringValue(registered.ID)
			plan.ClusterID = types.StringValue(registered.ID)
			plan.HelmCommand = types.StringValue("")
			applyClusterIdentity(&plan.State, &plan.Kind, registered)
			response.Diagnostics.Append(response.State.Set(ctx, plan)...)
			response.Diagnostics.AddError("Cluster registered but the import did not complete",
				fmt.Sprintf("%s\n\nThe platform registered cluster %q (id %s) before the import failed, so it "+
					"is recorded in state instead of being left outside Terraform. Terraform marks it tainted: "+
					"the next apply replaces it, which also issues a fresh agent install command. Run "+
					"`terraform untaint` instead to keep the registration as it is.",
					importError.Error(), registered.Name, registered.ID))
			return
		}
	}
	response.Diagnostics.AddError("Unable to import cluster", importError.Error())
}

// settleImported waits for the cluster agent to check in when the practitioner
// opted in, and otherwise resolves the row once so state and kind are recorded.
// ctx carries the create deadline.
func (clusterResourceInstance *clusterResource) settleImported(ctx context.Context, apiClient *client.Client, plan *clusterResourceModel, diagnostics *diag.Diagnostics) *client.Cluster {
	clusterID := plan.ClusterID.ValueString()

	if plan.WaitForOnline.IsNull() || plan.WaitForOnline.IsUnknown() || !plan.WaitForOnline.ValueBool() {
		cluster, readError := apiClient.GetClusterByID(ctx, clusterID)
		if readError != nil {
			diagnostics.AddError("Unable to read cluster", readError.Error())
			return nil
		}
		if cluster == nil {
			return &client.Cluster{ID: clusterID}
		}
		return cluster
	}

	cluster, waitError := apiClient.WaitForImportedCluster(ctx, clusterID, 0)
	if waitError != nil {
		diagnostics.AddError("Cluster agent did not check in", waitError.Error())
		return nil
	}
	return cluster
}

func (clusterResourceInstance *clusterResource) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	var state clusterResourceModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}

	apiClient, clientError := clusterResourceInstance.clientForToken(state.AnkraToken)
	if clientError != nil {
		response.Diagnostics.AddError("Missing API token", missingTokenDetail)
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
	applyClusterIdentity(&state.State, &state.Kind, cluster)
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
}

func (clusterResourceInstance *clusterResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	var plan, prior clusterResourceModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	response.Diagnostics.Append(request.State.Get(ctx, &prior)...)
	if response.Diagnostics.HasError() {
		return
	}
	importRequest, specDiagnostics := importRequestFromModel(&plan)
	response.Diagnostics.Append(specDiagnostics...)
	if response.Diagnostics.HasError() {
		return
	}

	// Nothing the platform stores changed - a rotated ankra_token, a new
	// timeout or wait_for_online - so there is nothing to re-import.
	if priorRequest, priorDiagnostics := importRequestFromModel(&prior); !priorDiagnostics.HasError() &&
		reflect.DeepEqual(priorRequest, importRequest) {
		response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
		return
	}

	apiClient, clientError := clusterResourceInstance.clientForToken(plan.AnkraToken)
	if clientError != nil {
		response.Diagnostics.AddError("Missing API token", missingTokenDetail)
		return
	}
	updateTimeout := resolveTimeout(ctx, plan.Timeouts.Update, defaultUpdateTimeout, &response.Diagnostics)
	if response.Diagnostics.HasError() {
		return
	}
	updateContext, cancel := context.WithTimeout(ctx, updateTimeout)
	defer cancel()

	importResponse, importError := apiClient.ImportCluster(updateContext, importRequest)
	if importError != nil {
		var rejected *client.ImportRejectedError
		if errors.As(importError, &rejected) {
			response.Diagnostics.AddError("Cluster specification rejected", importError.Error())
			return
		}
		response.Diagnostics.AddError("Unable to update cluster", importError.Error())
		return
	}
	switch {
	case importResponse.ClusterID == "":
		response.Diagnostics.AddWarning("Cluster update queued",
			"The platform queued the update instead of running it, so its outcome was not reported. "+
				"Check the cluster's stacks in Ankra.")
	case importResponse.ClusterID != plan.ClusterID.ValueString():
		response.Diagnostics.AddWarning("Cluster identity changed",
			fmt.Sprintf("The platform applied the update to cluster %s, not %s: the cluster was probably "+
				"deleted and registered again outside Terraform. Remove it from state and import it again.",
				importResponse.ClusterID, plan.ClusterID.ValueString()))
	}
	addImportWarnings(&response.Diagnostics, importResponse.Warnings)

	// helm_command is only issued when a cluster is first registered; a
	// re-import answers with an empty one, which must not replace the stored
	// command, so the plan (which carries the stored value) is saved as is.
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
}

// ValidateConfig reports stack problems at plan time - unresolvable parents,
// an addon without a registry URL, a malformed job_configuration - instead
// of at apply. Values still unknown are left to apply.
func (clusterResourceInstance *clusterResource) ValidateConfig(ctx context.Context, request resource.ValidateConfigRequest, response *resource.ValidateConfigResponse) {
	var stacks []stackModel
	if diagnostics := request.Config.GetAttribute(ctx, path.Root("stacks"), &stacks); diagnostics.HasError() {
		return
	}
	if !stacksFullyKnown(stacks) {
		return
	}
	_, diagnostics := stacksToAPI(stacks)
	response.Diagnostics.Append(diagnostics...)
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

	apiClient, clientError := clusterResourceInstance.clientForToken(state.AnkraToken)
	if clientError != nil {
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

// importRequestFromModel builds the import payload for a configuration.
func importRequestFromModel(model *clusterResourceModel) (client.ImportClusterRequest, diag.Diagnostics) {
	stacks, diagnostics := stacksToAPI(model.Stacks)
	return client.ImportClusterRequest{
		Name:        model.ClusterName.ValueString(),
		Description: "Managed by Terraform",
		Spec: client.ImportClusterSpec{
			GitRepository: client.GitRepository{
				Provider:       "github",
				CredentialName: model.GithubCredentialName.ValueString(),
				Branch:         model.GithubBranch.ValueString(),
				Repository:     model.GithubRepository.ValueString(),
			},
			Stacks: stacks,
		},
	}, diagnostics
}

// addImportWarnings surfaces the advisory findings the platform returned.
func addImportWarnings(diagnostics *diag.Diagnostics, warnings []client.ValidationWarning) {
	for _, warning := range warnings {
		diagnostics.AddWarning("Ankra platform warning",
			fmt.Sprintf("%s %q: %s: %s", warning.Kind, warning.Name, warning.Key, warning.Message))
	}
}

// clientForToken returns the configured client, or a copy overridden with the
// deprecated per-resource token when one is supplied. It reports an error
// rather than dereferencing a client the provider never configured.
func (clusterResourceInstance *clusterResource) clientForToken(token types.String) (*client.Client, error) {
	if clusterResourceInstance.client == nil {
		return nil, errMissingToken
	}
	if token.IsNull() || token.IsUnknown() || token.ValueString() == "" {
		if clusterResourceInstance.client.Token == "" {
			return nil, errMissingToken
		}
		return clusterResourceInstance.client, nil
	}
	override := *clusterResourceInstance.client
	override.Token = token.ValueString()
	return &override, nil
}
