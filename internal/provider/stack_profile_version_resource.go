// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"ankra.io/terraform-provider-ankra/internal/client"
)

var (
	_ resource.Resource                = (*stackProfileVersionResource)(nil)
	_ resource.ResourceWithConfigure   = (*stackProfileVersionResource)(nil)
	_ resource.ResourceWithImportState = (*stackProfileVersionResource)(nil)
	_ resource.ResourceWithModifyPlan  = (*stackProfileVersionResource)(nil)
)

type stackProfileVersionResource struct {
	configuredClient
}

type stackProfileVersionDeprecationModel struct {
	Reason     types.String `tfsdk:"reason"`
	Note       types.String `tfsdk:"note"`
	References types.List   `tfsdk:"references"`
}

type stackProfileVersionModel struct {
	ID                         types.String                         `tfsdk:"id"`
	ProfileID                  types.String                         `tfsdk:"profile_id"`
	SourceClusterID            types.String                         `tfsdk:"source_cluster_id"`
	StackName                  types.String                         `tfsdk:"stack_name"`
	IncludeAddonConfigurations types.Bool                           `tfsdk:"include_addon_configurations"`
	Channel                    types.String                         `tfsdk:"channel"`
	Changelog                  types.String                         `tfsdk:"changelog"`
	Deprecation                *stackProfileVersionDeprecationModel `tfsdk:"deprecation"`
	Version                    types.Int64                          `tfsdk:"version"`
	VersionID                  types.String                         `tfsdk:"version_id"`
	CreatedAt                  types.String                         `tfsdk:"created_at"`
}

// NewStackProfileVersionResource returns the ankra_stack_profile_version
// resource, the counterpart of `ankra stack-profiles save-version`,
// `deprecate` and `undeprecate`.
func NewStackProfileVersionResource() resource.Resource {
	return &stackProfileVersionResource{}
}

func (versionResource *stackProfileVersionResource) Metadata(_ context.Context, request resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_stack_profile_version"
}

