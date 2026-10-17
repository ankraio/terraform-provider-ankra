// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"errors"
	"net/http"

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
	_ resource.Resource                = (*helmRegistryCredentialResource)(nil)
	_ resource.ResourceWithConfigure   = (*helmRegistryCredentialResource)(nil)
	_ resource.ResourceWithImportState = (*helmRegistryCredentialResource)(nil)
)

type helmRegistryCredentialResource struct {
	configuredClient
}

type helmRegistryCredentialModel struct {
	ID        types.String `tfsdk:"id"`
	Name      types.String `tfsdk:"name"`
	Username  types.String `tfsdk:"username"`
	Password  types.String `tfsdk:"password"`
	ProjectID types.String `tfsdk:"project_id"`
}

// NewHelmRegistryCredentialResource returns the
// ankra_helm_registry_credential resource, the counterpart of
// `ankra helm credentials create|update|delete`.
func NewHelmRegistryCredentialResource() resource.Resource {
	return &helmRegistryCredentialResource{}
}

func (credentialResource *helmRegistryCredentialResource) Metadata(_ context.Context, request resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_helm_registry_credential"
}

func (credentialResource *helmRegistryCredentialResource) Schema(_ context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: "A username and password for pulling charts from a private Helm registry. " +
			"Attach it to a registry with `ankra_helm_registry.credential_name`, or to an `ankra_cluster` addon with `registry_credential_name`. " +
			"The counterpart of `ankra helm credentials create`, `update` and `delete`.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Identifier of the credential.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				MarkdownDescription: "Credential name, unique in the organisation. Changing it replaces the credential.",
				Required:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
				Validators:          []validator.String{configurationPathNameValidator{}, helmReservedNameValidator{isCredential: true}},
			},
			"username": schema.StringAttribute{
				MarkdownDescription: "Registry username. Read back to detect drift when the token holds the `credentials.reveal` permission; otherwise a change made outside Terraform is not detected.",
				Required:            true,
			},
			"password": schema.StringAttribute{
				MarkdownDescription: "Registry password or token. The platform never returns it, so a change made outside Terraform is not detected.",
				Required:            true,
				Sensitive:           true,
			},
			"project_id": helmProjectIDAttribute("credential"),
		},
	}
}

func (credentialResource *helmRegistryCredentialResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	apiClient := credentialResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var plan helmRegistryCredentialModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}

	apiClient = resourceProjectClient(ctx, apiClient, plan.ProjectID, &response.Diagnostics)
	if apiClient == nil {
		return
	}

	err := apiClient.CreateHelmRegistryCredential(ctx, client.HelmRegistryCredentialCreate{
		Name:      plan.Name.ValueString(),
		Username:  plan.Username.ValueString(),
		Password:  plan.Password.ValueString(),
		ProjectID: helmProjectIDBody(apiClient, plan.ProjectID),
	})
	if err != nil {
		response.Diagnostics.AddError("Unable to create Helm registry credential", err.Error())
		return
	}

	// The credential exists from here on. State is written before the
	// identifier is read back, so a failed read taints the credential instead
	// of orphaning it, which would make the next apply fail on its own name.
	plan.ID = types.StringNull()
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
	response.Diagnostics.Append(trackScope(ctx, apiClient, response.Private)...)
	if response.Diagnostics.HasError() {
		return
	}
	credential, err := apiClient.FindHelmRegistryCredential(ctx, plan.Name.ValueString())
	if err != nil {
		response.Diagnostics.AddError("Unable to read Helm registry credential", err.Error())
		return
	}
	plan.ID = types.StringValue(credential.ID)
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
}

func (credentialResource *helmRegistryCredentialResource) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	apiClient := credentialResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state helmRegistryCredentialModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	apiClient = resourceProjectClient(ctx, apiClient, state.ProjectID, &response.Diagnostics)
	if apiClient == nil {
		return
	}

	detail, err := apiClient.GetHelmRegistryCredential(ctx, state.Name.ValueString())
	if removeWhenNotFound(ctx, err, apiClient, response) {
		return
	}
	if err == nil {
		if readTargetChanged(state.ID, detail.ID) {
			removeWhenTargetChanged(ctx, apiClient, response)
			return
		}
		state.ID = types.StringValue(detail.ID)
		state.Username = types.StringValue(detail.Username)
		response.Diagnostics.Append(response.State.Set(ctx, &state)...)
		response.Diagnostics.Append(trackScope(ctx, apiClient, response.Private)...)
		return
	}
	if !helmRegistryCredentialRevealDenied(err) {
		response.Diagnostics.AddError("Unable to read Helm registry credential", err.Error())
		return
	}

	credential, err := apiClient.FindHelmRegistryCredential(ctx, state.Name.ValueString())
	if removeWhenNotFound(ctx, err, apiClient, response) {
		return
	}
	if err != nil {
		response.Diagnostics.AddError("Unable to read Helm registry credential", err.Error())
		return
	}
	if readTargetChanged(state.ID, credential.ID) {
		removeWhenTargetChanged(ctx, apiClient, response)
		return
	}
	state.ID = types.StringValue(credential.ID)
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
	response.Diagnostics.Append(trackScope(ctx, apiClient, response.Private)...)
}

func (credentialResource *helmRegistryCredentialResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	apiClient := credentialResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var plan helmRegistryCredentialModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}
	apiClient = resourceProjectClient(ctx, apiClient, plan.ProjectID, &response.Diagnostics)
	if apiClient == nil {
		return
	}

	err := apiClient.UpdateHelmRegistryCredential(ctx, plan.Name.ValueString(), client.HelmRegistryCredentialUpdate{
		Username: plan.Username.ValueString(),
		Password: plan.Password.ValueString(),
	})
	if err != nil {
		response.Diagnostics.AddError("Unable to update Helm registry credential", err.Error())
		return
	}
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
}

func (credentialResource *helmRegistryCredentialResource) Delete(ctx context.Context, request resource.DeleteRequest, response *resource.DeleteResponse) {
	apiClient := credentialResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state helmRegistryCredentialModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	apiClient = resourceProjectClient(ctx, apiClient, state.ProjectID, &response.Diagnostics)
	if apiClient == nil {
		return
	}

	if err := apiClient.DeleteHelmRegistryCredential(ctx, state.Name.ValueString()); err != nil {
		response.Diagnostics.AddError("Unable to delete Helm registry credential", err.Error())
	}
}

// ImportState imports by credential name. The password cannot be read back,
// so the first plan after an import shows the configured password as a
// change.
func (credentialResource *helmRegistryCredentialResource) ImportState(ctx context.Context, request resource.ImportStateRequest, response *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("name"), request, response)
}

// helmRegistryCredentialRevealDenied reports a 403 from the credential detail
// route, which needs the credentials.reveal permission the listing does not.
func helmRegistryCredentialRevealDenied(err error) bool {
	var apiError *client.APIError
	return errors.As(err, &apiError) && apiError.StatusCode == http.StatusForbidden
}
