// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64default"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/listdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
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
// never chose (enginekit/cisettings Defaults), and what destroy restores.
var ciSettingsDefaults = client.OrganisationCISettingsUpdate{
	BuildFallback:          "platform_builders",
	MaxParallelRuns:        16,
	MaxParallelSteps:       8,
	AllowedImagePrefixes:   []string{},
	ArtifactRetentionDays:  30,
	CacheRetentionDays:     14,
	RunRetentionDays:       90,
	ImageGate:              "app",
	IgnoreUnfixed:          true,
	EgressAllowedCIDRs:     []string{},
	AlwaysRunStages:        []string{},
	FullRunOnDefaultBranch: false,
	SkipMarkers:            "except_default_branch",
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

func (settings *ciSettingsResource) Schema(_ context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	emptyList := listdefault.StaticValue(types.ListValueMust(types.StringType, []attr.Value{}))
	response.Schema = schema.Schema{
		MarkdownDescription: "The organisation's Ankra Pipelines settings: where pipeline steps run, scheduler shares, image policy, " +
			"retention and the image gate. The counterpart of `ankra org ci-settings set`. One per organisation; changes need " +
			"organisation admin. Every attribute defaults to the platform default, and destroy restores those defaults.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Always `ci_settings`.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"ci_cluster_id": schema.StringAttribute{
				MarkdownDescription: "Cluster pipeline steps run on. Unset leaves the organisation without a pipeline cluster.",
				Optional:            true,
			},
			"ci_cluster_name": schema.StringAttribute{
				MarkdownDescription: "Name of the pipeline cluster; null when none is set or it was deleted.",
				Computed:            true,
			},
			"ci_build_fallback": schema.StringAttribute{
				MarkdownDescription: "`platform_builders` (default) keeps Ankra's build cluster available when the organisation's cannot build; `none` refuses the build.",
				Optional:            true, Computed: true,
				Default:    stringdefault.StaticString(ciSettingsDefaults.BuildFallback),
				Validators: []validator.String{organisationStringOneOf{values: []string{"platform_builders", "none"}}},
			},
			"ci_max_parallel_runs": schema.Int64Attribute{
				MarkdownDescription: "Runs in flight at once, 1 to 64. Defaults to 16.",
				Optional:            true, Computed: true,
				Default: int64default.StaticInt64(ciSettingsDefaults.MaxParallelRuns),
			},
			"ci_max_parallel_steps": schema.Int64Attribute{
				MarkdownDescription: "Steps in flight at once, 1 to 256. Defaults to 8.",
				Optional:            true, Computed: true,
				Default: int64default.StaticInt64(ciSettingsDefaults.MaxParallelSteps),
			},
			"ci_allowed_image_prefixes": schema.ListAttribute{
				MarkdownDescription: "Image prefixes steps may name; empty (default) allows any image.",
				ElementType:         types.StringType,
				Optional:            true, Computed: true,
				Default: emptyList,
			},
			"ci_artifact_retention_days": schema.Int64Attribute{
				MarkdownDescription: "Artifact retention, 1 to 365 days. Defaults to 30.",
				Optional:            true, Computed: true,
				Default: int64default.StaticInt64(ciSettingsDefaults.ArtifactRetentionDays),
			},
			"ci_cache_retention_days": schema.Int64Attribute{
				MarkdownDescription: "Cache retention, 1 to 365 days. Defaults to 14.",
				Optional:            true, Computed: true,
				Default: int64default.StaticInt64(ciSettingsDefaults.CacheRetentionDays),
			},
			"ci_run_retention_days": schema.Int64Attribute{
				MarkdownDescription: "Run history retention, 7 to 365 days. Defaults to 90.",
				Optional:            true, Computed: true,
				Default: int64default.StaticInt64(ciSettingsDefaults.RunRetentionDays),
			},
			"ci_image_gate": schema.StringAttribute{
				MarkdownDescription: "Image findings that block a publish: `app` (default, fixable critical and high findings in application dependencies), `all` or `off`.",
				Optional:            true, Computed: true,
				Default:    stringdefault.StaticString(ciSettingsDefaults.ImageGate),
				Validators: []validator.String{organisationStringOneOf{values: []string{"app", "all", "off"}}},
			},
			"ci_ignore_unfixed": schema.BoolAttribute{
				MarkdownDescription: "Organisation floor for the image gate's ignore_unfixed. Defaults to `true`.",
				Optional:            true, Computed: true,
				Default: booldefault.StaticBool(ciSettingsDefaults.IgnoreUnfixed),
			},
			"ci_egress_allowed_cidrs": schema.ListAttribute{
				MarkdownDescription: "CIDRs pipeline steps may reach. Defaults to empty.",
				ElementType:         types.StringType,
				Optional:            true, Computed: true,
				Default: emptyList,
			},
			"ci_always_run_stages": schema.ListAttribute{
				MarkdownDescription: "Stages that run whatever their path filters say. Defaults to empty.",
				ElementType:         types.StringType,
				Optional:            true, Computed: true,
				Default: emptyList,
			},
			"ci_full_run_on_default_branch": schema.BoolAttribute{
				MarkdownDescription: "Whether a push to the default branch runs every stage. Defaults to `false`.",
				Optional:            true, Computed: true,
				Default: booldefault.StaticBool(ciSettingsDefaults.FullRunOnDefaultBranch),
			},
			"ci_skip_markers": schema.StringAttribute{
				MarkdownDescription: "Where a `[skip ci]` marker skips a push: `everywhere`, `except_default_branch` (default) or `never`.",
				Optional:            true, Computed: true,
				Default:    stringdefault.StaticString(ciSettingsDefaults.SkipMarkers),
				Validators: []validator.String{organisationStringOneOf{values: []string{"everywhere", "except_default_branch", "never"}}},
			},
			"platform_builds_enabled": schema.BoolAttribute{
				MarkdownDescription: "Whether Ankra has granted the organisation its build cluster. Read-only.",
				Computed:            true,
			},
		},
	}
}

