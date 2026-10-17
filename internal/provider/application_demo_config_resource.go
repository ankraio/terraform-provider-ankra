// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"ankra.io/terraform-provider-ankra/internal/client"
)

var (
	_ resource.Resource                = (*applicationDemoConfigResource)(nil)
	_ resource.ResourceWithConfigure   = (*applicationDemoConfigResource)(nil)
	_ resource.ResourceWithImportState = (*applicationDemoConfigResource)(nil)
)

type applicationDemoConfigResource struct {
	configuredClient
}

type applicationDemoEnvironmentModel struct {
	Name   types.String `tfsdk:"name"`
	Value  types.String `tfsdk:"value"`
	Secret types.Bool   `tfsdk:"secret"`
}

type applicationDemoConfigModel struct {
	ApplicationID      types.String                      `tfsdk:"application_id"`
	Env                []applicationDemoEnvironmentModel `tfsdk:"env"`
	Database           types.Bool                        `tfsdk:"database"`
	BaseStackID        types.String                      `tfsdk:"base_stack_id"`
	MigrateCommand     types.String                      `tfsdk:"migrate_command"`
	DatabaseExtensions types.List                        `tfsdk:"database_extensions"`
	Protected          types.Bool                        `tfsdk:"protected"`
	ProtectedUsername  types.String                      `tfsdk:"protected_username"`
	GeneratedPassword  types.String                      `tfsdk:"generated_password"`
}

// NewApplicationDemoConfigResource returns the ankra_application_demo_config
// resource, the counterpart of `ankra application demo config set`.
func NewApplicationDemoConfigResource() resource.Resource {
	return &applicationDemoConfigResource{}
}

func (demoResource *applicationDemoConfigResource) Metadata(_ context.Context, request resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_application_demo_config"
}

func (demoResource *applicationDemoConfigResource) Schema(_ context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: "What every pull-request demo of an application starts with: environment variables, a throwaway database, " +
			"a shared base stack, a migration command, and an edge password guard. The counterpart of `ankra application demo config set`. " +
			"The full-stack profile and component routing are not managed here and are left as they are. " +
			"Destroying it restores the defaults: no variables, no database, no base stack and no password guard.",
		Attributes: map[string]schema.Attribute{
			"application_id": schema.StringAttribute{
				MarkdownDescription: "Identifier of the application. Changing it replaces the resource.",
				Required:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"env": schema.ListNestedAttribute{
				MarkdownDescription: "Environment variables every demo starts with. A secret one carries `${SECRET_SLOT:<uuid>}` references rather than plaintext.",
				Optional:            true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"name":  schema.StringAttribute{MarkdownDescription: "Variable name.", Required: true},
						"value": schema.StringAttribute{MarkdownDescription: "Variable value.", Required: true},
						"secret": schema.BoolAttribute{
							MarkdownDescription: "Whether the value references secret slots. Defaults to `false`.",
							Optional:            true,
							Computed:            true,
							Default:             booldefault.StaticBool(false),
						},
					},
				},
			},
			"database": schema.BoolAttribute{
				MarkdownDescription: "Whether each demo gets its own Postgres database. Defaults to `false`.",
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(false),
			},
			"base_stack_id": schema.StringAttribute{
				MarkdownDescription: "Identifier of the shared demo base stack demos depend on.",
				Optional:            true,
			},
			"migrate_command": schema.StringAttribute{
				MarkdownDescription: "Command run in the application image before the demo starts. Needs `database` or `base_stack_id`.",
				Optional:            true,
			},
			"database_extensions": schema.ListAttribute{
				MarkdownDescription: "Postgres extensions the demo database creates. Needs `database`.",
				Optional:            true,
				ElementType:         types.StringType,
			},
			"protected": schema.BoolAttribute{
				MarkdownDescription: "Whether demos sit behind Ankra's edge password guard. Defaults to `false`.",
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(false),
			},
			"protected_username": schema.StringAttribute{
				MarkdownDescription: "Login a protected demo accepts.",
				Computed:            true,
			},
			"generated_password": schema.StringAttribute{
				MarkdownDescription: "Password the platform minted when this resource turned protection on. It is only revealed once; " +
					"it stays null when protection was already on, and `ankra application demo config rotate-password` replaces it.",
				Computed:      true,
				Sensitive:     true,
				PlanModifiers: []planmodifier.String{applicationDemoPasswordModifier{}},
			},
		},
	}
}

// applicationDemoPasswordModifier keeps the revealed password while protection
// stays as it is, and plans it unknown when the plan turns protection on.
type applicationDemoPasswordModifier struct{}

func (applicationDemoPasswordModifier) Description(_ context.Context) string {
	return "Keeps the minted password until protection is turned on again."
}

func (modifier applicationDemoPasswordModifier) MarkdownDescription(ctx context.Context) string {
	return modifier.Description(ctx)
}

func (applicationDemoPasswordModifier) PlanModifyString(ctx context.Context, request planmodifier.StringRequest, response *planmodifier.StringResponse) {
	if request.State.Raw.IsNull() {
		return
	}
	var plannedProtected, priorProtected types.Bool
	response.Diagnostics.Append(request.Plan.GetAttribute(ctx, path.Root("protected"), &plannedProtected)...)
	response.Diagnostics.Append(request.State.GetAttribute(ctx, path.Root("protected"), &priorProtected)...)
	if plannedProtected.IsUnknown() || (plannedProtected.ValueBool() && !priorProtected.ValueBool()) {
		return
	}
	if !plannedProtected.ValueBool() {
		response.PlanValue = types.StringNull()
		return
	}
	response.PlanValue = request.StateValue
}