func (versionResource *stackProfileVersionResource) Schema(_ context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	createOnly := []planmodifier.String{catalogueReplaceUnlessImported()}
	response.Schema = schema.Schema{
		MarkdownDescription: "A published version of a stack profile: an immutable snapshot of `stack_name` on " +
			"`source_cluster_id`, which becomes the profile's current version. The counterpart of " +
			"`ankra stack-profiles save-version`, `deprecate` and `undeprecate`.\n\n" +
			"Versions are immutable, so changing what a version was made from publishes a new one. The platform cannot delete " +
			"a published version: destroying this resource only removes it from state, and the version stays in the " +
			"profile's history. Withdraw a version from new deployments with `deprecation` instead.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "`<profile_id>/<version>`.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"profile_id": schema.StringAttribute{
				MarkdownDescription: "Profile the version is published to.",
				Required:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"source_cluster_id": schema.StringAttribute{
				MarkdownDescription: "Cluster whose stack is snapshotted. Changing it publishes a new version.",
				Required:            true,
				PlanModifiers:       createOnly,
			},
			"stack_name": schema.StringAttribute{
				MarkdownDescription: "Stack on `source_cluster_id` that is snapshotted. Changing it publishes a new version.",
				Required:            true,
				PlanModifiers:       createOnly,
			},
			"include_addon_configurations": schema.BoolAttribute{
				MarkdownDescription: "Whether the version carries the add-ons' configuration values. Defaults to `true`.",
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(true),
				PlanModifiers:       []planmodifier.Bool{catalogueBoolReplaceUnlessImported()},
			},
			"channel": schema.StringAttribute{
				MarkdownDescription: "Release channel. Defaults to `stable`.",
				Optional:            true,
				Computed:            true,
				Default:             stringdefault.StaticString("stable"),
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"changelog": schema.StringAttribute{
				MarkdownDescription: "What changed in this version. Only sent when the version is published: a later change " +
					"is kept in state and does not publish another version, because a published version is immutable.",
				Optional: true,
			},
			"deprecation": schema.SingleNestedAttribute{
				MarkdownDescription: "Withdraws the version from new deployments. Unset clears it. The platform refuses to " +
					"deprecate the profile's current version, which a newly published version always is, so set this only " +
					"after another version has been made current.",
				Optional: true,
				Attributes: map[string]schema.Attribute{
					"reason": schema.StringAttribute{
						MarkdownDescription: "`incompatibility`, `critical_bug` or `cve`.",
						Required:            true,
						Validators:          []validator.String{catalogueDeprecationReasonValidator},
					},
					"note": schema.StringAttribute{
						MarkdownDescription: "What deployers should do instead (at most 2000 characters).",
						Optional:            true,
					},
					"references": schema.ListAttribute{
						MarkdownDescription: "Advisory identifiers or URLs (at most 20).",
						ElementType:         types.StringType,
						Optional:            true,
					},
				},
			},
			"version": schema.Int64Attribute{
				MarkdownDescription: "The version number the platform assigned.",
				Computed:            true,
				PlanModifiers:       []planmodifier.Int64{int64planmodifier.UseStateForUnknown()},
			},
			"version_id": schema.StringAttribute{
				MarkdownDescription: "Identifier of the version row.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"created_at": schema.StringAttribute{
				MarkdownDescription: "When the version was published.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
		},
	}
}

// ModifyPlan refuses a deprecation on a version about to be published: the
// version becomes current, and the platform refuses to deprecate the current
// version.
func (versionResource *stackProfileVersionResource) ModifyPlan(ctx context.Context, request resource.ModifyPlanRequest, response *resource.ModifyPlanResponse) {
	if request.Plan.Raw.IsNull() || !request.State.Raw.IsNull() {
		return
	}
	var deprecation *stackProfileVersionDeprecationModel
	response.Diagnostics.Append(request.Plan.GetAttribute(ctx, path.Root("deprecation"), &deprecation)...)
	if deprecation != nil {
		response.Diagnostics.AddAttributeError(path.Root("deprecation"), "A new version cannot be deprecated",
			"Publishing makes this version the profile's current version, and the platform refuses to deprecate the "+
				"current version. Publish it first, make another version current with the profile's current_version, "+
				"then add the deprecation.")
	}
}

func (versionResource *stackProfileVersionResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	apiClient := versionResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var plan stackProfileVersionModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}

	profileID := plan.ProfileID.ValueString()
	saved, err := apiClient.SaveStackProfileVersion(ctx, profileID, client.StackProfileVersionSave{
		SourceClusterID:            plan.SourceClusterID.ValueString(),
		StackName:                  plan.StackName.ValueString(),
		IncludeAddonConfigurations: plan.IncludeAddonConfigurations.ValueBool(),
		Channel:                    stringPointer(plan.Channel),
		Changelog:                  stringPointer(plan.Changelog),
	})
	if err != nil {
		response.Diagnostics.AddError("Unable to publish stack profile version", err.Error())
		return
	}
	for _, warning := range saved.Warnings {
		response.Diagnostics.AddWarning("Stack profile version published with a warning", warning)
	}
	deprecation := plan.Deprecation
	stackProfileVersionToState(saved, &plan)
	plan.Deprecation = nil
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
	if response.Diagnostics.HasError() || deprecation == nil {
		return
	}

	if !versionResource.applyDeprecation(ctx, apiClient, profileID, saved.Version, deprecation, &response.Diagnostics) {
		return
	}
	plan.Deprecation = deprecation
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
}

func (versionResource *stackProfileVersionResource) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	apiClient := versionResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state stackProfileVersionModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}

	version, err := apiClient.GetStackProfileVersion(ctx, state.ProfileID.ValueString(), state.Version.ValueInt64())
	if removeWhenNotFound(ctx, err, response) {
		return
	}
	if err != nil {
		response.Diagnostics.AddError("Unable to read stack profile version", err.Error())
		return
	}
	if readTargetChanged(state.VersionID, version.ID) {
		response.State.RemoveResource(ctx)
		return
	}
	stackProfileVersionToState(version, &state)
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
}