func (settings *ciSettingsResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	settings.write(ctx, request.Plan, &response.State, &response.Diagnostics)
}

func (settings *ciSettingsResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	settings.write(ctx, request.Plan, &response.State, &response.Diagnostics)
}

func (settings *ciSettingsResource) write(ctx context.Context, plan tfsdk.Plan, state *tfsdk.State, diagnostics *diag.Diagnostics) {
	apiClient := settings.requireClient(diagnostics)
	if apiClient == nil {
		return
	}
	var model ciSettingsModel
	diagnostics.Append(plan.Get(ctx, &model)...)
	if diagnostics.HasError() {
		return
	}
	update := client.OrganisationCISettingsUpdate{
		ClusterID:              stringPointer(model.ClusterID),
		BuildFallback:          model.BuildFallback.ValueString(),
		MaxParallelRuns:        model.MaxParallelRuns.ValueInt64(),
		MaxParallelSteps:       model.MaxParallelSteps.ValueInt64(),
		AllowedImagePrefixes:   organisationNonNilStrings(stringsFromList(ctx, model.AllowedImagePrefixes, diagnostics)),
		ArtifactRetentionDays:  model.ArtifactRetentionDays.ValueInt64(),
		CacheRetentionDays:     model.CacheRetentionDays.ValueInt64(),
		RunRetentionDays:       model.RunRetentionDays.ValueInt64(),
		ImageGate:              model.ImageGate.ValueString(),
		IgnoreUnfixed:          model.IgnoreUnfixed.ValueBool(),
		EgressAllowedCIDRs:     organisationNonNilStrings(stringsFromList(ctx, model.EgressAllowedCIDRs, diagnostics)),
		AlwaysRunStages:        organisationNonNilStrings(stringsFromList(ctx, model.AlwaysRunStages, diagnostics)),
		FullRunOnDefaultBranch: model.FullRunOnDefaultBranch.ValueBool(),
		SkipMarkers:            model.SkipMarkers.ValueString(),
	}
	if diagnostics.HasError() {
		return
	}
	stored, err := apiClient.UpdateOrganisationCISettings(ctx, update)
	if err != nil {
		diagnostics.AddError("Unable to write CI settings", err.Error())
		return
	}
	model.ID = types.StringValue(ciSettingsID)
	ciSettingsApply(&model, stored)
	diagnostics.Append(state.Set(ctx, &model)...)
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
	state.ID = types.StringValue(ciSettingsID)
	ciSettingsApply(&state, stored)
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
}

// Delete restores the platform defaults and clears the pipeline cluster.
func (settings *ciSettingsResource) Delete(ctx context.Context, _ resource.DeleteRequest, response *resource.DeleteResponse) {
	apiClient := settings.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	if _, err := apiClient.UpdateOrganisationCISettings(ctx, ciSettingsDefaults); err != nil {
		response.Diagnostics.AddError("Unable to restore the default CI settings", err.Error())
	}
}

// ImportState accepts any id: there is one CI settings object per
// organisation.
func (settings *ciSettingsResource) ImportState(ctx context.Context, _ resource.ImportStateRequest, response *resource.ImportStateResponse) {
	response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("id"), ciSettingsID)...)
}

func ciSettingsApply(model *ciSettingsModel, stored *client.OrganisationCISettings) {
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
