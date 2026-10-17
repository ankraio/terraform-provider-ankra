// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/objectplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"ankra.io/terraform-provider-ankra/internal/client"
)

const (
	securityScopeOrganisationAddon = "organisation_addon"
	securityScopeWorkload          = "workload"
	securityScopeImage             = "image"
	securityDefaultRevokeReason    = "Revoked by Terraform"
)

var (
	_ resource.Resource                   = (*securityDispositionResource)(nil)
	_ resource.ResourceWithConfigure      = (*securityDispositionResource)(nil)
	_ resource.ResourceWithImportState    = (*securityDispositionResource)(nil)
	_ resource.ResourceWithValidateConfig = (*securityDispositionResource)(nil)
)

type securityDispositionResource struct {
	configuredClient
}

type securityDispositionModel struct {
	ID                     types.String `tfsdk:"id"`
	OccurrenceID           types.String `tfsdk:"occurrence_id"`
	Scope                  types.String `tfsdk:"scope"`
	Disposition            types.String `tfsdk:"disposition"`
	Reason                 types.String `tfsdk:"reason"`
	ExpiresAt              types.String `tfsdk:"expires_at"`
	ExpireWhenFixAvailable types.Bool   `tfsdk:"expire_when_fix_available"`
	AllowUnmatched         types.Bool   `tfsdk:"allow_unmatched"`
	RevokeReason           types.String `tfsdk:"revoke_reason"`
	Selector               types.Object `tfsdk:"selector"`
	SelectorHash           types.String `tfsdk:"selector_hash"`
	Status                 types.String `tfsdk:"status"`
	MatchHealth            types.String `tfsdk:"match_health"`
	MatchedOccurrenceCount types.Int64  `tfsdk:"matched_occurrence_count"`
	CreatedBy              types.String `tfsdk:"created_by"`
	CreatedAt              types.String `tfsdk:"created_at"`
}

var securityDispositionSelectorAttributeTypes = map[string]attr.Type{
	"cve_id":             types.StringType,
	"package_type":       types.StringType,
	"package_name":       types.StringType,
	"addon_slug":         types.StringType,
	"cluster_id":         types.StringType,
	"workload_kind":      types.StringType,
	"workload_namespace": types.StringType,
	"workload_name":      types.StringType,
	"image_digest":       types.StringType,
}

// NewSecurityDispositionResource returns the ankra_security_disposition
// resource, the counterpart of `ankra security dispositions create|update|revoke`.
func NewSecurityDispositionResource() resource.Resource {
	return &securityDispositionResource{}
}

func (dispositionResource *securityDispositionResource) Metadata(_ context.Context, request resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_security_disposition"
}

// securityRequiresReplaceOnceKnown replaces the resource when the value
// changes from one the state already recorded. A value missing from state,
// as after an import (the platform never returns the anchoring occurrence),
// is recorded in place, because the stored policy cannot differ from it.
func securityRequiresReplaceOnceKnown() planmodifier.String {
	return stringplanmodifier.RequiresReplaceIf(
		func(_ context.Context, request planmodifier.StringRequest, response *stringplanmodifier.RequiresReplaceIfFuncResponse) {
			response.RequiresReplace = !request.StateValue.IsNull()
		},
		"Changing a recorded value replaces the disposition.",
		"Changing a recorded value replaces the disposition.",
	)
}

// securityRequiresReplaceWhenCleared replaces the resource when a value is
// removed: the platform can move a review deadline but never clear one.
func securityRequiresReplaceWhenCleared() planmodifier.String {
	return stringplanmodifier.RequiresReplaceIf(
		func(_ context.Context, request planmodifier.StringRequest, response *stringplanmodifier.RequiresReplaceIfFuncResponse) {
			response.RequiresReplace = !request.StateValue.IsNull() && request.PlanValue.IsNull()
		},
		"Removing the review deadline replaces the disposition, because the platform cannot clear one.",
		"Removing the review deadline replaces the disposition, because the platform cannot clear one.",
	)
}

