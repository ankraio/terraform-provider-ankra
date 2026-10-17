// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"sort"

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

// costAutopilotID is the fixed identifier of the organisation's one
// autopilot policy.
const costAutopilotID = "cost_autopilot"

var (
	_ resource.Resource                = (*costAutopilotResource)(nil)
	_ resource.ResourceWithConfigure   = (*costAutopilotResource)(nil)
	_ resource.ResourceWithImportState = (*costAutopilotResource)(nil)
)

type costAutopilotResource struct {
	configuredClient
}

type costAutopilotQuietHoursModel struct {
	Start    types.String `tfsdk:"start"`
	End      types.String `tfsdk:"end"`
	Timezone types.String `tfsdk:"timezone"`
}

type costAutopilotModel struct {
	ID                  types.String                  `tfsdk:"id"`
	Defaults            types.Map                     `tfsdk:"defaults"`
	EffectiveDefaults   types.Map                     `tfsdk:"effective_defaults"`
	QuietHours          *costAutopilotQuietHoursModel `tfsdk:"quiet_hours"`
	NotificationRouteID types.String                  `tfsdk:"notification_route_id"`
}

// NewCostAutopilotResource returns the ankra_cost_autopilot resource, the
// counterpart of `ankra cost autopilot set`.
func NewCostAutopilotResource() resource.Resource {
	return &costAutopilotResource{}
}

func (autopilotResource *costAutopilotResource) Metadata(_ context.Context, request resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_cost_autopilot"
}

func (autopilotResource *costAutopilotResource) Schema(_ context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: "The organisation's cost autopilot policy: how much Ankra may do about cost unasked, per environment kind. " +
			"There is one policy per organisation, so declare this resource at most once. " +
			"Every environment kind not named in `defaults` is held at `hands-off`. " +
			"Creating it while a policy was already set outside Terraform is refused: import it instead. " +
			"Destroying the resource resets only what the configuration set: the kinds named in `defaults` return to `hands-off`, " +
			"and the quiet hours and notification route are cleared only if the configuration set them. " +
			"After an import, before the first apply, every part counts as set. " +
			"The counterpart of `ankra cost autopilot set`; it needs `billing.manage` and `clusters.write`.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Fixed identifier of the policy, `" + costAutopilotID + "`.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"defaults": schema.MapAttribute{
				MarkdownDescription: "Tier per environment kind. Kinds are `production`, `staging`, `development`, `preview` and `unknown`; " +
					"tiers are `hands-off`, `scheduled`, `managed` and `ephemeral`. A kind left out is `hands-off`.",
				ElementType: types.StringType,
				Optional:    true,
				Validators:  []validator.Map{costAutopilotDefaultsValidator{}},
			},
			"effective_defaults": schema.MapAttribute{
				MarkdownDescription: "The tier every environment kind runs at, as the platform resolves it.",
				ElementType:         types.StringType,
				Computed:            true,
			},
			"quiet_hours": schema.SingleNestedAttribute{
				MarkdownDescription: "Daily window when the autopilot starts no change of its own. Unset means none.",
				Optional:            true,
				Attributes: map[string]schema.Attribute{
					"start": schema.StringAttribute{
						MarkdownDescription: "Start, 24-hour `HH:MM`.",
						Required:            true,
						Validators:          []validator.String{costTimeFormatValidator{layout: "15:04", example: "22:00"}},
					},
					"end": schema.StringAttribute{
						MarkdownDescription: "End, 24-hour `HH:MM`; an end before the start runs past midnight.",
						Required:            true,
						Validators:          []validator.String{costTimeFormatValidator{layout: "15:04", example: "06:00"}},
					},
					"timezone": schema.StringAttribute{
						MarkdownDescription: "IANA timezone, such as `Europe/Stockholm`.",
						Required:            true,
					},
				},
			},
			"notification_route_id": schema.StringAttribute{
				MarkdownDescription: "Notification route the autopilot's pre-notices go to. Unset means the organisation's default routing.",
				Optional:            true,
			},
		},
	}
}

