// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"ankra.io/terraform-provider-ankra/internal/client"
)

var (
	_ datasource.DataSource              = (*securityDispositionsDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*securityDispositionsDataSource)(nil)
)

// securityDispositionStatuses mirrors the platform's policy statuses.
var securityDispositionStatuses = []string{"active", "expiring", "fix_available", "mitigated", "unmatched", "expired", "revoked"}

type securityDispositionsDataSource struct {
	configuredDataSourceClient
}

type securityDispositionItemModel struct {
	ID                     types.String `tfsdk:"id"`
	Disposition            types.String `tfsdk:"disposition"`
	Reason                 types.String `tfsdk:"reason"`
	Status                 types.String `tfsdk:"status"`
	MatchHealth            types.String `tfsdk:"match_health"`
	MatchedOccurrenceCount types.Int64  `tfsdk:"matched_occurrence_count"`
	ExpiresAt              types.String `tfsdk:"expires_at"`
	ExpireWhenFixAvailable types.Bool   `tfsdk:"expire_when_fix_available"`
	CVEID                  types.String `tfsdk:"cve_id"`
	PackageName            types.String `tfsdk:"package_name"`
	AddonSlug              types.String `tfsdk:"addon_slug"`
	ClusterID              types.String `tfsdk:"cluster_id"`
	WorkloadNamespace      types.String `tfsdk:"workload_namespace"`
	WorkloadName           types.String `tfsdk:"workload_name"`
	CreatedBy              types.String `tfsdk:"created_by"`
	CreatedAt              types.String `tfsdk:"created_at"`
	RevokedAt              types.String `tfsdk:"revoked_at"`
}

type securityDispositionsDataSourceModel struct {
	ID           types.String                   `tfsdk:"id"`
	Statuses     types.List                     `tfsdk:"statuses"`
	Disposition  types.String                   `tfsdk:"disposition"`
	Dispositions []securityDispositionItemModel `tfsdk:"dispositions"`
}

// NewSecurityDispositionsDataSource returns the ankra_security_dispositions
// data source, the counterpart of `ankra security dispositions list`.
func NewSecurityDispositionsDataSource() datasource.DataSource {
	return &securityDispositionsDataSource{}
}

func (dispositions *securityDispositionsDataSource) Metadata(_ context.Context, request datasource.MetadataRequest, response *datasource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_security_dispositions"
}

func (dispositions *securityDispositionsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, response *datasource.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: "Lists the organisation's vulnerability disposition policies (acknowledgements and accepted risks), the " +
			"counterpart of `ankra security dispositions list`. Revoked policies are included unless `statuses` leaves them out.",
		Attributes: map[string]schema.Attribute{
			"id": serviceComputedString("Placeholder identifier for the data source."),
			"statuses": schema.ListAttribute{
				MarkdownDescription: "Only policies in one of these statuses: `active`, `expiring`, `fix_available`, `mitigated`, `unmatched`, `expired` or `revoked`.",
				ElementType:         types.StringType,
				Optional:            true,
				Validators:          []validator.List{securityStatusesValidator{}},
			},
			"disposition": schema.StringAttribute{
				MarkdownDescription: "Only `acknowledged` or only `accepted_risk` policies.",
				Optional:            true,
				Validators:          []validator.String{securityOneOf("acknowledged", "accepted_risk")},
			},
			"dispositions": schema.ListNestedAttribute{
				MarkdownDescription: "Disposition policies.",
				Computed:            true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id":                        serviceComputedString("Identifier of the policy."),
						"disposition":               serviceComputedString("`acknowledged` or `accepted_risk`."),
						"reason":                    serviceComputedString("Why the policy was made."),
						"status":                    serviceComputedString("Lifecycle status."),
						"match_health":              serviceComputedString("How well the selector still matches."),
						"matched_occurrence_count":  serviceComputedInt64("Occurrences the policy matches."),
						"expires_at":                serviceComputedString("Review deadline (RFC 3339); null for none."),
						"expire_when_fix_available": serviceComputedBool("Whether the policy ends once a fix is available."),
						"cve_id":                    serviceComputedString("CVE the selector pins, if any."),
						"package_name":              serviceComputedString("Package the selector pins, if any."),
						"addon_slug":                serviceComputedString("Add-on the selector pins, if any."),
						"cluster_id":                serviceComputedString("Cluster the selector pins, if any."),
						"workload_namespace":        serviceComputedString("Workload namespace the selector pins, if any."),
						"workload_name":             serviceComputedString("Workload the selector pins, if any."),
						"created_by":                serviceComputedString("Who made the policy."),
						"created_at":                serviceComputedString("When the policy was made (RFC 3339)."),
						"revoked_at":                serviceComputedString("When the policy was revoked; null while it is not."),
					},
				},
			},
		},
	}
}

