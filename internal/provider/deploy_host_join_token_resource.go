// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"
	"regexp"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"ankra.io/terraform-provider-ankra/internal/client"
)

// deployEnvironmentNamePattern mirrors the platform's environment name rule
// (pipelinespec.EnvironmentNamePattern).
var deployEnvironmentNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

// deployMaximumJoinTokenSeconds is the platform's ceiling on a join token's
// lifetime (deploy.MaxJoinTokenLifetime).
const deployMaximumJoinTokenSeconds = 24 * 60 * 60

var (
	_ resource.Resource                = (*deployHostJoinTokenResource)(nil)
	_ resource.ResourceWithConfigure   = (*deployHostJoinTokenResource)(nil)
	_ resource.ResourceWithImportState = (*deployHostJoinTokenResource)(nil)
)

type deployHostJoinTokenResource struct {
	configuredClient
}

type deployHostJoinTokenModel struct {
	ID          types.String `tfsdk:"id"`
	Environment types.String `tfsdk:"environment"`
	TTLSeconds  types.Int64  `tfsdk:"ttl_seconds"`
	JoinToken   types.String `tfsdk:"join_token"`
	ExpiresAt   types.String `tfsdk:"expires_at"`
}

// NewDeployHostJoinTokenResource returns the ankra_host_join_token resource,
// the counterpart of `ankra targets join-token create`.
func NewDeployHostJoinTokenResource() resource.Resource {
	return &deployHostJoinTokenResource{}
}

func (tokenResource *deployHostJoinTokenResource) Metadata(_ context.Context, request resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_host_join_token"
}

func (tokenResource *deployHostJoinTokenResource) Schema(_ context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: "A single-use token that lets a host register into a deploy environment, for example from a machine's cloud-init " +
			"(`ankra targets register --environment <environment> --name <name> --token-stdin`). The counterpart of `ankra targets join-token create`. " +
			"The environment is created in the provider's project when it does not exist yet.\n\n" +
			"The platform shows the token once and keeps only its hash: there is no read-back and no revoke. Terraform keeps the token " +
			"in state until it is destroyed, which removes it from state only; the token itself stays valid until it is used or `expires_at` passes. " +
			"Once `expires_at` has passed, the token is dropped from state on the next refresh and a new one is minted, so `join_token` changes: " +
			"anything that embeds it (such as a machine's user data) is planned for a change too. " +
			"Registering the host and revoking a host target are host-side operations and are not managed here.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "`<environment>/<expires_at>`, unique per minted token.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"environment": schema.StringAttribute{
				MarkdownDescription: "Environment the host registers into: lower-case letters, digits and `-`, at most 63 characters. Changing it mints a new token.",
				Required:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
				Validators:          []validator.String{deployPatternValidator{pattern: deployEnvironmentNamePattern, subject: "environment name"}},
			},
			"ttl_seconds": schema.Int64Attribute{
				MarkdownDescription: fmt.Sprintf("Token lifetime in seconds, 1 to %d. Unset takes the platform's one-hour default. Changing it mints a new token.", deployMaximumJoinTokenSeconds),
				Optional:            true,
				PlanModifiers:       []planmodifier.Int64{int64planmodifier.RequiresReplace()},
				Validators:          []validator.Int64{deployInt64Range{minimum: 1, maximum: deployMaximumJoinTokenSeconds}},
			},
			"join_token": schema.StringAttribute{
				MarkdownDescription: "The join token. Shown by the platform only once.",
				Computed:            true,
				Sensitive:           true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"expires_at": schema.StringAttribute{
				MarkdownDescription: "When the token stops being accepted (RFC 3339).",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
		},
	}
}

func (tokenResource *deployHostJoinTokenResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	apiClient := tokenResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var plan deployHostJoinTokenModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}
	token, err := apiClient.CreateDeployHostJoinToken(ctx, plan.Environment.ValueString(), client.DeployHostJoinTokenCreate{
		TTLSeconds: int64Pointer(plan.TTLSeconds),
	})
	if err != nil {
		response.Diagnostics.AddError("Unable to create host join token", err.Error())
		return
	}
	plan.ID = types.StringValue(token.Environment + "/" + token.ExpiresAt)
	plan.JoinToken = types.StringValue(token.JoinToken)
	plan.ExpiresAt = types.StringValue(token.ExpiresAt)
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
}

// Read keeps state as it is, since no route reads a join token back, until
// the token expires: an expired token is dropped so the next apply mints a
// new one.
func (tokenResource *deployHostJoinTokenResource) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	if tokenResource.requireClient(&response.Diagnostics) == nil {
		return
	}
	var state deployHostJoinTokenModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	if clusterSettingsIsPast(state.ExpiresAt.ValueString()) {
		response.State.RemoveResource(ctx)
		return
	}
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
}

// Update is never called with a change: every configurable attribute forces
// replacement.
func (tokenResource *deployHostJoinTokenResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	var plan deployHostJoinTokenModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
}

// Delete removes the token from state only: the platform has no revoke.
func (tokenResource *deployHostJoinTokenResource) Delete(_ context.Context, _ resource.DeleteRequest, response *resource.DeleteResponse) {
	tokenResource.requireClient(&response.Diagnostics)
}

// ImportState refuses: a join token is never readable after it is minted.
func (tokenResource *deployHostJoinTokenResource) ImportState(_ context.Context, _ resource.ImportStateRequest, response *resource.ImportStateResponse) {
	response.Diagnostics.AddError("Import not supported",
		"The platform shows a host join token only when it is minted and never reads it back, so an existing token cannot be imported. Create a new one instead.")
}
