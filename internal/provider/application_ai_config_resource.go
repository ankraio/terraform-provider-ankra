// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/diag"
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
	_ resource.Resource                   = (*applicationAIConfigResource)(nil)
	_ resource.ResourceWithConfigure      = (*applicationAIConfigResource)(nil)
	_ resource.ResourceWithImportState    = (*applicationAIConfigResource)(nil)
	_ resource.ResourceWithValidateConfig = (*applicationAIConfigResource)(nil)
)

// applicationAILaneNames are the lanes the platform knows
// (gatewayprofiles.LaneOrder).
var applicationAILaneNames = []string{"pull_request", "code", "actions", "skills", "previews"}

type applicationAIConfigResource struct {
	configuredClient
}

type applicationAILaneModel struct {
	Enabled                  types.Bool   `tfsdk:"enabled"`
	Model                    types.String `tfsdk:"model"`
	Autonomy                 types.String `tfsdk:"autonomy"`
	Branches                 types.List   `tfsdk:"branches"`
	IgnorePaths              types.List   `tfsdk:"ignore_paths"`
	SkipDrafts               types.Bool   `tfsdk:"skip_drafts"`
	SkillIDs                 types.List   `tfsdk:"skill_ids"`
	CustomInstructions       types.String `tfsdk:"custom_instructions"`
	MaxRunsPerDay            types.Int64  `tfsdk:"max_runs_per_day"`
	MaxInputCharacters       types.Int64  `tfsdk:"max_input_characters"`
	MaxReviewsPerPullRequest types.Int64  `tfsdk:"max_reviews_per_pull_request"`
}

type applicationAIConfigModel struct {
	ApplicationID types.String                      `tfsdk:"application_id"`
	Lanes         map[string]applicationAILaneModel `tfsdk:"lanes"`
	RepoFullName  types.String                      `tfsdk:"repo_full_name"`
}

// NewApplicationAIConfigResource returns the ankra_application_ai_config
// resource, the counterpart of `ankra application ai-config set|clear`.
func NewApplicationAIConfigResource() resource.Resource {
	return &applicationAIConfigResource{}
}

func (configResource *applicationAIConfigResource) Metadata(_ context.Context, request resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_application_ai_config"
}

func (configResource *applicationAIConfigResource) Schema(_ context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	optionalText := func(description string) schema.StringAttribute {
		return schema.StringAttribute{MarkdownDescription: description, Optional: true}
	}
	optionalList := func(description string) schema.ListAttribute {
		return schema.ListAttribute{MarkdownDescription: description, Optional: true, ElementType: types.StringType}
	}
	optionalNumber := func(description string) schema.Int64Attribute {
		return schema.Int64Attribute{MarkdownDescription: description, Optional: true}
	}
	response.Schema = schema.Schema{
		MarkdownDescription: "Per-application overrides of the organisation's Ankra AI lanes: which lanes run on the application's repository, " +
			"on which model and under which budget. The counterpart of `ankra application ai-config set`. " +
			"Only the lanes listed in `lanes` are managed; each one listed replaces that lane's override wholesale, and a field left " +
			"unset inherits from the organisation. Removing a lane, or destroying the resource, returns it to the organisation default. " +
			"The application's repository must be reachable through the Ankra GitHub App.",
		Attributes: map[string]schema.Attribute{
			"application_id": schema.StringAttribute{
				MarkdownDescription: "Identifier of the application. Changing it replaces the resource.",
				Required:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"lanes": schema.MapNestedAttribute{
				MarkdownDescription: "Lane overrides keyed by lane: `pull_request`, `code`, `actions`, `skills` or `previews`.",
				Required:            true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"enabled": schema.BoolAttribute{
							MarkdownDescription: "Whether the lane runs on this application. Unset follows the organisation.",
							Optional:            true,
						},
						"model": optionalText("Model the lane runs on."),
						"autonomy": schema.StringAttribute{
							MarkdownDescription: "`comment` or `propose_fix`.",
							Optional:            true,
							Validators:          []validator.String{applicationStringOneOf{values: []string{"comment", "propose_fix"}}},
						},
						"branches":                     optionalList("Branches the lane runs on; empty means every branch."),
						"ignore_paths":                 optionalList("Path globs the lane ignores. An empty list clears the platform's default list."),
						"skip_drafts":                  schema.BoolAttribute{MarkdownDescription: "Whether draft pull requests are skipped.", Optional: true},
						"skill_ids":                    optionalList("Skills the lane uses."),
						"custom_instructions":          optionalText("Review guidance appended to the lane's prompt."),
						"max_runs_per_day":             optionalNumber("Runs per day the lane may start; 0 is no cap."),
						"max_input_characters":         optionalNumber("Input characters one run may send; 0 is no cap."),
						"max_reviews_per_pull_request": optionalNumber("Automatic reviews one pull request may receive; 0 is no cap."),
					},
				},
			},
			"repo_full_name": schema.StringAttribute{
				MarkdownDescription: "Repository the lanes are configured on, as `owner/name`.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
		},
	}
}