func (dispositions *securityDispositionsDataSource) Read(ctx context.Context, request datasource.ReadRequest, response *datasource.ReadResponse) {
	apiClient := dispositions.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var config securityDispositionsDataSourceModel
	response.Diagnostics.Append(request.Config.Get(ctx, &config)...)
	if response.Diagnostics.HasError() {
		return
	}
	policies, err := apiClient.ListSecurityDispositionsFiltered(ctx, client.SecurityDispositionFilter{
		Statuses:    stringsFromList(ctx, config.Statuses, &response.Diagnostics),
		Disposition: config.Disposition.ValueString(),
	})
	if err != nil {
		response.Diagnostics.AddError("Unable to list security dispositions", err.Error())
		return
	}
	optionalTime := func(value *time.Time) types.String {
		if value == nil {
			return types.StringNull()
		}
		return types.StringValue(value.UTC().Format(time.RFC3339))
	}
	config.ID = types.StringValue("ankra_security_dispositions")
	config.Dispositions = make([]securityDispositionItemModel, 0, len(policies))
	for _, policy := range policies {
		config.Dispositions = append(config.Dispositions, securityDispositionItemModel{
			ID:                     types.StringValue(policy.ID),
			Disposition:            types.StringValue(policy.Disposition),
			Reason:                 types.StringValue(policy.Reason),
			Status:                 types.StringValue(policy.Status),
			MatchHealth:            types.StringValue(policy.MatchHealth),
			MatchedOccurrenceCount: types.Int64Value(policy.MatchedOccurrenceCount),
			ExpiresAt:              optionalTime(policy.ExpiresAt),
			ExpireWhenFixAvailable: types.BoolValue(policy.ExpireWhenFixAvailable),
			CVEID:                  optionalString(policy.Selector.CVEID),
			PackageName:            optionalString(policy.Selector.PackageName),
			AddonSlug:              optionalString(policy.Selector.AddonSlug),
			ClusterID:              optionalString(policy.Selector.ClusterID),
			WorkloadNamespace:      optionalString(policy.Selector.WorkloadNamespace),
			WorkloadName:           optionalString(policy.Selector.WorkloadName),
			CreatedBy:              types.StringValue(policy.CreatedBy),
			CreatedAt:              types.StringValue(policy.CreatedAt.UTC().Format(time.RFC3339)),
			RevokedAt:              optionalTime(policy.RevokedAt),
		})
	}
	response.Diagnostics.Append(response.State.Set(ctx, &config)...)
}

// securityStatusesValidator refuses a status the platform does not know.
type securityStatusesValidator struct{}

func (securityStatusesValidator) Description(_ context.Context) string {
	return "each status must be one of: active, expiring, fix_available, mitigated, unmatched, expired, revoked"
}

func (statusesValidator securityStatusesValidator) MarkdownDescription(ctx context.Context) string {
	return statusesValidator.Description(ctx)
}

func (statusesValidator securityStatusesValidator) ValidateList(ctx context.Context, request validator.ListRequest, response *validator.ListResponse) {
	if request.ConfigValue.IsNull() || request.ConfigValue.IsUnknown() {
		return
	}
	for _, element := range request.ConfigValue.Elements() {
		status, isString := element.(types.String)
		if !isString || status.IsNull() || status.IsUnknown() {
			continue
		}
		if !costContains(securityDispositionStatuses, status.ValueString()) {
			response.Diagnostics.AddAttributeError(request.Path, "Invalid status",
				"\""+status.ValueString()+"\" is not a disposition status; "+statusesValidator.Description(ctx)+".")
		}
	}
}
