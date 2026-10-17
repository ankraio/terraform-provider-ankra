// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"regexp"

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

// serviceNamespacePattern mirrors the platform's consumer namespace rule
// (servicepackage.ValidNamespace): a DNS label of at most 63 characters.
var serviceNamespacePattern = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?$`)

var (
	_ resource.Resource                = (*serviceConsumerResource)(nil)
	_ resource.ResourceWithConfigure   = (*serviceConsumerResource)(nil)
	_ resource.ResourceWithImportState = (*serviceConsumerResource)(nil)
)

type serviceConsumerResource struct {
	configuredClient
}

type serviceConsumerModel struct {
	ID              types.String `tfsdk:"id"`
	ApplicationID   types.String `tfsdk:"application_id"`
	ClusterID       types.String `tfsdk:"cluster_id"`
	Namespace       types.String `tfsdk:"namespace"`
	AllowPlanned    types.Bool   `tfsdk:"allow_planned"`
	LocalOnly       types.Bool   `tfsdk:"local_only"`
	Region          types.String `tfsdk:"region"`
	DataBoundary    types.String `tfsdk:"data_boundary"`
	BindingKind     types.String `tfsdk:"binding_kind"`
	BindingRevision types.Int64  `tfsdk:"binding_revision"`
	InstallationID  types.String `tfsdk:"installation_id"`
}

// NewServiceConsumerResource returns the ankra_service_consumer resource,
// the counterpart of `ankra services consumers bind|unbind`.
func NewServiceConsumerResource() resource.Resource {
	return &serviceConsumerResource{}
}

func (consumerResource *serviceConsumerResource) Metadata(_ context.Context, request resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_service_consumer"
}

func (consumerResource *serviceConsumerResource) Schema(_ context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: "A consumer binding: one application's namespace on one cluster that managed services may be set up for. " +
			"The counterpart of `ankra services consumers bind` and `unbind`; its id is what `ankra services setup --consumer` takes.\n\n" +
			"A binding that a live service still uses cannot be removed until that service is retired, so destroy fails with a 409 until then. " +
			"The platform does not return `allow_planned`, so a change to it made outside Terraform is not detected.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Identifier of the binding (the consumer id).",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"application_id": schema.StringAttribute{
				MarkdownDescription: "Application that consumes services. Changing it replaces the binding.",
				Required:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"cluster_id": schema.StringAttribute{
				MarkdownDescription: "Cluster the namespace is on. Changing it replaces the binding.",
				Required:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"namespace": schema.StringAttribute{
				MarkdownDescription: "Namespace of the application on the cluster. Changing it replaces the binding.",
				Required:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
				Validators:          []validator.String{deployPatternValidator{pattern: serviceNamespacePattern, subject: "namespace"}},
			},
			"allow_planned": schema.BoolAttribute{
				MarkdownDescription: "Bind a namespace the application is not installed in yet. Defaults to `false`, which requires the installation to exist.",
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(false),
			},
			"local_only": schema.BoolAttribute{
				MarkdownDescription: "Whether the consumer may use only services on its own cluster. Defaults to `false`.",
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(false),
			},
			"region": schema.StringAttribute{
				MarkdownDescription: "Region resolved from the cluster's placement policy.",
				Computed:            true,
			},
			"data_boundary": schema.StringAttribute{
				MarkdownDescription: "Data boundary resolved from the cluster's placement policy.",
				Computed:            true,
			},
			"binding_kind": schema.StringAttribute{
				MarkdownDescription: "Whether the binding resolved to an installation or is planned.",
				Computed:            true,
			},
			"binding_revision": schema.Int64Attribute{
				MarkdownDescription: "Revision of the binding; every change increases it.",
				Computed:            true,
			},
			"installation_id": schema.StringAttribute{
				MarkdownDescription: "Application installation the binding resolved to; null for a planned binding.",
				Computed:            true,
			},
		},
	}
}

func (consumerResource *serviceConsumerResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	apiClient := consumerResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var plan serviceConsumerModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}
	consumer, err := apiClient.BindServiceConsumer(ctx, plan.write(0))
	if err != nil {
		response.Diagnostics.AddError("Unable to bind service consumer",
			err.Error()+"\n\nA 409 means the namespace is already bound: import the existing binding by its id instead.")
		return
	}
	plan.applyAPI(consumer)
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
}

func (consumerResource *serviceConsumerResource) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	apiClient := consumerResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state serviceConsumerModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	consumer, err := apiClient.GetServiceConsumer(ctx, state.ID.ValueString())
	if serviceAdmissionUnavailable(err, &response.Diagnostics) || removeWhenNotFound(ctx, err, response) {
		return
	}
	if err != nil {
		response.Diagnostics.AddError("Unable to read service consumer", err.Error())
		return
	}
	state.ApplicationID = types.StringValue(consumer.ApplicationID)
	state.ClusterID = types.StringValue(consumer.ClusterID)
	state.Namespace = types.StringValue(consumer.Namespace)
	state.applyAPI(consumer)
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
}

func (consumerResource *serviceConsumerResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	apiClient := consumerResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var plan, state serviceConsumerModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	consumer, err := apiClient.BindServiceConsumer(ctx, plan.write(state.BindingRevision.ValueInt64()))
	if err != nil {
		response.Diagnostics.AddError("Unable to update service consumer", err.Error())
		return
	}
	plan.applyAPI(consumer)
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
}

func (consumerResource *serviceConsumerResource) Delete(ctx context.Context, request resource.DeleteRequest, response *resource.DeleteResponse) {
	apiClient := consumerResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state serviceConsumerModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	err := apiClient.UnbindServiceConsumer(ctx, state.ID.ValueString(), state.BindingRevision.ValueInt64())
	if err == nil || serviceAdmissionUnavailable(err, &response.Diagnostics) || client.IsNotFound(err) {
		return
	}
	response.Diagnostics.AddError("Unable to unbind service consumer",
		err.Error()+"\n\nA 409 means a service still uses the binding (retire it first) or the binding changed since Terraform read it.")
}

// ImportState imports by consumer id. allow_planned is not readable and
// imports as false.
func (consumerResource *serviceConsumerResource) ImportState(ctx context.Context, request resource.ImportStateRequest, response *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), request, response)
	response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("allow_planned"), false)...)
}

func (model serviceConsumerModel) write(expectedRevision int64) client.ServiceConsumerWrite {
	return client.ServiceConsumerWrite{
		ApplicationID:    model.ApplicationID.ValueString(),
		ClusterID:        model.ClusterID.ValueString(),
		Namespace:        model.Namespace.ValueString(),
		AllowPlanned:     model.AllowPlanned.ValueBool(),
		LocalOnly:        model.LocalOnly.ValueBool(),
		ExpectedRevision: expectedRevision,
	}
}

func (model *serviceConsumerModel) applyAPI(consumer *client.ServiceConsumer) {
	model.ID = types.StringValue(consumer.ID)
	model.LocalOnly = types.BoolValue(consumer.LocalOnly)
	model.Region = types.StringValue(consumer.Region)
	model.DataBoundary = types.StringValue(consumer.DataBoundary)
	model.BindingKind = types.StringValue(consumer.BindingKind)
	model.BindingRevision = types.Int64Value(consumer.BindingRevision)
	model.InstallationID = types.StringPointerValue(consumer.InstallationID)
}
