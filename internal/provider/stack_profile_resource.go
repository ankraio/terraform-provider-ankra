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
	_ resource.Resource                = (*stackProfileResource)(nil)
	_ resource.ResourceWithConfigure   = (*stackProfileResource)(nil)
	_ resource.ResourceWithImportState = (*stackProfileResource)(nil)
)

type stackProfileResource struct {
	configuredClient
}

type stackProfileModel struct {
	ID                         types.String `tfsdk:"id"`
	Name                       types.String `tfsdk:"name"`
	Description                types.String `tfsdk:"description"`
	LogoURL                    types.String `tfsdk:"logo_url"`
	Category                   types.String `tfsdk:"category"`
	Tags                       types.List   `tfsdk:"tags"`
	Visibility                 types.String `tfsdk:"visibility"`
	SourceClusterID            types.String `tfsdk:"source_cluster_id"`
	StackName                  types.String `tfsdk:"stack_name"`
	IncludeAddonConfigurations types.Bool   `tfsdk:"include_addon_configurations"`
	Changelog                  types.String `tfsdk:"changelog"`
	CurrentVersion             types.Int64  `tfsdk:"current_version"`
	LatestVersion              types.Int64  `tfsdk:"latest_version"`
	OrganisationID             types.String `tfsdk:"organisation_id"`
	HasUploadedLogo            types.Bool   `tfsdk:"has_uploaded_logo"`
}

// NewStackProfileResource returns the ankra_stack_profile resource, the
// counterpart of `ankra stack-profiles create|update|delete` and
// `ankra stack-profiles set-current-version`.
func NewStackProfileResource() resource.Resource {
	return &stackProfileResource{}
}

func (profileResource *stackProfileResource) Metadata(_ context.Context, request resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_stack_profile"
}

func (profileResource *stackProfileResource) Schema(_ context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	createOnly := []planmodifier.String{catalogueReplaceUnlessImported()}
	response.Schema = schema.Schema{
		MarkdownDescription: "A stack profile: a reusable, versioned definition of a stack, published to the organisation's " +
			"catalogue. Creating it snapshots `stack_name` on `source_cluster_id` as version 1. The counterpart of " +
			"`ankra stack-profiles create`, `update`, `delete` and `set-current-version`.\n\n" +
			"Publish further versions with `ankra_stack_profile_version`, share the profile with " +
			"`ankra_stack_profile_share`, and upload a logo with `ankra_stack_profile_logo`. `source_cluster_id`, " +
			"`stack_name`, `include_addon_configurations` and `changelog` only shape version 1: they are not read back, and " +
			"changing the first three replaces the profile. Builder drafts and suggestions are interactive editing sessions " +
			"and are not managed here.\n\n" +
			"**Replacing or destroying a profile deletes it for everyone**: every version goes, and the profile disappears for " +
			"every organisation it is shared with and, when `visibility = \"public\"`, from the public catalogue. Deployments " +
			"already made from it keep running.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Identifier of the profile.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				MarkdownDescription: "Profile name.",
				Required:            true,
			},
			"description": schema.StringAttribute{
				MarkdownDescription: "Free-text description.",
				Optional:            true,
			},
			"logo_url": schema.StringAttribute{
				MarkdownDescription: "External http(s) logo URL. A profile carries either this or an uploaded logo " +
					"(`ankra_stack_profile_logo`), never both: do not set it alongside one.",
				Optional: true,
			},
			"category": schema.StringAttribute{
				MarkdownDescription: "Catalogue category. Defaults to `general`.",
				Optional:            true,
				Computed:            true,
				Default:             stringdefault.StaticString("general"),
			},
			"tags": schema.ListAttribute{
				MarkdownDescription: "Catalogue tags.",
				ElementType:         types.StringType,
				Optional:            true,
			},
			"visibility": schema.StringAttribute{
				MarkdownDescription: "`organisation` (the default) or `public`. Making a profile public re-scans every version " +
					"for secrets.",
				Optional:   true,
				Computed:   true,
				Default:    stringdefault.StaticString("organisation"),
				Validators: []validator.String{catalogueStackProfileVisibilityValidator},
			},
			"source_cluster_id": schema.StringAttribute{
				MarkdownDescription: "Cluster whose stack is snapshotted as version 1. Changing it replaces the profile.",
				Required:            true,
				PlanModifiers:       createOnly,
			},
			"stack_name": schema.StringAttribute{
				MarkdownDescription: "Stack on `source_cluster_id` snapshotted as version 1. Changing it replaces the profile.",
				Required:            true,
				PlanModifiers:       createOnly,
			},
			"include_addon_configurations": schema.BoolAttribute{
				MarkdownDescription: "Whether version 1 carries the add-ons' configuration values. Defaults to `true`.",
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(true),
				PlanModifiers:       []planmodifier.Bool{catalogueBoolReplaceUnlessImported()},
			},
			"changelog": schema.StringAttribute{
				MarkdownDescription: "Changelog of version 1. Only used when the profile is created; a later change is " +
					"kept in state and sent nowhere, because a published version is immutable.",
				Optional: true,
			},
			"current_version": schema.Int64Attribute{
				MarkdownDescription: "The version new deployments get. Publishing a version makes it current; set this to pin " +
					"another one. The platform refuses a deprecated version, and a new profile only has version 1.",
				Optional: true,
				Computed: true,
			},
			"latest_version": schema.Int64Attribute{
				MarkdownDescription: "The newest published version.",
				Computed:            true,
			},
			"organisation_id": schema.StringAttribute{
				MarkdownDescription: "Organisation that owns the profile.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"has_uploaded_logo": schema.BoolAttribute{
				MarkdownDescription: "Whether the profile has an uploaded logo.",
				Computed:            true,
			},
		},
	}
}

