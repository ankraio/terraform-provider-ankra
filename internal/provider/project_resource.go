// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"ankra.io/terraform-provider-ankra/internal/client"
)

var (
	_ resource.Resource                = (*projectResource)(nil)
	_ resource.ResourceWithConfigure   = (*projectResource)(nil)
	_ resource.ResourceWithImportState = (*projectResource)(nil)
)

// projectDefaultSlug is reserved for the organisation's default project.
const projectDefaultSlug = "default"

const (
	projectNameMaximum        = 128
	projectDescriptionMaximum = 2048
)

const projectsDisabledResolution = "Projects are not enabled for this organisation. They are turned on per organisation; until then the " +
	"organisation has only its default project and no project can be created, read or deleted."

type projectResource struct {
	configuredClient
}

type projectModel struct {
	ID          types.String `tfsdk:"id"`
	Name        types.String `tfsdk:"name"`
	Slug        types.String `tfsdk:"slug"`
	Description types.String `tfsdk:"description"`
	IsDefault   types.Bool   `tfsdk:"is_default"`
	CreatedAt   types.String `tfsdk:"created_at"`
}

// NewProjectResource returns the ankra_project resource, the counterpart of
// `ankra project create`, `rename` and `delete`.
func NewProjectResource() resource.Resource {
	return &projectResource{}
}

func (project *projectResource) Metadata(_ context.Context, request resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_project"
}

func (project *projectResource) Schema(_ context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: "A project of the organisation: a team-sized scope for clusters, applications, environments, stack profiles, " +
			"pipeline repositories and AI automations, each of which belongs to exactly one project. The counterpart of `ankra project " +
			"create`, `rename` and `delete`. Managing projects needs the `projects.manage` permission, and projects must be turned on " +
			"for the organisation.\n\n" +
			"Projects belong to the organisation, so this resource needs no provider `project_id`, however many projects the " +
			"organisation has. Resources that live inside a project do: once the organisation has a second project, set the " +
			"provider's `project_id` (an ID or a slug, such as `default`) for them, or give each of them its own `project_id` " +
			"where it offers one.\n\n" +
			"**Destroy** deletes the project. The platform refuses to delete a project that still holds resources (move them with " +
			"`ankra project move` first) and never deletes the organisation's default project. Members whose current project it was " +
			"are pointed back at the default project, and tokens pinned to it stop working.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Project ID.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				MarkdownDescription: fmt.Sprintf("Display name, at most %d characters. Changing it renames the project in place.", projectNameMaximum),
				Required:            true,
				Validators:          []validator.String{projectNameValidator{}},
			},
			"slug": schema.StringAttribute{
				MarkdownDescription: "Lowercase letters, digits and hyphens, at most 63 characters, starting with a letter or digit. " +
					"`default` is reserved. The platform never changes a slug (it ends up in kubeconfig entries, CLI selections and " +
					"deep links), so changing it replaces the project, which the platform refuses while the project holds resources.",
				Required:      true,
				Validators:    []validator.String{projectSlugValidator{}},
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"description": schema.StringAttribute{
				MarkdownDescription: fmt.Sprintf("Description, at most %d characters. Unset clears it.", projectDescriptionMaximum),
				Optional:            true,
				Validators:          []validator.String{projectDescriptionValidator{}},
			},
			"is_default": schema.BoolAttribute{
				MarkdownDescription: "Whether this is the organisation's default project, which cannot be deleted. Only an imported default project reads `true`.",
				Computed:            true,
				PlanModifiers:       []planmodifier.Bool{boolplanmodifier.UseStateForUnknown()},
			},
			"created_at": schema.StringAttribute{
				MarkdownDescription: "When the project was created.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
		},
	}
}

func (project *projectResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	apiClient := project.requireOrganisationClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var plan projectModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}
	created, createError := apiClient.CreateProject(ctx, client.ProjectCreate{
		Name:        plan.Name.ValueString(),
		Slug:        plan.Slug.ValueString(),
		Description: plan.Description.ValueString(),
	})
	if createError != nil {
		response.Diagnostics.AddError("Unable to create project", projectErrorDetail(createError))
		return
	}
	projectApply(&plan, created)
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
	response.Diagnostics.Append(trackScope(ctx, apiClient, response.Private)...)
}

func (project *projectResource) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	apiClient := project.requireOrganisationClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state projectModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	stored, readError := apiClient.GetProject(ctx, state.ID.ValueString())
	if client.IsProjectsDisabled(readError) {
		response.Diagnostics.AddError("Projects are not enabled", projectsDisabledResolution)
		return
	}
	if removeWhenNotFound(ctx, readError, apiClient, response) {
		return
	}
	if readError != nil {
		response.Diagnostics.AddError("Unable to read project", readError.Error())
		return
	}
	projectApply(&state, stored)
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
	response.Diagnostics.Append(trackScope(ctx, apiClient, response.Private)...)
}

// Update renames the project or changes its description. An unset
// description is sent as empty, which clears one set elsewhere.
func (project *projectResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	apiClient := project.requireOrganisationClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state, plan projectModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}
	name := plan.Name.ValueString()
	description := plan.Description.ValueString()
	updated, updateError := apiClient.UpdateProject(ctx, state.ID.ValueString(), client.ProjectUpdate{Name: &name, Description: &description})
	if updateError != nil {
		response.Diagnostics.AddError("Unable to update project", projectErrorDetail(updateError))
		return
	}
	plan.ID = state.ID
	projectApply(&plan, updated)
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
}

