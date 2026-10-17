// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"sort"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"ankra.io/terraform-provider-ankra/internal/client"
)

var (
	_ datasource.DataSource              = (*servicePackagesDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*servicePackagesDataSource)(nil)
	_ datasource.DataSource              = (*managedServicesDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*managedServicesDataSource)(nil)
	_ datasource.DataSource              = (*serviceConsumersDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*serviceConsumersDataSource)(nil)
)

func serviceComputedString(description string) schema.StringAttribute {
	return schema.StringAttribute{MarkdownDescription: description, Computed: true}
}

func serviceComputedBool(description string) schema.BoolAttribute {
	return schema.BoolAttribute{MarkdownDescription: description, Computed: true}
}

func serviceComputedInt64(description string) schema.Int64Attribute {
	return schema.Int64Attribute{MarkdownDescription: description, Computed: true}
}

// serviceDataSourceUnavailable reports the bare 404 the service routes answer
// while managed services are off, which a data source cannot read past.
func serviceDataSourceUnavailable(err error, diagnostics *diag.Diagnostics) bool {
	if detail, isNotFound := clusterSettingsNotFoundDetail(err); !isNotFound || detail != "Not Found" {
		return false
	}
	diagnostics.AddError("Managed services not available",
		"The platform answered a bare 404, which it does while managed services (the `applications` feature) are not enabled for "+
			"the provider's organisation. ("+err.Error()+")")
	return true
}

// ankra_service_packages

type servicePackagesDataSource struct {
	configuredDataSourceClient
}

type servicePackageItemModel struct {
	ID          types.String `tfsdk:"id"`
	PublisherID types.String `tfsdk:"publisher_id"`
	Name        types.String `tfsdk:"name"`
	Version     types.String `tfsdk:"version"`
	Capability  types.String `tfsdk:"capability"`
	Digest      types.String `tfsdk:"digest"`
	FirstParty  types.Bool   `tfsdk:"first_party"`
	CreatedAt   types.String `tfsdk:"created_at"`
}

type servicePackagesDataSourceModel struct {
	ID       types.String              `tfsdk:"id"`
	Packages []servicePackageItemModel `tfsdk:"packages"`
}

// NewServicePackagesDataSource returns the ankra_service_packages data
// source, the counterpart of `ankra services packages list`.
func NewServicePackagesDataSource() datasource.DataSource {
	return &servicePackagesDataSource{}
}

func (packages *servicePackagesDataSource) Metadata(_ context.Context, request datasource.MetadataRequest, response *datasource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_service_packages"
}

func (packages *servicePackagesDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, response *datasource.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: "Lists the service package versions the organisation can set a managed service up from: its own, those shared " +
			"with it, and first-party ones. Use an `id` as `package_version_id` on `ankra_managed_service`.",
		Attributes: map[string]schema.Attribute{
			"id": serviceComputedString("Placeholder identifier for the data source."),
			"packages": schema.ListNestedAttribute{
				MarkdownDescription: "Package versions, in the platform's order.",
				Computed:            true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id":           serviceComputedString("Identifier of the package version."),
						"publisher_id": serviceComputedString("Organisation that published it."),
						"name":         serviceComputedString("Package name."),
						"version":      serviceComputedString("Version."),
						"capability":   serviceComputedString("What the package provides, such as a database engine."),
						"digest":       serviceComputedString("Content digest of the version."),
						"first_party":  serviceComputedBool("Whether Ankra publishes it."),
						"created_at":   serviceComputedString("When the version was published."),
					},
				},
			},
		},
	}
}

func (packages *servicePackagesDataSource) Read(ctx context.Context, _ datasource.ReadRequest, response *datasource.ReadResponse) {
	apiClient := packages.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	listed, err := apiClient.ListServicePackages(ctx)
	if serviceDataSourceUnavailable(err, &response.Diagnostics) {
		return
	}
	if err != nil {
		response.Diagnostics.AddError("Unable to list service packages", err.Error())
		return
	}
	state := servicePackagesDataSourceModel{ID: types.StringValue("ankra_service_packages"), Packages: make([]servicePackageItemModel, 0, len(listed))}
	for _, item := range listed {
		state.Packages = append(state.Packages, servicePackageItemModel{
			ID:          types.StringValue(item.ID),
			PublisherID: types.StringValue(item.PublisherID),
			Name:        types.StringValue(item.Name),
			Version:     types.StringValue(item.Version),
			Capability:  types.StringValue(item.Capability),
			Digest:      types.StringValue(item.Digest),
			FirstParty:  types.BoolValue(item.FirstParty),
			CreatedAt:   types.StringValue(item.CreatedAt),
		})
	}
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
}

// ankra_services

type managedServicesDataSource struct {
	configuredDataSourceClient
}

