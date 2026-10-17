// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/listplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"ankra.io/terraform-provider-ankra/internal/client"
)

var (
	_ resource.Resource                = (*ciSettingsResource)(nil)
	_ resource.ResourceWithConfigure   = (*ciSettingsResource)(nil)
	_ resource.ResourceWithImportState = (*ciSettingsResource)(nil)
)

// ciSettingsID is the fixed id of the organisation's one CI settings object.
const ciSettingsID = "ci_settings"

// ciSettingsDefaults is what the platform answers for an organisation that
// never chose (enginekit/cisettings Defaults). Destroy writes these back for
// the fields the configuration set. ci_cluster_id has no entry: clearing the
// pipeline cluster stalls every pipeline, so destroy leaves it as it is.
var ciSettingsDefaults = map[string]any{
	"ci_build_fallback":             "platform_builders",
	"ci_max_parallel_runs":          16,
	"ci_max_parallel_steps":         8,
	"ci_allowed_image_prefixes":     []string{},
	"ci_artifact_retention_days":    30,
	"ci_cache_retention_days":       14,
	"ci_run_retention_days":         90,
	"ci_image_gate":                 "app",
	"ci_ignore_unfixed":             true,
	"ci_egress_allowed_cidrs":       []string{},
	"ci_always_run_stages":          []string{},
	"ci_full_run_on_default_branch": false,
	"ci_skip_markers":               "except_default_branch",
}

type ciSettingsResource struct {
	configuredClient
}

type ciSettingsModel struct {
	ID                     types.String `tfsdk:"id"`
	ClusterID              types.String `tfsdk:"ci_cluster_id"`
	ClusterName            types.String `tfsdk:"ci_cluster_name"`
	BuildFallback          types.String `tfsdk:"ci_build_fallback"`
	MaxParallelRuns        types.Int64  `tfsdk:"ci_max_parallel_runs"`
	MaxParallelSteps       types.Int64  `tfsdk:"ci_max_parallel_steps"`
	AllowedImagePrefixes   types.List   `tfsdk:"ci_allowed_image_prefixes"`
	ArtifactRetentionDays  types.Int64  `tfsdk:"ci_artifact_retention_days"`
	CacheRetentionDays     types.Int64  `tfsdk:"ci_cache_retention_days"`
	RunRetentionDays       types.Int64  `tfsdk:"ci_run_retention_days"`
	ImageGate              types.String `tfsdk:"ci_image_gate"`
	IgnoreUnfixed          types.Bool   `tfsdk:"ci_ignore_unfixed"`
	EgressAllowedCIDRs     types.List   `tfsdk:"ci_egress_allowed_cidrs"`
	AlwaysRunStages        types.List   `tfsdk:"ci_always_run_stages"`
	FullRunOnDefaultBranch types.Bool   `tfsdk:"ci_full_run_on_default_branch"`
	SkipMarkers            types.String `tfsdk:"ci_skip_markers"`
	PlatformBuildsEnabled  types.Bool   `tfsdk:"platform_builds_enabled"`
}

// NewCISettingsResource returns the ankra_ci_settings resource, the
// counterpart of `ankra org ci-settings set`.
func NewCISettingsResource() resource.Resource {
	return &ciSettingsResource{}
}

func (settings *ciSettingsResource) Metadata(_ context.Context, request resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_ci_settings"
}

func ciSettingsString(description string, validators ...validator.String) schema.StringAttribute {
	return schema.StringAttribute{
		MarkdownDescription: description, Optional: true, Computed: true, Validators: validators,
		PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
	}
}

func ciSettingsInt64(description string) schema.Int64Attribute {
	return schema.Int64Attribute{
		MarkdownDescription: description, Optional: true, Computed: true,
		PlanModifiers: []planmodifier.Int64{int64planmodifier.UseStateForUnknown()},
	}
}

func ciSettingsBool(description string) schema.BoolAttribute {
	return schema.BoolAttribute{
		MarkdownDescription: description, Optional: true, Computed: true,
		PlanModifiers: []planmodifier.Bool{boolplanmodifier.UseStateForUnknown()},
	}
}

func ciSettingsList(description string) schema.ListAttribute {
	return schema.ListAttribute{
		MarkdownDescription: description, ElementType: types.StringType, Optional: true, Computed: true,
		PlanModifiers: []planmodifier.List{listplanmodifier.UseStateForUnknown()},
	}
}

