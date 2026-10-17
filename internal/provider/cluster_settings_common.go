// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"ankra.io/terraform-provider-ankra/internal/client"
)

// clusterSettingsNow is the clock plan-time checks read; tests replace it.
var clusterSettingsNow = time.Now

// clusterSettingsRFC3339Validator refuses a timestamp the platform would
// refuse, at plan time.
type clusterSettingsRFC3339Validator struct{}

func (clusterSettingsRFC3339Validator) Description(_ context.Context) string {
	return "value must be an RFC 3339 timestamp, e.g. 2026-12-31T23:00:00Z"
}

func (rfc3339 clusterSettingsRFC3339Validator) MarkdownDescription(ctx context.Context) string {
	return rfc3339.Description(ctx)
}

func (rfc3339 clusterSettingsRFC3339Validator) ValidateString(ctx context.Context, request validator.StringRequest, response *validator.StringResponse) {
	if request.ConfigValue.IsNull() || request.ConfigValue.IsUnknown() {
		return
	}
	if _, err := time.Parse(time.RFC3339, request.ConfigValue.ValueString()); err != nil {
		response.Diagnostics.AddAttributeError(request.Path, "Invalid timestamp", rfc3339.Description(ctx)+".")
	}
}

// clusterSettingsIsPast reports whether an RFC 3339 timestamp lies in the
// past; an unparsable or empty one is not.
func clusterSettingsIsPast(value string) bool {
	parsed, err := time.Parse(time.RFC3339, value)
	return err == nil && !parsed.After(clusterSettingsNow())
}

// clusterSettingsNotFoundDetail reads the detail of a 404 from the platform,
// and reports whether err is a 404 at all.
func clusterSettingsNotFoundDetail(err error) (string, bool) {
	var apiError *client.APIError
	if !errors.As(err, &apiError) || apiError.StatusCode != http.StatusNotFound {
		return "", false
	}
	var body struct {
		Detail string `json:"detail"`
	}
	if json.Unmarshal([]byte(apiError.Body), &body) != nil {
		return "", true
	}
	return strings.TrimSpace(body.Detail), true
}

// clusterSettingsScopeError tells a 404 that means "you cannot see this"
// from one that means "this object is gone". The cluster routes answer
// "Cluster not found" when the cluster is outside the provider's
// organisation and project scope (ClusterIsInScope), and the mesh routes
// answer "Not Found" while the organisation's cluster_mesh feature is dark.
// Neither says the object is gone, so dropping it from state, or counting a
// delete as done, would leave it running unmanaged. It returns nil for any
// other error.
func clusterSettingsScopeError(err error, clusterID string) error {
	detail, isNotFound := clusterSettingsNotFoundDetail(err)
	if !isNotFound {
		return nil
	}
	switch {
	case strings.EqualFold(detail, "cluster not found"):
		return fmt.Errorf("cluster %s is not visible in the provider's organisation and project scope. "+
			"Set the provider's project_id (or organisation_id) to the scope that holds the cluster; if the cluster was "+
			"deleted, remove this resource with `terraform state rm`. (%w)", clusterID, err)
	case detail == "Not Found":
		return fmt.Errorf("the cluster mesh feature is not enabled for this organisation, so the mesh cannot be read; "+
			"if the mesh is really gone, remove this resource with `terraform state rm`. (%w)", err)
	}
	return nil
}

// clusterSettingsReadNotFound handles a Read error that is a 404: an object
// really gone is removed from state, and a 404 that only means the cluster
// is out of scope is reported as an error. It reports whether it handled err.
func clusterSettingsReadNotFound(ctx context.Context, err error, apiClient *client.Client, clusterID string, response *resource.ReadResponse) bool {
	if scopeError := clusterSettingsScopeError(err, clusterID); scopeError != nil {
		response.Diagnostics.AddError("Cluster not in scope", scopeError.Error())
		return true
	}
	return removeWhenNotFound(ctx, err, apiClient, response)
}

// clusterSettingsDeleteError is the error a Delete reports: nil when the
// object is already gone, the scope error when the cluster is out of scope.
func clusterSettingsDeleteError(err error, clusterID string) error {
	if err == nil {
		return nil
	}
	if scopeError := clusterSettingsScopeError(err, clusterID); scopeError != nil {
		return scopeError
	}
	if client.IsNotFound(err) {
		return nil
	}
	return err
}

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

// clusterSettingsErrorText is the diagnostic detail for an API error, which
// names the scope problem when the cluster is out of scope.
func clusterSettingsErrorText(err error, clusterID string) string {
	if scopeError := clusterSettingsScopeError(err, clusterID); scopeError != nil {
		return scopeError.Error()
	}
	return err.Error()
}

// clusterSettingsIsConflict reports whether err is a 409 from the platform.
func clusterSettingsIsConflict(err error) bool {
	var apiError *client.APIError
	return errors.As(err, &apiError) && apiError.StatusCode == http.StatusConflict
}
