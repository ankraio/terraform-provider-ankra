// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/setplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"ankra.io/terraform-provider-ankra/internal/client"
)

var (
	_ resource.Resource                   = (*registryRobotResource)(nil)
	_ resource.ResourceWithConfigure      = (*registryRobotResource)(nil)
	_ resource.ResourceWithImportState    = (*registryRobotResource)(nil)
	_ resource.ResourceWithModifyPlan     = (*registryRobotResource)(nil)
	_ resource.ResourceWithValidateConfig = (*registryRobotResource)(nil)
)

type registryRobotResource struct {
	configuredClient
}

type registryRobotModel struct {
	ID                  types.String `tfsdk:"id"`
	Name                types.String `tfsdk:"name"`
	Project             types.String `tfsdk:"project"`
	Scope               types.String `tfsdk:"scope"`
	Permissions         types.Set    `tfsdk:"permissions"`
	Description         types.String `tfsdk:"description"`
	ExpiresInDays       types.Int64  `tfsdk:"expires_in_days"`
	Registry            types.String `tfsdk:"registry"`
	AdminCredentialName types.String `tfsdk:"admin_credential_name"`
	RotationTriggers    types.Map    `tfsdk:"rotation_triggers"`
	RobotName           types.String `tfsdk:"robot_name"`
	Host                types.String `tfsdk:"host"`
	CredentialName      types.String `tfsdk:"credential_name"`
	ExpiresAt           types.String `tfsdk:"expires_at"`
	RotatedAt           types.String `tfsdk:"rotated_at"`
	CreatedAt           types.String `tfsdk:"created_at"`
	Secret              types.String `tfsdk:"secret"`
	DockerLogin         types.String `tfsdk:"docker_login"`
}

// NewRegistryRobotResource returns the ankra_registry_robot resource, the
// counterpart of `ankra registry robots create|rotate|delete`.
func NewRegistryRobotResource() resource.Resource {
	return &registryRobotResource{}
}

func (robotResource *registryRobotResource) Metadata(_ context.Context, request resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_registry_robot"
}