func (settings *ciSettingsResource) Schema(_ context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: "The organisation's Ankra Pipelines settings: where pipeline steps run, scheduler shares, image policy, " +
			"retention and the image gate. The counterpart of `ankra org ci-settings set`. One per organisation; changes need " +
			"organisation admin.\n\n" +
			"Only the attributes you set are managed. An attribute left unset keeps whatever the organisation holds (set in the " +
			"portal or by the CLI) and is read back as a computed value; removing an attribute from configuration stops managing it " +
			"and leaves its value in place. Destroy resets the attributes the configuration set to the platform defaults, except " +
			"`ci_cluster_id`, which is left as it is because clearing it would stall every pipeline.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Always `ci_settings`.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"ci_cluster_id": ciSettingsString("Cluster pipeline steps run on. Unset leaves the organisation's choice alone; the provider never clears it."),
			"ci_cluster_name": schema.StringAttribute{
				MarkdownDescription: "Name of the pipeline cluster; null when none is set or it was deleted.",
				Computed:            true,
			},
			"ci_build_fallback": ciSettingsString("`platform_builders` (the platform default) keeps Ankra's build cluster available when the organisation's cannot build; `none` refuses the build.",
				organisationStringOneOf{values: []string{"platform_builders", "none"}}),
			"ci_max_parallel_runs":      ciSettingsInt64("Runs in flight at once, 1 to 64. Platform default 16."),
			"ci_max_parallel_steps":     ciSettingsInt64("Steps in flight at once, 1 to 256. Platform default 8."),
			"ci_allowed_image_prefixes": ciSettingsList("Image prefixes steps may name; empty (the platform default) allows any image."),
			"ci_artifact_retention_days": ciSettingsInt64("Artifact retention, 1 to 365 days. Platform default 30."),
			"ci_cache_retention_days":    ciSettingsInt64("Cache retention, 1 to 365 days. Platform default 14."),
			"ci_run_retention_days":      ciSettingsInt64("Run history retention, 7 to 365 days. Platform default 90."),
			"ci_image_gate": ciSettingsString("Image findings that block a publish: `app` (the platform default: fixable critical and high findings in application dependencies), `all` or `off`.",
				organisationStringOneOf{values: []string{"app", "all", "off"}}),
			"ci_ignore_unfixed":             ciSettingsBool("Organisation floor for the image gate's ignore_unfixed. Platform default `true`."),
			"ci_egress_allowed_cidrs":       ciSettingsList("CIDRs pipeline steps may reach. Platform default empty."),
			"ci_always_run_stages":          ciSettingsList("Stages that run whatever their path filters say. Platform default empty."),
			"ci_full_run_on_default_branch": ciSettingsBool("Whether a push to the default branch runs every stage. Platform default `false`."),
			"ci_skip_markers": ciSettingsString("Where a `[skip ci]` marker skips a push: `everywhere`, `except_default_branch` (the platform default) or `never`.",
				organisationStringOneOf{values: []string{"everywhere", "except_default_branch", "never"}}),
			"platform_builds_enabled": schema.BoolAttribute{
				MarkdownDescription: "Whether Ankra has granted the organisation its build cluster. Read-only.",
				Computed:            true,
			},
		},
	}
}

func (settings *ciSettingsResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	settings.write(ctx, request.Config, &response.State, response.Private, &response.Diagnostics)
}

func (settings *ciSettingsResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	settings.write(ctx, request.Config, &response.State, response.Private, &response.Diagnostics)
}

// write sends only the fields the configuration sets, so everything else
// keeps the organisation's value, then stores what the platform holds.
func (settings *ciSettingsResource) write(ctx context.Context, config tfsdk.Config, state *tfsdk.State, private privateStateWriter, diagnostics *diag.Diagnostics) {
	apiClient := settings.requireClient(diagnostics)
	if apiClient == nil {
		return
	}
	var model ciSettingsModel
	diagnostics.Append(config.Get(ctx, &model)...)
	if diagnostics.HasError() {
		return
	}
	body := ciSettingsBody(ctx, model, diagnostics)
	if diagnostics.HasError() {
		return
	}

	var stored *client.OrganisationCISettings
	var err error
	if len(body) == 0 {
		stored, err = apiClient.GetOrganisationCISettings(ctx)
	} else {
		stored, err = apiClient.UpdateOrganisationCISettings(ctx, body)
	}
	if err != nil {
		diagnostics.AddError("Unable to write CI settings", err.Error())
		return
	}
	ciSettingsApply(&model, stored)
	diagnostics.Append(state.Set(ctx, &model)...)
	diagnostics.Append(organisationRecordManagedKeys(ctx, body, private)...)
	diagnostics.Append(trackScope(ctx, apiClient, private)...)
}

func (settings *ciSettingsResource) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	apiClient := settings.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state ciSettingsModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	stored, err := apiClient.GetOrganisationCISettings(ctx)
	if err != nil {
		response.Diagnostics.AddError("Unable to read CI settings", err.Error())
		return
	}
	ciSettingsApply(&state, stored)
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
	response.Diagnostics.Append(trackScope(ctx, apiClient, response.Private)...)
}

