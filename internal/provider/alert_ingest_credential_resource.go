// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"ankra.io/terraform-provider-ankra/internal/client"
)

var alertIngestCredentialScopes = []string{"cluster", "platform", "mixed"}

var (
	_ resource.Resource                = (*alertIngestCredentialResource)(nil)
	_ resource.ResourceWithConfigure   = (*alertIngestCredentialResource)(nil)
	_ resource.ResourceWithImportState = (*alertIngestCredentialResource)(nil)
)

type alertIngestCredentialResource struct {
	configuredClient
}

type alertIngestCredentialModel struct {
	ID          types.String `tfsdk:"id"`
	Name        types.String `tfsdk:"name"`
	ClusterID   types.String `tfsdk:"cluster_id"`
	Scope       types.String `tfsdk:"scope"`
	Enabled     types.Bool   `tfsdk:"enabled"`
	Token       types.String `tfsdk:"token"`
	WebhookPath types.String `tfsdk:"webhook_path"`
}

// NewAlertIngestCredentialResource returns the ankra_alert_ingest_credential
// resource, the counterpart of `ankra alerts ingest-credentials`.
func NewAlertIngestCredentialResource() resource.Resource {
	return &alertIngestCredentialResource{}
}

func (credential *alertIngestCredentialResource) Metadata(_ context.Context, request resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_alert_ingest_credential"
}

func (credential *alertIngestCredentialResource) Schema(_ context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: "An alert ingest credential (`ankra alerts ingest-credentials`): the endpoint and token an external notifier, " +
			"such as Prometheus Alertmanager, delivers alerts into Ankra with. The platform returns the token only when it mints " +
			"it, so `token` is set at create and kept in state (marked sensitive), and is null after an import. Pinning to a " +
			"cluster and the scope change in place; a change of `name` or `enabled` mints a new credential. Destroy revokes the " +
			"credential, so its token stops delivering.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Identifier of the credential.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				MarkdownDescription: "Credential name. Changing it replaces the credential.",
				Required:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"cluster_id": schema.StringAttribute{
				MarkdownDescription: "Cluster the credential is pinned to. A `mixed` credential must be pinned; a `platform` one must not be. Unset unpins it.",
				Optional:            true,
			},
			"scope": schema.StringAttribute{
				MarkdownDescription: "`cluster`, `platform` or `mixed`: which alerts the credential may deliver.",
				Optional:            true,
				Computed:            true,
				Validators:          []validator.String{organisationStringOneOf{values: alertIngestCredentialScopes}},
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"enabled": schema.BoolAttribute{
				MarkdownDescription: "Whether the credential accepts deliveries. Defaults to `true`. Changing it replaces the credential.",
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(true),
				PlanModifiers:       []planmodifier.Bool{boolplanmodifier.RequiresReplace()},
			},
			"token": schema.StringAttribute{
				MarkdownDescription: "The token the notifier sends. Revealed only when minted; null after an import.",
				Computed:            true,
				Sensitive:           true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"webhook_path": schema.StringAttribute{
				MarkdownDescription: "Path, under the platform's address, that accepts Alertmanager webhook payloads for this credential.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
		},
	}
}

func (credential *alertIngestCredentialResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	apiClient := credential.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var plan alertIngestCredentialModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}
	minted, createError := apiClient.CreateAlertIngestCredential(ctx, client.AlertIngestCredentialCreate{
		Name: plan.Name.ValueString(), ClusterID: stringPointer(plan.ClusterID), Enabled: plan.Enabled.ValueBool(),
	})
	if createError != nil {
		response.Diagnostics.AddError("Unable to create the alert ingest credential", createError.Error())
		return
	}
	plan.Token = types.StringValue(minted.Token)
	plan.WebhookPath = types.StringValue(minted.WebhookPath)
	item := &minted.Item
	if wanted := stringPointer(plan.Scope); wanted != nil && (item.Scope == nil || *item.Scope != *wanted) {
		rebound, rebindError := apiClient.RebindAlertIngestCredential(ctx, item.ID, client.AlertIngestCredentialRebind{
			ClusterID: stringPointer(plan.ClusterID), Scope: *wanted,
		})
		if rebindError != nil {
			alertIngestCredentialApply(&plan, item)
			response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
			response.Diagnostics.AddError("Unable to set the alert ingest credential's scope", rebindError.Error())
			return
		}
		item = rebound
	}
	alertIngestCredentialApply(&plan, item)
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
	response.Diagnostics.Append(trackScope(ctx, apiClient, response.Private)...)
}

func (credential *alertIngestCredentialResource) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	apiClient := credential.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state alertIngestCredentialModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	credentials, listError := apiClient.ListAlertIngestCredentials(ctx)
	if listError != nil {
		response.Diagnostics.AddError("Unable to read the alert ingest credentials", listError.Error())
		return
	}
	for index := range credentials {
		if credentials[index].ID == state.ID.ValueString() {
			alertIngestCredentialApply(&state, &credentials[index])
			if state.WebhookPath.IsNull() {
				state.WebhookPath = types.StringValue("/webhooks/alerts/" + credentials[index].ID)
			}
			response.Diagnostics.Append(response.State.Set(ctx, &state)...)
			response.Diagnostics.Append(trackScope(ctx, apiClient, response.Private)...)
			return
		}
	}
	removeWhenTargetChanged(ctx, apiClient, response)
}

func (credential *alertIngestCredentialResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	apiClient := credential.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var plan, state alertIngestCredentialModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	rebind := client.AlertIngestCredentialRebind{ClusterID: stringPointer(plan.ClusterID)}
	if scope := stringPointer(plan.Scope); scope != nil {
		rebind.Scope = *scope
	}
	rebound, rebindError := apiClient.RebindAlertIngestCredential(ctx, state.ID.ValueString(), rebind)
	if rebindError != nil {
		response.Diagnostics.AddError("Unable to rebind the alert ingest credential", rebindError.Error())
		return
	}
	plan.Token = state.Token
	plan.WebhookPath = state.WebhookPath
	alertIngestCredentialApply(&plan, rebound)
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
}

func (credential *alertIngestCredentialResource) Delete(ctx context.Context, request resource.DeleteRequest, response *resource.DeleteResponse) {
	apiClient := credential.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state alertIngestCredentialModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	deleteError := apiClient.DeleteAlertIngestCredential(ctx, state.ID.ValueString())
	if deleteError == nil || deletedWhenNotFound(ctx, deleteError, apiClient, request.Private, &response.Diagnostics) || response.Diagnostics.HasError() {
		return
	}
	response.Diagnostics.AddError("Unable to delete the alert ingest credential", deleteError.Error())
}

// ImportState imports by credential id. The token cannot be read back.
func (credential *alertIngestCredentialResource) ImportState(ctx context.Context, request resource.ImportStateRequest, response *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), request, response)
}

func alertIngestCredentialApply(model *alertIngestCredentialModel, item *client.AlertIngestCredential) {
	model.ID = types.StringValue(item.ID)
	model.Name = types.StringValue(item.Name)
	model.ClusterID = types.StringPointerValue(item.ClusterID)
	model.Scope = types.StringPointerValue(item.Scope)
	model.Enabled = types.BoolValue(item.Enabled)
	if model.Token.IsUnknown() {
		model.Token = types.StringNull()
	}
}
