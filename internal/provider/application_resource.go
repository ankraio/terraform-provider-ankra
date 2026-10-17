// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"reflect"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"

	"ankra.io/terraform-provider-ankra/internal/client"
)

var (
	_ resource.Resource                   = (*applicationResource)(nil)
	_ resource.ResourceWithConfigure      = (*applicationResource)(nil)
	_ resource.ResourceWithImportState    = (*applicationResource)(nil)
	_ resource.ResourceWithValidateConfig = (*applicationResource)(nil)
)

// ValidateConfig refuses at plan time a registry declaration the platform
// would refuse at apply: managing Actions secrets needs the credential to
// write.
func (applicationResource *applicationResource) ValidateConfig(ctx context.Context, request resource.ValidateConfigRequest, response *resource.ValidateConfigResponse) {
	var registryObject types.Object
	response.Diagnostics.Append(request.Config.GetAttribute(ctx, path.Root("image_registry"), &registryObject)...)
	if response.Diagnostics.HasError() || registryObject.IsNull() || registryObject.IsUnknown() {
		return
	}
	var registry applicationImageRegistryModel
	response.Diagnostics.Append(registryObject.As(ctx, &registry, basetypes.ObjectAsOptions{})...)
	if response.Diagnostics.HasError() {
		return
	}
	if registry.ManageActionsSecrets.ValueBool() && registry.CredentialName.IsNull() {
		response.Diagnostics.AddAttributeError(path.Root("image_registry").AtName("credential_name"), "Missing credential_name",
			"manage_actions_secrets needs credential_name: it is the credential Ankra writes into the repository's Actions secrets.")
	}
}

type applicationResource struct {
	configuredClient
}

type applicationImageRegistryModel struct {
	URL                   types.String `tfsdk:"url"`
	CredentialName        types.String `tfsdk:"credential_name"`
	APIURL                types.String `tfsdk:"api_url"`
	PullSecretName        types.String `tfsdk:"pull_secret_name"`
	UsernameSecretName    types.String `tfsdk:"username_secret_name"`
	PasswordSecretName    types.String `tfsdk:"password_secret_name"`
	ManageActionsSecrets  types.Bool   `tfsdk:"manage_actions_secrets"`
	AdminCredentialName   types.String `tfsdk:"admin_credential_name"`
	FlatRepositories      types.Bool   `tfsdk:"flat_repositories"`
	ComponentRepositories types.Map    `tfsdk:"component_repositories"`
	ProjectPublic         types.Bool   `tfsdk:"project_public"`
}

type applicationModel struct {
	ID                    types.String                   `tfsdk:"id"`
	Name                  types.String                   `tfsdk:"name"`
	AppRepoCredentialName types.String                   `tfsdk:"app_repo_credential_name"`
	AppRepoOwner          types.String                   `tfsdk:"app_repo_owner"`
	AppRepoName           types.String                   `tfsdk:"app_repo_name"`
	AppRepoBranch         types.String                   `tfsdk:"app_repo_branch"`
	ImageRegistry         *applicationImageRegistryModel `tfsdk:"image_registry"`
	State                 types.String                   `tfsdk:"state"`
	ContainerImageURL     types.String                   `tfsdk:"container_image_url"`
}

// NewApplicationResource returns the ankra_application resource, the
// counterpart of `ankra application add`, `delete`, `credential set` and
// `registry set|clear`.
func NewApplicationResource() resource.Resource {
	return &applicationResource{}
}

func (applicationResource *applicationResource) Metadata(_ context.Context, request resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_application"
}

