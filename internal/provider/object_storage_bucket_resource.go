// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"ankra.io/terraform-provider-ankra/internal/client"
)

var (
	_ resource.Resource                = (*objectStorageBucketResource)(nil)
	_ resource.ResourceWithConfigure   = (*objectStorageBucketResource)(nil)
	_ resource.ResourceWithImportState = (*objectStorageBucketResource)(nil)
	_ resource.ResourceWithModifyPlan  = (*objectStorageBucketResource)(nil)
)

type objectStorageBucketResource struct {
	configuredClient
}

type objectStorageBucketModel struct {
	ID                       types.String `tfsdk:"id"`
	Name                     types.String `tfsdk:"name"`
	CredentialID             types.String `tfsdk:"credential_id"`
	Region                   types.String `tfsdk:"region"`
	Bucket                   types.String `tfsdk:"bucket"`
	AccessKeyID              types.String `tfsdk:"access_key_id"`
	SecretAccessKey          types.String `tfsdk:"secret_access_key"`
	DestroyProviderResources types.Bool   `tfsdk:"destroy_provider_resources"`
	Provider                 types.String `tfsdk:"storage_provider"`
	Endpoint                 types.String `tfsdk:"endpoint"`
	PathStyle                types.Bool   `tfsdk:"path_style"`
	Status                   types.String `tfsdk:"status"`
	ErrorExcerpt             types.String `tfsdk:"error_excerpt"`
	CreatedAt                types.String `tfsdk:"created_at"`
}

// NewObjectStorageBucketResource returns the ankra_object_storage_bucket
// resource, the counterpart of `ankra bucket create|delete`.
func NewObjectStorageBucketResource() resource.Resource {
	return &objectStorageBucketResource{}
}

func (bucketResource *objectStorageBucketResource) Metadata(_ context.Context, request resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_object_storage_bucket"
}

func (bucketResource *objectStorageBucketResource) Schema(_ context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	replaceString := []planmodifier.String{stringplanmodifier.RequiresReplace()}
	computedString := []planmodifier.String{stringplanmodifier.UseStateForUnknown()}
	response.Schema = schema.Schema{
		MarkdownDescription: "An object-storage bucket Ankra creates on one of the organisation's provider credentials, for " +
			"applications and add-ons to use. The counterpart of `ankra bucket create` and `ankra bucket delete`. Terraform " +
			"waits for the bucket to be ready.\n\n" +
			"The platform has no update for a bucket, so changing any setting (even `name`) replaces it. Destroying it only " +
			"stops Ankra managing the bucket unless `destroy_provider_resources` is set. **With `destroy_provider_resources = " +
			"true` a replacement empties and deletes the bucket and everything in it**, so Terraform refuses to plan a " +
			"replacement while state holds `true`: set it to `false` and apply first. Buckets are scoped to the provider's project.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Identifier of the bucket.",
				Computed:            true,
				PlanModifiers:       computedString,
			},
			"name": schema.StringAttribute{
				MarkdownDescription: "Name in Ankra, a lower-case label.",
				Required:            true,
				PlanModifiers:       replaceString,
				Validators:          []validator.String{catalogueLabelNameValidator},
			},
			"credential_id": schema.StringAttribute{
				MarkdownDescription: "Provider credential the bucket is created with; the provider comes from it.",
				Required:            true,
				PlanModifiers:       replaceString,
			},
			"region": schema.StringAttribute{
				MarkdownDescription: "Provider region, compared case-insensitively.",
				Required:            true,
				PlanModifiers:       replaceString,
			},
			"bucket": schema.StringAttribute{
				MarkdownDescription: "Bucket name on the provider; the platform derives a unique one when it is left out.",
				Optional:            true,
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown(), stringplanmodifier.RequiresReplace()},
				Validators:          []validator.String{catalogueBucketNameValidator},
			},
			"access_key_id": schema.StringAttribute{
				MarkdownDescription: "Hetzner only, whose API cannot mint Object Storage keys; may be left out when the Hetzner " +
					"credential stores a key pair. Never returned by the platform.",
				Optional:      true,
				Sensitive:     true,
				PlanModifiers: []planmodifier.String{catalogueReplaceUnlessImported()},
			},
			"secret_access_key": schema.StringAttribute{
				MarkdownDescription: "Secret key paired with `access_key_id`. Never returned by the platform.",
				Optional:            true,
				Sensitive:           true,
				PlanModifiers:       []planmodifier.String{catalogueReplaceUnlessImported()},
			},
			"destroy_provider_resources": schema.BoolAttribute{
				MarkdownDescription: "On destroy **or replacement**, also empty and delete the bucket on the provider, with " +
					"everything in it. Defaults to `false`. While it is `true` in state, Terraform refuses to plan a replacement. " +
					"A bucket destroyed while still provisioning is always torn down.",
				Optional: true,
				Computed: true,
				Default:  booldefault.StaticBool(false),
			},
			"storage_provider": schema.StringAttribute{
				MarkdownDescription: "Provider the bucket lives on.",
				Computed:            true,
				PlanModifiers:       computedString,
			},
			"endpoint": schema.StringAttribute{
				MarkdownDescription: "S3 endpoint of the bucket.",
				Computed:            true,
				PlanModifiers:       computedString,
			},
			"path_style": schema.BoolAttribute{
				MarkdownDescription: "Whether the bucket is addressed path-style.",
				Computed:            true,
				PlanModifiers:       []planmodifier.Bool{boolplanmodifier.UseStateForUnknown()},
			},
			"status": schema.StringAttribute{
				MarkdownDescription: "`provisioning`, `ready`, or `error`.",
				Computed:            true,
			},
			"error_excerpt": schema.StringAttribute{
				MarkdownDescription: "Why the bucket is in `error`.",
				Computed:            true,
			},
			"created_at": schema.StringAttribute{
				MarkdownDescription: "When the bucket was created.",
				Computed:            true,
				PlanModifiers:       computedString,
			},
		},
	}
}

