// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"sort"
	"time"

	"github.com/hashicorp/terraform-plugin-framework-timeouts/resource/timeouts"
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
	_ resource.Resource                = (*applicationDeploymentResource)(nil)
	_ resource.ResourceWithConfigure   = (*applicationDeploymentResource)(nil)
	_ resource.ResourceWithImportState = (*applicationDeploymentResource)(nil)
)

// applicationDeployDefaultTimeout bounds how long a create waits for the
// application's setup to finish before it gives up.
const applicationDeployDefaultTimeout = 30 * time.Minute

// applicationDeployRetryInterval is how long a create waits between deploys
// refused because setup has not finished. Tests shorten it.
var applicationDeployRetryInterval = 15 * time.Second

type applicationDeploymentResource struct {
	configuredClient
}

type applicationDeploymentModel struct {
	ID            types.String   `tfsdk:"id"`
	ApplicationID types.String   `tfsdk:"application_id"`
	ClusterID     types.String   `tfsdk:"cluster_id"`
	Namespace     types.String   `tfsdk:"namespace"`
	DeployMode    types.String   `tfsdk:"deploy_mode"`
	Inputs        types.Map      `tfsdk:"inputs"`
	StackName     types.String   `tfsdk:"stack_name"`
	Status        types.String   `tfsdk:"status"`
	Timeouts      timeouts.Value `tfsdk:"timeouts"`
}

// NewApplicationDeploymentResource returns the ankra_application_deployment
// resource, the counterpart of `ankra application deploy` and
// `ankra application remove`.
func NewApplicationDeploymentResource() resource.Resource {
	return &applicationDeploymentResource{}
}

func (deploymentResource *applicationDeploymentResource) Metadata(_ context.Context, request resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_application_deployment"
}

func (deploymentResource *applicationDeploymentResource) Schema(ctx context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: "An application deployed to one cluster and namespace, which the platform keeps running and moves onto new builds " +
			"when push-to-deploy is on. The counterpart of `ankra application deploy`; destroying it is `ankra application remove`, " +
			"which takes the application off this cluster and namespace only. The deploy and the removal are accepted and then run " +
			"asynchronously; `status` reports progress.\n\n" +
			"**A new application deploys in a second phase.** The platform refuses a deploy until the application's setup pull " +
			"request is merged and its first build has published an image. Create keeps retrying while the platform reports exactly " +
			"that, for up to the `create` timeout (30 minutes by default), so an `ankra_application` and its deployment can share " +
			"one apply when someone merges the setup pull request in that window, or when `auto_merge_setup_pull_requests` is on. " +
			"Otherwise apply the application first and the deployment once the setup pull request is merged.\n\n" +
			"**Import.** The platform does not report deploy `inputs`, so they import as unset. When the configuration sets " +
			"`inputs`, the first apply after an import redeploys with them.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Identifier of the deployment (the platform's installation id).",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"application_id": schema.StringAttribute{
				MarkdownDescription: "Identifier of the application. Changing it replaces the deployment.",
				Required:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"cluster_id": schema.StringAttribute{
				MarkdownDescription: "Identifier of the cluster to deploy to. Changing it replaces the deployment.",
				Required:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"namespace": schema.StringAttribute{
				MarkdownDescription: "Namespace to deploy into. Defaults to one the platform derives from the application name. Changing it replaces the deployment.",
				Optional:            true,
				Computed:            true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
					stringplanmodifier.RequiresReplace(),
				},
			},
			"deploy_mode": schema.StringAttribute{
				MarkdownDescription: "`quick` or `high_availability`. Defaults to `quick`. Changing it redeploys in place.",
				Optional:            true,
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
				Validators:          []validator.String{applicationStringOneOf{values: []string{"quick", "high_availability"}}},
			},
			"inputs": schema.MapAttribute{
				MarkdownDescription: "Deploy options the application declares, such as `ingress_host`, by name. Changing them redeploys in place; " +
					"an option removed here returns to the application's default. The platform does not report the options back, " +
					"so a change made outside Terraform is not detected.",
				Optional:    true,
				ElementType: types.StringType,
			},
			"stack_name": schema.StringAttribute{
				MarkdownDescription: "Stack the deployment runs as on the cluster.",
				Computed:            true,
			},
			"status": schema.StringAttribute{
				MarkdownDescription: "Deployment status as the platform last observed it.",
				Computed:            true,
			},
		},
		Blocks: map[string]schema.Block{
			"timeouts": timeouts.Block(ctx, timeouts.Opts{Create: true}),
		},
	}
}