// Delete resets the fields the configuration set to the platform defaults
// and leaves the rest, and the pipeline cluster, alone.
func (settings *ciSettingsResource) Delete(ctx context.Context, request resource.DeleteRequest, response *resource.DeleteResponse) {
	apiClient := settings.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	body := organisationResetBody(organisationManagedKeys(ctx, request.Private), ciSettingsDefaults)
	if len(body) == 0 {
		return
	}
	if _, err := apiClient.UpdateOrganisationCISettings(ctx, body); err != nil {
		response.Diagnostics.AddError("Unable to restore the default CI settings", err.Error())
	}
}

// ImportState accepts any id: there is one CI settings object per
// organisation. An imported resource manages nothing until the next apply
// records which attributes the configuration sets.
func (settings *ciSettingsResource) ImportState(ctx context.Context, _ resource.ImportStateRequest, response *resource.ImportStateResponse) {
	response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("id"), ciSettingsID)...)
}

// ciSettingsBody names the configured fields only. A null in configuration
// is indistinguishable from an unset attribute, so the provider never sends
// a null ci_cluster_id.
func ciSettingsBody(ctx context.Context, model ciSettingsModel, diagnostics *diag.Diagnostics) map[string]any {
	body := map[string]any{}
	putString := func(key string, value types.String) {
		if !value.IsNull() && !value.IsUnknown() {
			body[key] = value.ValueString()
		}
	}
	putInt64 := func(key string, value types.Int64) {
		if !value.IsNull() && !value.IsUnknown() {
			body[key] = value.ValueInt64()
		}
	}
	putBool := func(key string, value types.Bool) {
		if !value.IsNull() && !value.IsUnknown() {
			body[key] = value.ValueBool()
		}
	}
	putList := func(key string, value types.List) {
		if !value.IsNull() && !value.IsUnknown() {
			body[key] = organisationNonNilStrings(stringsFromList(ctx, value, diagnostics))
		}
	}
	putString("ci_cluster_id", model.ClusterID)
	putString("ci_build_fallback", model.BuildFallback)
	putInt64("ci_max_parallel_runs", model.MaxParallelRuns)
	putInt64("ci_max_parallel_steps", model.MaxParallelSteps)
	putList("ci_allowed_image_prefixes", model.AllowedImagePrefixes)
	putInt64("ci_artifact_retention_days", model.ArtifactRetentionDays)
	putInt64("ci_cache_retention_days", model.CacheRetentionDays)
	putInt64("ci_run_retention_days", model.RunRetentionDays)
	putString("ci_image_gate", model.ImageGate)
	putBool("ci_ignore_unfixed", model.IgnoreUnfixed)
	putList("ci_egress_allowed_cidrs", model.EgressAllowedCIDRs)
	putList("ci_always_run_stages", model.AlwaysRunStages)
	putBool("ci_full_run_on_default_branch", model.FullRunOnDefaultBranch)
	putString("ci_skip_markers", model.SkipMarkers)
	return body
}

func ciSettingsApply(model *ciSettingsModel, stored *client.OrganisationCISettings) {
	model.ID = types.StringValue(ciSettingsID)
	model.ClusterID = types.StringPointerValue(stored.ClusterID)
	model.ClusterName = types.StringPointerValue(stored.ClusterName)
	model.BuildFallback = types.StringValue(stored.BuildFallback)
	model.MaxParallelRuns = types.Int64Value(stored.MaxParallelRuns)
	model.MaxParallelSteps = types.Int64Value(stored.MaxParallelSteps)
	model.AllowedImagePrefixes = listFromStrings(organisationNonNilStrings(stored.AllowedImagePrefixes))
	model.ArtifactRetentionDays = types.Int64Value(stored.ArtifactRetentionDays)
	model.CacheRetentionDays = types.Int64Value(stored.CacheRetentionDays)
	model.RunRetentionDays = types.Int64Value(stored.RunRetentionDays)
	model.ImageGate = types.StringValue(stored.ImageGate)
	model.IgnoreUnfixed = types.BoolValue(stored.IgnoreUnfixed)
	model.EgressAllowedCIDRs = listFromStrings(organisationNonNilStrings(stored.EgressAllowedCIDRs))
	model.AlwaysRunStages = listFromStrings(organisationNonNilStrings(stored.AlwaysRunStages))
	model.FullRunOnDefaultBranch = types.BoolValue(stored.FullRunOnDefaultBranch)
	model.SkipMarkers = types.StringValue(stored.SkipMarkers)
	model.PlatformBuildsEnabled = types.BoolValue(stored.PlatformBuildsEnabled)
}
