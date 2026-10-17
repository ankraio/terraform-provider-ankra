// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"time"

	"github.com/hashicorp/terraform-plugin-framework-timeouts/resource/timeouts"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/mapplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/setplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"ankra.io/terraform-provider-ankra/internal/client"
)

// managedServiceNamePattern mirrors the platform's service name rule
// (servicepackage): lowercase words joined by single hyphens, at most 63
// characters.
var managedServiceNamePattern = regexp.MustCompile(`^[a-z][a-z0-9]*(-[a-z0-9]+)*$`)

// managedServicePollInterval paces the setup and retirement waits; tests
// shorten it.
var managedServicePollInterval = 10 * time.Second

const (
	managedServiceDefaultCreateTimeout = 30 * time.Minute
	managedServiceDefaultDeleteTimeout = 2 * time.Hour
)

var (
	_ resource.Resource                = (*managedServiceResource)(nil)
	_ resource.ResourceWithConfigure   = (*managedServiceResource)(nil)
	_ resource.ResourceWithImportState = (*managedServiceResource)(nil)
)

type managedServiceResource struct {
	configuredClient
}

type managedServiceModel struct {
	ID                     types.String   `tfsdk:"id"`
	PackageVersionID       types.String   `tfsdk:"package_version_id"`
	Name                   types.String   `tfsdk:"name"`
	Mode                   types.String   `tfsdk:"mode"`
	ClusterID              types.String   `tfsdk:"cluster_id"`
	Region                 types.String   `tfsdk:"region"`
	DataBoundary           types.String   `tfsdk:"data_boundary"`
	ConsumerIDs            types.Set      `tfsdk:"consumer_ids"`
	Parameters             types.Map      `tfsdk:"parameters"`
	SecretReferences       types.Map      `tfsdk:"secret_references"`
	WaitUntilReady         types.Bool     `tfsdk:"wait_until_ready"`
	AllowDataLossOnDestroy types.Bool     `tfsdk:"allow_data_loss_on_destroy"`
	ClusterName            types.String   `tfsdk:"cluster_name"`
	State                  types.String   `tfsdk:"state"`
	Readiness              types.String   `tfsdk:"readiness"`
	Generation             types.Int64    `tfsdk:"generation"`
	ConnectedConsumerIDs   types.Set      `tfsdk:"connected_consumer_ids"`
	CreatedAt              types.String   `tfsdk:"created_at"`
	Timeouts               timeouts.Value `tfsdk:"timeouts"`
}

// NewManagedServiceResource returns the ankra_managed_service resource, the
// counterpart of `ankra services setup` and `ankra services retire`.
func NewManagedServiceResource() resource.Resource {
	return &managedServiceResource{}
}

func (service *managedServiceResource) Metadata(_ context.Context, request resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_managed_service"
}

// managedServiceReplaceUnlessImported replaces the service when a set-up
// input the platform does not return changes, except right after an import,
// when state cannot know it.
func managedServiceSetReplaceUnlessImported() planmodifier.Set {
	return setplanmodifier.RequiresReplaceIf(
		func(_ context.Context, request planmodifier.SetRequest, response *setplanmodifier.RequiresReplaceIfFuncResponse) {
			response.RequiresReplace = !request.StateValue.IsNull()
		},
		"Changing this value replaces the service, except right after an import.",
		"Changing this value replaces the service, except right after an import.",
	)
}

func managedServiceMapReplaceUnlessImported() planmodifier.Map {
	return mapplanmodifier.RequiresReplaceIf(
		func(_ context.Context, request planmodifier.MapRequest, response *mapplanmodifier.RequiresReplaceIfFuncResponse) {
			response.RequiresReplace = !request.StateValue.IsNull()
		},
		"Changing this value replaces the service, except right after an import.",
		"Changing this value replaces the service, except right after an import.",
	)
}