func (configResource *applicationAIConfigResource) ValidateConfig(ctx context.Context, request resource.ValidateConfigRequest, response *resource.ValidateConfigResponse) {
	var lanes types.Map
	response.Diagnostics.Append(request.Config.GetAttribute(ctx, path.Root("lanes"), &lanes)...)
	if response.Diagnostics.HasError() || lanes.IsNull() || lanes.IsUnknown() {
		return
	}
	for name := range lanes.Elements() {
		if !applicationIsAILane(name) {
			response.Diagnostics.AddAttributeError(path.Root("lanes").AtMapKey(name), "Unknown AI lane",
				fmt.Sprintf("%q is not a lane; use one of: %s.", name, strings.Join(applicationAILaneNames, ", ")))
		}
	}
}

func applicationIsAILane(name string) bool {
	for _, lane := range applicationAILaneNames {
		if lane == name {
			return true
		}
	}
	return false
}

func applicationOptionalStrings(ctx context.Context, list types.List, diagnostics *diag.Diagnostics) *[]string {
	if list.IsNull() || list.IsUnknown() {
		return nil
	}
	values := stringsFromList(ctx, list, diagnostics)
	return &values
}

func applicationLanePatch(ctx context.Context, name string, lane applicationAILaneModel, diagnostics *diag.Diagnostics) client.ApplicationAILanePatch {
	settings := &client.ApplicationAILaneSettingsPatch{
		Model:              stringPointer(lane.Model),
		Autonomy:           stringPointer(lane.Autonomy),
		Branches:           applicationOptionalStrings(ctx, lane.Branches, diagnostics),
		IgnorePaths:        applicationOptionalStrings(ctx, lane.IgnorePaths, diagnostics),
		SkipDrafts:         boolPointer(lane.SkipDrafts),
		SkillIDs:           applicationOptionalStrings(ctx, lane.SkillIDs, diagnostics),
		CustomInstructions: stringPointer(lane.CustomInstructions),
	}
	budget := client.ApplicationAILaneBudget{
		MaxRunsPerDay:            int64Pointer(lane.MaxRunsPerDay),
		MaxInputCharacters:       int64Pointer(lane.MaxInputCharacters),
		MaxReviewsPerPullRequest: int64Pointer(lane.MaxReviewsPerPullRequest),
	}
	if budget.MaxRunsPerDay != nil || budget.MaxInputCharacters != nil || budget.MaxReviewsPerPullRequest != nil {
		settings.Budget = &budget
	}
	return client.ApplicationAILanePatch{
		Lane:     name,
		Override: true,
		Enabled:  boolPointer(lane.Enabled),
		Settings: settings,
	}
}

// applicationLaneFromEffective refreshes the fields the configuration sets from
// what the lane effectively runs with; unset fields stay unset, since they
// inherit and the effective value is the organisation's.
func applicationLaneFromEffective(prior applicationAILaneModel, lane client.ApplicationAILane) applicationAILaneModel {
	effective := lane.Effective
	refreshed := prior
	if !prior.Enabled.IsNull() {
		refreshed.Enabled = types.BoolValue(lane.Enabled)
	}
	if !prior.Model.IsNull() {
		refreshed.Model = types.StringValue(effective.Model)
	}
	if !prior.Autonomy.IsNull() {
		refreshed.Autonomy = types.StringValue(effective.Autonomy)
	}
	if !prior.Branches.IsNull() {
		refreshed.Branches = listFromStrings(nonNilStrings(effective.Branches))
	}
	if !prior.IgnorePaths.IsNull() {
		refreshed.IgnorePaths = listFromStrings(nonNilStrings(effective.IgnorePaths))
	}
	if !prior.SkipDrafts.IsNull() {
		refreshed.SkipDrafts = types.BoolValue(effective.SkipDrafts)
	}
	if !prior.SkillIDs.IsNull() {
		refreshed.SkillIDs = listFromStrings(nonNilStrings(effective.SkillIDs))
	}
	if !prior.CustomInstructions.IsNull() {
		refreshed.CustomInstructions = types.StringValue(effective.CustomInstructions)
	}
	if !prior.MaxRunsPerDay.IsNull() {
		refreshed.MaxRunsPerDay = types.Int64Value(effective.MaxRunsPerDay)
	}
	if !prior.MaxInputCharacters.IsNull() {
		refreshed.MaxInputCharacters = types.Int64Value(effective.MaxInputCharacters)
	}
	if !prior.MaxReviewsPerPullRequest.IsNull() {
		refreshed.MaxReviewsPerPullRequest = types.Int64Value(effective.MaxReviewsPerPullRequest)
	}
	return refreshed
}

func nonNilStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

func emptyApplicationAILane() applicationAILaneModel {
	return applicationAILaneModel{
		Enabled:                  types.BoolNull(),
		Model:                    types.StringNull(),
		Autonomy:                 types.StringNull(),
		Branches:                 types.ListNull(types.StringType),
		IgnorePaths:              types.ListNull(types.StringType),
		SkipDrafts:               types.BoolNull(),
		SkillIDs:                 types.ListNull(types.StringType),
		CustomInstructions:       types.StringNull(),
		MaxRunsPerDay:            types.Int64Null(),
		MaxInputCharacters:       types.Int64Null(),
		MaxReviewsPerPullRequest: types.Int64Null(),
	}
}

// write sends the planned lanes as overrides and returns every lane that was
// managed before but is no longer planned to the organisation default.
func (configResource *applicationAIConfigResource) write(ctx context.Context, plan *applicationAIConfigModel, previous map[string]applicationAILaneModel, diagnostics *diag.Diagnostics) {
	update := client.ApplicationAIConfigUpdate{Lanes: []client.ApplicationAILanePatch{}}
	names := make([]string, 0, len(plan.Lanes))
	for name := range plan.Lanes {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		update.Lanes = append(update.Lanes, applicationLanePatch(ctx, name, plan.Lanes[name], diagnostics))
	}
	released := []string{}
	for name := range previous {
		if _, kept := plan.Lanes[name]; !kept {
			released = append(released, name)
		}
	}
	sort.Strings(released)
	for _, name := range released {
		update.Lanes = append(update.Lanes, client.ApplicationAILanePatch{Lane: name, Override: false})
	}
	if diagnostics.HasError() {
		return
	}
	configuration, err := configResource.client.UpdateApplicationAIConfig(ctx, plan.ApplicationID.ValueString(), update)
	if err != nil {
		diagnostics.AddError("Unable to write application AI configuration", err.Error())
		return
	}
	plan.RepoFullName = types.StringValue(configuration.Binding.RepoFullName)
}

func (configResource *applicationAIConfigResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	if configResource.requireClient(&response.Diagnostics) == nil {
		return
	}
	var plan applicationAIConfigModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}
	configResource.write(ctx, &plan, nil, &response.Diagnostics)
	if response.Diagnostics.HasError() {
		return
	}
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
}

func (configResource *applicationAIConfigResource) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	apiClient := configResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state applicationAIConfigModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	configuration, err := apiClient.GetApplicationAIConfig(ctx, state.ApplicationID.ValueString())
	if removeWhenNotFound(ctx, err, response) {
		return
	}
	if err != nil {
		response.Diagnostics.AddError("Unable to read application AI configuration", err.Error())
		return
	}

	lanes := map[string]applicationAILaneModel{}
	for _, lane := range configuration.Lanes {
		if !lane.OverriddenForApplication {
			continue
		}
		prior, managed := state.Lanes[lane.Lane]
		if !managed {
			if state.Lanes != nil {
				continue
			}
			prior = emptyApplicationAILane()
			prior.Enabled = types.BoolValue(lane.Enabled)
		}
		lanes[lane.Lane] = applicationLaneFromEffective(prior, lane)
	}
	state.Lanes = lanes
	state.RepoFullName = types.StringValue(configuration.Binding.RepoFullName)
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
}

func (configResource *applicationAIConfigResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	if configResource.requireClient(&response.Diagnostics) == nil {
		return
	}
	var plan, state applicationAIConfigModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	configResource.write(ctx, &plan, state.Lanes, &response.Diagnostics)
	if response.Diagnostics.HasError() {
		return
	}
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
}

func (configResource *applicationAIConfigResource) Delete(ctx context.Context, request resource.DeleteRequest, response *resource.DeleteResponse) {
	apiClient := configResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state applicationAIConfigModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() || len(state.Lanes) == 0 {
		return
	}
	update := client.ApplicationAIConfigUpdate{Lanes: []client.ApplicationAILanePatch{}}
	for name := range state.Lanes {
		update.Lanes = append(update.Lanes, client.ApplicationAILanePatch{Lane: name, Override: false})
	}
	sort.Slice(update.Lanes, func(left, right int) bool { return update.Lanes[left].Lane < update.Lanes[right].Lane })
	_, err := apiClient.UpdateApplicationAIConfig(ctx, state.ApplicationID.ValueString(), update)
	if err != nil && !client.IsNotFound(err) {
		response.Diagnostics.AddError("Unable to reset application AI configuration", err.Error())
	}
}

// ImportState imports by application id. Every lane the application overrides
// is imported with its `enabled` value only, because the platform reports the
// effective settings and not which of them the override sets.
func (configResource *applicationAIConfigResource) ImportState(ctx context.Context, request resource.ImportStateRequest, response *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("application_id"), request, response)
}