func (dispositionResource *securityDispositionResource) Schema(_ context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	computedString := func(description string) schema.StringAttribute {
		return schema.StringAttribute{MarkdownDescription: description, Computed: true}
	}
	response.Schema = schema.Schema{
		MarkdownDescription: "A vulnerability disposition: an acknowledgement or an accepted risk over the finding occurrences its selector matches. " +
			"The selector is derived once, from the anchoring occurrence and the scope; the reason and the lifecycle can change in place. " +
			"Destroying the resource revokes the policy; revocation is the platform's only removal and keeps the policy in the audit history. " +
			"A policy revoked outside Terraform is planned for recreation. " +
			"The counterpart of `ankra security dispositions create`, `update` and `revoke`.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Identifier of the disposition policy.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"occurrence_id": schema.StringAttribute{
				MarkdownDescription: "Finding occurrence the policy is anchored on (from `ankra security finding <id>`). " +
					"The platform never returns it, so it is not read back; after an import the configured value is recorded without a change. " +
					"Changing it replaces the disposition.",
				Required:      true,
				Validators:    []validator.String{securityUUIDValidator{}},
				PlanModifiers: []planmodifier.String{securityRequiresReplaceOnceKnown()},
			},
			"scope": schema.StringAttribute{
				MarkdownDescription: "Selector scope: `organisation_addon` (the default: the occurrence's add-on across the organisation, which needs a confident add-on attribution), " +
					"`workload` (the one workload the occurrence was seen in) or `image` (the image digest, wherever it runs). Changing it replaces the disposition.",
				Optional: true,
				Computed: true,
				Validators: []validator.String{
					securityOneOf(securityScopeOrganisationAddon, securityScopeWorkload, securityScopeImage),
				},
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown(), stringplanmodifier.RequiresReplace()},
			},
			"disposition": schema.StringAttribute{
				MarkdownDescription: "`acknowledged` or `accepted_risk`. An accepted risk needs `expires_at`. Changing it replaces the disposition.",
				Required:            true,
				Validators:          []validator.String{securityOneOf("acknowledged", "accepted_risk")},
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"reason": schema.StringAttribute{
				MarkdownDescription: "Why the finding is acknowledged or its risk accepted, kept for audit (at most 2000 characters).",
				Required:            true,
				Validators:          []validator.String{securityReasonValidator{}},
			},
			"expires_at": schema.StringAttribute{
				MarkdownDescription: "Review deadline, an RFC 3339 timestamp or a `YYYY-MM-DD` date; it must be in the future, and within a year for an accepted risk. " +
					"It can be moved in place, but removing it replaces the disposition because the platform cannot clear a deadline.",
				Optional:      true,
				Validators:    []validator.String{securityTimestampValidator{}},
				PlanModifiers: []planmodifier.String{securityRequiresReplaceWhenCleared()},
			},
			"expire_when_fix_available": schema.BoolAttribute{
				MarkdownDescription: "End the disposition the moment a fixed version appears. Defaults to `false`.",
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(false),
			},
			"allow_unmatched": schema.BoolAttribute{
				MarkdownDescription: "Store the policy even when its anchor no longer matches any occurrence. Only read at creation, so changing it replaces the disposition. Defaults to `false`.",
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(false),
				PlanModifiers:       []planmodifier.Bool{boolplanmodifier.RequiresReplace()},
			},
			"revoke_reason": schema.StringAttribute{
				MarkdownDescription: "Reason recorded when Terraform revokes the policy on destroy. Changing it only changes what a later destroy records. Defaults to `" + securityDefaultRevokeReason + "`.",
				Optional:            true,
				Computed:            true,
				Default:             stringdefault.StaticString(securityDefaultRevokeReason),
			},
			"selector": schema.SingleNestedAttribute{
				MarkdownDescription: "What the policy matches, as the platform derived it. Members the scope does not pin are null.",
				Computed:            true,
				PlanModifiers:       []planmodifier.Object{objectplanmodifier.UseStateForUnknown()},
				Attributes: map[string]schema.Attribute{
					"cve_id":             computedString("CVE identifier."),
					"package_type":       computedString("Package ecosystem."),
					"package_name":       computedString("Package name."),
					"addon_slug":         computedString("Add-on the policy is pinned to (scope `organisation_addon`)."),
					"cluster_id":         computedString("Cluster of the pinned workload (scope `workload`)."),
					"workload_kind":      computedString("Kind of the pinned workload (scope `workload`)."),
					"workload_namespace": computedString("Namespace of the pinned workload (scope `workload`)."),
					"workload_name":      computedString("Name of the pinned workload (scope `workload`)."),
					"image_digest":       computedString("Pinned image digest (scope `image`)."),
				},
			},
			"selector_hash": schema.StringAttribute{
				MarkdownDescription: "Hash of the canonical selector.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"status":                   computedString("Lifecycle status: active, expiring, fix_available, mitigated, unmatched or expired."),
			"match_health":             computedString("Whether the selector currently matches: matched, unmatched or fix_available."),
			"matched_occurrence_count": schema.Int64Attribute{MarkdownDescription: "Occurrences the policy currently matches.", Computed: true},
			"created_by": schema.StringAttribute{
				MarkdownDescription: "Ankra user id that created the policy.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"created_at": schema.StringAttribute{
				MarkdownDescription: "When the policy was created (RFC 3339).",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
		},
	}
}