// Update only ever changes the deprecation notice, or a create-only input
// after an import, which lives in state: every other configurable attribute
// publishes a new version.
func (versionResource *stackProfileVersionResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	apiClient := versionResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var plan, state stackProfileVersionModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}

	profileID := state.ProfileID.ValueString()
	version := state.Version.ValueInt64()
	switch {
	case plan.Deprecation != nil && !stackProfileDeprecationsEqual(plan.Deprecation, state.Deprecation):
		if !versionResource.applyDeprecation(ctx, apiClient, profileID, version, plan.Deprecation, &response.Diagnostics) {
			return
		}
	case plan.Deprecation == nil && state.Deprecation != nil:
		if err := apiClient.UndeprecateStackProfileVersion(ctx, profileID, version); err != nil {
			response.Diagnostics.AddError("Unable to clear the stack profile version's deprecation", err.Error())
			return
		}
	}
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
}

// Delete removes the version from state only: the platform keeps every
// published version.
func (versionResource *stackProfileVersionResource) Delete(_ context.Context, _ resource.DeleteRequest, _ *resource.DeleteResponse) {
}

// ImportState imports by "<profile_id>/<version>".
func (versionResource *stackProfileVersionResource) ImportState(ctx context.Context, request resource.ImportStateRequest, response *resource.ImportStateResponse) {
	profileID, versionText, found := strings.Cut(request.ID, "/")
	version, parseError := strconv.ParseInt(versionText, 10, 64)
	if !found || profileID == "" || parseError != nil || version < 1 {
		response.Diagnostics.AddError("Invalid import ID",
			fmt.Sprintf("Expected \"<profile_id>/<version>\" with a positive version number, got %q.", request.ID))
		return
	}
	response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("profile_id"), profileID)...)
	response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("version"), version)...)
}

func (versionResource *stackProfileVersionResource) applyDeprecation(ctx context.Context, apiClient *client.Client, profileID string, version int64, deprecation *stackProfileVersionDeprecationModel, diagnostics *diag.Diagnostics) bool {
	references := []string{}
	if !deprecation.References.IsNull() && !deprecation.References.IsUnknown() {
		for _, element := range deprecation.References.Elements() {
			if text, ok := element.(types.String); ok && !text.IsNull() && !text.IsUnknown() {
				references = append(references, text.ValueString())
			}
		}
	}
	if err := apiClient.DeprecateStackProfileVersion(ctx, profileID, version, client.StackProfileVersionDeprecate{
		Reason:     deprecation.Reason.ValueString(),
		Note:       stringPointer(deprecation.Note),
		References: references,
	}); err != nil {
		diagnostics.AddError("Unable to deprecate stack profile version", err.Error())
		return false
	}
	return true
}

func stackProfileDeprecationsEqual(left, right *stackProfileVersionDeprecationModel) bool {
	if left == nil || right == nil {
		return left == right
	}
	return left.Reason.Equal(right.Reason) && left.Note.Equal(right.Note) && left.References.Equal(right.References)
}

// stackProfileVersionToState copies what the platform reports. The snapshot
// inputs are not reported and stay as configured. An empty reference list
// stays null when the configuration has none.
func stackProfileVersionToState(version *client.StackProfileVersion, state *stackProfileVersionModel) {
	state.ID = types.StringValue(state.ProfileID.ValueString() + "/" + strconv.FormatInt(version.Version, 10))
	state.Version = types.Int64Value(version.Version)
	state.VersionID = types.StringValue(version.ID)
	state.CreatedAt = types.StringValue(version.CreatedAt)
	state.Channel = types.StringValue(version.Channel)
	if state.Changelog.IsNull() || state.Changelog.IsUnknown() {
		state.Changelog = types.StringNull()
		if version.Changelog != nil {
			state.Changelog = optionalString(*version.Changelog)
		}
	}
	if version.Deprecation == nil {
		state.Deprecation = nil
		return
	}
	references := types.ListNull(types.StringType)
	if len(version.Deprecation.References) > 0 || (state.Deprecation != nil && !state.Deprecation.References.IsNull()) {
		references = listFromStrings(append([]string{}, version.Deprecation.References...))
	}
	note := types.StringNull()
	if version.Deprecation.Note != nil {
		note = optionalString(*version.Deprecation.Note)
	}
	state.Deprecation = &stackProfileVersionDeprecationModel{
		Reason:     types.StringValue(version.Deprecation.Reason),
		Note:       note,
		References: references,
	}
}