// ModifyPlan refuses a replacement while destroy_provider_resources is true:
// it would empty and delete the bucket.
func (bucketResource *objectStorageBucketResource) ModifyPlan(ctx context.Context, request resource.ModifyPlanRequest, response *resource.ModifyPlanResponse) {
	if request.State.Raw.IsNull() || request.Plan.Raw.IsNull() {
		return
	}
	var plan, state objectStorageBucketModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() || !state.DestroyProviderResources.ValueBool() {
		return
	}
	isReplaced := catalogueWriteOnlyChanged(plan.AccessKeyID, state.AccessKeyID) ||
		catalogueWriteOnlyChanged(plan.SecretAccessKey, state.SecretAccessKey) ||
		catalogueAnyChanged([]attr.Value{plan.Name, plan.CredentialID, plan.Region, plan.Bucket},
			[]attr.Value{state.Name, state.CredentialID, state.Region, state.Bucket})
	if isReplaced {
		response.Diagnostics.AddAttributeError(path.Root("destroy_provider_resources"), "Replacement would delete the bucket",
			"This change replaces the bucket, and destroy_provider_resources is true in state, so the replacement would "+
				"empty and delete it with everything in it. Set destroy_provider_resources = false and apply first, then "+
				"make this change.")
	}
}

func (bucketResource *objectStorageBucketResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	apiClient := bucketResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var plan objectStorageBucketModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}

	bucket, err := apiClient.CreateObjectStorageBucket(ctx, client.ObjectStorageBucketCreate{
		Name:            plan.Name.ValueString(),
		CredentialID:    plan.CredentialID.ValueString(),
		Region:          plan.Region.ValueString(),
		Bucket:          stringPointer(plan.Bucket),
		AccessKeyID:     stringPointer(plan.AccessKeyID),
		SecretAccessKey: stringPointer(plan.SecretAccessKey),
	})
	if err != nil {
		response.Diagnostics.AddError("Unable to create object storage bucket", err.Error())
		return
	}
	objectStorageBucketToState(bucket, &plan)
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}

	waitError := catalogueWaitWhileProvisioning(ctx, bucket.Status, func(waitContext context.Context) (string, error) {
		current, readError := apiClient.GetObjectStorageBucket(waitContext, bucket.ID)
		if readError != nil {
			return "", readError
		}
		bucket = current
		return current.Status, nil
	})
	objectStorageBucketToState(bucket, &plan)
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
	if waitError != nil {
		response.Diagnostics.AddError("Object storage bucket did not finish provisioning", waitError.Error())
		return
	}
	if bucket.Status == client.BackupVaultStatusError {
		response.Diagnostics.AddError("Object storage bucket is in error", plan.ErrorExcerpt.ValueString())
	}
}

func (bucketResource *objectStorageBucketResource) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	apiClient := bucketResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state objectStorageBucketModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}

	bucket, err := apiClient.GetObjectStorageBucket(ctx, state.ID.ValueString())
	if removeWhenNotFound(ctx, err, response) {
		return
	}
	if err != nil {
		response.Diagnostics.AddError("Unable to read object storage bucket", err.Error())
		return
	}
	objectStorageBucketToState(bucket, &state)
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
}

// Update only ever changes destroy_provider_resources, or keys after an
// import, which live in state: every other configurable attribute replaces
// the bucket.
func (bucketResource *objectStorageBucketResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	var plan, state objectStorageBucketModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	plan.Status = state.Status
	plan.ErrorExcerpt = state.ErrorExcerpt
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
}

func (bucketResource *objectStorageBucketResource) Delete(ctx context.Context, request resource.DeleteRequest, response *resource.DeleteResponse) {
	apiClient := bucketResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state objectStorageBucketModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	if err := apiClient.DeleteObjectStorageBucket(ctx, state.ID.ValueString(), state.DestroyProviderResources.ValueBool()); err != nil {
		response.Diagnostics.AddError("Unable to delete object storage bucket", err.Error())
	}
}

// ImportState imports by bucket ID. Keys are never returned: after an import
// they are taken from configuration without replacing the bucket.
func (bucketResource *objectStorageBucketResource) ImportState(ctx context.Context, request resource.ImportStateRequest, response *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), request, response)
}

func objectStorageBucketToState(bucket *client.ObjectStorageBucket, state *objectStorageBucketModel) {
	state.ID = types.StringValue(bucket.ID)
	state.Name = types.StringValue(bucket.Name)
	state.CredentialID = types.StringValue(bucket.CredentialID)
	if state.Region.IsNull() || state.Region.IsUnknown() || !strings.EqualFold(state.Region.ValueString(), bucket.Region) {
		state.Region = types.StringValue(bucket.Region)
	}
	state.Bucket = types.StringValue(bucket.Bucket)
	state.Provider = types.StringValue(bucket.Provider)
	state.Endpoint = types.StringValue(bucket.Endpoint)
	state.PathStyle = types.BoolValue(bucket.PathStyle)
	state.Status = types.StringValue(bucket.Status)
	state.ErrorExcerpt = types.StringPointerValue(bucket.ErrorExcerpt)
	state.CreatedAt = types.StringValue(bucket.CreatedAt)
	if state.DestroyProviderResources.IsNull() || state.DestroyProviderResources.IsUnknown() {
		state.DestroyProviderResources = types.BoolValue(false)
	}
}
