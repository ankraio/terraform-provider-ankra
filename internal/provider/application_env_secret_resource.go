// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"ankra.io/terraform-provider-ankra/internal/client"
)

var (
	_ resource.Resource                = (*applicationEnvSecretResource)(nil)
	_ resource.ResourceWithConfigure   = (*applicationEnvSecretResource)(nil)
	_ resource.ResourceWithImportState = (*applicationEnvSecretResource)(nil)
)

type applicationEnvSecretResource struct {
	configuredClient
}

type applicationEnvSecretModel struct {
	ID            types.String `tfsdk:"id"`
	ApplicationID types.String `tfsdk:"application_id"`
	Key           types.String `tfsdk:"key"`
	Value         types.String `tfsdk:"value"`
	Status        types.String `tfsdk:"status"`
}

// NewApplicationEnvSecretResource returns the ankra_application_env_secret
// resource, the counterpart of `ankra application env-secrets set|delete`.
func NewApplicationEnvSecretResource() resource.Resource {
	return &applicationEnvSecretResource{}
}

func (secretResource *applicationEnvSecretResource) Metadata(_ context.Context, request resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_application_env_secret"
}

func (secretResource *applicationEnvSecretResource) Schema(_ context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: "One key of an application's environment Secret, the values its generated manifests read through `secretKeyRef`. " +
			"The counterpart of `ankra application env-secrets set` and `ankra application env-secrets delete`. " +
			"Setting a value does not roll running workloads: the next deploy seals it, or run `ankra application env-secrets apply`.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "`<application_id>/<key>`.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"application_id": schema.StringAttribute{
				MarkdownDescription: "Identifier of the application. Changing it replaces the secret.",
				Required:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"key": schema.StringAttribute{
				MarkdownDescription: "Environment variable name: letters, digits and underscores, starting with a letter or underscore, at most 63 characters. Changing it replaces the secret.",
				Required:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
				Validators:          []validator.String{applicationEnvSecretKeyValidator{}},
			},
			"value": schema.StringAttribute{
				MarkdownDescription: "Secret value. The platform never returns it, so a value changed outside Terraform is not detected; " +
					"a value deleted from the secret store outside Terraform is, and plans the secret again.",
				Required:  true,
				Sensitive: true,
			},
			"status": schema.StringAttribute{
				MarkdownDescription: "`set` while the secret store holds the value.",
				Computed:            true,
			},
		},
	}
}

func (secretResource *applicationEnvSecretResource) write(ctx context.Context, plan *applicationEnvSecretModel) error {
	secret, err := secretResource.client.SetApplicationEnvSecret(ctx, plan.ApplicationID.ValueString(), plan.Key.ValueString(), plan.Value.ValueString())
	if err != nil {
		return err
	}
	plan.ID = types.StringValue(plan.ApplicationID.ValueString() + "/" + plan.Key.ValueString())
	plan.Status = types.StringValue(secret.Status)
	return nil
}

func (secretResource *applicationEnvSecretResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	if secretResource.requireClient(&response.Diagnostics) == nil {
		return
	}
	var plan applicationEnvSecretModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}
	if err := secretResource.write(ctx, &plan); err != nil {
		response.Diagnostics.AddError("Unable to set application environment secret", err.Error())
		return
	}
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
}

func (secretResource *applicationEnvSecretResource) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	apiClient := secretResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state applicationEnvSecretModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	secret, err := apiClient.GetApplicationEnvSecret(ctx, state.ApplicationID.ValueString(), state.Key.ValueString())
	if removeWhenNotFound(ctx, err, response) {
		return
	}
	if err != nil {
		response.Diagnostics.AddError("Unable to read application environment secret", err.Error())
		return
	}
	if secret.Status != client.ApplicationEnvSecretStatusSet {
		response.State.RemoveResource(ctx)
		return
	}
	state.ID = types.StringValue(state.ApplicationID.ValueString() + "/" + secret.Key)
	state.Status = types.StringValue(secret.Status)
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
}

func (secretResource *applicationEnvSecretResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	if secretResource.requireClient(&response.Diagnostics) == nil {
		return
	}
	var plan applicationEnvSecretModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}
	if err := secretResource.write(ctx, &plan); err != nil {
		response.Diagnostics.AddError("Unable to update application environment secret", err.Error())
		return
	}
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
}

func (secretResource *applicationEnvSecretResource) Delete(ctx context.Context, request resource.DeleteRequest, response *resource.DeleteResponse) {
	apiClient := secretResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state applicationEnvSecretModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	if err := apiClient.DeleteApplicationEnvSecret(ctx, state.ApplicationID.ValueString(), state.Key.ValueString()); err != nil {
		response.Diagnostics.AddError("Unable to delete application environment secret", err.Error())
	}
}

// ImportState imports by `<application_id>/<key>`. The value cannot be read
// back, so the first plan after an import sets the configured value again.
func (secretResource *applicationEnvSecretResource) ImportState(ctx context.Context, request resource.ImportStateRequest, response *resource.ImportStateResponse) {
	parts, err := applicationSplitImportID(request.ID, "application_id", "key")
	if err != nil {
		response.Diagnostics.AddError("Invalid import id", err.Error())
		return
	}
	response.Diagnostics.Append(response.State.Set(ctx, &applicationEnvSecretModel{
		ID:            types.StringValue(request.ID),
		ApplicationID: types.StringValue(parts[0]),
		Key:           types.StringValue(parts[1]),
		Value:         types.StringNull(),
		Status:        types.StringNull(),
	})...)
}
