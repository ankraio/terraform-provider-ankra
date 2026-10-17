// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
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
	_ resource.Resource                = (*registryRetentionResource)(nil)
	_ resource.ResourceWithConfigure   = (*registryRetentionResource)(nil)
	_ resource.ResourceWithImportState = (*registryRetentionResource)(nil)
)

// registryRetentionID is the fixed identifier of the organisation's single
// retention.
const registryRetentionID = "registry_retention"

type registryRetentionResource struct {
	configuredClient
}

type registryRetentionPolicyModel struct {
	KeepDays   types.Int64 `tfsdk:"keep_days"`
	KeepLatest types.Int64 `tfsdk:"keep_latest"`
}

type registryRetentionRuleModel struct {
	RepositoryPattern types.String `tfsdk:"repository_pattern"`
	ApplicationID     types.String `tfsdk:"application_id"`
	KeepDays          types.Int64  `tfsdk:"keep_days"`
	KeepLatest        types.Int64  `tfsdk:"keep_latest"`
}

type registryRetentionModel struct {
	ID       types.String                  `tfsdk:"id"`
	Default  *registryRetentionPolicyModel `tfsdk:"default"`
	Rules    []registryRetentionRuleModel  `tfsdk:"rules"`
	Source   types.String                  `tfsdk:"source"`
	Version  types.String                  `tfsdk:"version"`
	InForce  types.Object                  `tfsdk:"default_in_force"`
	Platform types.Object                  `tfsdk:"platform_default"`
}

// NewRegistryRetentionResource returns the ankra_registry_retention
// resource, the counterpart of `ankra registry storage retention set` and
// `ankra registry storage retention rule add|remove`.
func NewRegistryRetentionResource() resource.Resource {
	return &registryRetentionResource{}
}

func (retentionResource *registryRetentionResource) Metadata(_ context.Context, request resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_registry_retention"
}

func registryRetentionPolicyAttributes(computed bool) map[string]schema.Attribute {
	return map[string]schema.Attribute{
		"keep_days": schema.Int64Attribute{
			MarkdownDescription: "Keep every image pushed within this many days (0 to 3650; 0 turns this rule off).",
			Required:            !computed,
			Computed:            computed,
		},
		"keep_latest": schema.Int64Attribute{
			MarkdownDescription: "Keep this many most recently pushed images of each repository, whatever their age (1 to 1000).",
			Required:            !computed,
			Computed:            computed,
		},
	}
}

func (retentionResource *registryRetentionResource) Schema(_ context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: "The organisation's image retention on the Ankra registry: which images it keeps, so it can bring its " +
			"storage down itself. One per organisation. The counterpart of `ankra registry storage retention set` and " +
			"`ankra registry storage retention rule add|remove`.\n\n" +
			"This resource owns the whole policy and rule list: rules added outside Terraform show as drift and are removed " +
			"on the next apply. Creating it refuses to overwrite a policy or rules the organisation already set; import " +
			"them instead. Every save sends the version Terraform last read, so a change someone made in between is " +
			"refused instead of overwritten. Destroying the resource restores the platform default and removes every rule.\n\n" +
			"Retention deletes images. Two protections hold whatever is set: at least the newest image of every repository " +
			"is kept, and an organisation-set policy also keeps every image pulled within the last week.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Always `" + registryRetentionID + "`.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"default": schema.SingleNestedAttribute{
				MarkdownDescription: "The organisation's project-wide policy. Unset uses the platform default, which keeps nothing a " +
					"repository rule could narrow, so `rules` need it set.",
				Optional:   true,
				Attributes: registryRetentionPolicyAttributes(false),
			},
			"rules": schema.SetNestedAttribute{
				MarkdownDescription: "Per-repository policies (at most 50). A repository a rule matches follows that rule instead of " +
					"`default`; when rules overlap, the most specific pattern decides.",
				Optional: true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"repository_pattern": schema.StringAttribute{
							MarkdownDescription: "Repository path pattern inside the registry project, lowercase, where `*` matches within a " +
								"path segment and `**` across segments (for example `ankra-ci/**`).",
							Required: true,
						},
						"application_id": schema.StringAttribute{
							MarkdownDescription: "Application the rule was made for, if any: a lower-case UUID.",
							Optional:            true,
							Validators:          []validator.String{catalogueLowerCaseUUIDValidator},
						},
						"keep_days":   registryRetentionPolicyAttributes(false)["keep_days"],
						"keep_latest": registryRetentionPolicyAttributes(false)["keep_latest"],
					},
				},
			},
			"source": schema.StringAttribute{
				MarkdownDescription: "`organisation` when `default` is set, `default` while the platform default applies.",
				Computed:            true,
			},
			"version": schema.StringAttribute{
				MarkdownDescription: "Opaque version of the stored policy and rules, sent back on every save.",
				Computed:            true,
			},
			"default_in_force": schema.SingleNestedAttribute{
				MarkdownDescription: "The project-wide policy in force: `default`, or the platform default while it is unset.",
				Computed:            true,
				Attributes:          registryRetentionPolicyAttributes(true),
			},
			"platform_default": schema.SingleNestedAttribute{
				MarkdownDescription: "The platform default policy.",
				Computed:            true,
				Attributes:          registryRetentionPolicyAttributes(true),
			},
		},
	}
}

