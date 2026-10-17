// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/listplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"ankra.io/terraform-provider-ankra/internal/client"
)

var (
	_ resource.Resource                   = (*helmRegistryResource)(nil)
	_ resource.ResourceWithConfigure      = (*helmRegistryResource)(nil)
	_ resource.ResourceWithImportState    = (*helmRegistryResource)(nil)
	_ resource.ResourceWithValidateConfig = (*helmRegistryResource)(nil)
)

const (
	helmRegistryMinimumReadJobInterval = 60
	helmRegistryMaximumReadJobInterval = 86400
)

type helmRegistryResource struct {
	configuredClient
}

type helmRegistryModel struct {
	ID              types.String `tfsdk:"id"`
	Name            types.String `tfsdk:"name"`
	URL             types.String `tfsdk:"url"`
	Kind            types.String `tfsdk:"kind"`
	CredentialName  types.String `tfsdk:"credential_name"`
	ExcludeCharts   types.List   `tfsdk:"exclude_charts"`
	ReadJobInterval types.Int64  `tfsdk:"read_job_interval"`
	LastSyncError   types.String `tfsdk:"last_sync_error"`
	ProjectID       types.String `tfsdk:"project_id"`
}

// NewHelmRegistryResource returns the ankra_helm_registry resource, the
// counterpart of `ankra helm registries create|update|delete`.
func NewHelmRegistryResource() resource.Resource {
	return &helmRegistryResource{}
}

func (registryResource *helmRegistryResource) Metadata(_ context.Context, request resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_helm_registry"
}

func (registryResource *helmRegistryResource) Schema(_ context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: "A Helm chart registry connected to the organisation, so its charts appear in the catalogue and addons can install them. " +
			"Reference it from an `ankra_cluster` addon with `registry_name = ankra_helm_registry.<name>.name` and " +
			"`registry_url = ankra_helm_registry.<name>.url`. The counterpart of `ankra helm registries create`, `update` and `delete`.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Same as `name`.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				MarkdownDescription: "Registry name, unique in the organisation. Changing it replaces the registry.",
				Required:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
				Validators:          []validator.String{configurationPathNameValidator{}, helmReservedNameValidator{}},
			},
			"url": schema.StringAttribute{
				MarkdownDescription: "Registry URL: `oci://...` for an OCI registry, `https://...` for an HTTP chart repository. " +
					"Without a trailing slash, which the platform strips. Changing it replaces the registry.",
				Required:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"kind": schema.StringAttribute{
				MarkdownDescription: "`oci` or `http`, from the URL scheme.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"credential_name": schema.StringAttribute{
				MarkdownDescription: "Name of the `ankra_helm_registry_credential` used to pull from a private registry. " +
					"The platform cannot change it in place, so changing it replaces the registry.",
				Optional:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"exclude_charts": schema.ListAttribute{
				MarkdownDescription: "Charts to leave out of the catalogue. HTTP registries only. Changing it replaces the registry.",
				Optional:            true,
				ElementType:         types.StringType,
				PlanModifiers:       []planmodifier.List{listplanmodifier.RequiresReplace()},
			},
			"read_job_interval": schema.Int64Attribute{
				MarkdownDescription: "Seconds between automatic background syncs of the chart index: `0` disables them, " +
					"otherwise between 60 and 86400. The platform default is 120.",
				Optional:      true,
				Computed:      true,
				PlanModifiers: []planmodifier.Int64{int64planmodifier.UseStateForUnknown()},
			},
			"last_sync_error": schema.StringAttribute{
				MarkdownDescription: "Why the most recent index sync failed, when it did.",
				Computed:            true,
			},
			"project_id": helmProjectIDAttribute("registry"),
		},
	}
}

func (registryResource *helmRegistryResource) ValidateConfig(ctx context.Context, request resource.ValidateConfigRequest, response *resource.ValidateConfigResponse) {
	var config helmRegistryModel
	response.Diagnostics.Append(request.Config.Get(ctx, &config)...)
	if response.Diagnostics.HasError() {
		return
	}
	if !config.ReadJobInterval.IsNull() && !config.ReadJobInterval.IsUnknown() {
		interval := config.ReadJobInterval.ValueInt64()
		if interval != 0 && (interval < helmRegistryMinimumReadJobInterval || interval > helmRegistryMaximumReadJobInterval) {
			response.Diagnostics.AddAttributeError(path.Root("read_job_interval"), "Invalid read_job_interval",
				fmt.Sprintf("read_job_interval must be 0 (disabled) or between %d and %d seconds, got %d.",
					helmRegistryMinimumReadJobInterval, helmRegistryMaximumReadJobInterval, interval))
		}
	}
	if config.URL.IsNull() || config.URL.IsUnknown() {
		return
	}
	if strings.HasSuffix(config.URL.ValueString(), "/") {
		response.Diagnostics.AddAttributeError(path.Root("url"), "Trailing slash in url",
			"The platform stores registry URLs without a trailing slash, so the configured value would never match; remove it.")
	}
	if client.HelmRegistryKind(config.URL.ValueString()) == "oci" && !config.ExcludeCharts.IsNull() {
		response.Diagnostics.AddAttributeError(path.Root("exclude_charts"), "exclude_charts is not supported for OCI registries",
			"The platform only filters charts for HTTP registries; remove exclude_charts or use an https:// URL.")
	}
}