func (autopilotResource *costAutopilotResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	apiClient := autopilotResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var plan costAutopilotModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}
	current, err := apiClient.GetCostAutopilot(ctx)
	if err != nil {
		response.Diagnostics.AddError("Unable to read cost autopilot policy", err.Error())
		return
	}
	if costAutopilotCustomised(current) {
		response.Diagnostics.AddError("Cost autopilot policy already set",
			"The organisation's cost autopilot policy was set outside Terraform. Creating this resource would overwrite it, so import it instead: "+
				"terraform import <address> "+costAutopilotID)
		return
	}
	costWriteAutopilot(ctx, apiClient, &plan, &response.Diagnostics)
	if response.Diagnostics.HasError() {
		return
	}
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
	response.Diagnostics.Append(trackScope(ctx, apiClient, response.Private)...)
	costRecordConfiguredAttributes(ctx, request.Config, costAutopilotAttributes, response.Private, &response.Diagnostics)
}

// costAutopilotAttributes are the parts of the policy the configuration may set.
var costAutopilotAttributes = []string{"defaults", "quiet_hours", "notification_route_id"}

// costAutopilotCustomised reports whether the policy holds anything but the
// never-set behaviour: some kind above hands-off, quiet hours or a route.
func costAutopilotCustomised(policy *client.CostAutopilotPolicy) bool {
	if policy.QuietHours != nil || policy.NotificationRouteID != nil {
		return true
	}
	for _, tier := range policy.Defaults {
		if tier != client.CostAutopilotTierHandsOff {
			return true
		}
	}
	return false
}

func (autopilotResource *costAutopilotResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	apiClient := autopilotResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var plan costAutopilotModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}
	costWriteAutopilot(ctx, apiClient, &plan, &response.Diagnostics)
	if response.Diagnostics.HasError() {
		return
	}
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
	response.Diagnostics.Append(trackScope(ctx, apiClient, response.Private)...)
	costRecordConfiguredAttributes(ctx, request.Config, costAutopilotAttributes, response.Private, &response.Diagnostics)
}

// costWriteAutopilot sends the whole policy: every kind the platform knows, the ones the
// plan leaves out at hands-off, and the quiet hours and route, null when
// unset so they clear.
func costWriteAutopilot(ctx context.Context, apiClient *client.Client, plan *costAutopilotModel, diagnostics *diag.Diagnostics) {
	configured := map[string]string{}
	if !plan.Defaults.IsNull() && !plan.Defaults.IsUnknown() {
		diagnostics.Append(plan.Defaults.ElementsAs(ctx, &configured, false)...)
		if diagnostics.HasError() {
			return
		}
	}
	current, err := apiClient.GetCostAutopilot(ctx)
	if err != nil {
		diagnostics.AddError("Unable to read cost autopilot policy", err.Error())
		return
	}
	update := client.CostAutopilotPolicyUpdate{
		Defaults:            costCompleteDefaults(current.EnvironmentKinds, configured),
		NotificationRouteID: stringPointer(plan.NotificationRouteID),
	}
	if plan.QuietHours != nil {
		update.QuietHours = &client.CostAutopilotQuietHours{
			Start:    plan.QuietHours.Start.ValueString(),
			End:      plan.QuietHours.End.ValueString(),
			Timezone: plan.QuietHours.Timezone.ValueString(),
		}
	}
	policy, err := apiClient.UpdateCostAutopilot(ctx, update)
	if err != nil {
		diagnostics.AddError("Unable to write cost autopilot policy", err.Error())
		return
	}
	plan.ID = types.StringValue(costAutopilotID)
	plan.EffectiveDefaults = costStringMap(policy.Defaults)
}

