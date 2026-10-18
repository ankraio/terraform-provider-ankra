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
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"ankra.io/terraform-provider-ankra/internal/client"
)

var (
	_ resource.Resource                = (*costAutopilotProjectDefaultResource)(nil)
	_ resource.ResourceWithConfigure   = (*costAutopilotProjectDefaultResource)(nil)
	_ resource.ResourceWithImportState = (*costAutopilotProjectDefaultResource)(nil)
	_ resource.ResourceWithModifyPlan  = (*costAutopilotProjectDefaultResource)(nil)
)

type costAutopilotProjectDefaultResource struct {
	configuredClient
}

type costAutopilotProjectDefaultModel struct {
	ID          types.String `tfsdk:"id"`
	ProjectID   types.String `tfsdk:"project_id"`
	Defaults    types.Map    `tfsdk:"defaults"`
	ProjectName types.String `tfsdk:"project_name"`
}

// NewCostAutopilotProjectDefaultResource returns the
// ankra_cost_autopilot_project_default resource: one project's own default
// autopilot tiers, layered over the organisation's.
func NewCostAutopilotProjectDefaultResource() resource.Resource {
	return &costAutopilotProjectDefaultResource{}
}

func (projectDefault *costAutopilotProjectDefaultResource) Metadata(_ context.Context, request resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_cost_autopilot_project_default"
}

func (projectDefault *costAutopilotProjectDefaultResource) Schema(_ context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: "One project's default cost autopilot tiers, layered over the organisation's (`ankra_cost_autopilot`). " +
			"An environment kind the project leaves out follows the organisation default. The project's map replaces any earlier " +
			"one as a whole. Changes need the `billing.manage` and `clusters.write` permissions across the organisation. " +
			"Destroy removes the project's defaults, so its clusters follow the organisation's again.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "The project's ID, whichever way `project_id` names it.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"project_id": schema.StringAttribute{
				MarkdownDescription: "Project the defaults apply to, by ID or slug. Moving to another project replaces the resource; writing the same project differently (its slug instead of its ID) does not.",
				Required:            true,
				Validators:          []validator.String{costProjectReferenceValidator{}},
			},
			"defaults": schema.MapAttribute{
				MarkdownDescription: "Tier per environment kind. Kinds: `production`, `staging`, `development`, `preview`, `unknown`. " +
					"Tiers: `hands-off`, `scheduled`, `managed`, `ephemeral`. At least one.",
				ElementType: types.StringType,
				Required:    true,
				Validators:  []validator.Map{costAutopilotDefaultsValidator{}, costAutopilotProjectDefaultsNotEmpty{}},
			},
			"project_name": schema.StringAttribute{
				MarkdownDescription: "Name of the project.",
				Computed:            true,
			},
		},
	}
}

// ModifyPlan replaces the resource when project_id names another project,
// but not when it only respells the same one.
func (projectDefault *costAutopilotProjectDefaultResource) ModifyPlan(ctx context.Context, request resource.ModifyPlanRequest, response *resource.ModifyPlanResponse) {
	helmPlanProjectReplacement(ctx, projectDefault.client, request, response)
}

func (projectDefault *costAutopilotProjectDefaultResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	projectDefault.write(ctx, request.Plan, &response.State, response.Private, &response.Diagnostics)
}

func (projectDefault *costAutopilotProjectDefaultResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	projectDefault.write(ctx, request.Plan, &response.State, response.Private, &response.Diagnostics)
}

func (projectDefault *costAutopilotProjectDefaultResource) write(ctx context.Context, plan tfsdk.Plan, state *tfsdk.State, private privateStateWriter, diagnostics *diag.Diagnostics) {
	apiClient := projectDefault.requireClient(diagnostics)
	if apiClient == nil {
		return
	}
	var model costAutopilotProjectDefaultModel
	diagnostics.Append(plan.Get(ctx, &model)...)
	if diagnostics.HasError() {
		return
	}
	defaults := map[string]string{}
	diagnostics.Append(model.Defaults.ElementsAs(ctx, &defaults, false)...)
	if diagnostics.HasError() {
		return
	}
	projectID, err := apiClient.ProjectIDFor(ctx, model.ProjectID.ValueString())
	if err != nil {
		diagnostics.AddError("Unable to resolve project_id", err.Error())
		return
	}
	stored, err := apiClient.SetCostAutopilotProject(ctx, projectID, defaults)
	if err != nil {
		diagnostics.AddError("Unable to set the project's autopilot defaults",
			err.Error()+"\n\nA 404 means the project is not a live project of the organisation.")
		return
	}
	model.apply(ctx, stored, diagnostics)
	diagnostics.Append(state.Set(ctx, &model)...)
	diagnostics.Append(trackScope(ctx, apiClient, private)...)
}

