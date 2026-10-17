// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64default"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"ankra.io/terraform-provider-ankra/internal/client"
)

var (
	_ resource.Resource                   = (*cloudflareDNSRecordResource)(nil)
	_ resource.ResourceWithConfigure      = (*cloudflareDNSRecordResource)(nil)
	_ resource.ResourceWithImportState    = (*cloudflareDNSRecordResource)(nil)
	_ resource.ResourceWithValidateConfig = (*cloudflareDNSRecordResource)(nil)
)

// cloudflareManagedComment is the comment the platform stamps on a record
// written without one.
const cloudflareManagedComment = "Managed by Ankra"

type cloudflareDNSRecordResource struct {
	configuredClient
}

type cloudflareDNSRecordModel struct {
	ID             types.String `tfsdk:"id"`
	CredentialName types.String `tfsdk:"credential_name"`
	ZoneID         types.String `tfsdk:"zone_id"`
	Name           types.String `tfsdk:"name"`
	RecordType     types.String `tfsdk:"record_type"`
	Content        types.String `tfsdk:"content"`
	TTL            types.Int64  `tfsdk:"ttl"`
	Proxied        types.Bool   `tfsdk:"proxied"`
	Priority       types.Int64  `tfsdk:"priority"`
	Comment        types.String `tfsdk:"comment"`
	FQDN           types.String `tfsdk:"fqdn"`
	ZoneName       types.String `tfsdk:"zone_name"`
	ManagedBy      types.String `tfsdk:"managed_by"`
}

// NewCloudflareDNSRecordResource returns the ankra_cloudflare_dns_record
// resource, the counterpart of `ankra org cloudflare add|update|delete`.
func NewCloudflareDNSRecordResource() resource.Resource {
	return &cloudflareDNSRecordResource{}
}

func (recordResource *cloudflareDNSRecordResource) Metadata(_ context.Context, request resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_cloudflare_dns_record"
}

func (recordResource *cloudflareDNSRecordResource) Schema(_ context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	replaced := []planmodifier.String{stringplanmodifier.RequiresReplace()}
	response.Schema = schema.Schema{
		MarkdownDescription: "A DNS record in a Cloudflare zone, written through a connected `ankra_cloudflare_credential`. " +
			"Find zone ids with the `ankra_cloudflare_domains` data source. The counterpart of `ankra org cloudflare add|update|delete`.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Cloudflare's id for the record.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"credential_name": schema.StringAttribute{
				MarkdownDescription: "Cloudflare credential to write with. Unset uses the organisation's only Cloudflare credential. Changing it replaces the record.",
				Optional:            true,
				PlanModifiers:       replaced,
			},
			"zone_id": schema.StringAttribute{
				MarkdownDescription: "Cloudflare zone id. Changing it replaces the record.",
				Required:            true,
				PlanModifiers:       replaced,
			},
			"name": schema.StringAttribute{
				MarkdownDescription: "Record name: a label such as `www` (qualified with the zone) or the full name. An import stores the label, so name an imported record by its label. Changing it replaces the record.",
				Required:            true,
				PlanModifiers:       replaced,
			},
			"record_type": schema.StringAttribute{
				MarkdownDescription: "`A`, `AAAA`, `CNAME`, `TXT`, `MX` or `NS`. Changing it replaces the record.",
				Required:            true,
				PlanModifiers:       replaced,
				Validators:          []validator.String{alertDNSOneOfValidator{allowed: []string{"A", "AAAA", "CNAME", "TXT", "MX", "NS"}}},
			},
			"content": schema.StringAttribute{
				MarkdownDescription: "What the record points at, without leading or trailing whitespace (the platform trims it).",
				Required:            true,
				Validators:          []validator.String{alertDNSTrimmedValidator{}},
			},
			"ttl": schema.Int64Attribute{
				MarkdownDescription: "TTL in seconds, 60 to 86400, or `1` for automatic. Defaults to `1`; a proxied record is always automatic.",
				Optional:            true,
				Computed:            true,
				Default:             int64default.StaticInt64(client.CloudflareTTLAutomatic),
			},
			"proxied": schema.BoolAttribute{
				MarkdownDescription: "Route the record through Cloudflare's proxy (`A`, `AAAA` and `CNAME` only). Defaults to `false`.",
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(false),
			},
			"priority": schema.Int64Attribute{
				MarkdownDescription: "Priority of an `MX` record; required for `MX` and refused for other types.",
				Optional:            true,
			},
			"comment": schema.StringAttribute{
				MarkdownDescription: "Comment stored on the record. Defaults to `" + cloudflareManagedComment + "`, which the platform stamps on a record written without one; an empty comment is therefore refused.",
				Optional:            true,
				Computed:            true,
				Default:             stringdefault.StaticString(cloudflareManagedComment),
				Validators:          []validator.String{alertDNSNonEmptyValidator{}},
			},
			"fqdn": schema.StringAttribute{
				MarkdownDescription: "Full name of the record.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"zone_name": schema.StringAttribute{
				MarkdownDescription: "Name of the zone.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"managed_by": schema.StringAttribute{
				MarkdownDescription: "`ankra` when the record carries the platform's comment, otherwise `external`.",
				Computed:            true,
			},
		},
	}
}