type managedServiceItemModel struct {
	ID                   types.String `tfsdk:"id"`
	Name                 types.String `tfsdk:"name"`
	PackageVersionID     types.String `tfsdk:"package_version_id"`
	Mode                 types.String `tfsdk:"mode"`
	ClusterID            types.String `tfsdk:"cluster_id"`
	ClusterName          types.String `tfsdk:"cluster_name"`
	Region               types.String `tfsdk:"region"`
	DataBoundary         types.String `tfsdk:"data_boundary"`
	State                types.String `tfsdk:"state"`
	Readiness            types.String `tfsdk:"readiness"`
	Generation           types.Int64  `tfsdk:"generation"`
	ConnectedConsumerIDs types.List   `tfsdk:"connected_consumer_ids"`
	CreatedAt            types.String `tfsdk:"created_at"`
	ReleasedAt           types.String `tfsdk:"released_at"`
}

type managedServicesDataSourceModel struct {
	ID            types.String              `tfsdk:"id"`
	ApplicationID types.String              `tfsdk:"application_id"`
	ClusterID     types.String              `tfsdk:"cluster_id"`
	State         types.String              `tfsdk:"state"`
	Services      []managedServiceItemModel `tfsdk:"services"`
}

// NewManagedServicesDataSource returns the ankra_services data source, the
// counterpart of `ankra services list`.
func NewManagedServicesDataSource() datasource.DataSource {
	return &managedServicesDataSource{}
}

func (services *managedServicesDataSource) Metadata(_ context.Context, request datasource.MetadataRequest, response *datasource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_services"
}

func (services *managedServicesDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, response *datasource.SchemaResponse) {
	filter := func(description string, validators ...validator.String) schema.StringAttribute {
		return schema.StringAttribute{MarkdownDescription: description, Optional: true, Validators: validators}
	}
	response.Schema = schema.Schema{
		MarkdownDescription: "Lists the organisation's managed services on clusters the token can read, the counterpart of `ankra services list`. " +
			"Retired services stay listed with state `released` and a `released_at`; filter with `state` to leave them out.",
		Attributes: map[string]schema.Attribute{
			"id":             serviceComputedString("Placeholder identifier for the data source."),
			"application_id": filter("Only services connected to this application's consumers.", securityUUIDValidator{}),
			"cluster_id":     filter("Only services on this cluster.", securityUUIDValidator{}),
			"state": filter("Only services in this state.", organisationStringOneOf{values: []string{
				"deploying", "deployed", "serving", "degraded", "failed", "retiring", "released", "unknown",
			}}),
			"services": schema.ListNestedAttribute{
				MarkdownDescription: "Managed services.",
				Computed:            true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id":                 serviceComputedString("Identifier of the service."),
						"name":               serviceComputedString("Service name."),
						"package_version_id": serviceComputedString("Package version it was set up from."),
						"mode":               serviceComputedString("`customer`, `existing` or `hosted`."),
						"cluster_id":         serviceComputedString("Cluster it runs on."),
						"cluster_name":       serviceComputedString("Name of the cluster."),
						"region":             serviceComputedString("Region."),
						"data_boundary":      serviceComputedString("Data boundary."),
						"state":              serviceComputedString("`deploying`, `deployed`, `serving`, `degraded`, `failed`, `retiring`, `released` or `unknown`."),
						"readiness":          serviceComputedString("Readiness the platform last observed."),
						"generation":         serviceComputedInt64("Generation of the service."),
						"connected_consumer_ids": schema.ListAttribute{
							MarkdownDescription: "Consumer bindings it is connected to.",
							ElementType:         types.StringType,
							Computed:            true,
						},
						"created_at":  serviceComputedString("When it was set up."),
						"released_at": serviceComputedString("When it was retired; null while it is live."),
					},
				},
			},
		},
	}
}

func (services *managedServicesDataSource) Read(ctx context.Context, request datasource.ReadRequest, response *datasource.ReadResponse) {
	apiClient := services.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var config managedServicesDataSourceModel
	response.Diagnostics.Append(request.Config.Get(ctx, &config)...)
	if response.Diagnostics.HasError() {
		return
	}
	listed, err := apiClient.ListManagedServices(ctx, client.ManagedServiceFilter{
		ApplicationID: config.ApplicationID.ValueString(),
		ClusterID:     config.ClusterID.ValueString(),
		State:         config.State.ValueString(),
	})
	if serviceDataSourceUnavailable(err, &response.Diagnostics) {
		return
	}
	if err != nil {
		response.Diagnostics.AddError("Unable to list managed services", err.Error())
		return
	}
	config.ID = types.StringValue("ankra_services")
	config.Services = make([]managedServiceItemModel, 0, len(listed))
	for _, item := range listed {
		consumerIDs := make([]string, 0, len(item.Consumers))
		for _, consumer := range item.Consumers {
			consumerIDs = append(consumerIDs, consumer.ID)
		}
		sort.Strings(consumerIDs)
		config.Services = append(config.Services, managedServiceItemModel{
			ID:                   types.StringValue(item.ID),
			Name:                 types.StringValue(item.Name),
			PackageVersionID:     types.StringValue(item.PackageVersionID),
			Mode:                 types.StringValue(item.Mode),
			ClusterID:            types.StringValue(item.ClusterID),
			ClusterName:          types.StringValue(item.ClusterName),
			Region:               types.StringValue(item.Region),
			DataBoundary:         types.StringValue(item.DataBoundary),
			State:                types.StringValue(item.State),
			Readiness:            types.StringValue(item.Readiness),
			Generation:           types.Int64Value(item.Generation),
			ConnectedConsumerIDs: listFromStrings(consumerIDs),
			CreatedAt:            types.StringValue(item.CreatedAt),
			ReleasedAt:           types.StringPointerValue(item.ReleasedAt),
		})
	}
	response.Diagnostics.Append(response.State.Set(ctx, &config)...)
}