func (profileResource *stackProfileResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	apiClient := profileResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var plan stackProfileModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}

	tags := stringsFromList(ctx, plan.Tags, &response.Diagnostics)
	if response.Diagnostics.HasError() {
		return
	}
	if tags == nil {
		tags = []string{}
	}
	created, err := apiClient.CreateStackProfile(ctx, client.StackProfileCreate{
		Name:                       plan.Name.ValueString(),
		Description:                stringPointer(plan.Description),
		LogoURL:                    stringPointer(plan.LogoURL),
		Category:                   stringPointer(plan.Category),
		Tags:                       tags,
		Visibility:                 stringPointer(plan.Visibility),
		SourceClusterID:            plan.SourceClusterID.ValueString(),
		StackName:                  plan.StackName.ValueString(),
		IncludeAddonConfigurations: plan.IncludeAddonConfigurations.ValueBool(),
		Changelog:                  stringPointer(plan.Changelog),
	})
	if err != nil {
		response.Diagnostics.AddError("Unable to create stack profile", err.Error())
		return
	}
	for _, warning := range created.Warnings {
		response.Diagnostics.AddWarning("Stack profile created with a warning", warning)
	}
	requestedVersion := plan.CurrentVersion
	stackProfileToState(created.Profile, &plan)
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
	response.Diagnostics.Append(trackScope(ctx, apiClient, response.Private)...)
	if response.Diagnostics.HasError() || requestedVersion.IsUnknown() || requestedVersion.IsNull() ||
		requestedVersion.ValueInt64() == created.Profile.CurrentVersion {
		return
	}
	if err := apiClient.SetStackProfileCurrentVersion(ctx, created.Profile.ID, requestedVersion.ValueInt64()); err != nil {
		response.Diagnostics.AddError("Unable to set the stack profile's current version", err.Error())
		return
	}
	plan.CurrentVersion = requestedVersion
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
	response.Diagnostics.Append(trackScope(ctx, apiClient, response.Private)...)
}

func (profileResource *stackProfileResource) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	apiClient := profileResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state stackProfileModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}

	detail, err := apiClient.GetStackProfile(ctx, state.ID.ValueString())
	if removeWhenNotFound(ctx, err, apiClient, response) {
		return
	}
	if err != nil {
		response.Diagnostics.AddError("Unable to read stack profile", err.Error())
		return
	}
	stackProfileToState(detail.Profile, &state)
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
	response.Diagnostics.Append(trackScope(ctx, apiClient, response.Private)...)
}