func (project *projectResource) Delete(ctx context.Context, request resource.DeleteRequest, response *resource.DeleteResponse) {
	apiClient := project.requireOrganisationClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state projectModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	if state.IsDefault.ValueBool() {
		response.Diagnostics.AddError("The default project cannot be deleted",
			fmt.Sprintf("Project %q is the organisation's default project, which the platform never deletes. Run `terraform state rm` "+
				"to stop managing it instead.", state.Slug.ValueString()))
		return
	}
	deleteError := apiClient.DeleteProject(ctx, state.ID.ValueString())
	if deleteError == nil {
		return
	}
	if client.IsProjectsDisabled(deleteError) {
		response.Diagnostics.AddError("Projects are not enabled", projectsDisabledResolution)
		return
	}
	if deletedWhenNotFound(ctx, deleteError, apiClient, request.Private, &response.Diagnostics) || response.Diagnostics.HasError() {
		return
	}
	response.Diagnostics.AddError("Unable to delete project", projectErrorDetail(deleteError))
}

// ImportState imports a project by ID or by slug.
func (project *projectResource) ImportState(ctx context.Context, request resource.ImportStateRequest, response *resource.ImportStateResponse) {
	reference := strings.TrimSpace(request.ID)
	if uuidPattern.MatchString(reference) {
		response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("id"), reference)...)
		return
	}
	apiClient := project.requireOrganisationClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	projectID, resolveError := apiClient.ProjectIDFor(ctx, reference)
	if resolveError != nil {
		response.Diagnostics.AddError("Unable to import project", resolveError.Error())
		return
	}
	response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("id"), projectID)...)
}

// projectApply copies the platform's project into the model. An empty
// description stays null when the configuration leaves it unset.
func projectApply(model *projectModel, stored *client.Project) {
	model.ID = types.StringValue(stored.ID)
	model.Name = types.StringValue(stored.Name)
	model.Slug = types.StringValue(stored.Slug)
	if stored.Description != "" || !model.Description.IsNull() {
		model.Description = types.StringValue(stored.Description)
	}
	model.IsDefault = types.BoolValue(stored.IsDefault)
	model.CreatedAt = types.StringValue(stored.CreatedAt)
}

// projectErrorDetail explains the platform's refusals a configuration can
// run into; any other error is reported as it came.
func projectErrorDetail(requestError error) string {
	if client.IsProjectsDisabled(requestError) {
		return projectsDisabledResolution
	}
	return requestError.Error()
}

// projectSlugValidator rejects at plan time a slug the platform refuses.
type projectSlugValidator struct{}

func (projectSlugValidator) Description(_ context.Context) string {
	return "value must be lowercase letters, digits and hyphens, at most 63 characters, start with a letter or digit, and not be `default` or a UUID"
}

func (slugValidator projectSlugValidator) MarkdownDescription(ctx context.Context) string {
	return slugValidator.Description(ctx)
}

func (slugValidator projectSlugValidator) ValidateString(ctx context.Context, request validator.StringRequest, response *validator.StringResponse) {
	if request.ConfigValue.IsNull() || request.ConfigValue.IsUnknown() {
		return
	}
	slug := request.ConfigValue.ValueString()
	switch {
	case !projectSlugPattern.MatchString(slug):
		response.Diagnostics.AddAttributeError(request.Path, "Invalid project slug",
			fmt.Sprintf("%q is refused by the platform: %s.", slug, slugValidator.Description(ctx)))
	case slug == projectDefaultSlug:
		response.Diagnostics.AddAttributeError(request.Path, "Reserved project slug",
			"The slug `default` is reserved for the organisation's default project. Import that project instead of creating it.")
	case uuidPattern.MatchString(slug):
		response.Diagnostics.AddAttributeError(request.Path, "Invalid project slug",
			"A slug shaped like a UUID would be read as a project ID wherever a project is named by ID or slug.")
	}
}

// projectNameValidator requires a name the platform stores as written: not
// empty, no surrounding whitespace (the platform trims it, which would plan a
// change on every run), and within the length bound.
type projectNameValidator struct{}

func (projectNameValidator) Description(_ context.Context) string {
	return fmt.Sprintf("value must be 1 to %d characters with no leading or trailing whitespace", projectNameMaximum)
}

func (nameValidator projectNameValidator) MarkdownDescription(ctx context.Context) string {
	return nameValidator.Description(ctx)
}

func (nameValidator projectNameValidator) ValidateString(ctx context.Context, request validator.StringRequest, response *validator.StringResponse) {
	if request.ConfigValue.IsNull() || request.ConfigValue.IsUnknown() {
		return
	}
	name := request.ConfigValue.ValueString()
	if name == "" || strings.TrimSpace(name) != name || len(name) > projectNameMaximum {
		response.Diagnostics.AddAttributeError(request.Path, "Invalid project name", nameValidator.Description(ctx)+".")
	}
}

// projectDescriptionValidator bounds the description's length.
type projectDescriptionValidator struct{}

func (projectDescriptionValidator) Description(_ context.Context) string {
	return fmt.Sprintf("value must be at most %d characters", projectDescriptionMaximum)
}

func (descriptionValidator projectDescriptionValidator) MarkdownDescription(ctx context.Context) string {
	return descriptionValidator.Description(ctx)
}

func (descriptionValidator projectDescriptionValidator) ValidateString(ctx context.Context, request validator.StringRequest, response *validator.StringResponse) {
	if request.ConfigValue.IsNull() || request.ConfigValue.IsUnknown() {
		return
	}
	if len(request.ConfigValue.ValueString()) > projectDescriptionMaximum {
		response.Diagnostics.AddAttributeError(request.Path, "Description too long", descriptionValidator.Description(ctx)+".")
	}
}