// ValidateConfig refuses at plan time an accepted risk without a review
// deadline, which the platform rejects.
func (dispositionResource *securityDispositionResource) ValidateConfig(ctx context.Context, request resource.ValidateConfigRequest, response *resource.ValidateConfigResponse) {
	var disposition, expiresAt types.String
	response.Diagnostics.Append(request.Config.GetAttribute(ctx, path.Root("disposition"), &disposition)...)
	response.Diagnostics.Append(request.Config.GetAttribute(ctx, path.Root("expires_at"), &expiresAt)...)
	if response.Diagnostics.HasError() || disposition.IsUnknown() || expiresAt.IsUnknown() {
		return
	}
	if disposition.ValueString() == "accepted_risk" && expiresAt.IsNull() {
		response.Diagnostics.AddAttributeError(path.Root("expires_at"), "Missing review deadline",
			"An accepted risk needs a review deadline: set expires_at.")
	}
}

func (dispositionResource *securityDispositionResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	apiClient := dispositionResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var plan securityDispositionModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}
	expiresAt, ok := securityExpiresAt(plan.ExpiresAt, &response.Diagnostics)
	if !ok {
		return
	}

	created, err := apiClient.CreateSecurityDisposition(ctx, client.SecurityDispositionCreate{
		OccurrenceID:           plan.OccurrenceID.ValueString(),
		Scope:                  plan.Scope.ValueString(),
		Disposition:            plan.Disposition.ValueString(),
		ExpireWhenFixAvailable: plan.ExpireWhenFixAvailable.ValueBool(),
		Reason:                 plan.Reason.ValueString(),
		ExpiresAt:              expiresAt,
		AllowUnmatched:         plan.AllowUnmatched.ValueBool(),
	})
	if err != nil {
		response.Diagnostics.AddError("Unable to create security disposition", err.Error())
		return
	}

	plan.ID = types.StringValue(created.ID)
	securityApplyDisposition(&plan, created)
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
	response.Diagnostics.Append(trackScope(ctx, apiClient, response.Private)...)
}