func (robotResource *registryRobotResource) Schema(_ context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	replaceString := []planmodifier.String{stringplanmodifier.RequiresReplace()}
	computedString := []planmodifier.String{stringplanmodifier.UseStateForUnknown()}
	response.Schema = schema.Schema{
		MarkdownDescription: "A robot account on a registry project: a docker login for CI or a cluster, scoped to push or pull. " +
			"The counterpart of `ankra registry robots create`, `rotate` and `delete`.\n\n" +
			"The platform answers the robot's secret once, at creation and at each rotation, so it is kept in state and " +
			"cannot be recovered on import. A robot cannot be edited in place: every setting except `rotation_triggers` " +
			"replaces it. Change any value in `rotation_triggers` to rotate the secret in place: the robot keeps its name and " +
			"the organisation credential holding its login, so references to `credential_name` stay valid, and only the " +
			"old secret stops working.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "The robot name.",
				Computed:            true,
				PlanModifiers:       computedString,
			},
			"name": schema.StringAttribute{
				MarkdownDescription: "Robot name, unique in the organisation. Changing it replaces the robot.",
				Required:            true,
				PlanModifiers:       replaceString,
				Validators:          []validator.String{catalogueRegistryRobotNameValidator},
			},
			"project": schema.StringAttribute{
				MarkdownDescription: "Registry project the robot is bound to: `default` (the organisation's own project, the default) " +
					"or the name of an `ankra_registry_project`. On an integrated registry, that registry's project, and required there.",
				Optional:      true,
				Computed:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown(), stringplanmodifier.RequiresReplace()},
			},
			"scope": schema.StringAttribute{
				MarkdownDescription: "Preset grants: `push` (push and pull) or `pull`. Conflicts with `permissions`. When neither is set the " +
					"platform mints a push robot. When `permissions` is set the platform reports the preset they amount to " +
					"(`push` or `pull`), or `custom` when they are neither.",
				Optional:      true,
				Computed:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown(), stringplanmodifier.RequiresReplace()},
				Validators:    []validator.String{catalogueRobotScopeValidator},
			},
			"permissions": schema.SetAttribute{
				MarkdownDescription: "The robot's own grants, written `resource:action` (see `ankra registry robots permissions`). " +
					"Conflicts with `scope`. Grants beyond push and pull need the `credentials.reveal` permission. The platform " +
					"normalises the selection (lower case, `repository:pull` added to `repository:push`); a configured set that " +
					"normalises to what the platform holds is kept as written.",
				ElementType:   types.StringType,
				Optional:      true,
				Computed:      true,
				PlanModifiers: []planmodifier.Set{setplanmodifier.UseStateForUnknown(), setplanmodifier.RequiresReplace()},
			},
			"description": schema.StringAttribute{
				MarkdownDescription: "Free-text description, at most 200 characters, without leading or trailing whitespace (the " +
					"platform trims it). Changing it replaces the robot.",
				Optional:      true,
				PlanModifiers: replaceString,
				Validators:    []validator.String{catalogueRobotDescriptionValidator{}},
			},
			"expires_in_days": schema.Int64Attribute{
				MarkdownDescription: "Days until the registry stops honouring the robot. Unset is a robot that never expires. The platform " +
					"reports only `expires_at`, so after an import this value is taken from configuration without replacing the robot.",
				Optional: true,
				PlanModifiers: []planmodifier.Int64{int64planmodifier.RequiresReplaceIf(registryRobotExpiryRequiresReplace,
					"Changing the expiry replaces the robot.", "Changing the expiry replaces the robot.")},
			},
			"registry": schema.StringAttribute{
				MarkdownDescription: "Integrated OCI registry entry (a Harbor the organisation runs itself) to mint the robot on, instead " +
					"of the Ankra registry. Requires `admin_credential_name` and `project`.",
				Optional:      true,
				PlanModifiers: replaceString,
			},
			"admin_credential_name": schema.StringAttribute{
				MarkdownDescription: "The organisation credential allowed to manage robots on `registry`.",
				Optional:            true,
				PlanModifiers:       replaceString,
			},
			"rotation_triggers": schema.MapAttribute{
				MarkdownDescription: "Arbitrary values; changing any of them rotates the robot's secret in place.",
				ElementType:         types.StringType,
				Optional:            true,
			},
			"robot_name": schema.StringAttribute{
				MarkdownDescription: "The robot's user name on the registry, used as the docker login user.",
				Computed:            true,
				PlanModifiers:       computedString,
			},
			"host": schema.StringAttribute{
				MarkdownDescription: "Registry host the robot logs in to.",
				Computed:            true,
				PlanModifiers:       computedString,
			},
			"credential_name": schema.StringAttribute{
				MarkdownDescription: "Organisation credential holding the robot's login.",
				Computed:            true,
				PlanModifiers:       computedString,
			},
			"expires_at": schema.StringAttribute{
				MarkdownDescription: "When the registry stops honouring the robot; null for one that never expires.",
				Computed:            true,
				PlanModifiers:       computedString,
			},
			"rotated_at": schema.StringAttribute{
				MarkdownDescription: "When the secret was last rotated; null before the first rotation.",
				Computed:            true,
				PlanModifiers:       computedString,
			},
			"created_at": schema.StringAttribute{
				MarkdownDescription: "When the robot was minted. A robot of the same name minted later is a different robot: " +
					"Terraform then treats this one as gone instead of adopting it.",
				Computed:      true,
				PlanModifiers: computedString,
			},
			"secret": schema.StringAttribute{
				MarkdownDescription: "The robot's secret, answered once at creation and at each rotation. Null after an import.",
				Computed:            true,
				Sensitive:           true,
				PlanModifiers:       computedString,
			},
			"docker_login": schema.StringAttribute{
				MarkdownDescription: "A `docker login` command using the secret. Null after an import.",
				Computed:            true,
				Sensitive:           true,
				PlanModifiers:       computedString,
			},
		},
	}
}

// registryRobotExpiryRequiresReplace replaces the robot when its expiry
// changes, except when no expiry was known: after an import the platform
// cannot say how many days the robot was minted with, and replacing a robot
// to learn it would revoke a working login.
func registryRobotExpiryRequiresReplace(_ context.Context, request planmodifier.Int64Request, response *int64planmodifier.RequiresReplaceIfFuncResponse) {
	response.RequiresReplace = !request.StateValue.IsNull()
}

