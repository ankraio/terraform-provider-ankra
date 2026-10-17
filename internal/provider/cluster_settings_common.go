// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// clusterSettingsOneOfValidator refuses at plan time a value outside the
// vocabulary the platform accepts, which it would refuse at apply.
type clusterSettingsOneOfValidator struct {
	allowed []string
}

func clusterSettingsOneOf(allowed ...string) validator.String {
	return clusterSettingsOneOfValidator{allowed: allowed}
}

func (oneOf clusterSettingsOneOfValidator) Description(_ context.Context) string {
	return "value must be one of: " + strings.Join(oneOf.allowed, ", ")
}

func (oneOf clusterSettingsOneOfValidator) MarkdownDescription(ctx context.Context) string {
	return oneOf.Description(ctx)
}

func (oneOf clusterSettingsOneOfValidator) ValidateString(ctx context.Context, request validator.StringRequest, response *validator.StringResponse) {
	if request.ConfigValue.IsNull() || request.ConfigValue.IsUnknown() {
		return
	}
	for _, allowed := range oneOf.allowed {
		if request.ConfigValue.ValueString() == allowed {
			return
		}
	}
	response.Diagnostics.AddAttributeError(request.Path, "Invalid value",
		fmt.Sprintf("%q is not accepted: %s.", request.ConfigValue.ValueString(), oneOf.Description(ctx)))
}

// clusterSettingsNoSlashValidator refuses a value the platform addresses in
// the URL path, where an escaped slash is not routed: the object could be
// created but never read back or removed.
type clusterSettingsNoSlashValidator struct{}

func (clusterSettingsNoSlashValidator) Description(_ context.Context) string {
	return "value must not contain \"/\""
}

func (noSlash clusterSettingsNoSlashValidator) MarkdownDescription(ctx context.Context) string {
	return noSlash.Description(ctx)
}

func (clusterSettingsNoSlashValidator) ValidateString(_ context.Context, request validator.StringRequest, response *validator.StringResponse) {
	if request.ConfigValue.IsNull() || request.ConfigValue.IsUnknown() {
		return
	}
	if strings.Contains(request.ConfigValue.ValueString(), "/") {
		response.Diagnostics.AddAttributeError(request.Path, "Invalid value",
			"The value cannot contain \"/\": the platform addresses it in the URL path.")
	}
}

// clusterSettingsClusterIDAttribute is the cluster_id every cluster-scoped
// resource of this domain takes; changing it replaces the resource.
func clusterSettingsClusterIDAttribute() schema.StringAttribute {
	return schema.StringAttribute{
		MarkdownDescription: "Identifier of the cluster. Changing it replaces the resource.",
		Required:            true,
		PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
	}
}

// clusterSettingsIDAttribute is the computed id of a resource.
func clusterSettingsIDAttribute(description string) schema.StringAttribute {
	return schema.StringAttribute{
		MarkdownDescription: description,
		Computed:            true,
		PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
	}
}

// clusterSettingsSplitImportID parses a composite "<cluster_id>/<key>" import
// id strictly: exactly two non-empty parts.
func clusterSettingsSplitImportID(importID, keyName string) (string, string, error) {
	parts := strings.Split(importID, "/")
	if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
		return "", "", fmt.Errorf("expected an import id of the form <cluster_id>/<%s>, got %q", keyName, importID)
	}
	return parts[0], parts[1], nil
}

// clusterSettingsImportComposite imports a "<cluster_id>/<key>" id into the
// cluster_id attribute and the key attribute.
func clusterSettingsImportComposite(ctx context.Context, request resource.ImportStateRequest, response *resource.ImportStateResponse, keyAttribute string) {
	clusterID, key, err := clusterSettingsSplitImportID(request.ID, keyAttribute)
	if err != nil {
		response.Diagnostics.AddError("Invalid import id", err.Error())
		return
	}
	response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("cluster_id"), clusterID)...)
	response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root(keyAttribute), key)...)
}

// clusterSettingsImportClusterID imports a singleton settings resource by
// its cluster id.
func clusterSettingsImportClusterID(ctx context.Context, request resource.ImportStateRequest, response *resource.ImportStateResponse) {
	if strings.TrimSpace(request.ID) == "" || strings.Contains(request.ID, "/") {
		response.Diagnostics.AddError("Invalid import id", fmt.Sprintf("expected a cluster id, got %q", request.ID))
		return
	}
	response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("cluster_id"), request.ID)...)
	response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("id"), request.ID)...)
}

// clusterSettingsTimestamp keeps the configured spelling of a timestamp when
// the platform returns the same instant in another format (it answers UTC
// with a Z suffix), so the first plan after apply shows no diff.
func clusterSettingsTimestamp(configured types.String, returned *string) types.String {
	if returned == nil || *returned == "" {
		return types.StringNull()
	}
	if !configured.IsNull() && !configured.IsUnknown() {
		configuredTime, configuredError := time.Parse(time.RFC3339, configured.ValueString())
		returnedTime, returnedError := time.Parse(time.RFC3339, *returned)
		if configuredError == nil && returnedError == nil && configuredTime.Equal(returnedTime) {
			return configured
		}
	}
	return types.StringValue(*returned)
}

// clusterSettingsNormaliseZone is the platform's zone normalisation: trimmed,
// lower case, without the trailing root dot or a leading wildcard label.
func clusterSettingsNormaliseZone(zone string) string {
	return strings.TrimPrefix(strings.TrimSuffix(strings.ToLower(strings.TrimSpace(zone)), "."), "*.")
}