func (service *managedServiceResource) Schema(ctx context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	replacedString := func(description string, validators ...validator.String) schema.StringAttribute {
		return schema.StringAttribute{
			MarkdownDescription: description, Required: true, Validators: validators,
			PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
		}
	}
	computedString := func(description string) schema.StringAttribute {
		return schema.StringAttribute{MarkdownDescription: description, Computed: true}
	}
	response.Schema = schema.Schema{
		MarkdownDescription: "A managed service set up from a service package on a cluster, the counterpart of `ankra services setup` and " +
			"`ankra services retire`. Setting it up reviews the plan and confirms it straight away (the platform's review is valid " +
			"for ten minutes), then waits until the service is serving.\n\n" +
			"A managed service cannot be changed in place: changing any set-up input replaces it, which retires the old service " +
			"first. **Retiring a service deletes its data** (its stack, namespace, volumes and secrets). Destroy, and so any " +
			"replacement, therefore refuses unless `allow_data_loss_on_destroy` is `true`; set it, apply, then destroy.\n\n" +
			"The platform does not return `consumer_ids`, `parameters` or `secret_references`, so they are kept as configured and " +
			"are null after an import. Consumers connected later, outside Terraform, show in `connected_consumer_ids` and are " +
			"disconnected when the service is retired.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Identifier of the service (the id of the review it was set up from).",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"package_version_id": replacedString("Service package version to set the service up from (`ankra_service_packages`).", securityUUIDValidator{}),
			"name": replacedString("Service name: lowercase letters, digits and single hyphens, starting with a letter, at most 63 characters. Unique in the organisation.",
				deployPatternValidator{pattern: managedServiceNamePattern, subject: "name"}, costStringLengthValidator{minimum: 1, maximum: 63}),
			"mode": schema.StringAttribute{
				MarkdownDescription: "`customer` (the default) runs the service on the cluster; `existing` adopts one already there.",
				Optional:            true,
				Computed:            true,
				Default:             stringdefault.StaticString("customer"),
				Validators:          []validator.String{organisationStringOneOf{values: []string{"customer", "existing"}}},
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"cluster_id":    replacedString("Cluster the service runs on. It needs a declared placement policy (`ankra_service_placement_policy`).", securityUUIDValidator{}),
			"region":        replacedString("Region; it must equal the region of the cluster's placement policy."),
			"data_boundary": replacedString("Data boundary; it must equal the data boundary of the cluster's placement policy."),
			"consumer_ids": schema.SetAttribute{
				MarkdownDescription: "Consumer bindings (`ankra_service_consumer`) to connect the service to at setup, at most 100.",
				ElementType:         types.StringType,
				Optional:            true,
				PlanModifiers:       []planmodifier.Set{managedServiceSetReplaceUnlessImported()},
			},
			"parameters": schema.MapAttribute{
				MarkdownDescription: "Integer parameters the package takes, at most 32.",
				ElementType:         types.Int64Type,
				Optional:            true,
				PlanModifiers:       []planmodifier.Map{managedServiceMapReplaceUnlessImported()},
			},
			"secret_references": schema.MapAttribute{
				MarkdownDescription: "Secrets the package takes, by name (such as `ADMIN_PASSWORD`), each the id of a credential grant. At most 16.",
				ElementType:         types.StringType,
				Optional:            true,
				PlanModifiers:       []planmodifier.Map{managedServiceMapReplaceUnlessImported()},
			},
			"wait_until_ready": schema.BoolAttribute{
				MarkdownDescription: "Wait at setup until the service is serving (or deployed). Defaults to `true`; a failed install then fails the apply.",
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(true),
			},
			"allow_data_loss_on_destroy": schema.BoolAttribute{
				MarkdownDescription: "Allow destroy (and replacement) to retire the service, which deletes its data. Defaults to `false`, which makes destroy fail without touching the service.",
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(false),
			},
			"cluster_name": computedString("Name of the cluster."),
			"state":        computedString("`deploying`, `deployed`, `serving`, `degraded`, `failed`, `retiring` or `unknown`."),
			"readiness":    computedString("Readiness the platform last observed."),
			"generation": schema.Int64Attribute{
				MarkdownDescription: "Generation of the service; every change increases it.",
				Computed:            true,
			},
			"connected_consumer_ids": schema.SetAttribute{
				MarkdownDescription: "Consumer bindings the service is connected to now.",
				ElementType:         types.StringType,
				Computed:            true,
			},
			"created_at": computedString("When the service was set up."),
		},
		Blocks: map[string]schema.Block{
			"timeouts": timeouts.Block(ctx, timeouts.Opts{Create: true, Delete: true}),
		},
	}
}

