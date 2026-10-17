// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"regexp"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"ankra.io/terraform-provider-ankra/internal/client"
)

// serviceLocationPattern mirrors the platform's region and data boundary
// rule (serviceadmission.locationPattern), at most 63 characters.
var serviceLocationPattern = regexp.MustCompile(`^[a-z][a-z0-9]*(-[a-z0-9]+)*$`)

// serviceLocationValidator rejects a region or data boundary the platform
// refuses: off-pattern, longer than 63 characters, or the reserved
// "unspecified" of the undeclared default.
type serviceLocationValidator struct{}

func (serviceLocationValidator) Description(_ context.Context) string {
	return "lower-case letters, digits and single hyphens, starting with a letter, at most 63 characters, and not \"unspecified\""
}

func (locationValidator serviceLocationValidator) MarkdownDescription(ctx context.Context) string {
	return locationValidator.Description(ctx)
}

func (locationValidator serviceLocationValidator) ValidateString(ctx context.Context, request validator.StringRequest, response *validator.StringResponse) {
	if request.ConfigValue.IsNull() || request.ConfigValue.IsUnknown() {
		return
	}
	value := request.ConfigValue.ValueString()
	if len(value) > 63 || value == "unspecified" || !serviceLocationPattern.MatchString(value) {
		response.Diagnostics.AddAttributeError(request.Path, "Invalid location", "The platform refuses it: use "+locationValidator.Description(ctx)+".")
	}
}

// serviceAdmissionUnavailable reports, as an error, a 404 that says nothing
// about the object: the service admission routes answer a bare "Not Found"
// while the organisation's `applications` feature is off, whereas an object
// that is really gone is answered with the platform's own sentence. Reading
// the bare one as "gone" would drop the resource and let a destroy succeed
// while the object lives on. It reports whether it added the error.
func serviceAdmissionUnavailable(err error, diagnostics *diag.Diagnostics) bool {
	detail, isNotFound := clusterSettingsNotFoundDetail(err)
	if !isNotFound || detail != "Not Found" {
		return false
	}
	diagnostics.AddError("Managed services not available",
		"The platform answered a bare 404 for the service admission routes, which it does while managed services (the `applications` feature) "+
			"are not enabled for the provider's organisation. That does not mean the object is gone, so it is kept. "+
			"Check the provider's organisation, or remove the resource with `terraform state rm` if it is really gone. ("+err.Error()+")")
	return true
}

var (
	_ resource.Resource                = (*servicePlacementPolicyResource)(nil)
	_ resource.ResourceWithConfigure   = (*servicePlacementPolicyResource)(nil)
	_ resource.ResourceWithImportState = (*servicePlacementPolicyResource)(nil)
)

type servicePlacementPolicyResource struct {
	configuredClient
}

type servicePlacementPolicyModel struct {
	ID           types.String `tfsdk:"id"`
	ClusterID    types.String `tfsdk:"cluster_id"`
	Region       types.String `tfsdk:"region"`
	DataBoundary types.String `tfsdk:"data_boundary"`
	LocalOnly    types.Bool   `tfsdk:"local_only"`
	Revision     types.Int64  `tfsdk:"revision"`
}

// NewServicePlacementPolicyResource returns the ankra_service_placement_policy
// resource, the counterpart of `ankra services policy set|get`.
func NewServicePlacementPolicyResource() resource.Resource {
	return &servicePlacementPolicyResource{}
}

func (policyResource *servicePlacementPolicyResource) Metadata(_ context.Context, request resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_service_placement_policy"
}

