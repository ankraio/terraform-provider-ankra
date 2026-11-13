// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

const provenancePolicyID = "provenance_policy"

var provenanceModes = []string{"off", "warn", "enforce"}

var (
	_ resource.Resource                = (*provenancePolicyResource)(nil)
	_ resource.ResourceWithConfigure   = (*provenancePolicyResource)(nil)
	_ resource.ResourceWithImportState = (*provenancePolicyResource)(nil)
)

type provenancePolicyResource struct {
	configuredClient
}

type provenancePolicyModel struct {
	ID   types.String `tfsdk:"id"`
	Mode types.String `tfsdk:"mode"`
}

// NewProvenancePolicyResource returns the ankra_provenance_policy resource,
// the counterpart of `ankra org provenance set`.
func NewProvenancePolicyResource() resource.Resource {
	return &provenancePolicyResource{}
}

func (policy *provenancePolicyResource) Metadata(_ context.Context, request resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_provenance_policy"
}

func (policy *provenancePolicyResource) Schema(_ context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: "The organisation's default deploy provenance mode (`ankra org provenance set`), which applications follow when " +
			"they choose none: `off`, `warn` (deploy and file a finding for an image without verified provenance) or `enforce` " +
			"(refuse such a deploy). There is one per organisation; an application's own mode (`ankra application provenance set`) " +
			"overrides it. Signing key rotation and revocation stay imperative (`ankra org provenance rotate`, `revoke`). Setting " +
			"the mode needs `security.manage`. Destroy leaves the mode as it is.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Always `provenance_policy`.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"mode": schema.StringAttribute{
				MarkdownDescription: "`off`, `warn` or `enforce`.",
				Required:            true,
				Validators:          []validator.String{organisationStringOneOf{values: provenanceModes}},
			},
		},
	}
}

func (policy *provenancePolicyResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	var plan provenancePolicyModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if policy.write(ctx, &plan, &response.Diagnostics) {
		response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
		response.Diagnostics.Append(trackScope(ctx, policy.client, response.Private)...)
	}
}

func (policy *provenancePolicyResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	var plan provenancePolicyModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if policy.write(ctx, &plan, &response.Diagnostics) {
		response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
	}
}

func (policy *provenancePolicyResource) write(ctx context.Context, plan *provenancePolicyModel, diagnostics *diag.Diagnostics) bool {
	apiClient := policy.requireOrganisationClient(diagnostics)
	if apiClient == nil || diagnostics.HasError() {
		return false
	}
	stored, setError := apiClient.SetOrganisationProvenancePolicy(ctx, stringPointer(plan.Mode))
	if setError != nil {
		diagnostics.AddError("Unable to set the provenance policy", setError.Error())
		return false
	}
	plan.ID = types.StringValue(provenancePolicyID)
	plan.Mode = types.StringPointerValue(stored.Mode)
	return true
}

func (policy *provenancePolicyResource) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	apiClient := policy.requireOrganisationClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state provenancePolicyModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	stored, readError := apiClient.GetOrganisationProvenancePolicy(ctx)
	if readError != nil {
		response.Diagnostics.AddError("Unable to read the provenance policy", readError.Error())
		return
	}
	if stored.Mode == nil {
		response.State.RemoveResource(ctx)
		return
	}
	state.ID = types.StringValue(provenancePolicyID)
	state.Mode = types.StringValue(*stored.Mode)
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
	response.Diagnostics.Append(trackScope(ctx, apiClient, response.Private)...)
}

// Delete leaves the mode as it is: clearing it would hand applications back
// to Ankra's default, which can be looser than the organisation chose.
func (policy *provenancePolicyResource) Delete(_ context.Context, _ resource.DeleteRequest, _ *resource.DeleteResponse) {
}

// ImportState accepts any id: there is one provenance policy per
// organisation.
func (policy *provenancePolicyResource) ImportState(ctx context.Context, _ resource.ImportStateRequest, response *resource.ImportStateResponse) {
	response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("id"), provenancePolicyID)...)
}