func (robotResource *registryRobotResource) ValidateConfig(ctx context.Context, request resource.ValidateConfigRequest, response *resource.ValidateConfigResponse) {
	var config registryRobotModel
	response.Diagnostics.Append(request.Config.Get(ctx, &config)...)
	if response.Diagnostics.HasError() {
		return
	}
	if !config.Scope.IsNull() && !config.Permissions.IsNull() {
		response.Diagnostics.AddAttributeError(path.Root("permissions"), "Conflicting robot grants",
			"Set either scope (a preset) or permissions (the robot's own grants), not both.")
	}
	isRegistrySet := !config.Registry.IsNull()
	isAdminCredentialSet := !config.AdminCredentialName.IsNull()
	if isRegistrySet != isAdminCredentialSet {
		response.Diagnostics.AddAttributeError(path.Root("registry"), "Incomplete integrated registry",
			"registry and admin_credential_name are set together: the robot is minted on that registry as that credential.")
	}
}

// ModifyPlan marks the secret and its derived values unknown when
// rotation_triggers changes, because the update rotates them.
func (robotResource *registryRobotResource) ModifyPlan(ctx context.Context, request resource.ModifyPlanRequest, response *resource.ModifyPlanResponse) {
	if request.State.Raw.IsNull() || request.Plan.Raw.IsNull() {
		return
	}
	var plan, state registryRobotModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() || registryRotationTriggersEqual(plan.RotationTriggers, state.RotationTriggers) {
		return
	}
	plan.Secret = types.StringUnknown()
	plan.DockerLogin = types.StringUnknown()
	plan.RotatedAt = types.StringUnknown()
	response.Diagnostics.Append(response.Plan.Set(ctx, &plan)...)
}

func (robotResource *registryRobotResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	apiClient := robotResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var plan registryRobotModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}

	permissions := registryStringsFromSet(ctx, plan.Permissions, &response.Diagnostics)
	if response.Diagnostics.HasError() {
		return
	}
	created, err := apiClient.CreateRegistryRobot(ctx, client.RegistryRobotCreate{
		Name:                plan.Name.ValueString(),
		Project:             stringPointer(plan.Project),
		Scope:               stringPointer(plan.Scope),
		Permissions:         permissions,
		Description:         stringPointer(plan.Description),
		ExpiresInDays:       int64Pointer(plan.ExpiresInDays),
		Registry:            stringPointer(plan.Registry),
		AdminCredentialName: stringPointer(plan.AdminCredentialName),
	})
	if err != nil {
		response.Diagnostics.AddError("Unable to create registry robot", err.Error())
		return
	}
	if created.Warning != "" {
		response.Diagnostics.AddWarning("Registry robot secret not stored by the platform", created.Warning)
	}
	registryRobotToState(&created.RegistryRobot, &plan, &response.Diagnostics)
	plan.Secret = types.StringValue(created.Secret)
	plan.DockerLogin = optionalString(created.DockerLogin)
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
}

func (robotResource *registryRobotResource) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	apiClient := robotResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state registryRobotModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}

	robot, err := apiClient.GetRegistryRobot(ctx, state.Name.ValueString())
	if removeWhenNotFound(ctx, err, response) {
		return
	}
	if err != nil {
		response.Diagnostics.AddError("Unable to read registry robot", err.Error())
		return
	}
	if readTargetChanged(state.CreatedAt, robot.CreatedAt) {
		response.State.RemoveResource(ctx)
		return
	}
	registryRobotToState(robot, &state, &response.Diagnostics)
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
}

// Update only ever rotates: every other configurable attribute replaces the
// robot, and expires_in_days changes in place only from an unknown (imported)
// value, which the platform has no way to change anyway.
func (robotResource *registryRobotResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	apiClient := robotResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var plan, state registryRobotModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}

	if !registryRotationTriggersEqual(plan.RotationTriggers, state.RotationTriggers) {
		rotated, err := apiClient.RotateRegistryRobotSecret(ctx, state.Name.ValueString())
		if err != nil {
			response.Diagnostics.AddError("Unable to rotate registry robot secret", err.Error())
			return
		}
		if rotated.Warning != "" {
			response.Diagnostics.AddWarning("Registry robot secret not stored by the platform", rotated.Warning)
		}
		registryRobotToState(&rotated.RegistryRobot, &plan, &response.Diagnostics)
		plan.Secret = types.StringValue(rotated.Secret)
		plan.DockerLogin = optionalString(rotated.DockerLogin)
	}
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
}

func (robotResource *registryRobotResource) Delete(ctx context.Context, request resource.DeleteRequest, response *resource.DeleteResponse) {
	apiClient := robotResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state registryRobotModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	if err := apiClient.DeleteRegistryRobot(ctx, state.Name.ValueString()); err != nil {
		response.Diagnostics.AddError("Unable to delete registry robot", err.Error())
	}
}