func (deploymentResource *applicationDeploymentResource) deploy(ctx context.Context, plan *applicationDeploymentModel, inputs map[string]string, resetInputs []string) error {
	apiClient := deploymentResource.client
	result, err := apiClient.DeployApplication(ctx, plan.ApplicationID.ValueString(), client.ApplicationDeployRequest{
		ClusterID:   plan.ClusterID.ValueString(),
		Namespace:   plan.Namespace.ValueString(),
		DeployMode:  plan.DeployMode.ValueString(),
		Inputs:      inputs,
		ResetInputs: resetInputs,
	})
	if err != nil {
		return err
	}
	installation, err := apiClient.GetApplicationInstallation(ctx, plan.ApplicationID.ValueString(), result.InstallationID)
	if err != nil {
		return err
	}
	applicationDeploymentToModel(installation, plan)
	return nil
}

func applicationDeploymentToModel(installation *client.ApplicationInstallation, model *applicationDeploymentModel) {
	model.ID = types.StringValue(installation.ID)
	model.ClusterID = types.StringValue(installation.ClusterID)
	model.Namespace = types.StringValue(installation.Namespace)
	model.DeployMode = types.StringValue(installation.DeployMode)
	model.StackName = types.StringPointerValue(installation.StackName)
	model.Status = types.StringValue(installation.Status)
}

func (deploymentResource *applicationDeploymentResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	if deploymentResource.requireClient(&response.Diagnostics) == nil {
		return
	}
	var plan applicationDeploymentModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}
	createTimeout, timeoutDiagnostics := plan.Timeouts.Create(ctx, applicationDeployDefaultTimeout)
	response.Diagnostics.Append(timeoutDiagnostics...)
	inputs := applicationStringMap(ctx, plan.Inputs, &response.Diagnostics)
	if response.Diagnostics.HasError() {
		return
	}
	deadline := time.Now().Add(createTimeout)
	for {
		err := deploymentResource.deploy(ctx, &plan, inputs, nil)
		if err == nil {
			break
		}
		if !client.IsApplicationNotReady(err) || time.Now().Add(applicationDeployRetryInterval).After(deadline) {
			response.Diagnostics.AddError("Unable to deploy application", err.Error())
			return
		}
		select {
		case <-ctx.Done():
			response.Diagnostics.AddError("Unable to deploy application", ctx.Err().Error())
			return
		case <-time.After(applicationDeployRetryInterval):
		}
	}
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
}

func (deploymentResource *applicationDeploymentResource) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	apiClient := deploymentResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state applicationDeploymentModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}

	var installation *client.ApplicationInstallation
	var err error
	if state.ID.IsNull() || state.ID.ValueString() == "" {
		installation, err = apiClient.FindApplicationInstallation(ctx, state.ApplicationID.ValueString(), state.ClusterID.ValueString(), state.Namespace.ValueString())
	} else {
		installation, err = apiClient.GetApplicationInstallation(ctx, state.ApplicationID.ValueString(), state.ID.ValueString())
	}
	if removeWhenNotFound(ctx, err, response) {
		return
	}
	if err != nil {
		response.Diagnostics.AddError("Unable to read application deployment", err.Error())
		return
	}
	if installation.Status == client.ApplicationInstallationStatusRemoving {
		response.State.RemoveResource(ctx)
		return
	}
	applicationDeploymentToModel(installation, &state)
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
}

func (deploymentResource *applicationDeploymentResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	if deploymentResource.requireClient(&response.Diagnostics) == nil {
		return
	}
	var plan, state applicationDeploymentModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	planned := applicationStringMap(ctx, plan.Inputs, &response.Diagnostics)
	resetInputs := []string{}
	for name := range applicationStringMap(ctx, state.Inputs, &response.Diagnostics) {
		if _, kept := planned[name]; !kept {
			resetInputs = append(resetInputs, name)
		}
	}
	sort.Strings(resetInputs)
	if err := deploymentResource.deploy(ctx, &plan, planned, resetInputs); err != nil {
		response.Diagnostics.AddError("Unable to redeploy application", err.Error())
		return
	}
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
}

func (deploymentResource *applicationDeploymentResource) Delete(ctx context.Context, request resource.DeleteRequest, response *resource.DeleteResponse) {
	apiClient := deploymentResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state applicationDeploymentModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	if err := apiClient.RemoveApplicationDeployment(ctx, state.ApplicationID.ValueString(), state.ClusterID.ValueString(), state.Namespace.ValueString()); err != nil {
		response.Diagnostics.AddError("Unable to remove application deployment", err.Error())
	}
}

// ImportState imports by `<application_id>/<cluster_id>/<namespace>`. Deploy
// inputs are not reported by the platform and import as unset, so the first
// apply after an import redeploys when the configuration sets them.
func (deploymentResource *applicationDeploymentResource) ImportState(ctx context.Context, request resource.ImportStateRequest, response *resource.ImportStateResponse) {
	parts, err := applicationSplitImportID(request.ID, "application_id", "cluster_id", "namespace")
	if err != nil {
		response.Diagnostics.AddError("Invalid import id", err.Error())
		return
	}
	response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("application_id"), parts[0])...)
	response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("cluster_id"), parts[1])...)
	response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("namespace"), parts[2])...)
}