func (recordResource *cloudflareDNSRecordResource) ValidateConfig(ctx context.Context, request resource.ValidateConfigRequest, response *resource.ValidateConfigResponse) {
	var configuration cloudflareDNSRecordModel
	response.Diagnostics.Append(request.Config.Get(ctx, &configuration)...)
	if response.Diagnostics.HasError() {
		return
	}
	if !configuration.TTL.IsNull() && !configuration.TTL.IsUnknown() {
		ttl := configuration.TTL.ValueInt64()
		if ttl != client.CloudflareTTLAutomatic && (ttl < 60 || ttl > 86400) {
			response.Diagnostics.AddAttributeError(path.Root("ttl"), "Invalid Cloudflare record",
				"ttl must be 1 (automatic) or between 60 and 86400 seconds.")
		}
		if configuration.Proxied.ValueBool() && ttl != client.CloudflareTTLAutomatic {
			response.Diagnostics.AddAttributeError(path.Root("ttl"), "Invalid Cloudflare record",
				"A proxied record's TTL is always automatic: leave ttl unset or set it to 1.")
		}
	}
	recordType := configuration.RecordType.ValueString()
	if configuration.Proxied.ValueBool() && !configuration.RecordType.IsUnknown() &&
		recordType != "A" && recordType != "AAAA" && recordType != "CNAME" {
		response.Diagnostics.AddAttributeError(path.Root("proxied"), "Invalid Cloudflare record",
			"Only A, AAAA and CNAME records can be proxied.")
	}
	if !configuration.RecordType.IsUnknown() {
		if !configuration.Priority.IsNull() && recordType != "MX" {
			response.Diagnostics.AddAttributeError(path.Root("priority"), "Invalid Cloudflare record",
				"priority only applies to MX records.")
		}
		// The platform cannot clear a priority, so an MX record must always state one.
		if configuration.Priority.IsNull() && recordType == "MX" {
			response.Diagnostics.AddAttributeError(path.Root("priority"), "Invalid Cloudflare record",
				"An MX record needs a priority.")
		}
	}
}

func (recordResource *cloudflareDNSRecordResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	apiClient := recordResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var plan cloudflareDNSRecordModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}

	record, err := apiClient.CreateCloudflareRecord(ctx, plan.CredentialName.ValueString(), plan.ZoneID.ValueString(), client.CloudflareRecordCreate{
		Name:       plan.Name.ValueString(),
		RecordType: plan.RecordType.ValueString(),
		Content:    plan.Content.ValueString(),
		TTL:        plan.TTL.ValueInt64(),
		Proxied:    plan.Proxied.ValueBool(),
		Priority:   int64Pointer(plan.Priority),
		Comment:    plan.Comment.ValueString(),
	})
	if err != nil {
		response.Diagnostics.AddError("Unable to create Cloudflare DNS record", err.Error())
		return
	}
	cloudflareDNSRecordApply(record, &plan)
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
}

