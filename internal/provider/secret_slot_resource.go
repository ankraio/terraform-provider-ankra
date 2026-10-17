// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"regexp"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"ankra.io/terraform-provider-ankra/internal/client"
)

// secretSlotLabelPattern mirrors the platform's label rule: not blank, and
// no newline, carriage return or tab.
var secretSlotLabelPattern = regexp.MustCompile(`^[^\n\r\t]*[^\s][^\n\r\t]*$`)

// secretSlotValueMaximumBytes is the largest value the platform stores.
const secretSlotValueMaximumBytes = 64 * 1024

var (
	_ resource.Resource                = (*secretSlotResource)(nil)
	_ resource.ResourceWithConfigure   = (*secretSlotResource)(nil)
	_ resource.ResourceWithImportState = (*secretSlotResource)(nil)
)

type secretSlotResource struct {
	configuredClient
}

type secretSlotModel struct {
	ID         types.String `tfsdk:"id"`
	Label      types.String `tfsdk:"label"`
	Value      types.String `tfsdk:"value"`
	Hint       types.String `tfsdk:"hint"`
	Sentinel   types.String `tfsdk:"sentinel"`
	CreatedVia types.String `tfsdk:"created_via"`
	CreatedAt  types.String `tfsdk:"created_at"`
	PinnedAt   types.String `tfsdk:"pinned_at"`
}

// NewSecretSlotResource returns the ankra_secret_slot resource.
func NewSecretSlotResource() resource.Resource {
	return &secretSlotResource{}
}

func (slot *secretSlotResource) Metadata(_ context.Context, request resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_secret_slot"
}

func (slot *secretSlotResource) Schema(_ context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: "An organisation secret slot: a value stored by the platform and referenced everywhere else by its marker, " +
			"`${SECRET_SLOT:<id>}` (the `sentinel` attribute). Use the marker wherever the platform asks for one, such as a secret header of " +
			"`ankra_mcp_server`, so the value itself never appears in that resource's configuration.\n\n" +
			"The platform never returns the value, so a value changed outside Terraform is not detected, and an imported slot plans one " +
			"value write. Changing `value` replaces it in place, keeping the id and marker; changing `label` or `hint` replaces the slot. " +
			"A slot that a live resource still references cannot be deleted: destroy fails with a 409 until the reference is removed. " +
			"Slots created here never expire.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Identifier of the slot.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"label": schema.StringAttribute{
				MarkdownDescription: "Label of the slot, unique in the organisation; at most 120 characters, without newlines or tabs. Changing it replaces the slot.",
				Required:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
				Validators: []validator.String{
					deployPatternValidator{pattern: secretSlotLabelPattern, subject: "label"},
					costStringLengthValidator{minimum: 1, maximum: 120},
				},
			},
			"value": schema.StringAttribute{
				MarkdownDescription: "The secret value, at most 64 KiB. It is stored in Terraform state, as every sensitive value is; protect the state accordingly.",
				Required:            true,
				Sensitive:           true,
				Validators:          []validator.String{secretSlotValueSize{}},
			},
			"hint": schema.StringAttribute{
				MarkdownDescription: "A note shown next to the slot, at most 240 characters. Changing it replaces the slot.",
				Optional:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
				Validators:          []validator.String{costStringLengthValidator{minimum: 1, maximum: 240}},
			},
			"sentinel": schema.StringAttribute{
				MarkdownDescription: "The marker to use in place of the value, `${SECRET_SLOT:<id>}`.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"created_via": schema.StringAttribute{
				MarkdownDescription: "`user` for a slot holding a given value, `generated` for one the platform generated.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"created_at": schema.StringAttribute{
				MarkdownDescription: "When the slot was created.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"pinned_at": schema.StringAttribute{
				MarkdownDescription: "When a resource first referenced the slot; null while nothing has.",
				Computed:            true,
			},
		},
	}
}