func (applicationResource *applicationResource) Schema(_ context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	optionalText := func(description string) schema.StringAttribute {
		return schema.StringAttribute{MarkdownDescription: description, Optional: true, Validators: []validator.String{applicationTrimmedValidator{}}}
	}
	defaultFalse := func(description string) schema.BoolAttribute {
		return schema.BoolAttribute{
			MarkdownDescription: description + " Defaults to `false`.",
			Optional:            true,
			Computed:            true,
			Default:             booldefault.StaticBool(false),
		}
	}
	response.Schema = schema.Schema{
		MarkdownDescription: "An application: a Git repository Ankra builds, packages and deploys. " +
			"The counterpart of `ankra application add` and `ankra application delete`, plus `ankra application credential set` " +
			"and `ankra application registry set|clear`, which change an existing application in place.\n\n" +
			"**Creating it changes the repository.** Ankra analyses the repository and opens a setup pull request on it " +
			"(a Dockerfile, a chart and `.ankra/pipeline.yaml`). The application cannot be deployed until that pull request is merged " +
			"and the first build has published an image. With `ankra_application_settings.auto_merge_setup_pull_requests` on, " +
			"Ankra merges it unattended once its checks pass.\n\n" +
			"**Two phases.** Because of that, an `ankra_application_deployment` of a new application, and an " +
			"`ankra_pipeline_definition`, `ankra_pipeline_schedule` or `ankra_pipeline_merge_train` that addresses it by " +
			"`application_id`, cannot succeed in the same apply that creates it. The deployment waits (see its `timeouts`); " +
			"for the others, apply the application first, merge the setup pull request, then apply the rest.\n\n" +
			"**Destroying or replacing it is destructive.** Destroy deletes the application, tears down every deployment of it on " +
			"every cluster, deletes its environment secret values, revokes its registry robot and unlinks its pipeline repository. " +
			"Changing `name`, `app_repo_owner`, `app_repo_name` or `app_repo_branch` replaces it, which does all of that and opens " +
			"a new setup pull request. Protect it with `lifecycle { prevent_destroy = true }`.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Identifier of the application.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				MarkdownDescription: "Application name, unique in the organisation. Changing it replaces the application.",
				Required:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"app_repo_credential_name": schema.StringAttribute{
				MarkdownDescription: "Name of the organisation's GitHub credential the repository is reached through. Changes in place.",
				Required:            true,
			},
			"app_repo_owner": schema.StringAttribute{
				MarkdownDescription: "Repository owner or organisation. Changing it replaces the application.",
				Required:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"app_repo_name": schema.StringAttribute{
				MarkdownDescription: "Repository name. Changing it replaces the application.",
				Required:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"app_repo_branch": schema.StringAttribute{
				MarkdownDescription: "Branch Ankra builds and deploys. Defaults to `main`. Changing it replaces the application.",
				Optional:            true,
				Computed:            true,
				Default:             stringdefault.StaticString("main"),
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"image_registry": schema.SingleNestedAttribute{
				MarkdownDescription: "Container registry the application publishes to, when it is not the organisation's own Ankra registry project. " +
					"Changes in place; removing the block hands the application back to the organisation's registry project.",
				Optional: true,
				Attributes: map[string]schema.Attribute{
					"url": schema.StringAttribute{
						MarkdownDescription: "Registry project as `oci://<host>/<project>`.",
						Required:            true,
						Validators:          []validator.String{applicationRegistryURLValidator{}},
					},
					"credential_name": optionalText("Registry credential of the organisation that authenticates to it."),
					"api_url": schema.StringAttribute{
						MarkdownDescription: "Registry management API base, `http://` or `https://`.",
						Optional:            true,
						Validators:          []validator.String{applicationHTTPURLValidator{}},
					},
					"pull_secret_name":       optionalText("Name of the dockerconfigjson Secret the generated manifests reference."),
					"username_secret_name":   optionalText("Repository Actions secret the build logs in with."),
					"password_secret_name":   optionalText("Repository Actions secret holding the registry password."),
					"admin_credential_name":  optionalText("Registry credential with project administrator rights, which lets Ankra mint the application's own push robot."),
					"manage_actions_secrets": defaultFalse("Whether Ankra writes `credential_name` into the repository's Actions secrets. Needs `credential_name`."),
					"flat_repositories":      defaultFalse("Publish monorepo components as `<project>/<component>` instead of `<project>/<app>/<component>`."),
					"project_public": defaultFalse("Ask for the project to be public when Ankra creates it. " +
						"The platform does not report it back, so a change made outside Terraform is not detected."),
					"component_repositories": schema.MapAttribute{
						MarkdownDescription: "Repository inside the project for a component, keyed by component name. Names and repositories are lower-case, as the platform stores them.",
						Optional:            true,
						ElementType:         types.StringType,
						Validators:          []validator.Map{applicationComponentRepositoriesValidator{}},
					},
				},
			},
			"state": schema.StringAttribute{
				MarkdownDescription: "Lifecycle of the application record: `creating` while onboarding runs, `up` once it is complete, `error` when onboarding failed. It says nothing about whether a deployment is running.",
				Computed:            true,
			},
			"container_image_url": schema.StringAttribute{
				MarkdownDescription: "Image repository the application's builds publish to.",
				Computed:            true,
			},
		},
	}
}

func applicationImageRegistryRequest(ctx context.Context, model *applicationImageRegistryModel, diagnostics *diag.Diagnostics) *client.ApplicationImageRegistry {
	if model == nil {
		return nil
	}
	return &client.ApplicationImageRegistry{
		URL:                   model.URL.ValueString(),
		CredentialName:        model.CredentialName.ValueString(),
		APIURL:                model.APIURL.ValueString(),
		PullSecretName:        model.PullSecretName.ValueString(),
		UsernameSecretName:    model.UsernameSecretName.ValueString(),
		PasswordSecretName:    model.PasswordSecretName.ValueString(),
		ManageActionsSecrets:  model.ManageActionsSecrets.ValueBool(),
		AdminCredentialName:   model.AdminCredentialName.ValueString(),
		FlatRepositories:      model.FlatRepositories.ValueBool(),
		ComponentRepositories: applicationStringMap(ctx, model.ComponentRepositories, diagnostics),
		ProjectPublic:         model.ProjectPublic.ValueBool(),
	}
}

// applicationImageRegistryFromView maps the platform's registry view to state.
// project_public is not reported, so it carries over from prior state.
func applicationImageRegistryFromView(view *client.ApplicationImageRegistryView, prior *applicationImageRegistryModel) *applicationImageRegistryModel {
	if !view.Declared {
		return nil
	}
	projectPublic := types.BoolValue(false)
	if prior != nil && !prior.ProjectPublic.IsNull() && !prior.ProjectPublic.IsUnknown() {
		projectPublic = prior.ProjectPublic
	}
	return &applicationImageRegistryModel{
		URL:                   types.StringValue(view.URL),
		CredentialName:        optionalString(view.CredentialName),
		APIURL:                optionalString(view.APIURL),
		PullSecretName:        optionalString(view.PullSecretName),
		UsernameSecretName:    optionalString(view.UsernameSecretName),
		PasswordSecretName:    optionalString(view.PasswordSecretName),
		ManageActionsSecrets:  types.BoolValue(view.ManageActionsSecrets),
		AdminCredentialName:   optionalString(view.AdminCredentialName),
		FlatRepositories:      types.BoolValue(view.FlatRepositories),
		ComponentRepositories: applicationMapValue(view.ComponentRepositories),
		ProjectPublic:         projectPublic,
	}
}

// readApplication refreshes model from the platform. It reports false when the
// application is gone.
func readApplication(ctx context.Context, apiClient *client.Client, model *applicationModel, diagnostics *diag.Diagnostics) bool {
	applicationID := model.ID.ValueString()
	application, err := apiClient.GetApplication(ctx, applicationID)
	if client.IsNotFound(err) {
		return false
	}
	if err != nil {
		diagnostics.AddError("Unable to read application", err.Error())
		return true
	}
	credential, err := apiClient.GetApplicationRepositoryCredential(ctx, applicationID)
	if client.IsNotFound(err) {
		return false
	}
	if err != nil {
		diagnostics.AddError("Unable to read application repository credential", err.Error())
		return true
	}
	registry, err := apiClient.GetApplicationImageRegistry(ctx, applicationID)
	if client.IsNotFound(err) {
		return false
	}
	if err != nil {
		diagnostics.AddError("Unable to read application image registry", err.Error())
		return true
	}

	model.ID = types.StringValue(application.ID)
	model.Name = types.StringValue(application.Name)
	model.AppRepoOwner = types.StringValue(application.AppRepoOwner)
	model.AppRepoName = types.StringValue(application.AppRepoName)
	model.AppRepoBranch = types.StringValue(application.AppRepoBranch)
	model.AppRepoCredentialName = types.StringValue(credential.CredentialName)
	model.ImageRegistry = applicationImageRegistryFromView(registry, model.ImageRegistry)
	model.State = types.StringValue(application.State)
	model.ContainerImageURL = types.StringPointerValue(application.ContainerImageURL)
	return true
}

func (applicationResource *applicationResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	apiClient := applicationResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var plan applicationModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}
	registry := applicationImageRegistryRequest(ctx, plan.ImageRegistry, &response.Diagnostics)
	if response.Diagnostics.HasError() {
		return
	}

	applicationID, err := apiClient.CreateApplication(ctx, client.ApplicationCreate{
		Name:                  plan.Name.ValueString(),
		AppRepoCredentialName: plan.AppRepoCredentialName.ValueString(),
		AppRepoOwner:          plan.AppRepoOwner.ValueString(),
		AppRepoName:           plan.AppRepoName.ValueString(),
		AppRepoBranch:         plan.AppRepoBranch.ValueString(),
		ImageRegistry:         registry,
	})
	if err != nil {
		response.Diagnostics.AddError("Unable to create application", err.Error())
		return
	}

	plan.ID = types.StringValue(applicationID)
	response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("id"), applicationID)...)
	if !readApplication(ctx, apiClient, &plan, &response.Diagnostics) {
		response.Diagnostics.AddError("Unable to read application", "The application "+applicationID+" was created but could not be read back.")
	}
	if response.Diagnostics.HasError() {
		return
	}
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
}