func (retentionResource *registryRetentionResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	apiClient := retentionResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var plan registryRetentionModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}

	existing, err := apiClient.GetRegistryRetention(ctx)
	if err != nil {
		response.Diagnostics.AddError("Unable to read registry retention", err.Error())
		return
	}
	if existing.Source == client.RegistryRetentionSourceOrganisation || len(existing.Rules) > 0 {
		response.Diagnostics.AddError("Registry retention already set",
			"The organisation already has its own retention policy or rules, and creating this resource would overwrite "+
				"them. Import it instead: terraform import <address> "+registryRetentionID)
		return
	}
	retention, harborApplyError, err := apiClient.SetRegistryRetention(ctx, registryRetentionRequest(&plan, stringPointer(types.StringValue(existing.Version))))
	if err != nil {
		response.Diagnostics.AddError("Unable to set registry retention", err.Error())
		return
	}
	registryRetentionHarborWarning(harborApplyError, &response.Diagnostics)
	registryRetentionToState(retention, &plan)
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
}

func (retentionResource *registryRetentionResource) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	apiClient := retentionResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state registryRetentionModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}

	retention, err := apiClient.GetRegistryRetention(ctx)
	if err != nil {
		response.Diagnostics.AddError("Unable to read registry retention", err.Error())
		return
	}
	registryRetentionToState(retention, &state)
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
}

func (retentionResource *registryRetentionResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	apiClient := retentionResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var plan, state registryRetentionModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}

	retention, harborApplyError, err := apiClient.SetRegistryRetention(ctx, registryRetentionRequest(&plan, stringPointer(state.Version)))
	if err != nil {
		response.Diagnostics.AddError("Unable to set registry retention", err.Error())
		return
	}
	registryRetentionHarborWarning(harborApplyError, &response.Diagnostics)
	registryRetentionToState(retention, &plan)
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
}

// Delete restores the platform default and removes every rule.
func (retentionResource *registryRetentionResource) Delete(ctx context.Context, request resource.DeleteRequest, response *resource.DeleteResponse) {
	apiClient := retentionResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state registryRetentionModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	if _, _, err := apiClient.SetRegistryRetention(ctx, client.RegistryRetentionSet{Rules: []client.RegistryRetentionRule{}}); err != nil {
		response.Diagnostics.AddError("Unable to restore the default registry retention", err.Error())
	}
}

