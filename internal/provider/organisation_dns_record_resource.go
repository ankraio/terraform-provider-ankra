// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"strings"

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
	_ resource.Resource                = (*organisationDNSRecordResource)(nil)
	_ resource.ResourceWithConfigure   = (*organisationDNSRecordResource)(nil)
	_ resource.ResourceWithImportState = (*organisationDNSRecordResource)(nil)
)

type organisationDNSRecordResource struct {
	configuredClient
}

type organisationDNSRecordModel struct {
	ID         types.String `tfsdk:"id"`
	Name       types.String `tfsdk:"name"`
	RecordType types.String `tfsdk:"record_type"`
	Content    types.String `tfsdk:"content"`
	TTL        types.Int64  `tfsdk:"ttl"`
	FQDN       types.String `tfsdk:"fqdn"`
	State      types.String `tfsdk:"state"`
	LastError  types.String `tfsdk:"last_error"`
}

// NewOrganisationDNSRecordResource returns the ankra_organisation_dns_record
// resource, the counterpart of `ankra org dns add|update|delete`.
func NewOrganisationDNSRecordResource() resource.Resource {
	return &organisationDNSRecordResource{}
}

func (recordResource *organisationDNSRecordResource) Metadata(_ context.Context, request resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_organisation_dns_record"
}

func (recordResource *organisationDNSRecordResource) Schema(_ context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: "A DNS record in the organisation's own delegated zone (see `ankra_organisation_dns_zone`). " +
			"The platform publishes it asynchronously: `state` moves from `pending` to `active`, or `failed` with `last_error`. " +
			"The counterpart of `ankra org dns add|update|delete`.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Identifier of the record.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				MarkdownDescription: "Label inside the organisation's zone, such as `logs` or `eu.logs`, in lower case. Changing it replaces the record.",
				Required:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
				Validators:          []validator.String{alertDNSLowerCaseValidator{}},
			},
			"record_type": schema.StringAttribute{
				MarkdownDescription: "`A`, `AAAA`, `CNAME` or `TXT`. Changing it replaces the record.",
				Required:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
				Validators:          []validator.String{alertDNSOneOfValidator{allowed: []string{"A", "AAAA", "CNAME", "TXT"}}},
			},
			"content": schema.StringAttribute{
				MarkdownDescription: "What the record points at: an address for `A`/`AAAA`, a hostname for `CNAME`, text for `TXT`. Leading and trailing whitespace is refused, because the platform trims it.",
				Required:            true,
				Validators:          []validator.String{alertDNSTrimmedValidator{}},
			},
			"ttl": schema.Int64Attribute{
				MarkdownDescription: "TTL in seconds, 30 to 86400. Unset is Auto.",
				Optional:            true,
				Validators:          []validator.Int64{alertDNSRangeValidator{minimum: 30, maximum: 86400}},
			},
			"fqdn": schema.StringAttribute{
				MarkdownDescription: "Full name of the record: the label plus the organisation's zone.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"state": schema.StringAttribute{
				MarkdownDescription: "Publication state: `pending`, `active` or `failed`.",
				Computed:            true,
			},
			"last_error": schema.StringAttribute{
				MarkdownDescription: "Why the last publication failed, when it did.",
				Computed:            true,
			},
		},
	}
}

func (recordResource *organisationDNSRecordResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	apiClient := recordResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var plan organisationDNSRecordModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}

	record, err := apiClient.CreateOrganisationDNSRecord(ctx, client.OrganisationDNSRecordCreate{
		Name:       plan.Name.ValueString(),
		RecordType: plan.RecordType.ValueString(),
		Content:    plan.Content.ValueString(),
		TTL:        int64Pointer(plan.TTL),
	})
	if err != nil {
		response.Diagnostics.AddError("Unable to create organisation DNS record", err.Error())
		return
	}
	organisationDNSRecordApply(record, &plan)
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
}

func (recordResource *organisationDNSRecordResource) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	apiClient := recordResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state organisationDNSRecordModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}

	record, err := apiClient.GetOrganisationDNSRecord(ctx, state.ID.ValueString())
	if removeWhenNotFound(ctx, err, response) {
		return
	}
	if err != nil {
		response.Diagnostics.AddError("Unable to read organisation DNS record", err.Error())
		return
	}
	if state.Name.IsNull() {
		zone, zoneError := apiClient.GetOrganisationDNSZone(ctx)
		if zoneError != nil {
			response.Diagnostics.AddError("Unable to read the organisation's DNS zone", zoneError.Error())
			return
		}
		state.Name = types.StringValue(strings.TrimSuffix(record.Name, "."+zone.FQDN))
	}
	organisationDNSRecordApply(record, &state)
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
}

func (recordResource *organisationDNSRecordResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	apiClient := recordResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var plan organisationDNSRecordModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}

	record, err := apiClient.UpdateOrganisationDNSRecord(ctx, plan.ID.ValueString(), client.OrganisationDNSRecordUpdate{
		Content: plan.Content.ValueString(),
		TTL:     int64Pointer(plan.TTL),
	})
	if err != nil {
		response.Diagnostics.AddError("Unable to update organisation DNS record", err.Error())
		return
	}
	organisationDNSRecordApply(record, &plan)
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
}

func (recordResource *organisationDNSRecordResource) Delete(ctx context.Context, request resource.DeleteRequest, response *resource.DeleteResponse) {
	apiClient := recordResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state organisationDNSRecordModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}

	if err := apiClient.DeleteOrganisationDNSRecord(ctx, state.ID.ValueString()); err != nil {
		response.Diagnostics.AddError("Unable to delete organisation DNS record", err.Error())
	}
}

// ImportState imports by record id; the label is derived from the record's
// FQDN and the organisation's zone.
func (recordResource *organisationDNSRecordResource) ImportState(ctx context.Context, request resource.ImportStateRequest, response *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), request, response)
}

// organisationDNSRecordApply copies the platform's record onto a model. The
// record type is taken from the platform only when it differs other than in
// case, so a lower-case configuration does not plan a replacement.
func organisationDNSRecordApply(record *client.OrganisationDNSRecord, model *organisationDNSRecordModel) {
	model.ID = types.StringValue(record.ID)
	if !strings.EqualFold(model.RecordType.ValueString(), record.RecordType) {
		model.RecordType = types.StringValue(record.RecordType)
	}
	model.Content = types.StringValue(record.Content)
	model.TTL = types.Int64PointerValue(record.TTL)
	model.FQDN = types.StringValue(record.Name)
	model.State = types.StringValue(record.State)
	model.LastError = types.StringPointerValue(record.LastError)
}
