// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"

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
	organisationRoleKubeScopes = []string{"cluster", "namespace"}
	organisationRoleKubeRoles  = []string{"view", "edit", "admin", "cluster-admin"}
)

var (
	_ resource.Resource                = (*organisationRoleResource)(nil)
	_ resource.ResourceWithConfigure   = (*organisationRoleResource)(nil)
	_ resource.ResourceWithImportState = (*organisationRoleResource)(nil)
)

type organisationRoleResource struct {
	configuredClient
}

type organisationRoleModel struct {
	ID          types.String `tfsdk:"id"`
	Slug        types.String `tfsdk:"slug"`
	Name        types.String `tfsdk:"name"`
	Description types.String `tfsdk:"description"`
	Permissions types.Set    `tfsdk:"permissions"`
	KubeGrants  types.Set    `tfsdk:"kube_grants"`
}

type organisationRoleKubeGrantModel struct {
	Scope     types.String `tfsdk:"scope"`
	Namespace types.String `tfsdk:"namespace"`
	K8sRole   types.String `tfsdk:"k8s_role"`
}

var organisationRoleKubeGrantType = types.ObjectType{AttrTypes: map[string]attr.Type{
	"scope":     types.StringType,
	"namespace": types.StringType,
	"k8s_role":  types.StringType,
}}

// NewOrganisationRoleResource returns the ankra_organisation_role resource,
// the counterpart of `ankra org roles`.
func NewOrganisationRoleResource() resource.Resource {
	return &organisationRoleResource{}
}

func (role *organisationRoleResource) Metadata(_ context.Context, request resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_organisation_role"
}

func (role *organisationRoleResource) Schema(_ context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: "A custom organisation role (`ankra org roles`): a named set of permissions, and optionally Kubernetes access " +
			"grants, that members and service accounts can be given. Changing `name`, `description`, `permissions` or `kube_grants` " +
			"updates the role in place, and every member holding it gets the new permissions. Destroy retires the role, which the " +
			"platform refuses while anyone still holds it. Managing roles needs `members.manage`; a role may not carry permissions " +
			"the caller lacks.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Identifier of the role.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"slug": schema.StringAttribute{
				MarkdownDescription: "Slug members are assigned the role by, unique in the organisation. The platform never changes it, so changing it replaces the role.",
				Required:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"name": schema.StringAttribute{
				MarkdownDescription: "Display name of the role.",
				Required:            true,
			},
			"description": schema.StringAttribute{
				MarkdownDescription: "Free-text description.",
				Optional:            true,
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"permissions": schema.SetAttribute{
				MarkdownDescription: "Permissions the role grants, such as `clusters.read` or `applications.write`.",
				Required:            true,
				ElementType:         types.StringType,
			},
			"kube_grants": schema.SetNestedAttribute{
				MarkdownDescription: "Kubernetes access the role grants through the kube gateway.",
				Optional:            true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"scope": schema.StringAttribute{
							MarkdownDescription: "`cluster` or `namespace`.",
							Required:            true,
							Validators:          []validator.String{organisationStringOneOf{values: organisationRoleKubeScopes}},
						},
						"namespace": schema.StringAttribute{
							MarkdownDescription: "Namespace of a `namespace` grant.",
							Optional:            true,
						},
						"k8s_role": schema.StringAttribute{
							MarkdownDescription: "Kubernetes role bound: `view`, `edit`, `admin` or `cluster-admin`.",
							Required:            true,
							Validators:          []validator.String{organisationStringOneOf{values: organisationRoleKubeRoles}},
						},
					},
				},
			},
		},
	}
}

func (role *organisationRoleResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	apiClient := role.requireOrganisationClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var plan organisationRoleModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	write := organisationRoleWrite(ctx, plan, &response.Diagnostics)
	if response.Diagnostics.HasError() {
		return
	}
	write.Slug = plan.Slug.ValueString()
	created, createError := apiClient.CreateOrganisationCustomRole(ctx, write)
	if createError != nil {
		response.Diagnostics.AddError("Unable to create the organisation role", createError.Error())
		return
	}
	organisationRoleApply(&plan, created)
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
	response.Diagnostics.Append(trackScope(ctx, apiClient, response.Private)...)
}