func (recordResource *cloudflareDNSRecordResource) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	apiClient := recordResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state cloudflareDNSRecordModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}

	record, err := apiClient.GetCloudflareRecord(ctx, state.CredentialName.ValueString(), state.ZoneID.ValueString(),
		state.ID.ValueString(), state.FQDN.ValueString(), state.RecordType.ValueString())
	if removeWhenNotFound(ctx, err, response) {
		return
	}
	if err != nil {
		response.Diagnostics.AddError("Unable to read Cloudflare DNS record", err.Error())
		return
	}
	if state.Name.IsNull() {
		state.Name = types.StringValue(cloudflareRecordLabel(record.Name, record.ZoneName))
	}
	if state.RecordType.IsNull() {
		state.RecordType = types.StringValue(record.RecordType)
	}
	cloudflareDNSRecordApply(record, &state)
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
}

func (recordResource *cloudflareDNSRecordResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	apiClient := recordResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var plan cloudflareDNSRecordModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}

	record, err := apiClient.UpdateCloudflareRecord(ctx, plan.CredentialName.ValueString(), plan.ZoneID.ValueString(), plan.ID.ValueString(),
		client.CloudflareRecordUpdate{
			Content:  plan.Content.ValueString(),
			TTL:      plan.TTL.ValueInt64(),
			Proxied:  plan.Proxied.ValueBool(),
			Priority: int64Pointer(plan.Priority),
			Comment:  plan.Comment.ValueString(),
		})
	if err != nil {
		response.Diagnostics.AddError("Unable to update Cloudflare DNS record", err.Error())
		return
	}
	cloudflareDNSRecordApply(record, &plan)
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
}

func (recordResource *cloudflareDNSRecordResource) Delete(ctx context.Context, request resource.DeleteRequest, response *resource.DeleteResponse) {
	apiClient := recordResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state cloudflareDNSRecordModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	if err := apiClient.DeleteCloudflareRecord(ctx, state.CredentialName.ValueString(), state.ZoneID.ValueString(), state.ID.ValueString()); err != nil {
		response.Diagnostics.AddError("Unable to delete Cloudflare DNS record", err.Error())
	}
}

// ImportState imports by `zone_id/record_id`, or
// `credential_name/zone_id/record_id` when the organisation has more than one
// Cloudflare credential.
func (recordResource *cloudflareDNSRecordResource) ImportState(ctx context.Context, request resource.ImportStateRequest, response *resource.ImportStateResponse) {
	parts := strings.Split(request.ID, "/")
	for _, part := range parts {
		if part == "" {
			parts = nil
			break
		}
	}
	var credentialName types.String
	switch len(parts) {
	case 2:
		credentialName = types.StringNull()
	case 3:
		credentialName = types.StringValue(parts[0])
		parts = parts[1:]
	default:
		response.Diagnostics.AddError("Invalid import id",
			"Expected zone_id/record_id or credential_name/zone_id/record_id, got \""+request.ID+"\".")
		return
	}
	response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("credential_name"), credentialName)...)
	response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("zone_id"), parts[0])...)
	response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("id"), parts[1])...)
}

// cloudflareDNSRecordApply copies the record as Cloudflare stores it onto a
// model. The configured name and type are kept: the platform qualifies a
// label and upper-cases the type, and neither can change in place.
func cloudflareDNSRecordApply(record *client.CloudflareRecord, model *cloudflareDNSRecordModel) {
	model.ID = types.StringValue(record.ID)
	model.ZoneID = types.StringValue(record.ZoneID)
	model.Content = types.StringValue(record.Content)
	model.TTL = types.Int64Value(record.TTL)
	model.Proxied = types.BoolValue(record.Proxied)
	model.Priority = types.Int64PointerValue(record.Priority)
	model.Comment = types.StringValue(record.Comment)
	model.FQDN = types.StringValue(record.Name)
	model.ZoneName = types.StringValue(record.ZoneName)
	model.ManagedBy = types.StringValue(record.ManagedBy)
}

// cloudflareRecordLabel is the label of a record name inside its zone, or the
// name itself for the zone apex or a name outside the zone.
func cloudflareRecordLabel(fqdn, zoneName string) string {
	if zoneName != "" && strings.HasSuffix(fqdn, "."+zoneName) {
		return strings.TrimSuffix(fqdn, "."+zoneName)
	}
	return fqdn
}