func (projectDefault *costAutopilotProjectDefaultResource) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	apiClient := projectDefault.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state costAutopilotProjectDefaultModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	projectID, err := apiClient.ProjectIDFor(ctx, state.ProjectID.ValueString())
	if err != nil {
		response.Diagnostics.AddError("Unable to resolve project_id", err.Error())
		return
	}
	stored, err := apiClient.GetCostAutopilotProject(ctx, projectID)
	if removeWhenNotFound(ctx, err, apiClient, response) {
		return
	}
	if err != nil {
		response.Diagnostics.AddError("Unable to read the project's autopilot defaults", err.Error())
		return
	}
	state.apply(ctx, stored, &response.Diagnostics)
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
	response.Diagnostics.Append(trackScope(ctx, apiClient, response.Private)...)
}

func (projectDefault *costAutopilotProjectDefaultResource) Delete(ctx context.Context, request resource.DeleteRequest, response *resource.DeleteResponse) {
	apiClient := projectDefault.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state costAutopilotProjectDefaultModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	projectID, err := apiClient.ProjectIDFor(ctx, state.ProjectID.ValueString())
	if err != nil {
		response.Diagnostics.AddError("Unable to resolve project_id", err.Error())
		return
	}
	err = apiClient.ClearCostAutopilotProject(ctx, projectID)
	if err == nil || deletedWhenNotFound(ctx, err, apiClient, request.Private, &response.Diagnostics) || response.Diagnostics.HasError() {
		return
	}
	response.Diagnostics.AddError("Unable to remove the project's autopilot defaults", err.Error())
}

// ImportState imports by project ID or slug.
func (projectDefault *costAutopilotProjectDefaultResource) ImportState(ctx context.Context, request resource.ImportStateRequest, response *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), request, response)
	response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("project_id"), request.ID)...)
}

func (model *costAutopilotProjectDefaultModel) apply(ctx context.Context, stored *client.CostAutopilotProject, diagnostics *diag.Diagnostics) {
	model.ID = types.StringValue(stored.ProjectID)
	if model.ProjectID.IsNull() || model.ProjectID.IsUnknown() || model.ProjectID.ValueString() == "" {
		model.ProjectID = types.StringValue(stored.ProjectID)
	}
	model.ProjectName = types.StringValue(stored.ProjectName)
	defaults, mapDiagnostics := types.MapValueFrom(ctx, types.StringType, stored.Defaults)
	diagnostics.Append(mapDiagnostics...)
	model.Defaults = defaults
}

// costAutopilotProjectDefaultsNotEmpty refuses an empty map, which the
// platform rejects; destroy the resource to clear a project's defaults.
type costAutopilotProjectDefaultsNotEmpty struct{}

func (costAutopilotProjectDefaultsNotEmpty) Description(_ context.Context) string {
	return "must name at least one environment kind"
}

func (notEmpty costAutopilotProjectDefaultsNotEmpty) MarkdownDescription(ctx context.Context) string {
	return notEmpty.Description(ctx)
}

func (costAutopilotProjectDefaultsNotEmpty) ValidateMap(_ context.Context, request validator.MapRequest, response *validator.MapResponse) {
	if request.ConfigValue.IsNull() || request.ConfigValue.IsUnknown() {
		return
	}
	if len(request.ConfigValue.Elements()) == 0 {
		response.Diagnostics.AddAttributeError(request.Path, "Empty defaults",
			"Name at least one environment kind. To make a project follow the organisation's defaults, destroy this resource.")
	}
}

// costProjectReferenceValidator accepts a project ID or a project slug.
type costProjectReferenceValidator struct{}

func (costProjectReferenceValidator) Description(_ context.Context) string {
	return "must be a project ID or a project slug (lowercase letters, digits and \"-\", at most 63 characters)"
}

func (referenceValidator costProjectReferenceValidator) MarkdownDescription(ctx context.Context) string {
	return referenceValidator.Description(ctx)
}

func (referenceValidator costProjectReferenceValidator) ValidateString(ctx context.Context, request validator.StringRequest, response *validator.StringResponse) {
	if request.ConfigValue.IsNull() || request.ConfigValue.IsUnknown() {
		return
	}
	value := request.ConfigValue.ValueString()
	if !uuidPattern.MatchString(value) && !projectSlugPattern.MatchString(value) {
		response.Diagnostics.AddAttributeError(request.Path, "Invalid project_id",
			fmt.Sprintf("%q %s.", value, referenceValidator.Description(ctx)))
	}
}