func (dispositionResource *securityDispositionResource) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	apiClient := dispositionResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state securityDispositionModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}

	policy, err := apiClient.GetSecurityDisposition(ctx, state.ID.ValueString())
	if removeWhenNotFound(ctx, err, apiClient, response) {
		return
	}
	if err != nil {
		response.Diagnostics.AddError("Unable to read security disposition", err.Error())
		return
	}
	if policy.Status == client.SecurityDispositionStatusRevoked || policy.RevokedAt != nil {
		response.State.RemoveResource(ctx)
		return
	}

	state.Disposition = types.StringValue(policy.Disposition)
	state.Reason = types.StringValue(policy.Reason)
	state.ExpireWhenFixAvailable = types.BoolValue(policy.ExpireWhenFixAvailable)
	state.ExpiresAt = securityReadExpiresAt(state.ExpiresAt, policy.ExpiresAt)
	securityApplyDisposition(&state, policy)
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
	response.Diagnostics.Append(trackScope(ctx, apiClient, response.Private)...)
}

func (dispositionResource *securityDispositionResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	apiClient := dispositionResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var plan, state securityDispositionModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	expiresAt, ok := securityExpiresAt(plan.ExpiresAt, &response.Diagnostics)
	if !ok {
		return
	}

	plan.ID = state.ID
	if state.OccurrenceID.IsNull() && !plan.OccurrenceID.IsNull() &&
		!securityAnchorMatches(ctx, apiClient, plan, state, &response.Diagnostics) {
		return
	}
	isPolicyChanged := !plan.Reason.Equal(state.Reason) ||
		!plan.ExpiresAt.Equal(state.ExpiresAt) ||
		!plan.ExpireWhenFixAvailable.Equal(state.ExpireWhenFixAvailable)
	if !isPolicyChanged {
		plan.Selector = state.Selector
		plan.SelectorHash = state.SelectorHash
		plan.Status = state.Status
		plan.MatchHealth = state.MatchHealth
		plan.MatchedOccurrenceCount = state.MatchedOccurrenceCount
		plan.CreatedBy = state.CreatedBy
		plan.CreatedAt = state.CreatedAt
		plan.Scope = state.Scope
		response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
		response.Diagnostics.Append(trackScope(ctx, apiClient, response.Private)...)
		return
	}

	updated, err := apiClient.UpdateSecurityDisposition(ctx, state.ID.ValueString(), client.SecurityDispositionUpdate{
		Reason:                 stringPointer(plan.Reason),
		ExpiresAt:              expiresAt,
		ExpireWhenFixAvailable: boolPointer(plan.ExpireWhenFixAvailable),
	})
	if err != nil {
		response.Diagnostics.AddError("Unable to update security disposition", err.Error())
		return
	}

	securityApplyDisposition(&plan, updated)
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
	response.Diagnostics.Append(trackScope(ctx, apiClient, response.Private)...)
}

func (dispositionResource *securityDispositionResource) Delete(ctx context.Context, request resource.DeleteRequest, response *resource.DeleteResponse) {
	apiClient := dispositionResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state securityDispositionModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}

	reason := state.RevokeReason.ValueString()
	if reason == "" {
		reason = securityDefaultRevokeReason
	}
	err := apiClient.RevokeSecurityDisposition(ctx, state.ID.ValueString(), reason)
	if err == nil || deletedWhenNotFound(ctx, err, apiClient, request.Private, &response.Diagnostics) || response.Diagnostics.HasError() {
		return
	}
	response.Diagnostics.AddError("Unable to revoke security disposition", err.Error())
}

// securityAnchorMatches checks, when the occurrence is first recorded after
// an import, that the configured occurrence and scope derive the selector
// the stored policy already has. The platform never returns the anchoring
// occurrence, so without this any occurrence in configuration would be
// recorded while the policy goes on matching something else.
func securityAnchorMatches(ctx context.Context, apiClient *client.Client, plan, state securityDispositionModel, diagnostics *diag.Diagnostics) bool {
	preview, err := apiClient.PreviewSecurityDisposition(ctx, client.SecurityDispositionPreviewRequest{
		OccurrenceID: plan.OccurrenceID.ValueString(),
		Scope:        plan.Scope.ValueString(),
		Disposition:  plan.Disposition.ValueString(),
	})
	if err != nil {
		diagnostics.AddError("Unable to check the disposition's anchoring occurrence", err.Error())
		return false
	}
	if preview.SelectorHash != state.SelectorHash.ValueString() {
		diagnostics.AddAttributeError(path.Root("occurrence_id"), "Occurrence does not match the imported disposition",
			"occurrence_id "+plan.OccurrenceID.ValueString()+" with scope "+plan.Scope.ValueString()+" derives selector "+
				preview.SelectorHash+", but the imported policy's selector is "+state.SelectorHash.ValueString()+
				". Configure the occurrence and scope the policy was created from, or replace the disposition.")
		return false
	}
	return true
}