func (applicationResource *applicationResource) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	apiClient := applicationResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state applicationModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}

	if !readApplication(ctx, apiClient, &state, &response.Diagnostics) {
		response.State.RemoveResource(ctx)
		return
	}
	if response.Diagnostics.HasError() {
		return
	}
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
}

func (applicationResource *applicationResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	apiClient := applicationResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var plan, state applicationModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	applicationID := state.ID.ValueString()

	if !plan.AppRepoCredentialName.Equal(state.AppRepoCredentialName) {
		if _, err := apiClient.SetApplicationRepositoryCredential(ctx, applicationID, plan.AppRepoCredentialName.ValueString()); err != nil {
			response.Diagnostics.AddError("Unable to change application repository credential", err.Error())
			return
		}
	}
	if !reflect.DeepEqual(plan.ImageRegistry, state.ImageRegistry) {
		registry := applicationImageRegistryRequest(ctx, plan.ImageRegistry, &response.Diagnostics)
		if response.Diagnostics.HasError() {
			return
		}
		if _, err := apiClient.SetApplicationImageRegistry(ctx, applicationID, registry); err != nil {
			response.Diagnostics.AddError("Unable to change application image registry", err.Error())
			return
		}
	}

	plan.ID = state.ID
	if !readApplication(ctx, apiClient, &plan, &response.Diagnostics) {
		response.Diagnostics.AddError("Unable to read application", "The application "+applicationID+" disappeared during the update.")
	}
	if response.Diagnostics.HasError() {
		return
	}
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
}

func (applicationResource *applicationResource) Delete(ctx context.Context, request resource.DeleteRequest, response *resource.DeleteResponse) {
	apiClient := applicationResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state applicationModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}

	if err := apiClient.DeleteApplication(ctx, state.ID.ValueString()); err != nil {
		response.Diagnostics.AddError("Unable to delete application", err.Error())
	}
}

// ImportState imports by application id. `image_registry.project_public` is
// not reported by the platform and imports as `false`.
func (applicationResource *applicationResource) ImportState(ctx context.Context, request resource.ImportStateRequest, response *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), request, response)
}
