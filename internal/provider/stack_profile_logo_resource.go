// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"mime"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ resource.Resource                = (*stackProfileLogoResource)(nil)
	_ resource.ResourceWithConfigure   = (*stackProfileLogoResource)(nil)
	_ resource.ResourceWithImportState = (*stackProfileLogoResource)(nil)
)

type stackProfileLogoResource struct {
	configuredClient
}

type stackProfileLogoModel struct {
	ID            types.String `tfsdk:"id"`
	ProfileID     types.String `tfsdk:"profile_id"`
	ContentBase64 types.String `tfsdk:"content_base64"`
	ContentType   types.String `tfsdk:"content_type"`
	ContentSHA256 types.String `tfsdk:"content_sha256"`
}

// NewStackProfileLogoResource returns the ankra_stack_profile_logo resource,
// the counterpart of `ankra stack-profiles logo set|clear`.
func NewStackProfileLogoResource() resource.Resource {
	return &stackProfileLogoResource{}
}

func (logoResource *stackProfileLogoResource) Metadata(_ context.Context, request resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_stack_profile_logo"
}

func (logoResource *stackProfileLogoResource) Schema(_ context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: "An uploaded logo for a stack profile: a PNG, JPEG or WebP image of at most 512 KiB and 1024x1024 " +
			"pixels. The counterpart of `ankra stack-profiles logo set` and `ankra stack-profiles logo clear`. Uploading clears " +
			"the profile's `logo_url`, and setting `logo_url` deletes the upload, so manage one or the other. A logo replaced " +
			"outside Terraform shows as a change to `content_base64`.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "The profile ID.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"profile_id": schema.StringAttribute{
				MarkdownDescription: "Profile the logo belongs to.",
				Required:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"content_base64": schema.StringAttribute{
				MarkdownDescription: "The image, base64-encoded; typically `filebase64(\"logo.png\")`.",
				Required:            true,
			},
			"content_type": schema.StringAttribute{
				MarkdownDescription: "`image/png`, `image/jpeg` or `image/webp`; it must match the image.",
				Required:            true,
				Validators:          []validator.String{catalogueLogoContentTypeValidator},
			},
			"content_sha256": schema.StringAttribute{
				MarkdownDescription: "SHA-256 of the uploaded image, hex-encoded.",
				Computed:            true,
			},
		},
	}
}

func (logoResource *stackProfileLogoResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	var plan stackProfileLogoModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}
	if logoResource.upload(ctx, &plan, &response.Diagnostics) {
		response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
	}
}

func (logoResource *stackProfileLogoResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	var plan stackProfileLogoModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}
	if logoResource.upload(ctx, &plan, &response.Diagnostics) {
		response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
	}
}

// upload decodes the configured image, uploads it, and records its digest.
func (logoResource *stackProfileLogoResource) upload(ctx context.Context, plan *stackProfileLogoModel, diagnostics *diag.Diagnostics) bool {
	apiClient := logoResource.requireClient(diagnostics)
	if apiClient == nil {
		return false
	}
	content, err := base64.StdEncoding.DecodeString(plan.ContentBase64.ValueString())
	if err != nil {
		diagnostics.AddAttributeError(path.Root("content_base64"), "Invalid logo content",
			"content_base64 is not valid base64: "+err.Error())
		return false
	}
	if err := apiClient.PutStackProfileLogo(ctx, plan.ProfileID.ValueString(), content, plan.ContentType.ValueString()); err != nil {
		diagnostics.AddError("Unable to upload stack profile logo", err.Error())
		return false
	}
	plan.ID = types.StringValue(plan.ProfileID.ValueString())
	plan.ContentSHA256 = types.StringValue(stackProfileLogoDigest(content))
	return true
}

func (logoResource *stackProfileLogoResource) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	apiClient := logoResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state stackProfileLogoModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}

	logo, err := apiClient.GetStackProfileLogo(ctx, state.ProfileID.ValueString())
	if removeWhenNotFound(ctx, err, response) {
		return
	}
	if err != nil {
		response.Diagnostics.AddError("Unable to read stack profile logo", err.Error())
		return
	}
	state.ID = types.StringValue(state.ProfileID.ValueString())
	digest := stackProfileLogoDigest(logo.Content)
	if digest != stackProfileLogoConfiguredDigest(state.ContentBase64) {
		state.ContentBase64 = types.StringValue(base64.StdEncoding.EncodeToString(logo.Content))
	}
	state.ContentSHA256 = types.StringValue(digest)
	if mediaType, _, parseError := mime.ParseMediaType(logo.ContentType); parseError == nil {
		state.ContentType = types.StringValue(mediaType)
	}
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
}

func (logoResource *stackProfileLogoResource) Delete(ctx context.Context, request resource.DeleteRequest, response *resource.DeleteResponse) {
	apiClient := logoResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state stackProfileLogoModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	if err := apiClient.DeleteStackProfileLogo(ctx, state.ProfileID.ValueString()); err != nil {
		response.Diagnostics.AddError("Unable to remove stack profile logo", err.Error())
	}
}

// ImportState imports by profile ID.
func (logoResource *stackProfileLogoResource) ImportState(ctx context.Context, request resource.ImportStateRequest, response *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("profile_id"), request, response)
}

func stackProfileLogoDigest(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

// stackProfileLogoConfiguredDigest is the digest of the configured image, or
// "" when it is not valid base64.
func stackProfileLogoConfiguredDigest(contentBase64 types.String) string {
	content, err := base64.StdEncoding.DecodeString(contentBase64.ValueString())
	if err != nil {
		return ""
	}
	return stackProfileLogoDigest(content)
}
