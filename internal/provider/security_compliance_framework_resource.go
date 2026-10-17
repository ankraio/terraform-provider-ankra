// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"net/http"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"ankra.io/terraform-provider-ankra/internal/client"
)

var (
	_ resource.Resource                = (*securityComplianceFrameworkResource)(nil)
	_ resource.ResourceWithConfigure   = (*securityComplianceFrameworkResource)(nil)
	_ resource.ResourceWithImportState = (*securityComplianceFrameworkResource)(nil)
)

type securityComplianceFrameworkResource struct {
	configuredClient
}

type securityComplianceFrameworkModel struct {
	ID           types.String `tfsdk:"id"`
	Key          types.String `tfsdk:"key"`
	Title        types.String `tfsdk:"title"`
	ShortTitle   types.String `tfsdk:"short_title"`
	Version      types.String `tfsdk:"version"`
	ControlCount types.Int64  `tfsdk:"control_count"`
	EnabledAt    types.String `tfsdk:"enabled_at"`
}

// NewSecurityComplianceFrameworkResource returns the
// ankra_security_compliance_framework resource, the counterpart of
// `ankra security compliance frameworks enable|disable`.
func NewSecurityComplianceFrameworkResource() resource.Resource {
	return &securityComplianceFrameworkResource{}
}

func (frameworkResource *securityComplianceFrameworkResource) Metadata(_ context.Context, request resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_security_compliance_framework"
}

func (frameworkResource *securityComplianceFrameworkResource) Schema(_ context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	stateString := func(description string) schema.StringAttribute {
		return schema.StringAttribute{
			MarkdownDescription: description,
			Computed:            true,
			PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
		}
	}
	response.Schema = schema.Schema{
		MarkdownDescription: "Tracks one compliance framework for the organisation: while the resource exists the framework is enabled and scored in the Security Center. " +
			"Destroying it disables the framework. A framework disabled outside Terraform is planned for re-enabling. " +
			"The counterpart of `ankra security compliance frameworks enable` and `disable`.",
		Attributes: map[string]schema.Attribute{
			"id": stateString("The framework key."),
			"key": schema.StringAttribute{
				MarkdownDescription: "Framework key from the catalogue (see the `ankra_security_compliance_frameworks` data source). Changing it replaces the resource.",
				Required:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"title":       stateString("Framework title."),
			"short_title": stateString("Short framework title."),
			"version":     stateString("Framework version."),
			"control_count": schema.Int64Attribute{
				MarkdownDescription: "Controls the framework defines.",
				Computed:            true,
			},
			"enabled_at": stateString("When the framework was enabled."),
		},
	}
}

func (frameworkResource *securityComplianceFrameworkResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	apiClient := frameworkResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var plan securityComplianceFrameworkModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}

	framework, err := apiClient.SetSecurityComplianceFrameworkEnabled(ctx, plan.Key.ValueString(), true)
	if err != nil {
		response.Diagnostics.AddError("Unable to enable compliance framework", err.Error())
		return
	}
	securityApplyComplianceFramework(&plan, framework)
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
}

func (frameworkResource *securityComplianceFrameworkResource) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	apiClient := frameworkResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state securityComplianceFrameworkModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}

	framework, err := apiClient.GetSecurityComplianceFramework(ctx, state.Key.ValueString())
	if removeWhenNotFound(ctx, err, response) {
		return
	}
	if err != nil {
		response.Diagnostics.AddError("Unable to read compliance framework", err.Error())
		return
	}
	if readTargetChanged(state.ID, framework.Key) || !framework.Enabled {
		response.State.RemoveResource(ctx)
		return
	}
	securityApplyComplianceFramework(&state, framework)
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
}

// Update is never called with a change: the key forces replacement and every
// other attribute is computed.
func (frameworkResource *securityComplianceFrameworkResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	var plan securityComplianceFrameworkModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
}

func (frameworkResource *securityComplianceFrameworkResource) Delete(ctx context.Context, request resource.DeleteRequest, response *resource.DeleteResponse) {
	apiClient := frameworkResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state securityComplianceFrameworkModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}

	if _, err := apiClient.SetSecurityComplianceFrameworkEnabled(ctx, state.Key.ValueString(), false, http.StatusNotFound); err != nil {
		response.Diagnostics.AddError("Unable to disable compliance framework", err.Error())
	}
}

// ImportState imports by framework key.
func (frameworkResource *securityComplianceFrameworkResource) ImportState(ctx context.Context, request resource.ImportStateRequest, response *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("key"), request, response)
}

func securityApplyComplianceFramework(model *securityComplianceFrameworkModel, framework *client.SecurityComplianceFramework) {
	model.ID = types.StringValue(framework.Key)
	model.Key = types.StringValue(framework.Key)
	model.Title = types.StringValue(framework.Title)
	model.ShortTitle = types.StringValue(framework.ShortTitle)
	model.Version = types.StringValue(framework.Version)
	model.ControlCount = types.Int64Value(framework.ControlCount)
	model.EnabledAt = types.StringPointerValue(framework.EnabledAt)
}
