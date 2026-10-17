// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ datasource.DataSource              = (*backupVaultsDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*backupVaultsDataSource)(nil)
	_ datasource.DataSource              = (*objectStorageBucketsDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*objectStorageBucketsDataSource)(nil)
)

func storageComputedString(description string) schema.StringAttribute {
	return schema.StringAttribute{MarkdownDescription: description, Computed: true}
}

type backupVaultsDataSource struct {
	configuredDataSourceClient
}

type backupVaultItemModel struct {
	ID           types.String `tfsdk:"id"`
	Name         types.String `tfsdk:"name"`
	Kind         types.String `tfsdk:"kind"`
	Provider     types.String `tfsdk:"provider"`
	Endpoint     types.String `tfsdk:"endpoint"`
	Region       types.String `tfsdk:"region"`
	Bucket       types.String `tfsdk:"bucket"`
	PathStyle    types.Bool   `tfsdk:"path_style"`
	Status       types.String `tfsdk:"status"`
	CredentialID types.String `tfsdk:"credential_id"`
}

type backupVaultsDataSourceModel struct {
	ID     types.String           `tfsdk:"id"`
	Vaults []backupVaultItemModel `tfsdk:"vaults"`
}

// NewBackupVaultsDataSource returns the ankra_backup_vaults data source, the
// counterpart of `ankra backup vaults list`.
func NewBackupVaultsDataSource() datasource.DataSource {
	return &backupVaultsDataSource{}
}

func (vaultsDataSource *backupVaultsDataSource) Metadata(_ context.Context, request datasource.MetadataRequest, response *datasource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_backup_vaults"
}

func (vaultsDataSource *backupVaultsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, response *datasource.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: "Lists the backup vaults in the provider's project. Keys are never listed. The counterpart of " +
			"`ankra backup vaults list`.",
		Attributes: map[string]schema.Attribute{
			"id": storageComputedString("Placeholder identifier for the data source."),
			"vaults": schema.ListNestedAttribute{
				MarkdownDescription: "Backup vaults.",
				Computed:            true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id":            storageComputedString("Identifier of the vault."),
						"name":          storageComputedString("Vault name."),
						"kind":          storageComputedString("`customer_s3` or `ankra_provisioned`."),
						"provider":      storageComputedString("Object-storage provider."),
						"endpoint":      storageComputedString("S3 endpoint."),
						"region":        storageComputedString("Bucket region."),
						"bucket":        storageComputedString("Bucket name."),
						"path_style":    schema.BoolAttribute{MarkdownDescription: "Whether the bucket is addressed path-style.", Computed: true},
						"status":        storageComputedString("`provisioning`, `ready` or `error`."),
						"credential_id": storageComputedString("Credential a provisioned vault was created with."),
					},
				},
			},
		},
	}
}

func (vaultsDataSource *backupVaultsDataSource) Read(ctx context.Context, _ datasource.ReadRequest, response *datasource.ReadResponse) {
	apiClient := vaultsDataSource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	vaults, err := apiClient.ListBackupVaults(ctx)
	if err != nil {
		response.Diagnostics.AddError("Unable to list backup vaults", err.Error())
		return
	}
	state := backupVaultsDataSourceModel{
		ID:     types.StringValue("ankra_backup_vaults"),
		Vaults: make([]backupVaultItemModel, 0, len(vaults)),
	}
	for _, vault := range vaults {
		state.Vaults = append(state.Vaults, backupVaultItemModel{
			ID:           types.StringValue(vault.ID),
			Name:         types.StringValue(vault.Name),
			Kind:         types.StringValue(vault.Kind),
			Provider:     types.StringValue(vault.Provider),
			Endpoint:     types.StringValue(vault.Endpoint),
			Region:       optionalString(vault.Region),
			Bucket:       types.StringValue(vault.Bucket),
			PathStyle:    types.BoolValue(vault.PathStyle),
			Status:       types.StringValue(vault.Status),
			CredentialID: types.StringPointerValue(vault.ProvisionedViaCredentialID),
		})
	}
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
}

type objectStorageBucketsDataSource struct {
	configuredDataSourceClient
}

type objectStorageBucketItemModel struct {
	ID           types.String `tfsdk:"id"`
	Name         types.String `tfsdk:"name"`
	Provider     types.String `tfsdk:"provider"`
	Region       types.String `tfsdk:"region"`
	Bucket       types.String `tfsdk:"bucket"`
	Endpoint     types.String `tfsdk:"endpoint"`
	PathStyle    types.Bool   `tfsdk:"path_style"`
	CredentialID types.String `tfsdk:"credential_id"`
	Status       types.String `tfsdk:"status"`
}

type objectStorageBucketsDataSourceModel struct {
	ID      types.String                   `tfsdk:"id"`
	Buckets []objectStorageBucketItemModel `tfsdk:"buckets"`
}

// NewObjectStorageBucketsDataSource returns the ankra_object_storage_buckets
// data source, the counterpart of `ankra bucket list`.
func NewObjectStorageBucketsDataSource() datasource.DataSource {
	return &objectStorageBucketsDataSource{}
}

func (bucketsDataSource *objectStorageBucketsDataSource) Metadata(_ context.Context, request datasource.MetadataRequest, response *datasource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_object_storage_buckets"
}

func (bucketsDataSource *objectStorageBucketsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, response *datasource.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: "Lists the object-storage buckets Ankra created in the provider's project, newest first. Keys are " +
			"never listed. The counterpart of `ankra bucket list`.",
		Attributes: map[string]schema.Attribute{
			"id": storageComputedString("Placeholder identifier for the data source."),
			"buckets": schema.ListNestedAttribute{
				MarkdownDescription: "Buckets.",
				Computed:            true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id":            storageComputedString("Identifier of the bucket."),
						"name":          storageComputedString("Name in Ankra."),
						"provider":      storageComputedString("Provider the bucket lives on."),
						"region":        storageComputedString("Provider region."),
						"bucket":        storageComputedString("Bucket name on the provider."),
						"endpoint":      storageComputedString("S3 endpoint."),
						"path_style":    schema.BoolAttribute{MarkdownDescription: "Whether the bucket is addressed path-style.", Computed: true},
						"credential_id": storageComputedString("Credential the bucket was created with."),
						"status":        storageComputedString("`provisioning`, `ready` or `error`."),
					},
				},
			},
		},
	}
}

func (bucketsDataSource *objectStorageBucketsDataSource) Read(ctx context.Context, _ datasource.ReadRequest, response *datasource.ReadResponse) {
	apiClient := bucketsDataSource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	buckets, err := apiClient.ListObjectStorageBuckets(ctx)
	if err != nil {
		response.Diagnostics.AddError("Unable to list object storage buckets", err.Error())
		return
	}
	state := objectStorageBucketsDataSourceModel{
		ID:      types.StringValue("ankra_object_storage_buckets"),
		Buckets: make([]objectStorageBucketItemModel, 0, len(buckets)),
	}
	for _, bucket := range buckets {
		state.Buckets = append(state.Buckets, objectStorageBucketItemModel{
			ID:           types.StringValue(bucket.ID),
			Name:         types.StringValue(bucket.Name),
			Provider:     types.StringValue(bucket.Provider),
			Region:       types.StringValue(bucket.Region),
			Bucket:       types.StringValue(bucket.Bucket),
			Endpoint:     types.StringValue(bucket.Endpoint),
			PathStyle:    types.BoolValue(bucket.PathStyle),
			CredentialID: types.StringValue(bucket.CredentialID),
			Status:       types.StringValue(bucket.Status),
		})
	}
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
}