func (role *organisationRoleResource) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	apiClient := role.requireOrganisationClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state organisationRoleModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	roles, listError := apiClient.ListOrganisationCustomRoles(ctx)
	if listError != nil {
		response.Diagnostics.AddError("Unable to read the organisation roles", listError.Error())
		return
	}
	found := organisationRoleFind(roles, state.ID.ValueString(), state.Slug.ValueString())
	if found == nil {
		removeWhenTargetChanged(ctx, apiClient, response)
		return
	}
	if found.Builtin {
		response.Diagnostics.AddError("Built-in role", fmt.Sprintf("%q is a built-in role, which cannot be managed; import a custom role.", found.Slug))
		return
	}
	organisationRoleApply(&state, found)
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
	response.Diagnostics.Append(trackScope(ctx, apiClient, response.Private)...)
}

func (role *organisationRoleResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	apiClient := role.requireOrganisationClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var plan, state organisationRoleModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	write := organisationRoleWrite(ctx, plan, &response.Diagnostics)
	if response.Diagnostics.HasError() {
		return
	}
	updated, updateError := apiClient.UpdateOrganisationCustomRole(ctx, state.ID.ValueString(), write)
	if updateError != nil {
		response.Diagnostics.AddError("Unable to update the organisation role", updateError.Error())
		return
	}
	organisationRoleApply(&plan, updated)
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
}

func (role *organisationRoleResource) Delete(ctx context.Context, request resource.DeleteRequest, response *resource.DeleteResponse) {
	apiClient := role.requireOrganisationClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state organisationRoleModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	deleteError := apiClient.DeleteOrganisationCustomRole(ctx, state.ID.ValueString())
	if deleteError == nil || deletedWhenNotFound(ctx, deleteError, apiClient, request.Private, &response.Diagnostics) || response.Diagnostics.HasError() {
		return
	}
	response.Diagnostics.AddError("Unable to delete the organisation role", deleteError.Error())
}

// ImportState imports by role id or slug.
func (role *organisationRoleResource) ImportState(ctx context.Context, request resource.ImportStateRequest, response *resource.ImportStateResponse) {
	if securityUUIDPattern.MatchString(request.ID) {
		response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("id"), request.ID)...)
		return
	}
	response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("slug"), request.ID)...)
}

// organisationRoleFind finds the role by id or, before the id is known (an
// import by slug), by slug.
func organisationRoleFind(roles []client.OrganisationCustomRole, roleID, slug string) *client.OrganisationCustomRole {
	for index := range roles {
		if roleID != "" && roles[index].ID == roleID {
			return &roles[index]
		}
		if roleID == "" && slug != "" && roles[index].Slug == slug {
			return &roles[index]
		}
	}
	return nil
}

func organisationRoleWrite(ctx context.Context, plan organisationRoleModel, diagnostics *diag.Diagnostics) client.OrganisationCustomRoleWrite {
	write := client.OrganisationCustomRoleWrite{
		Name:        plan.Name.ValueString(),
		Description: plan.Description.ValueString(),
		Permissions: organisationNonNilStrings(organisationStringsFromSet(ctx, plan.Permissions, diagnostics)),
		KubeGrants:  []client.OrganisationRoleKubeGrant{},
	}
	if plan.KubeGrants.IsNull() || plan.KubeGrants.IsUnknown() {
		return write
	}
	var grants []organisationRoleKubeGrantModel
	diagnostics.Append(plan.KubeGrants.ElementsAs(ctx, &grants, false)...)
	for _, grant := range grants {
		write.KubeGrants = append(write.KubeGrants, client.OrganisationRoleKubeGrant{
			Scope: grant.Scope.ValueString(), Namespace: stringPointer(grant.Namespace), K8sRole: grant.K8sRole.ValueString(),
		})
	}
	return write
}

func organisationRoleApply(model *organisationRoleModel, role *client.OrganisationCustomRole) {
	model.ID = types.StringValue(role.ID)
	model.Slug = types.StringValue(role.Slug)
	model.Name = types.StringValue(role.Name)
	model.Description = types.StringValue(role.Description)
	model.Permissions = organisationSetFromStrings(organisationNonNilStrings(role.Permissions))
	if len(role.KubeGrants) == 0 && model.KubeGrants.IsNull() {
		return
	}
	grants := make([]attr.Value, 0, len(role.KubeGrants))
	for _, grant := range role.KubeGrants {
		grants = append(grants, types.ObjectValueMust(organisationRoleKubeGrantType.AttrTypes, map[string]attr.Value{
			"scope":     types.StringValue(grant.Scope),
			"namespace": optionalStringFromPointer(grant.Namespace),
			"k8s_role":  types.StringValue(grant.K8sRole),
		}))
	}
	model.KubeGrants = types.SetValueMust(organisationRoleKubeGrantType, grants)
}