func (registryResource *helmRegistryResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	apiClient := registryResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var plan helmRegistryModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}

	apiClient = resourceProjectClient(ctx, apiClient, plan.ProjectID, &response.Diagnostics)
	if apiClient == nil {
		return
	}

	err := apiClient.CreateHelmRegistry(ctx, client.HelmRegistrySpecification{
		Name:           plan.Name.ValueString(),
		URL:            plan.URL.ValueString(),
		ExcludeCharts:  stringsFromList(ctx, plan.ExcludeCharts, &response.Diagnostics),
		CredentialName: stringPointer(plan.CredentialName),
		ProjectID:      helmProjectIDBody(apiClient, plan.ProjectID),
	})
	if err != nil {
		response.Diagnostics.AddError("Unable to create Helm registry", err.Error())
		return
	}

	desiredInterval := plan.ReadJobInterval
	// The registry exists from here on. State is written from the plan before
	// the read-back, so a failed read taints the registry instead of
	// orphaning it, which would make the next apply fail on its own name.
	plan.ID = plan.Name
	plan.Kind = types.StringValue(client.HelmRegistryKind(plan.URL.ValueString()))
	plan.LastSyncError = types.StringNull()
	if plan.ReadJobInterval.IsUnknown() {
		plan.ReadJobInterval = types.Int64Null()
	}
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
	response.Diagnostics.Append(trackScope(ctx, apiClient, response.Private)...)
	if response.Diagnostics.HasError() {
		return
	}
	if !registryResource.refresh(ctx, apiClient, &plan, &response.Diagnostics) {
		return
	}
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}

	if desiredInterval.IsNull() || desiredInterval.IsUnknown() || desiredInterval.Equal(plan.ReadJobInterval) {
		return
	}
	if err := apiClient.UpdateHelmRegistry(ctx, plan.Name.ValueString(), desiredInterval.ValueInt64()); err != nil {
		// A warning, not an error: an error would taint the registry and the
		// next apply would replace it, dropping its chart index. Terraform
		// requires the configured value in state after apply, so it is
		// recorded; the next refresh reads the platform's interval back and the
		// plan retries the update in place.
		response.Diagnostics.AddWarning("Unable to set the Helm registry sync interval",
			fmt.Sprintf("The registry was created with the platform's sync interval of %d seconds, but setting read_job_interval "+
				"to %d failed; the next plan shows the difference and applies it in place: %s",
				plan.ReadJobInterval.ValueInt64(), desiredInterval.ValueInt64(), err.Error()))
	}
	plan.ReadJobInterval = desiredInterval
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
}

func (registryResource *helmRegistryResource) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	apiClient := registryResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state helmRegistryModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	apiClient = resourceProjectClient(ctx, apiClient, state.ProjectID, &response.Diagnostics)
	if apiClient == nil {
		return
	}

	detail, err := apiClient.GetHelmRegistry(ctx, state.Name.ValueString())
	if removeWhenNotFound(ctx, err, apiClient, response) {
		return
	}
	if err != nil {
		response.Diagnostics.AddError("Unable to read Helm registry", err.Error())
		return
	}
	if helmRegistryTargetChanged(state, detail) {
		removeWhenTargetChanged(ctx, apiClient, response)
		return
	}
	helmRegistryApplyDetail(&state, detail)
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
	response.Diagnostics.Append(trackScope(ctx, apiClient, response.Private)...)
}

func (registryResource *helmRegistryResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	apiClient := registryResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var plan, state helmRegistryModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	apiClient = resourceProjectClient(ctx, apiClient, state.ProjectID, &response.Diagnostics)
	if apiClient == nil {
		return
	}

	if !plan.ReadJobInterval.IsUnknown() && !plan.ReadJobInterval.IsNull() && !plan.ReadJobInterval.Equal(state.ReadJobInterval) {
		if err := apiClient.UpdateHelmRegistry(ctx, plan.Name.ValueString(), plan.ReadJobInterval.ValueInt64()); err != nil {
			response.Diagnostics.AddError("Unable to update Helm registry", err.Error())
			return
		}
	}
	if !registryResource.refresh(ctx, apiClient, &plan, &response.Diagnostics) {
		return
	}
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
}

