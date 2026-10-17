// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"ankra.io/terraform-provider-ankra/internal/client"
)

var (
	_ resource.Resource                = (*applicationSettingsResource)(nil)
	_ resource.ResourceWithConfigure   = (*applicationSettingsResource)(nil)
	_ resource.ResourceWithImportState = (*applicationSettingsResource)(nil)
)

// applicationSettingsID is the fixed id of the organisation's single
// application settings object.
const applicationSettingsID = "application_settings"

type applicationSettingsResource struct {
	configuredClient
}

type applicationSettingsModel struct {
	ID                         types.String `tfsdk:"id"`
	CIRunnerLabel              types.String `tfsdk:"ci_runner_label"`
	AutoMergeSetupPullRequests types.Bool   `tfsdk:"auto_merge_setup_pull_requests"`
}

// NewApplicationSettingsResource returns the ankra_application_settings
// resource, the counterpart of `ankra application settings set`.
func NewApplicationSettingsResource() resource.Resource {
	return &applicationSettingsResource{}
}

func (settingsResource *applicationSettingsResource) Metadata(_ context.Context, request resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_application_settings"
}

func (settingsResource *applicationSettingsResource) Schema(_ context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: "The organisation's application CI settings: the runner label generated pipelines request, and whether Ankra merges an " +
			"application's setup pull request itself once its checks pass. The counterpart of `ankra application settings set`. " +
			"There is one per organisation and it needs an organisation admin. Destroying it restores the platform defaults: " +
			"the default runner and no auto-merge.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Always `application_settings`.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"ci_runner_label": schema.StringAttribute{
				MarkdownDescription: "GitHub Actions runner label generated pipelines run on. Unset uses Ankra's default runner.",
				Optional:            true,
			},
			"auto_merge_setup_pull_requests": schema.BoolAttribute{
				MarkdownDescription: "Whether Ankra merges an application's setup pull request once its checks pass. Defaults to `false`.",
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(false),
			},
		},
	}
}

func (settingsResource *applicationSettingsResource) write(ctx context.Context, plan *applicationSettingsModel) error {
	apiClient := settingsResource.client
	settings, err := apiClient.UpdateApplicationSettings(ctx, client.ApplicationSettingsUpdate{
		CIRunnerLabel:              stringPointer(plan.CIRunnerLabel),
		AutoMergeSetupPullRequests: plan.AutoMergeSetupPullRequests.ValueBool(),
	})
	if err != nil {
		return err
	}
	applicationSettingsToModel(settings, plan)
	return nil
}

func applicationSettingsToModel(settings *client.ApplicationSettings, model *applicationSettingsModel) {
	model.ID = types.StringValue(applicationSettingsID)
	if settings.IsDefault {
		model.CIRunnerLabel = types.StringNull()
	} else {
		model.CIRunnerLabel = types.StringValue(settings.CIRunnerLabel)
	}
	model.AutoMergeSetupPullRequests = types.BoolValue(settings.AutoMergeSetupPullRequests)
}

func (settingsResource *applicationSettingsResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	if settingsResource.requireClient(&response.Diagnostics) == nil {
		return
	}
	var plan applicationSettingsModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}
	if err := settingsResource.write(ctx, &plan); err != nil {
		response.Diagnostics.AddError("Unable to set application settings", err.Error())
		return
	}
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
	response.Diagnostics.Append(trackScope(ctx, settingsResource.client, response.Private)...)
}

func (settingsResource *applicationSettingsResource) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	apiClient := settingsResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state applicationSettingsModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	settings, err := apiClient.GetApplicationSettings(ctx)
	if err != nil {
		response.Diagnostics.AddError("Unable to read application settings", err.Error())
		return
	}
	applicationSettingsToModel(settings, &state)
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
	response.Diagnostics.Append(trackScope(ctx, settingsResource.client, response.Private)...)
}

func (settingsResource *applicationSettingsResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	if settingsResource.requireClient(&response.Diagnostics) == nil {
		return
	}
	var plan applicationSettingsModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}
	if err := settingsResource.write(ctx, &plan); err != nil {
		response.Diagnostics.AddError("Unable to update application settings", err.Error())
		return
	}
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
}

func (settingsResource *applicationSettingsResource) Delete(ctx context.Context, _ resource.DeleteRequest, response *resource.DeleteResponse) {
	if settingsResource.requireClient(&response.Diagnostics) == nil {
		return
	}
	defaults := applicationSettingsModel{
		CIRunnerLabel:              types.StringNull(),
		AutoMergeSetupPullRequests: types.BoolValue(false),
	}
	if err := settingsResource.write(ctx, &defaults); err != nil {
		response.Diagnostics.AddError("Unable to restore application settings", err.Error())
	}
}

// ImportState accepts any id, conventionally `application_settings`: there is
// one settings object per organisation.
func (settingsResource *applicationSettingsResource) ImportState(ctx context.Context, _ resource.ImportStateRequest, response *resource.ImportStateResponse) {
	response.Diagnostics.Append(response.State.Set(ctx, &applicationSettingsModel{
		ID:                         types.StringValue(applicationSettingsID),
		CIRunnerLabel:              types.StringNull(),
		AutoMergeSetupPullRequests: types.BoolNull(),
	})...)
}