func (service *managedServiceResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	apiClient := service.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var plan managedServiceModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}
	reviewRequest := client.ManagedServiceReviewRequest{
		PackageVersionID: plan.PackageVersionID.ValueString(),
		Name:             plan.Name.ValueString(),
		Mode:             plan.Mode.ValueString(),
		Region:           plan.Region.ValueString(),
		DataBoundary:     plan.DataBoundary.ValueString(),
		ClusterID:        plan.ClusterID.ValueString(),
		ConsumerIDs:      organisationStringsFromSet(ctx, plan.ConsumerIDs, &response.Diagnostics),
		Parameters:       map[string]int64{},
		SecretReferences: map[string]string{},
	}
	if !plan.Parameters.IsNull() && !plan.Parameters.IsUnknown() {
		response.Diagnostics.Append(plan.Parameters.ElementsAs(ctx, &reviewRequest.Parameters, false)...)
	}
	if !plan.SecretReferences.IsNull() && !plan.SecretReferences.IsUnknown() {
		response.Diagnostics.Append(plan.SecretReferences.ElementsAs(ctx, &reviewRequest.SecretReferences, false)...)
	}
	if response.Diagnostics.HasError() {
		return
	}
	createTimeout, timeoutDiagnostics := plan.Timeouts.Create(ctx, managedServiceDefaultCreateTimeout)
	response.Diagnostics.Append(timeoutDiagnostics...)
	if response.Diagnostics.HasError() {
		return
	}

	review, err := apiClient.ReviewManagedService(ctx, reviewRequest)
	if serviceAdmissionUnavailable(err, &response.Diagnostics) {
		return
	}
	if err != nil {
		response.Diagnostics.AddError("Unable to review managed service",
			err.Error()+"\n\nA 409 means the name is taken, the cluster has no placement policy, or region and data_boundary differ from it.")
		return
	}
	if _, err := apiClient.ConfirmManagedService(ctx, review.ID, review.Digest); err != nil {
		response.Diagnostics.AddError("Unable to confirm managed service",
			err.Error()+"\n\nThe review was not confirmed, so nothing was set up. A 409 means the review changed or expired; apply again.")
		return
	}

	plan.ID = types.StringValue(review.ID)
	instance, err := apiClient.GetManagedService(ctx, review.ID)
	if err != nil {
		plan.applyComputedUnknown()
		response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
		response.Diagnostics.Append(trackScope(ctx, apiClient, response.Private)...)
		response.Diagnostics.AddError("Unable to read the managed service after setting it up", err.Error())
		return
	}
	if plan.WaitUntilReady.ValueBool() {
		waitContext, cancel := context.WithTimeout(ctx, createTimeout)
		defer cancel()
		instance, err = managedServiceWaitUntilReady(waitContext, apiClient, instance)
	}
	plan.applyAPI(instance)
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
	response.Diagnostics.Append(trackScope(ctx, apiClient, response.Private)...)
	if err != nil {
		response.Diagnostics.AddError("Managed service did not become ready", err.Error())
	}
}

// managedServiceWaitUntilReady polls until the install settles. It returns
// the last instance read, so state records it even when the wait fails.
func managedServiceWaitUntilReady(ctx context.Context, apiClient *client.Client, instance *client.ManagedService) (*client.ManagedService, error) {
	for {
		switch instance.State {
		case client.ManagedServiceStateServing, client.ManagedServiceStateDeployed:
			return instance, nil
		case client.ManagedServiceStateFailed:
			return instance, fmt.Errorf("the install of service %s failed (deployment state %q). Destroying it retires it, which needs allow_data_loss_on_destroy", instance.ID, instance.DeploymentState)
		}
		select {
		case <-ctx.Done():
			return instance, fmt.Errorf("service %s is still %q; the install goes on, and the next plan reads its outcome", instance.ID, instance.State)
		case <-time.After(managedServicePollInterval):
		}
		next, err := apiClient.GetManagedService(ctx, instance.ID)
		if err != nil {
			return instance, err
		}
		instance = next
	}
}