func (policyResource *servicePlacementPolicyResource) Schema(_ context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: "A cluster's managed-service placement policy: the region it runs in, the data boundary its data stays within, " +
			"and whether its services serve only consumers on the same cluster. The counterpart of `ankra services policy set` and `get`. " +
			"It is the organisation's declaration, not verified location evidence.\n\n" +
			"The platform has no route that removes a declaration, so destroying this resource only removes it from state: the cluster keeps " +
			"its last declared policy. Updates carry the revision Terraform last read, so a declaration changed by someone else in between " +
			"is refused rather than overwritten.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "The cluster id.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"cluster_id": schema.StringAttribute{
				MarkdownDescription: "Cluster the policy is declared for. Changing it replaces the resource.",
				Required:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"region": schema.StringAttribute{
				MarkdownDescription: "Physical region the cluster runs in, for example `eu-north-1`.",
				Required:            true,
				Validators:          []validator.String{serviceLocationValidator{}},
			},
			"data_boundary": schema.StringAttribute{
				MarkdownDescription: "Boundary the data may stay within, for example `eu`.",
				Required:            true,
				Validators:          []validator.String{serviceLocationValidator{}},
			},
			"local_only": schema.BoolAttribute{
				MarkdownDescription: "Whether services on the cluster serve only consumers on the same cluster. Defaults to `false`.",
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(false),
			},
			"revision": schema.Int64Attribute{
				MarkdownDescription: "Revision of the declaration; it increases with every change.",
				Computed:            true,
			},
		},
	}
}

func (policyResource *servicePlacementPolicyResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	apiClient := policyResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var plan servicePlacementPolicyModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}
	policy, err := apiClient.SetServicePlacementPolicy(ctx, plan.ClusterID.ValueString(), client.ServicePlacementPolicyWrite{
		ExpectedRevision: 0,
		Region:           plan.Region.ValueString(),
		DataBoundary:     plan.DataBoundary.ValueString(),
		LocalOnly:        plan.LocalOnly.ValueBool(),
	})
	if err != nil {
		response.Diagnostics.AddError("Unable to declare service placement policy",
			err.Error()+"\n\nA 409 means the cluster already declares a policy: import it with its cluster id instead.")
		return
	}
	plan.applyAPI(policy)
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
}

func (policyResource *servicePlacementPolicyResource) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	apiClient := policyResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state servicePlacementPolicyModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	policy, err := apiClient.GetServicePlacementPolicy(ctx, state.ClusterID.ValueString())
	if serviceAdmissionUnavailable(err, &response.Diagnostics) || removeWhenNotFound(ctx, err, response) {
		return
	}
	if err != nil {
		response.Diagnostics.AddError("Unable to read service placement policy", err.Error())
		return
	}
	state.applyAPI(policy)
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
}

func (policyResource *servicePlacementPolicyResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	apiClient := policyResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var plan, state servicePlacementPolicyModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	policy, err := apiClient.SetServicePlacementPolicy(ctx, state.ClusterID.ValueString(), client.ServicePlacementPolicyWrite{
		ExpectedRevision: state.Revision.ValueInt64(),
		Region:           plan.Region.ValueString(),
		DataBoundary:     plan.DataBoundary.ValueString(),
		LocalOnly:        plan.LocalOnly.ValueBool(),
	})
	if err != nil {
		response.Diagnostics.AddError("Unable to update service placement policy", err.Error())
		return
	}
	plan.applyAPI(policy)
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
}

// Delete removes the policy from state only: the platform has no route that
// withdraws a declaration.
func (policyResource *servicePlacementPolicyResource) Delete(_ context.Context, _ resource.DeleteRequest, response *resource.DeleteResponse) {
	policyResource.requireClient(&response.Diagnostics)
}

// ImportState imports by cluster id.
func (policyResource *servicePlacementPolicyResource) ImportState(ctx context.Context, request resource.ImportStateRequest, response *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("cluster_id"), request, response)
}

func (model *servicePlacementPolicyModel) applyAPI(policy *client.ServicePlacementPolicy) {
	model.ID = types.StringValue(policy.ClusterID)
	model.ClusterID = types.StringValue(policy.ClusterID)
	model.Region = types.StringValue(policy.Region)
	model.DataBoundary = types.StringValue(policy.DataBoundary)
	model.LocalOnly = types.BoolValue(policy.LocalOnly)
	model.Revision = types.Int64Value(policy.Revision)
}