func (slot *secretSlotResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	apiClient := slot.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var plan secretSlotModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}
	created, err := apiClient.CreateSecretSlot(ctx, client.SecretSlotCreate{
		Label: plan.Label.ValueString(),
		Value: plan.Value.ValueString(),
		Hint:  stringPointer(plan.Hint),
	})
	if err != nil {
		response.Diagnostics.AddError("Unable to create secret slot",
			err.Error()+"\n\nA 409 means the organisation already has a slot with this label: import it by its id instead.")
		return
	}
	secretSlotApply(&plan, created)
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
	response.Diagnostics.Append(trackScope(ctx, apiClient, response.Private)...)
}

func (slot *secretSlotResource) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	apiClient := slot.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state secretSlotModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	stored, err := apiClient.GetSecretSlot(ctx, state.ID.ValueString())
	if removeWhenNotFound(ctx, err, apiClient, response) {
		return
	}
	if err != nil {
		response.Diagnostics.AddError("Unable to read secret slot", err.Error())
		return
	}
	state.Label = types.StringValue(stored.Label)
	state.Hint = types.StringPointerValue(stored.Hint)
	secretSlotApply(&state, stored)
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
	response.Diagnostics.Append(trackScope(ctx, apiClient, response.Private)...)
}

// Update can only be a value change: label and hint replace the slot.
func (slot *secretSlotResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	apiClient := slot.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var plan secretSlotModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}
	stored, err := apiClient.ReplaceSecretSlotValue(ctx, plan.ID.ValueString(), plan.Value.ValueString())
	if err != nil {
		response.Diagnostics.AddError("Unable to replace the secret slot's value", err.Error())
		return
	}
	secretSlotApply(&plan, stored)
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
	response.Diagnostics.Append(trackScope(ctx, apiClient, response.Private)...)
}

func (slot *secretSlotResource) Delete(ctx context.Context, request resource.DeleteRequest, response *resource.DeleteResponse) {
	apiClient := slot.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state secretSlotModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	err := apiClient.DeleteSecretSlot(ctx, state.ID.ValueString())
	if err == nil || deletedWhenNotFound(ctx, err, apiClient, request.Private, &response.Diagnostics) || response.Diagnostics.HasError() {
		return
	}
	response.Diagnostics.AddError("Unable to delete secret slot",
		err.Error()+"\n\nA 409 means a resource still references the slot's marker: remove the reference first.")
}

// ImportState imports by slot id. The value cannot be read, so the next
// apply writes the configured value once.
func (slot *secretSlotResource) ImportState(ctx context.Context, request resource.ImportStateRequest, response *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), request, response)
}

func secretSlotApply(model *secretSlotModel, stored *client.SecretSlot) {
	model.ID = types.StringValue(stored.SlotID)
	model.Sentinel = types.StringValue(stored.Sentinel)
	model.CreatedVia = types.StringValue(stored.CreatedVia)
	model.CreatedAt = types.StringValue(stored.CreatedAt)
	model.PinnedAt = types.StringPointerValue(stored.PinnedAt)
}

// secretSlotValueSize refuses an empty value or one over the platform's
// 64 KiB limit at plan time.
type secretSlotValueSize struct{}

func (secretSlotValueSize) Description(_ context.Context) string {
	return "value must be 1 byte to 64 KiB"
}

func (size secretSlotValueSize) MarkdownDescription(ctx context.Context) string {
	return size.Description(ctx)
}

func (size secretSlotValueSize) ValidateString(ctx context.Context, request validator.StringRequest, response *validator.StringResponse) {
	if request.ConfigValue.IsNull() || request.ConfigValue.IsUnknown() {
		return
	}
	length := len(request.ConfigValue.ValueString())
	if length == 0 || length > secretSlotValueMaximumBytes {
		response.Diagnostics.AddAttributeError(request.Path, "Invalid secret value", "The platform refuses it: "+size.Description(ctx)+".")
	}
}