func (service *managedServiceResource) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	apiClient := service.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state managedServiceModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	instance, err := apiClient.GetManagedService(ctx, state.ID.ValueString())
	if serviceAdmissionUnavailable(err, &response.Diagnostics) || removeWhenNotFound(ctx, err, apiClient, response) {
		return
	}
	if err != nil {
		response.Diagnostics.AddError("Unable to read managed service", err.Error())
		return
	}
	if instance.State == client.ManagedServiceStateReleased {
		response.State.RemoveResource(ctx)
		return
	}
	state.PackageVersionID = types.StringValue(instance.PackageVersionID)
	state.Name = types.StringValue(instance.Name)
	state.Mode = types.StringValue(instance.Mode)
	state.ClusterID = types.StringValue(instance.ClusterID)
	state.Region = types.StringValue(instance.Region)
	state.DataBoundary = types.StringValue(instance.DataBoundary)
	state.applyAPI(instance)
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
	response.Diagnostics.Append(trackScope(ctx, apiClient, response.Private)...)
}

// Update only records the provider-side settings (the wait and the destroy
// guard): every set-up input replaces the service.
func (service *managedServiceResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	var plan, state managedServiceModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	state.WaitUntilReady = plan.WaitUntilReady
	state.AllowDataLossOnDestroy = plan.AllowDataLossOnDestroy
	state.ConsumerIDs = plan.ConsumerIDs
	state.Parameters = plan.Parameters
	state.SecretReferences = plan.SecretReferences
	state.Timeouts = plan.Timeouts
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
}

// Delete retires the service, which deletes its data, and only when the
// configuration allows it. Without allow_data_loss_on_destroy it sends
// nothing.
func (service *managedServiceResource) Delete(ctx context.Context, request resource.DeleteRequest, response *resource.DeleteResponse) {
	var state managedServiceModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	if !state.AllowDataLossOnDestroy.ValueBool() {
		response.Diagnostics.AddError("Retiring the managed service would delete its data",
			fmt.Sprintf("Destroying %s (or replacing it) retires the service, which deletes its stack, namespace, volumes and secrets. "+
				"Nothing was changed. To go ahead, set allow_data_loss_on_destroy = true, apply, and destroy again. "+
				"To stop managing the service without retiring it, run `terraform state rm`.", state.Name.ValueString()))
		return
	}
	apiClient := service.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	deleteTimeout, timeoutDiagnostics := state.Timeouts.Delete(ctx, managedServiceDefaultDeleteTimeout)
	response.Diagnostics.Append(timeoutDiagnostics...)
	if response.Diagnostics.HasError() {
		return
	}
	waitContext, cancel := context.WithTimeout(ctx, deleteTimeout)
	defer cancel()

	serviceID := state.ID.ValueString()
	instance, err := apiClient.GetManagedService(waitContext, serviceID)
	if serviceAdmissionUnavailable(err, &response.Diagnostics) || deletedWhenNotFound(waitContext, err, apiClient, request.Private, &response.Diagnostics) || response.Diagnostics.HasError() {
		return
	}
	if err != nil {
		response.Diagnostics.AddError("Unable to read managed service before retiring it", err.Error())
		return
	}
	if instance.State == client.ManagedServiceStateReleased {
		return
	}

	retirementID := ""
	if instance.Retirement != nil && instance.Retirement.State != client.ServiceRetirementStateExpired &&
		(instance.Retirement.State != client.ServiceRetirementStateSettled || managedServiceRetired(instance.Retirement.Outcome)) {
		retirementID = instance.Retirement.ID
	}
	if retirementID == "" {
		consumerIDs := make([]string, 0, len(instance.Consumers))
		for _, consumer := range instance.Consumers {
			consumerIDs = append(consumerIDs, consumer.ID)
		}
		sort.Strings(consumerIDs)
		planned, err := apiClient.PlanManagedServiceRetirement(waitContext, serviceID, client.ManagedServiceRetirementRequest{
			ExpectedGeneration:    instance.Generation,
			DisconnectConsumerIDs: consumerIDs,
			AcknowledgeDataLoss:   true,
		})
		if err != nil {
			response.Diagnostics.AddError("Unable to plan the retirement of the managed service",
				err.Error()+"\n\nA 409 means the install is still running, the service changed since it was read, or the stack was changed outside Services.")
			return
		}
		if _, err := apiClient.ConfirmManagedServiceRetirement(waitContext, serviceID, planned.ID, planned.Digest); err != nil {
			response.Diagnostics.AddError("Unable to confirm the retirement of the managed service", err.Error())
			return
		}
		retirementID = planned.ID
	}

	if err := managedServiceWaitUntilRetired(waitContext, apiClient, serviceID, retirementID); err != nil {
		response.Diagnostics.AddError("Managed service was not retired", err.Error())
	}
}