func (autopilotResource *costAutopilotResource) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	apiClient := autopilotResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state costAutopilotModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}

	policy, err := apiClient.GetCostAutopilot(ctx)
	if err != nil {
		response.Diagnostics.AddError("Unable to read cost autopilot policy", err.Error())
		return
	}

	named := map[string]string{}
	if !state.Defaults.IsNull() && !state.Defaults.IsUnknown() {
		response.Diagnostics.Append(state.Defaults.ElementsAs(ctx, &named, false)...)
		if response.Diagnostics.HasError() {
			return
		}
	}
	observed := map[string]string{}
	for kind, tier := range policy.Defaults {
		if _, isNamed := named[kind]; isNamed || tier != client.CostAutopilotTierHandsOff {
			observed[kind] = tier
		}
	}
	for kind := range named {
		if _, isKnown := policy.Defaults[kind]; !isKnown {
			observed[kind] = client.CostAutopilotTierHandsOff
		}
	}
	if len(observed) == 0 && state.Defaults.IsNull() {
		state.Defaults = types.MapNull(types.StringType)
	} else {
		state.Defaults = costStringMap(observed)
	}

	state.ID = types.StringValue(costAutopilotID)
	state.EffectiveDefaults = costStringMap(policy.Defaults)
	state.NotificationRouteID = types.StringPointerValue(policy.NotificationRouteID)
	state.QuietHours = nil
	if policy.QuietHours != nil {
		state.QuietHours = &costAutopilotQuietHoursModel{
			Start:    types.StringValue(policy.QuietHours.Start),
			End:      types.StringValue(policy.QuietHours.End),
			Timezone: types.StringValue(policy.QuietHours.Timezone),
		}
	}
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
	response.Diagnostics.Append(trackScope(ctx, apiClient, response.Private)...)
}

// Delete resets only what the configuration set: the kinds it named go back
// to hands-off, and the quiet hours and route are cleared only when it set
// them. The platform has no way to unset the policy row itself.
func (autopilotResource *costAutopilotResource) Delete(ctx context.Context, request resource.DeleteRequest, response *resource.DeleteResponse) {
	apiClient := autopilotResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state costAutopilotModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	configured := costConfiguredAttributes(ctx, costAutopilotAttributes, request.Private, &response.Diagnostics)
	if response.Diagnostics.HasError() {
		return
	}
	named := map[string]string{}
	if configured["defaults"] && !state.Defaults.IsNull() && !state.Defaults.IsUnknown() {
		response.Diagnostics.Append(state.Defaults.ElementsAs(ctx, &named, false)...)
	}
	kinds := make([]string, 0, len(named))
	for kind := range named {
		kinds = append(kinds, kind)
	}
	sort.Strings(kinds)
	if err := apiClient.ResetCostAutopilot(ctx, kinds, configured["quiet_hours"], configured["notification_route_id"]); err != nil {
		response.Diagnostics.AddError("Unable to reset cost autopilot policy", err.Error())
	}
}

// ImportState accepts any identifier; the organisation has one policy.
func (autopilotResource *costAutopilotResource) ImportState(ctx context.Context, _ resource.ImportStateRequest, response *resource.ImportStateResponse) {
	response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("id"), costAutopilotID)...)
}

// costCompleteDefaults names every known kind: the configured tier, or
// hands-off for a kind the configuration leaves out. A configured kind the
// platform does not list is still sent, so the platform names the mistake.
func costCompleteDefaults(kinds []string, configured map[string]string) map[string]string {
	defaults := make(map[string]string, len(kinds)+len(configured))
	for _, kind := range kinds {
		defaults[kind] = client.CostAutopilotTierHandsOff
	}
	for kind, tier := range configured {
		defaults[kind] = tier
	}
	return defaults
}

func costStringMap(values map[string]string) types.Map {
	elements := make(map[string]attr.Value, len(values))
	for key, value := range values {
		elements[key] = types.StringValue(value)
	}
	return types.MapValueMust(types.StringType, elements)
}