func (profileResource *stackProfileResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	apiClient := profileResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var plan, state stackProfileModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}

	tags := stringsFromList(ctx, plan.Tags, &response.Diagnostics)
	if response.Diagnostics.HasError() {
		return
	}
	if tags == nil {
		tags = []string{}
	}
	update := client.StackProfileUpdate{
		Name:       stringPointer(plan.Name),
		Category:   stringPointer(plan.Category),
		Tags:       tags,
		Visibility: stringPointer(plan.Visibility),
	}
	update.Description = stackProfileClearableValue(plan.Description, state.Description)
	update.LogoURL = stackProfileClearableValue(plan.LogoURL, state.LogoURL)
	profileID := state.ID.ValueString()
	if err := apiClient.UpdateStackProfile(ctx, profileID, update); err != nil {
		response.Diagnostics.AddError("Unable to update stack profile", err.Error())
		return
	}
	if !plan.CurrentVersion.IsUnknown() && !plan.CurrentVersion.IsNull() && !plan.CurrentVersion.Equal(state.CurrentVersion) {
		if err := apiClient.SetStackProfileCurrentVersion(ctx, profileID, plan.CurrentVersion.ValueInt64()); err != nil {
			response.Diagnostics.AddError("Unable to set the stack profile's current version", err.Error())
			return
		}
	}

	detail, err := apiClient.GetStackProfile(ctx, profileID)
	if err != nil {
		response.Diagnostics.AddError("Unable to read stack profile", err.Error())
		return
	}
	stackProfileToState(detail.Profile, &plan)
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
	response.Diagnostics.Append(trackScope(ctx, apiClient, response.Private)...)
}

func (profileResource *stackProfileResource) Delete(ctx context.Context, request resource.DeleteRequest, response *resource.DeleteResponse) {
	apiClient := profileResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state stackProfileModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	err := apiClient.DeleteStackProfile(ctx, state.ID.ValueString())
	if err == nil || deletedWhenNotFound(ctx, err, apiClient, request.Private, &response.Diagnostics) || response.Diagnostics.HasError() {
		return
	}
	response.Diagnostics.AddError("Unable to delete stack profile", err.Error())
}

// ImportState imports by profile ID. The version 1 inputs are not read
// back: after an import they are taken from configuration without replacing
// the profile.
func (profileResource *stackProfileResource) ImportState(ctx context.Context, request resource.ImportStateRequest, response *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), request, response)
}

// stackProfileClearableValue is the PATCH value for a field the platform
// only clears when sent "": the configured value, "" when the configuration
// removed a value that was set, and nothing (left as it is) otherwise. An
// unchanged logo_url is never sent, because sending it deletes an uploaded
// logo.
func stackProfileClearableValue(planned, prior types.String) *string {
	if planned.Equal(prior) {
		return nil
	}
	if planned.IsNull() {
		cleared := ""
		return &cleared
	}
	return stringPointer(planned)
}

// stackProfileToState copies what the platform reports. The version 1 inputs
// are not reported and stay as configured. An empty tag list stays null when
// the configuration has none.
func stackProfileToState(profile *client.StackProfile, state *stackProfileModel) {
	state.ID = types.StringValue(profile.ID)
	state.Name = types.StringValue(profile.Name)
	state.Description = types.StringNull()
	if profile.Description != nil {
		state.Description = optionalString(*profile.Description)
	}
	state.LogoURL = types.StringNull()
	if profile.LogoURL != nil {
		state.LogoURL = optionalString(*profile.LogoURL)
	}
	state.Category = types.StringValue(profile.Category)
	if len(profile.Tags) == 0 && (state.Tags.IsNull() || state.Tags.IsUnknown()) {
		state.Tags = types.ListNull(types.StringType)
	} else {
		state.Tags = listFromStrings(append([]string{}, profile.Tags...))
	}
	state.Visibility = types.StringValue(profile.Visibility)
	state.CurrentVersion = types.Int64Value(profile.CurrentVersion)
	state.LatestVersion = types.Int64Value(profile.LatestVersion)
	state.OrganisationID = types.StringValue(profile.OrganisationID)
	state.HasUploadedLogo = types.BoolValue(profile.HasUploadedLogo)
}