func managedServiceRetired(outcome *string) bool {
	return outcome != nil && *outcome == client.ServiceRetirementOutcomeRetire
}

// managedServiceWaitUntilRetired polls a confirmed retirement until it
// settles. Only the "retired" outcome releases the service.
func managedServiceWaitUntilRetired(ctx context.Context, apiClient *client.Client, serviceID, retirementID string) error {
	for {
		retirement, err := apiClient.GetManagedServiceRetirement(ctx, serviceID, retirementID)
		if err != nil {
			return err
		}
		switch retirement.State {
		case client.ServiceRetirementStateSettled:
			if managedServiceRetired(retirement.Outcome) {
				return nil
			}
			outcome, reason := "", ""
			if retirement.Outcome != nil {
				outcome = *retirement.Outcome
			}
			if retirement.Reason != nil {
				reason = *retirement.Reason
			}
			return fmt.Errorf("retirement %s settled with outcome %q: %s", retirementID, outcome, reason)
		case client.ServiceRetirementStateExpired:
			return fmt.Errorf("retirement %s expired before it was confirmed; destroy again", retirementID)
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("retirement %s is still running; it goes on, and the next destroy waits for it", retirementID)
		case <-time.After(managedServicePollInterval):
		}
	}
}

// ImportState imports by service id. The inputs the platform does not
// return import as null, the wait as true and the destroy guard as false.
func (service *managedServiceResource) ImportState(ctx context.Context, request resource.ImportStateRequest, response *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), request, response)
	response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("wait_until_ready"), true)...)
	response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("allow_data_loss_on_destroy"), false)...)
}

func (model *managedServiceModel) applyComputedUnknown() {
	model.ClusterName = types.StringNull()
	model.State = types.StringNull()
	model.Readiness = types.StringNull()
	model.Generation = types.Int64Null()
	model.ConnectedConsumerIDs = types.SetNull(types.StringType)
	model.CreatedAt = types.StringNull()
}

func (model *managedServiceModel) applyAPI(instance *client.ManagedService) {
	model.ID = types.StringValue(instance.ID)
	model.ClusterName = types.StringValue(instance.ClusterName)
	model.State = types.StringValue(instance.State)
	model.Readiness = types.StringValue(instance.Readiness)
	model.Generation = types.Int64Value(instance.Generation)
	consumerIDs := make([]string, 0, len(instance.Consumers))
	for _, consumer := range instance.Consumers {
		consumerIDs = append(consumerIDs, consumer.ID)
	}
	sort.Strings(consumerIDs)
	model.ConnectedConsumerIDs = managedServiceStringSet(consumerIDs)
	model.CreatedAt = types.StringValue(instance.CreatedAt)
}

func managedServiceStringSet(values []string) types.Set {
	elements := make([]types.String, 0, len(values))
	for _, value := range values {
		elements = append(elements, types.StringValue(value))
	}
	set, _ := types.SetValueFrom(context.Background(), types.StringType, elements)
	return set
}