func applicationDemoUpdateFrom(ctx context.Context, model *applicationDemoConfigModel, diagnostics *diag.Diagnostics) client.ApplicationDemoConfigurationUpdate {
	update := client.ApplicationDemoConfigurationUpdate{
		Env:                []client.ApplicationDemoEnvironmentVariable{},
		Database:           model.Database.ValueBool(),
		MigrateCommand:     model.MigrateCommand.ValueString(),
		DatabaseExtensions: []string{},
		Protected:          model.Protected.ValueBool(),
	}
	for _, variable := range model.Env {
		update.Env = append(update.Env, client.ApplicationDemoEnvironmentVariable{
			Name:   variable.Name.ValueString(),
			Value:  variable.Value.ValueString(),
			Secret: variable.Secret.ValueBool(),
		})
	}
	if !model.BaseStackID.IsNull() && !model.BaseStackID.IsUnknown() {
		update.BaseStack = &client.ApplicationDemoBaseStack{ID: model.BaseStackID.ValueString()}
	}
	if extensions := stringsFromList(ctx, model.DatabaseExtensions, diagnostics); extensions != nil {
		update.DatabaseExtensions = extensions
	}
	return update
}

func applicationDemoToModel(configuration *client.ApplicationDemoConfiguration, model *applicationDemoConfigModel) {
	model.Env = nil
	for _, variable := range configuration.Env {
		model.Env = append(model.Env, applicationDemoEnvironmentModel{
			Name:   types.StringValue(variable.Name),
			Value:  types.StringValue(variable.Value),
			Secret: types.BoolValue(variable.Secret),
		})
	}
	model.Database = types.BoolValue(configuration.Database)
	model.BaseStackID = types.StringNull()
	if configuration.BaseStack != nil && configuration.BaseStack.ID != "" {
		model.BaseStackID = types.StringValue(configuration.BaseStack.ID)
	}
	model.MigrateCommand = types.StringNull()
	if configuration.MigrateCommand != nil {
		model.MigrateCommand = optionalString(*configuration.MigrateCommand)
	}
	model.DatabaseExtensions = types.ListNull(types.StringType)
	if configuration.DatabaseExtensions != nil && len(*configuration.DatabaseExtensions) > 0 {
		model.DatabaseExtensions = listFromStrings(*configuration.DatabaseExtensions)
	}
	isProtected := configuration.Protected != nil && *configuration.Protected
	model.Protected = types.BoolValue(isProtected)
	model.ProtectedUsername = optionalString(configuration.ProtectedUsername)
	if !isProtected {
		model.GeneratedPassword = types.StringNull()
	}
}

func (demoResource *applicationDemoConfigResource) write(ctx context.Context, plan *applicationDemoConfigModel, diagnostics *diag.Diagnostics) {
	update := applicationDemoUpdateFrom(ctx, plan, diagnostics)
	if diagnostics.HasError() {
		return
	}
	configuration, err := demoResource.client.UpdateApplicationDemoConfiguration(ctx, plan.ApplicationID.ValueString(), update)
	if err != nil {
		diagnostics.AddError("Unable to write application demo configuration", err.Error())
		return
	}
	if plan.GeneratedPassword.IsUnknown() {
		plan.GeneratedPassword = optionalString(configuration.GeneratedPassword)
	}
	applicationDemoToModel(configuration, plan)
}

func (demoResource *applicationDemoConfigResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	if demoResource.requireClient(&response.Diagnostics) == nil {
		return
	}
	var plan applicationDemoConfigModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}
	demoResource.write(ctx, &plan, &response.Diagnostics)
	if response.Diagnostics.HasError() {
		return
	}
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
}

func (demoResource *applicationDemoConfigResource) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	apiClient := demoResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state applicationDemoConfigModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	configuration, err := apiClient.GetApplicationDemoConfiguration(ctx, state.ApplicationID.ValueString())
	if removeWhenNotFound(ctx, err, response) {
		return
	}
	if err != nil {
		response.Diagnostics.AddError("Unable to read application demo configuration", err.Error())
		return
	}
	applicationDemoToModel(configuration, &state)
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
}

func (demoResource *applicationDemoConfigResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	if demoResource.requireClient(&response.Diagnostics) == nil {
		return
	}
	var plan applicationDemoConfigModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}
	demoResource.write(ctx, &plan, &response.Diagnostics)
	if response.Diagnostics.HasError() {
		return
	}
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
}

func (demoResource *applicationDemoConfigResource) Delete(ctx context.Context, request resource.DeleteRequest, response *resource.DeleteResponse) {
	apiClient := demoResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state applicationDemoConfigModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	_, err := apiClient.UpdateApplicationDemoConfiguration(ctx, state.ApplicationID.ValueString(), client.ApplicationDemoConfigurationUpdate{
		Env:                []client.ApplicationDemoEnvironmentVariable{},
		DatabaseExtensions: []string{},
	})
	if err != nil && !client.IsNotFound(err) {
		response.Diagnostics.AddError("Unable to restore application demo configuration", err.Error())
	}
}

// ImportState imports by application id. The minted password is never read
// back, so `generated_password` imports as null.
func (demoResource *applicationDemoConfigResource) ImportState(ctx context.Context, request resource.ImportStateRequest, response *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("application_id"), request, response)
}