// ImportState imports by policy id. The anchoring occurrence is never
// returned, so `occurrence_id` stays empty until the next apply records the
// configured value, after checking that it derives the stored selector.
func (dispositionResource *securityDispositionResource) ImportState(ctx context.Context, request resource.ImportStateRequest, response *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), request, response)
	response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("allow_unmatched"), false)...)
	response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("revoke_reason"), securityDefaultRevokeReason)...)
}

// securityExpiresAt parses the configured review deadline; null is nil.
func securityExpiresAt(value types.String, diagnostics *diag.Diagnostics) (*time.Time, bool) {
	if value.IsNull() || value.IsUnknown() {
		return nil, true
	}
	parsed, err := securityParseTimestamp(value.ValueString())
	if err != nil {
		diagnostics.AddAttributeError(path.Root("expires_at"), "Invalid timestamp", err.Error())
		return nil, false
	}
	utc := parsed.UTC()
	return &utc, true
}

// securityReadExpiresAt keeps the configured spelling of a deadline while it
// names the same instant the platform stored, so a date or an offset
// timestamp does not plan a diff against the UTC value read back.
func securityReadExpiresAt(current types.String, stored *time.Time) types.String {
	if stored == nil {
		return types.StringNull()
	}
	if !current.IsNull() && !current.IsUnknown() {
		if parsed, err := securityParseTimestamp(current.ValueString()); err == nil && parsed.Equal(*stored) {
			return current
		}
	}
	return types.StringValue(stored.UTC().Format(time.RFC3339))
}

// securityApplyDisposition copies the platform-derived members of a policy
// onto a model.
func securityApplyDisposition(model *securityDispositionModel, policy *client.SecurityDisposition) {
	selector := policy.Selector
	model.Selector = types.ObjectValueMust(securityDispositionSelectorAttributeTypes, map[string]attr.Value{
		"cve_id":             optionalString(selector.CVEID),
		"package_type":       optionalString(selector.PackageType),
		"package_name":       optionalString(selector.PackageName),
		"addon_slug":         optionalString(selector.AddonSlug),
		"cluster_id":         optionalString(selector.ClusterID),
		"workload_kind":      optionalString(selector.WorkloadKind),
		"workload_namespace": optionalString(selector.WorkloadNamespace),
		"workload_name":      optionalString(selector.WorkloadName),
		"image_digest":       optionalString(selector.ImageDigest),
	})
	model.Scope = types.StringValue(securityScopeOfSelector(selector))
	model.SelectorHash = types.StringValue(policy.SelectorHash)
	model.Status = types.StringValue(policy.Status)
	model.MatchHealth = types.StringValue(policy.MatchHealth)
	model.MatchedOccurrenceCount = types.Int64Value(policy.MatchedOccurrenceCount)
	model.CreatedBy = types.StringValue(policy.CreatedBy)
	model.CreatedAt = types.StringValue(policy.CreatedAt.UTC().Format(time.RFC3339))
}

// securityScopeOfSelector names the scope a selector was built with: an image
// scope pins only the digest, a workload scope pins the workload, and the
// add-on scope pins the add-on.
func securityScopeOfSelector(selector client.SecurityDispositionSelector) string {
	switch {
	case selector.ImageDigest != "":
		return securityScopeImage
	case selector.WorkloadName != "":
		return securityScopeWorkload
	default:
		return securityScopeOrganisationAddon
	}
}