// ImportState imports by robot name. The secret cannot be read back, so an
// imported robot has a null secret until its next rotation.
func (robotResource *registryRobotResource) ImportState(ctx context.Context, request resource.ImportStateRequest, response *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("name"), request, response)
}

// registryRobotToState copies what the platform reports into state. The
// secret, expires_in_days and rotation_triggers are not reported and are
// left as they are.
func registryRobotToState(robot *client.RegistryRobot, state *registryRobotModel, diagnostics *diag.Diagnostics) {
	state.ID = types.StringValue(robot.Name)
	state.Name = types.StringValue(robot.Name)
	if robot.ProjectName != "" {
		state.Project = types.StringValue(robot.ProjectName)
	} else if state.Project.IsUnknown() || state.Project.IsNull() {
		state.Project = optionalString(robot.Project)
	}
	state.Scope = optionalString(robot.Scope)
	if !registryRobotPermissionsMatch(state.Permissions, robot.Permissions) {
		state.Permissions = registrySetFromStrings(robot.Permissions, diagnostics)
	}
	state.Description = optionalString(robot.Description)
	state.Registry = optionalString(robot.Registry)
	state.AdminCredentialName = optionalString(robot.AdminCredentialName)
	state.RobotName = types.StringValue(robot.RobotName)
	state.Host = types.StringValue(robot.Host)
	state.CredentialName = optionalString(robot.CredentialName)
	state.ExpiresAt = types.StringPointerValue(robot.ExpiresAt)
	state.RotatedAt = types.StringPointerValue(robot.RotatedAt)
	state.CreatedAt = types.StringValue(robot.CreatedAt)
	if state.Secret.IsUnknown() {
		state.Secret = types.StringNull()
	}
	if state.DockerLogin.IsUnknown() {
		state.DockerLogin = types.StringNull()
	}
}

// registryStringsFromSet reads a set-of-strings attribute; null and unknown
// read as nil.
func registryStringsFromSet(ctx context.Context, set types.Set, diagnostics *diag.Diagnostics) []string {
	if set.IsNull() || set.IsUnknown() {
		return nil
	}
	var values []string
	diagnostics.Append(set.ElementsAs(ctx, &values, false)...)
	return values
}

// registrySetFromStrings is the inverse of registryStringsFromSet; an empty
// or nil slice is an empty set, which the platform reports for a robot
// without grants.
func registrySetFromStrings(values []string, diagnostics *diag.Diagnostics) types.Set {
	elements := make([]attr.Value, 0, len(values))
	for _, value := range values {
		elements = append(elements, types.StringValue(value))
	}
	set, setDiagnostics := types.SetValue(types.StringType, elements)
	diagnostics.Append(setDiagnostics...)
	return set
}

// registryRotationTriggersEqual compares two trigger maps, reading a null map
// and an empty one as the same: neither asks for a rotation.
func registryRotationTriggersEqual(left, right types.Map) bool {
	isLeftEmpty := left.IsNull() || (!left.IsUnknown() && len(left.Elements()) == 0)
	isRightEmpty := right.IsNull() || (!right.IsUnknown() && len(right.Elements()) == 0)
	if isLeftEmpty && isRightEmpty {
		return true
	}
	return left.Equal(right)
}

// registryRobotPermissionsMatch reports whether the configured grants in state
// normalise to the grants the platform holds. The platform lower-cases and
// trims each grant, adds repository:pull to a selection holding
// repository:push, and deduplicates (registry_robot_permissions.go in the
// organisation usecase), so a configuration written differently still names
// the same robot and must not plan a replacement.
func registryRobotPermissionsMatch(configured types.Set, held []string) bool {
	if configured.IsNull() || configured.IsUnknown() {
		return false
	}
	selected := map[string]bool{}
	for _, element := range configured.Elements() {
		text, ok := element.(types.String)
		if !ok || text.IsNull() || text.IsUnknown() {
			return false
		}
		selected[strings.ToLower(strings.TrimSpace(text.ValueString()))] = true
	}
	if selected[registryRobotPermissionPush] {
		selected[registryRobotPermissionPull] = true
	}
	heldSet := map[string]bool{}
	for _, permission := range held {
		heldSet[permission] = true
	}
	if len(heldSet) != len(selected) {
		return false
	}
	for permission := range selected {
		if !heldSet[permission] {
			return false
		}
	}
	return true
}

const (
	registryRobotPermissionPull = "repository:pull"
	registryRobotPermissionPush = "repository:push"
)