// ImportState takes any identifier: there is one retention per
// organisation.
func (retentionResource *registryRetentionResource) ImportState(ctx context.Context, _ resource.ImportStateRequest, response *resource.ImportStateResponse) {
	response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("id"), registryRetentionID)...)
}

// registryRetentionHarborWarning surfaces a save the registry did not apply
// at once: the platform stored it and retries on its next pass.
func registryRetentionHarborWarning(harborApplyError *string, diagnostics *diag.Diagnostics) {
	if harborApplyError != nil && *harborApplyError != "" {
		diagnostics.AddWarning("Registry retention saved but not yet applied",
			"The platform stored the retention, but the registry did not accept it yet; the platform applies it on its "+
				"next pass. The registry answered: "+*harborApplyError)
	}
}

func registryRetentionRequest(plan *registryRetentionModel, expectedVersion *string) client.RegistryRetentionSet {
	request := client.RegistryRetentionSet{Rules: []client.RegistryRetentionRule{}, ExpectedVersion: expectedVersion}
	if plan.Default != nil {
		request.Default = &client.RegistryRetentionPolicy{
			KeepDays:   plan.Default.KeepDays.ValueInt64(),
			KeepLatest: plan.Default.KeepLatest.ValueInt64(),
		}
	}
	for _, rule := range plan.Rules {
		request.Rules = append(request.Rules, client.RegistryRetentionRule{
			RepositoryPattern: rule.RepositoryPattern.ValueString(),
			ApplicationID:     stringPointer(rule.ApplicationID),
			KeepDays:          rule.KeepDays.ValueInt64(),
			KeepLatest:        rule.KeepLatest.ValueInt64(),
		})
	}
	return request
}

func registryRetentionPolicyToModel(policy client.RegistryRetentionPolicy) *registryRetentionPolicyModel {
	return &registryRetentionPolicyModel{
		KeepDays:   types.Int64Value(policy.KeepDays),
		KeepLatest: types.Int64Value(policy.KeepLatest),
	}
}

var registryRetentionPolicyAttributeTypes = map[string]attr.Type{
	"keep_days":   types.Int64Type,
	"keep_latest": types.Int64Type,
}

func registryRetentionPolicyObject(policy client.RegistryRetentionPolicy) types.Object {
	return types.ObjectValueMust(registryRetentionPolicyAttributeTypes, map[string]attr.Value{
		"keep_days":   types.Int64Value(policy.KeepDays),
		"keep_latest": types.Int64Value(policy.KeepLatest),
	})
}

// registryRetentionToState copies the stored retention into state. The
// policy in force is only the organisation's own when its source says so;
// otherwise default stays null, matching a configuration that leaves it out.
// An empty rule list stays null when the configuration had none.
func registryRetentionToState(retention *client.RegistryRetention, state *registryRetentionModel) {
	state.ID = types.StringValue(registryRetentionID)
	state.Source = types.StringValue(retention.Source)
	state.Version = types.StringValue(retention.Version)
	state.InForce = registryRetentionPolicyObject(retention.Default)
	state.Platform = registryRetentionPolicyObject(retention.PlatformDefault)
	state.Default = nil
	if retention.Source == client.RegistryRetentionSourceOrganisation {
		state.Default = registryRetentionPolicyToModel(retention.Default)
	}
	if len(retention.Rules) == 0 {
		if state.Rules != nil {
			state.Rules = []registryRetentionRuleModel{}
		}
		return
	}
	state.Rules = make([]registryRetentionRuleModel, 0, len(retention.Rules))
	for _, rule := range retention.Rules {
		state.Rules = append(state.Rules, registryRetentionRuleModel{
			RepositoryPattern: types.StringValue(rule.RepositoryPattern),
			ApplicationID:     types.StringPointerValue(rule.ApplicationID),
			KeepDays:          types.Int64Value(rule.KeepDays),
			KeepLatest:        types.Int64Value(rule.KeepLatest),
		})
	}
}