// ankra_service_consumers

type serviceConsumersDataSource struct {
	configuredDataSourceClient
}

type serviceConsumerItemModel struct {
	ID            types.String `tfsdk:"id"`
	ApplicationID types.String `tfsdk:"application_id"`
	ClusterID     types.String `tfsdk:"cluster_id"`
	Namespace     types.String `tfsdk:"namespace"`
	AllowPlanned  types.Bool   `tfsdk:"allow_planned"`
	LocalOnly     types.Bool   `tfsdk:"local_only"`
	Revision      types.Int64  `tfsdk:"revision"`
	UpdatedAt     types.String `tfsdk:"updated_at"`
}

type serviceConsumersDataSourceModel struct {
	ID            types.String               `tfsdk:"id"`
	ApplicationID types.String               `tfsdk:"application_id"`
	Consumers     []serviceConsumerItemModel `tfsdk:"consumers"`
}

// NewServiceConsumersDataSource returns the ankra_service_consumers data
// source, the counterpart of `ankra services consumers list`.
func NewServiceConsumersDataSource() datasource.DataSource {
	return &serviceConsumersDataSource{}
}

func (consumers *serviceConsumersDataSource) Metadata(_ context.Context, request datasource.MetadataRequest, response *datasource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_service_consumers"
}

func (consumers *serviceConsumersDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, response *datasource.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: "Lists one application's consumer bindings, the namespaces managed services may be set up for. Bindings on " +
			"clusters the token cannot read are not listed.",
		Attributes: map[string]schema.Attribute{
			"id": serviceComputedString("The application id."),
			"application_id": schema.StringAttribute{
				MarkdownDescription: "Application whose bindings to list.",
				Required:            true,
				Validators:          []validator.String{securityUUIDValidator{}},
			},
			"consumers": schema.ListNestedAttribute{
				MarkdownDescription: "Consumer bindings.",
				Computed:            true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id":             serviceComputedString("Identifier of the binding (the consumer id)."),
						"application_id": serviceComputedString("Application."),
						"cluster_id":     serviceComputedString("Cluster of the namespace."),
						"namespace":      serviceComputedString("Namespace."),
						"allow_planned":  serviceComputedBool("Whether the binding was made before the application was installed."),
						"local_only":     serviceComputedBool("Whether only services on the same cluster may be used."),
						"revision":       serviceComputedInt64("Revision of the binding."),
						"updated_at":     serviceComputedString("When the binding last changed."),
					},
				},
			},
		},
	}
}

func (consumers *serviceConsumersDataSource) Read(ctx context.Context, request datasource.ReadRequest, response *datasource.ReadResponse) {
	apiClient := consumers.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var config serviceConsumersDataSourceModel
	response.Diagnostics.Append(request.Config.Get(ctx, &config)...)
	if response.Diagnostics.HasError() {
		return
	}
	listed, err := apiClient.ListServiceConsumerBindings(ctx, config.ApplicationID.ValueString())
	if serviceDataSourceUnavailable(err, &response.Diagnostics) {
		return
	}
	if err != nil {
		response.Diagnostics.AddError("Unable to list service consumers", err.Error())
		return
	}
	config.ID = config.ApplicationID
	config.Consumers = make([]serviceConsumerItemModel, 0, len(listed))
	for _, item := range listed {
		config.Consumers = append(config.Consumers, serviceConsumerItemModel{
			ID:            types.StringValue(item.ID),
			ApplicationID: types.StringValue(item.ApplicationID),
			ClusterID:     types.StringValue(item.ClusterID),
			Namespace:     types.StringValue(item.Namespace),
			AllowPlanned:  types.BoolValue(item.AllowPlanned),
			LocalOnly:     types.BoolValue(item.LocalOnly),
			Revision:      types.Int64Value(item.Revision),
			UpdatedAt:     types.StringValue(item.UpdatedAt),
		})
	}
	response.Diagnostics.Append(response.State.Set(ctx, &config)...)
}
