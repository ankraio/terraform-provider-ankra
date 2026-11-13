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

var (
	serviceCredentialNamePattern   = regexp.MustCompile(`^[a-z][a-z0-9]*(-[a-z0-9]+)*$`)
	serviceCredentialDigestPattern = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)
	serviceCredentialInputPattern  = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,62}$`)
	serviceCredentialGrantModes    = []string{"hosted", "customer", "existing"}
)

const serviceCredentialRotationSummary = "Writing a new value retires the previous one, revokes its grants and reissues them on the new value."

var (
	_ resource.ResourceWithImportState = (*serviceCredentialResource)(nil)
	_ resource.ResourceWithImportState = (*serviceCredentialGrantResource)(nil)
	_ resource.ResourceWithImportState = (*servicePackageShareResource)(nil)
	_ resource.ResourceWithConfigure   = (*serviceCredentialResource)(nil)
	_ resource.ResourceWithConfigure   = (*serviceCredentialGrantResource)(nil)
	_ resource.ResourceWithConfigure   = (*servicePackageShareResource)(nil)
)

type serviceCredentialResource struct {
	configuredClient
}

type serviceCredentialModel struct {
	ID             types.String `tfsdk:"id"`
	Name           types.String `tfsdk:"name"`
	ValueWO        types.String `tfsdk:"value_wo"`
	ValueWOVersion types.Int64  `tfsdk:"value_wo_version"`
	ActiveValueID  types.String `tfsdk:"active_value_id"`
}

// NewServiceCredentialResource returns the ankra_service_credential
// resource, the counterpart of `ankra services credentials create` and
// `ankra services credentials values add`.
func NewServiceCredentialResource() resource.Resource {
	return &serviceCredentialResource{}
}

func (credential *serviceCredentialResource) Metadata(_ context.Context, request resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_service_credential"
}

func (credential *serviceCredentialResource) Schema(_ context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: "A managed-service credential (`ankra services credentials`): a named family of immutable secret values a " +
			"service package's inputs are granted. The value is write-only (Terraform 1.11 or later): it goes to the platform's " +
			"secret store and never reaches plan or state. Changing `value_wo_version` writes `value_wo` as the new active value " +
			"in place. " + serviceCredentialRotationSummary + " The platform has no route that deletes a credential family, so " +
			"destroy only stops managing it: the family and its active value stay. Needs `applications.write`.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Identifier of the credential family.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				MarkdownDescription: "Family name: lower-case words joined by hyphens, at most 63 characters. Changing it creates another family.",
				Required:            true,
				Validators:          []validator.String{deployPatternValidator{pattern: serviceCredentialNamePattern, subject: "name"}},
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"value_wo": schema.StringAttribute{
				MarkdownDescription: "The secret value, as a write-only attribute.",
				Required:            true,
				Sensitive:           true,
				WriteOnly:           true,
			},
			"value_wo_version": schema.Int64Attribute{
				MarkdownDescription: "Version of `value_wo`. Changing it writes `value_wo` as the new active value. Right after an import the configured version is recorded without writing a value.",
				Required:            true,
			},
			"active_value_id": schema.StringAttribute{
				MarkdownDescription: "Identifier of the active value, which grants reference.",
				Computed:            true,
			},
		},
	}
}

func (credential *serviceCredentialResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	apiClient := credential.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var plan serviceCredentialModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	value := writeOnlyString(ctx, request.Config, "value_wo", &response.Diagnostics)
	if response.Diagnostics.HasError() {
		return
	}
	family, createError := apiClient.CreateServiceCredentialFamily(ctx, plan.Name.ValueString())
	if createError != nil {
		response.Diagnostics.AddError("Unable to create the service credential", createError.Error())
		return
	}
	plan.ID = types.StringValue(family.ID)
	plan.ValueWO = types.StringNull()
	rotation, writeError := apiClient.WriteServiceCredentialValue(ctx, family.ID, value, family.ActiveValueID)
	if writeError != nil {
		plan.ValueWOVersion = types.Int64Null()
		plan.ActiveValueID = types.StringPointerValue(family.ActiveValueID)
		response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
		response.Diagnostics.AddError("Unable to write the service credential's value",
			writeError.Error()+"\n\nThe credential family was created; the next apply writes the value.")
		return
	}
	plan.ActiveValueID = types.StringValue(rotation.Value.ID)
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
	response.Diagnostics.Append(trackScope(ctx, apiClient, response.Private)...)
}

func (credential *serviceCredentialResource) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	apiClient := credential.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state serviceCredentialModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	family, readError := apiClient.GetServiceCredentialFamily(ctx, state.ID.ValueString())
	if removeWhenNotFound(ctx, readError, apiClient, response) {
		return
	}
	if readError != nil {
		response.Diagnostics.AddError("Unable to read the service credential", readError.Error())
		return
	}
	state.Name = types.StringValue(family.Name)
	state.ActiveValueID = types.StringPointerValue(family.ActiveValueID)
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
	response.Diagnostics.Append(trackScope(ctx, apiClient, response.Private)...)
}

func (credential *serviceCredentialResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	apiClient := credential.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var plan, state serviceCredentialModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	plan.ID = state.ID
	plan.ActiveValueID = state.ActiveValueID
	isImportRecord := state.ValueWOVersion.IsNull() && !state.ActiveValueID.IsNull()
	if !isImportRecord && !plan.ValueWOVersion.Equal(state.ValueWOVersion) {
		value := writeOnlyString(ctx, request.Config, "value_wo", &response.Diagnostics)
		if response.Diagnostics.HasError() {
			return
		}
		rotation, writeError := apiClient.WriteServiceCredentialValue(ctx, state.ID.ValueString(), value, stringPointer(state.ActiveValueID))
		if writeError != nil {
			response.Diagnostics.AddError("Unable to write the service credential's value", writeError.Error())
			return
		}
		plan.ActiveValueID = types.StringValue(rotation.Value.ID)
	}
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
}

// Delete only stops managing the family: the platform has no route that
// deletes one.
func (credential *serviceCredentialResource) Delete(_ context.Context, _ resource.DeleteRequest, response *resource.DeleteResponse) {
	response.Diagnostics.AddWarning("Service credential left in place",
		"The platform has no route that deletes a managed-service credential family, so it and its active value stay; "+
			"revoke its grants to stop packages using it.")
}

// ImportState imports by family id. The first apply records the configured
// value_wo_version without writing a value.
func (credential *serviceCredentialResource) ImportState(ctx context.Context, request resource.ImportStateRequest, response *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), request, response)
}

type serviceCredentialGrantResource struct {
	configuredClient
}

type serviceCredentialGrantModel struct {
	ID            types.String `tfsdk:"id"`
	FamilyID      types.String `tfsdk:"family_id"`
	ValueID       types.String `tfsdk:"value_id"`
	PackageDigest types.String `tfsdk:"package_digest"`
	InputName     types.String `tfsdk:"input_name"`
	Mode          types.String `tfsdk:"mode"`
}

// NewServiceCredentialGrantResource returns the
// ankra_service_credential_grant resource, the counterpart of
// `ankra services credentials grants issue` and `revoke`.
func NewServiceCredentialGrantResource() resource.Resource {
	return &serviceCredentialGrantResource{}
}

func (grant *serviceCredentialGrantResource) Metadata(_ context.Context, request resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_service_credential_grant"
}

func serviceCredentialReplacedString(description string, validators ...validator.String) schema.StringAttribute {
	return schema.StringAttribute{
		MarkdownDescription: description,
		Required:            true,
		Validators:          validators,
		PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
	}
}

func (grant *serviceCredentialGrantResource) Schema(_ context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: "A purpose-bound reference to a managed-service credential value (`ankra services credentials grants`): it lets " +
			"one input of one service package digest read the value, in the given mode. It carries no secret. The platform cannot " +
			"change a grant, so any change replaces it. A grant revoked outside Terraform, or by a rotation of its credential, is " +
			"planned again. Destroy revokes it.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Identifier of the grant.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"family_id": serviceCredentialReplacedString("Identifier of the credential family (`ankra_service_credential`)."),
			"value_id":  serviceCredentialReplacedString("Identifier of the credential value granted, normally the family's `active_value_id`."),
			"package_digest": serviceCredentialReplacedString("Digest of the service package, `sha256:` and 64 hex digits.",
				deployPatternValidator{pattern: serviceCredentialDigestPattern, subject: "package_digest"}),
			"input_name": serviceCredentialReplacedString("Package input the value is bound to: upper-case letters, digits and underscores, starting with a letter.",
				deployPatternValidator{pattern: serviceCredentialInputPattern, subject: "input_name"}),
			"mode": serviceCredentialReplacedString("`hosted`, `customer` or `existing`.",
				organisationStringOneOf{values: serviceCredentialGrantModes}),
		},
	}
}

func (grant *serviceCredentialGrantResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	apiClient := grant.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var plan serviceCredentialGrantModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}
	issued, issueError := apiClient.IssueServiceCredentialGrant(ctx, client.ServiceCredentialGrantCreate{
		FamilyID: plan.FamilyID.ValueString(), ValueID: plan.ValueID.ValueString(), PackageDigest: plan.PackageDigest.ValueString(),
		InputName: plan.InputName.ValueString(), Mode: plan.Mode.ValueString(),
	})
	if issueError != nil {
		response.Diagnostics.AddError("Unable to issue the service credential grant", issueError.Error())
		return
	}
	plan.ID = types.StringValue(issued.ID)
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
	response.Diagnostics.Append(trackScope(ctx, apiClient, response.Private)...)
}

func (grant *serviceCredentialGrantResource) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	apiClient := grant.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state serviceCredentialGrantModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	grants, listError := apiClient.ListServiceCredentialGrants(ctx, state.FamilyID.ValueString())
	if removeWhenNotFound(ctx, listError, apiClient, response) {
		return
	}
	if listError != nil {
		response.Diagnostics.AddError("Unable to read the service credential grants", listError.Error())
		return
	}
	for _, listed := range grants {
		if listed.ID != state.ID.ValueString() {
			continue
		}
		if listed.RevokedAt != nil {
			break
		}
		state.ValueID = types.StringValue(listed.ValueID)
		state.PackageDigest = types.StringValue(listed.PackageDigest)
		state.InputName = types.StringValue(listed.InputName)
		state.Mode = types.StringValue(listed.Mode)
		response.Diagnostics.Append(response.State.Set(ctx, &state)...)
		response.Diagnostics.Append(trackScope(ctx, apiClient, response.Private)...)
		return
	}
	removeWhenTargetChanged(ctx, apiClient, response)
}

// Update is never planned: every attribute replaces the grant.
func (grant *serviceCredentialGrantResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	var plan serviceCredentialGrantModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
}

func (grant *serviceCredentialGrantResource) Delete(ctx context.Context, request resource.DeleteRequest, response *resource.DeleteResponse) {
	apiClient := grant.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state serviceCredentialGrantModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	revokeError := apiClient.RevokeServiceCredentialGrant(ctx, state.ID.ValueString())
	if revokeError == nil || deletedWhenNotFound(ctx, revokeError, apiClient, request.Private, &response.Diagnostics) || response.Diagnostics.HasError() {
		return
	}
	response.Diagnostics.AddError("Unable to revoke the service credential grant", revokeError.Error())
}

// ImportState imports by `<family_id>/<grant_id>`.
func (grant *serviceCredentialGrantResource) ImportState(ctx context.Context, request resource.ImportStateRequest, response *resource.ImportStateResponse) {
	parts, splitError := organisationSplitImportID(request.ID, 2, "<family_id>/<grant_id>")
	if splitError != nil {
		response.Diagnostics.AddError("Invalid import id", splitError.Error())
		return
	}
	response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("family_id"), parts[0])...)
	response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("id"), parts[1])...)
}

type servicePackageShareResource struct {
	configuredClient
}

type servicePackageShareModel struct {
	ID             types.String `tfsdk:"id"`
	VersionID      types.String `tfsdk:"version_id"`
	OrganisationID types.String `tfsdk:"organisation_id"`
}

// NewServicePackageShareResource returns the ankra_service_package_share
// resource: one recipient organisation of a service package version.
func NewServicePackageShareResource() resource.Resource {
	return &servicePackageShareResource{}
}

func (share *servicePackageShareResource) Metadata(_ context.Context, request resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_service_package_share"
}

func (share *servicePackageShareResource) Schema(_ context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: "Shares a service package version the organisation owns with another organisation, so it appears in that " +
			"organisation's catalogue. Sharing grants no deployment permission and does not approve hosted operation. Destroy " +
			"revokes the share. Needs `profiles.share` on the package's profile.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "`<version_id>/<organisation_id>`.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"version_id":      serviceCredentialReplacedString("Identifier of the service package version."),
			"organisation_id": serviceCredentialReplacedString("Identifier of the recipient organisation."),
		},
	}
}

func (share *servicePackageShareResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	apiClient := share.requireOrganisationClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var plan servicePackageShareModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}
	if shareError := apiClient.ShareServicePackage(ctx, plan.VersionID.ValueString(), plan.OrganisationID.ValueString()); shareError != nil {
		response.Diagnostics.AddError("Unable to share the service package", shareError.Error())
		return
	}
	plan.ID = types.StringValue(plan.VersionID.ValueString() + "/" + plan.OrganisationID.ValueString())
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
	response.Diagnostics.Append(trackScope(ctx, apiClient, response.Private)...)
}

func (share *servicePackageShareResource) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	apiClient := share.requireOrganisationClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state servicePackageShareModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	recipients, listError := apiClient.ListServicePackageShares(ctx, state.VersionID.ValueString())
	if removeWhenNotFound(ctx, listError, apiClient, response) {
		return
	}
	if listError != nil {
		response.Diagnostics.AddError("Unable to read the service package's shares", listError.Error())
		return
	}
	for _, recipient := range recipients {
		if recipient == state.OrganisationID.ValueString() {
			state.ID = types.StringValue(state.VersionID.ValueString() + "/" + recipient)
			response.Diagnostics.Append(response.State.Set(ctx, &state)...)
			response.Diagnostics.Append(trackScope(ctx, apiClient, response.Private)...)
			return
		}
	}
	removeWhenTargetChanged(ctx, apiClient, response)
}

// Update is never planned: both attributes replace the share.
func (share *servicePackageShareResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	var plan servicePackageShareModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
}

func (share *servicePackageShareResource) Delete(ctx context.Context, request resource.DeleteRequest, response *resource.DeleteResponse) {
	apiClient := share.requireOrganisationClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state servicePackageShareModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	revokeError := apiClient.RevokeServicePackageShare(ctx, state.VersionID.ValueString(), state.OrganisationID.ValueString())
	if revokeError == nil || deletedWhenNotFound(ctx, revokeError, apiClient, request.Private, &response.Diagnostics) || response.Diagnostics.HasError() {
		return
	}
	response.Diagnostics.AddError("Unable to revoke the service package share", revokeError.Error())
}

// ImportState imports by `<version_id>/<organisation_id>`.
func (share *servicePackageShareResource) ImportState(ctx context.Context, request resource.ImportStateRequest, response *resource.ImportStateResponse) {
	parts, splitError := organisationSplitImportID(request.ID, 2, "<version_id>/<organisation_id>")
	if splitError != nil {
		response.Diagnostics.AddError("Invalid import id", splitError.Error())
		return
	}
	response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("version_id"), parts[0])...)
	response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("organisation_id"), parts[1])...)
	response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("id"), request.ID)...)
}
