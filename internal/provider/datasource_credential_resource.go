// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"ankra.io/terraform-provider-ankra/internal/client"
)

var (
	_ resource.Resource                = (*datasourceCredentialResource)(nil)
	_ resource.ResourceWithConfigure   = (*datasourceCredentialResource)(nil)
	_ resource.ResourceWithImportState = (*datasourceCredentialResource)(nil)
)

type datasourceCredentialResource struct {
	configuredClient
}

type datasourceCredentialModel struct {
	ID              types.String `tfsdk:"id"`
	Name            types.String `tfsdk:"name"`
	AuthScheme      types.String `tfsdk:"auth_scheme"`
	Username        types.String `tfsdk:"username"`
	PasswordWO      types.String `tfsdk:"password_wo"`
	TokenWO         types.String `tfsdk:"token_wo"`
	SecretWOVersion types.Int64  `tfsdk:"secret_wo_version"`
}

// NewDatasourceCredentialResource returns the ankra_datasource_credential
// resource, the counterpart of `ankra credentials datasource create`.
func NewDatasourceCredentialResource() resource.Resource {
	return &datasourceCredentialResource{}
}

func (credential *datasourceCredentialResource) Metadata(_ context.Context, request resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_datasource_credential"
}

func datasourceCredentialReplacedString(description string) schema.StringAttribute {
	return schema.StringAttribute{
		MarkdownDescription: description,
		Optional:            true,
		PlanModifiers:       []planmodifier.String{credentialReplaceString()},
	}
}

func (credential *datasourceCredentialResource) Schema(_ context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: "A data source credential (`ankra credentials datasource create`): the user name and password, or token, a " +
			"cluster's metrics or log source authenticates to its backend with, referenced by name from the cluster's " +
			"`spec.prometheus_metrics` or `spec.log_source`. The password and token are write-only (Terraform 1.11 or later): they " +
			"go to the platform's secret store and never reach plan or state. The platform has no update for this credential, so " +
			"any change, including a new `secret_wo_version`, replaces it; the platform refuses to delete a credential a cluster " +
			"still uses. Needs `credentials.read` and `credentials.write` (and `credentials.delete` to destroy).",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Identifier of the credential.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				MarkdownDescription: "Credential name the metrics or log source references. Changing it replaces the credential.",
				Required:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"auth_scheme": datasourceCredentialReplacedString("How the backend is authenticated to, such as `basic` or `bearer`. " +
				"The platform does not return it, so a change made outside Terraform is not detected. Changing it replaces the credential. Right after an import the configured value is recorded instead."),
			"username": datasourceCredentialReplacedString("User name of basic authentication. The platform does not return it, " +
				"so a change made outside Terraform is not detected. Changing it replaces the credential. Right after an import the configured value is recorded instead."),
			"password_wo": schema.StringAttribute{
				MarkdownDescription: "Password of basic authentication, as a write-only attribute.",
				Optional:            true,
				Sensitive:           true,
				WriteOnly:           true,
			},
			"token_wo": schema.StringAttribute{
				MarkdownDescription: "Bearer token, as a write-only attribute.",
				Optional:            true,
				Sensitive:           true,
				WriteOnly:           true,
			},
			"secret_wo_version": schema.Int64Attribute{
				MarkdownDescription: "Version of `password_wo` and `token_wo`. Terraform cannot see a change to a write-only value, so change this to store a new one; that replaces the credential. Right after an import the configured version is recorded without sending a secret.",
				Optional:            true,
				PlanModifiers:       []planmodifier.Int64{writeOnlyVersionReplaceUnlessImported()},
			},
		},
	}
}

func (credential *datasourceCredentialResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	apiClient := credential.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var plan datasourceCredentialModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	create := client.DatasourceCredentialCreate{
		Name:       plan.Name.ValueString(),
		AuthScheme: plan.AuthScheme.ValueString(),
		Username:   plan.Username.ValueString(),
		Password:   writeOnlyString(ctx, request.Config, "password_wo", &response.Diagnostics),
		Token:      writeOnlyString(ctx, request.Config, "token_wo", &response.Diagnostics),
	}
	if response.Diagnostics.HasError() {
		return
	}
	credentialID, createError := apiClient.CreateDatasourceCredential(ctx, create)
	if createError != nil {
		response.Diagnostics.AddError("Unable to create the data source credential", createError.Error())
		return
	}
	plan.ID = types.StringValue(credentialID)
	plan.PasswordWO = types.StringNull()
	plan.TokenWO = types.StringNull()
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
	response.Diagnostics.Append(trackScope(ctx, apiClient, response.Private)...)
}

func (credential *datasourceCredentialResource) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	apiClient := credential.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state datasourceCredentialModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	credentials, listError := apiClient.ListDatasourceCredentials(ctx)
	if listError != nil {
		response.Diagnostics.AddError("Unable to read the data source credentials", listError.Error())
		return
	}
	for _, listed := range credentials {
		if listed.ID == state.ID.ValueString() {
			state.Name = types.StringValue(listed.Name)
			response.Diagnostics.Append(response.State.Set(ctx, &state)...)
			response.Diagnostics.Append(trackScope(ctx, apiClient, response.Private)...)
			return
		}
	}
	removeWhenTargetChanged(ctx, apiClient, response)
}

// Update only records configuration Terraform cannot compare after an
// import; every setting the platform stores replaces the credential.
func (credential *datasourceCredentialResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	var plan datasourceCredentialModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
	response.Diagnostics.Append(response.Private.SetKey(ctx, credentialImportedKey, nil)...)
}

func (credential *datasourceCredentialResource) Delete(ctx context.Context, request resource.DeleteRequest, response *resource.DeleteResponse) {
	apiClient := credential.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state datasourceCredentialModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	deleteError := apiClient.DeleteCredential(ctx, state.ID.ValueString())
	if deleteError == nil || deletedWhenNotFound(ctx, deleteError, apiClient, request.Private, &response.Diagnostics) || response.Diagnostics.HasError() {
		return
	}
	response.Diagnostics.AddError("Unable to delete the data source credential", deleteError.Error())
}

// ImportState imports by credential id. The secret, user name and scheme are
// never read back.
func (credential *datasourceCredentialResource) ImportState(ctx context.Context, request resource.ImportStateRequest, response *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), request, response)
	response.Diagnostics.Append(response.Private.SetKey(ctx, credentialImportedKey, []byte("true"))...)
}

// writeOnlyVersionReplaceUnlessImported replaces the credential when the
// version of its write-only secret changes, except right after an import,
// when the configured version is recorded in place.
func writeOnlyVersionReplaceUnlessImported() planmodifier.Int64 {
	return int64planmodifier.RequiresReplaceIf(
		func(ctx context.Context, request planmodifier.Int64Request, response *int64planmodifier.RequiresReplaceIfFuncResponse) {
			response.RequiresReplace = credentialRequiresReplace(ctx, request.StateValue.IsNull(), request.Private)
		},
		credentialReplaceDescription, credentialReplaceDescription,
	)
}