func (registryResource *helmRegistryResource) Delete(ctx context.Context, request resource.DeleteRequest, response *resource.DeleteResponse) {
	apiClient := registryResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state helmRegistryModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	apiClient = resourceProjectClient(ctx, apiClient, state.ProjectID, &response.Diagnostics)
	if apiClient == nil {
		return
	}

	err := apiClient.DeleteHelmRegistry(ctx, state.Name.ValueString())
	if err == nil || deletedWhenNotFound(ctx, err, apiClient, request.Private, &response.Diagnostics) || response.Diagnostics.HasError() {
		return
	}
	response.Diagnostics.AddError("Unable to delete Helm registry", err.Error())
}

// ImportState imports by registry name.
func (registryResource *helmRegistryResource) ImportState(ctx context.Context, request resource.ImportStateRequest, response *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("name"), request, response)
}

// refresh reads the registry back into model, reporting whether it could.
func (registryResource *helmRegistryResource) refresh(ctx context.Context, apiClient *client.Client, model *helmRegistryModel, diagnostics *diag.Diagnostics) bool {
	detail, err := apiClient.GetHelmRegistry(ctx, model.Name.ValueString())
	if err != nil {
		diagnostics.AddError("Unable to read Helm registry", err.Error())
		return false
	}
	helmRegistryApplyDetail(model, detail)
	return true
}

// helmProjectIDAttribute is the optional project a Helm registry or
// credential is created in. The platform does not return it, so it is kept
// as configured; every request for the resource is scoped to that project.
func helmProjectIDAttribute(subject string) schema.StringAttribute {
	return schema.StringAttribute{
		MarkdownDescription: "Project to create the " + subject + " in, by ID or slug, instead of the provider's project. Every request for this " + subject +
			" is then made in that project. The platform does not report a " + subject + "'s project, so this is kept as configured and a move " +
			"made outside Terraform is not detected; it is null after an import. Changing it replaces the " + subject + ".",
		Optional:      true,
		PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
	}
}

// helmProjectIDBody is the project_id a create sends: the resolved ID of the
// resource's own project, or nil to leave the request's project in charge.
func helmProjectIDBody(apiClient *client.Client, projectID types.String) *string {
	if projectID.IsNull() || projectID.IsUnknown() {
		return nil
	}
	resolved := apiClient.ScopeProjectID()
	return &resolved
}

// helmRegistryTargetChanged is readTargetChanged for registries, whose API
// carries no identifier: the name in state now resolves to a registry with
// another URL. A registry's URL cannot change in place, so that is a
// different registry (another project's, once the request scope moved, or
// one that took the name after ours was deleted), and adopting it would let
// the next apply or destroy act on it.
func helmRegistryTargetChanged(state helmRegistryModel, detail *client.HelmRegistryDetail) bool {
	if state.URL.IsNull() || state.URL.IsUnknown() || state.ID.IsNull() || state.ID.ValueString() == "" {
		return false
	}
	return state.URL.ValueString() != detail.Registry.URL
}

// helmRegistryApplyDetail copies what the platform reports onto model. An
// empty exclude_charts list stays unset when the configuration left it out.
func helmRegistryApplyDetail(model *helmRegistryModel, detail *client.HelmRegistryDetail) {
	registry := detail.Registry
	model.ID = types.StringValue(registry.Name)
	model.Name = types.StringValue(registry.Name)
	model.URL = types.StringValue(registry.URL)
	model.Kind = types.StringValue(client.HelmRegistryKind(registry.URL))
	if registry.CredentialName != nil && *registry.CredentialName != "" {
		model.CredentialName = types.StringValue(*registry.CredentialName)
	} else {
		model.CredentialName = types.StringNull()
	}
	if len(registry.ExcludeCharts) == 0 && (model.ExcludeCharts.IsNull() || model.ExcludeCharts.IsUnknown()) {
		model.ExcludeCharts = types.ListNull(types.StringType)
	} else {
		model.ExcludeCharts = listFromStrings(registry.ExcludeCharts)
	}
	if detail.ReadJobInterval != nil {
		model.ReadJobInterval = types.Int64Value(*detail.ReadJobInterval)
	} else if model.ReadJobInterval.IsUnknown() {
		model.ReadJobInterval = types.Int64Null()
	}
	lastSyncError := detail.LastSyncError
	if lastSyncError == nil {
		lastSyncError = registry.LastSyncError
	}
	model.LastSyncError = types.StringPointerValue(lastSyncError)
}
