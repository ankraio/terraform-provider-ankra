// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"ankra.io/terraform-provider-ankra/internal/client"
)

// providerTokenDetail is the missing-token detail for resources and data
// sources added after 0.2, which take the token from the provider only and
// carry no deprecated per-resource ankra_token.
const providerTokenDetail = "Set an API token on the provider block or the ANKRA_TOKEN environment variable."

// configuredClient holds the API client the provider hands to a resource or
// data source. Embed it to get Configure for free; call requireClient at the
// top of every CRUD method.
type configuredClient struct {
	client *client.Client
}

func (holder *configuredClient) Configure(_ context.Context, request resource.ConfigureRequest, response *resource.ConfigureResponse) {
	holder.client = clientFromProviderData(request.ProviderData, &response.Diagnostics)
}

// requireClient returns the configured client, or records why there is none
// and returns nil so the caller sends no request: no token, or an
// organisation_id / project_id that is not known until apply.
func (holder *configuredClient) requireClient(diagnostics *diag.Diagnostics) *client.Client {
	if holder.client == nil || holder.client.Token == "" {
		diagnostics.AddError("Missing API token", providerTokenDetail)
		return nil
	}
	if holder.client.UnresolvedScope != "" {
		diagnostics.AddError("Provider "+holder.client.UnresolvedScope+" is not known",
			"The provider's "+holder.client.UnresolvedScope+" depends on a value that is only known after apply. "+
				"Requests are not sent without it, because they would act in a different organisation or project. "+
				"Set it from a known value, or apply the resource it depends on first (-target).")
		return nil
	}
	lookupContext, cancel := context.WithTimeout(context.Background(), client.DefaultTimeout)
	defer cancel()
	if err := holder.client.ResolveProject(lookupContext); err != nil {
		diagnostics.AddError("Unable to resolve the provider's project_id", err.Error())
		return nil
	}
	projectCount, err := holder.client.FloatingProjectCount(lookupContext)
	if err != nil {
		diagnostics.AddError("Unable to read the organisation's projects", err.Error())
		return nil
	}
	if projectCount > 1 {
		diagnostics.AddError("Provider project_id is required",
			fmt.Sprintf("This organisation has %d projects and the provider sets no project_id, so the platform would scope every request to "+
				"the project the token's user last selected in the portal. Objects in another project would then read as deleted, and "+
				"destroy would report success while they live on. Set project_id on the provider (or ANKRA_PROJECT_ID) to the project these resources belong in.",
				projectCount))
		return nil
	}
	return holder.client
}

// resourceProjectClient scopes apiClient to a resource's own project_id (an
// ID or a slug) when it sets one, so every request for that resource, reads
// and deletes included, acts in that project. It returns apiClient unchanged
// when project_id is null, and nil after recording why the project could not
// be resolved.
func resourceProjectClient(ctx context.Context, apiClient *client.Client, projectID types.String, diagnostics *diag.Diagnostics) *client.Client {
	if apiClient == nil || projectID.IsNull() || projectID.IsUnknown() {
		return apiClient
	}
	scoped, err := apiClient.InProject(ctx, projectID.ValueString())
	if err != nil {
		diagnostics.AddAttributeError(path.Root("project_id"), "Unable to resolve project_id", err.Error())
		return nil
	}
	return scoped
}

// readTargetChanged reports whether the object found for a resource is a
// different one from the object in state: the same name now resolves to
// another ID, as happens when the request scope moved (another project) or
// the original was deleted and something else took its name. Read must then
// treat the original as gone rather than adopt the other object, or the next
// apply would overwrite it and destroy would delete it.
func readTargetChanged(stateID types.String, foundID string) bool {
	return !stateID.IsNull() && !stateID.IsUnknown() && stateID.ValueString() != "" && stateID.ValueString() != foundID
}

// configuredDataSourceClient is configuredClient for data sources, whose
// Configure takes the data source request types.
type configuredDataSourceClient struct {
	configuredClient
}

func (holder *configuredDataSourceClient) Configure(_ context.Context, request datasource.ConfigureRequest, response *datasource.ConfigureResponse) {
	holder.client = clientFromProviderData(request.ProviderData, &response.Diagnostics)
}

func clientFromProviderData(providerData any, diagnostics *diag.Diagnostics) *client.Client {
	if providerData == nil {
		return nil
	}
	apiClient, ok := providerData.(*client.Client)
	if !ok {
		diagnostics.AddError(
			"Unexpected provider data type",
			fmt.Sprintf("Expected *client.Client, got %T. This is a bug in the provider.", providerData),
		)
		return nil
	}
	return apiClient
}

// stringPointer maps an optional string attribute to the pointer a JSON
// request field expects: null and unknown become nil.
func stringPointer(value types.String) *string {
	if value.IsNull() || value.IsUnknown() {
		return nil
	}
	text := value.ValueString()
	return &text
}

// boolPointer is stringPointer for booleans.
func boolPointer(value types.Bool) *bool {
	if value.IsNull() || value.IsUnknown() {
		return nil
	}
	flag := value.ValueBool()
	return &flag
}

// int64Pointer is stringPointer for integers.
func int64Pointer(value types.Int64) *int64 {
	if value.IsNull() || value.IsUnknown() {
		return nil
	}
	number := value.ValueInt64()
	return &number
}

// optionalString maps an API string that may be empty to a state value:
// empty becomes null, so an unset optional attribute does not plan a diff.
func optionalString(value string) types.String {
	if value == "" {
		return types.StringNull()
	}
	return types.StringValue(value)
}

// stringsFromList reads a list-of-strings attribute; null and unknown read as
// nil, and unknown elements are skipped.
func stringsFromList(ctx context.Context, list types.List, diagnostics *diag.Diagnostics) []string {
	if list.IsNull() || list.IsUnknown() {
		return nil
	}
	var elements []types.String
	diagnostics.Append(list.ElementsAs(ctx, &elements, false)...)
	values := make([]string, 0, len(elements))
	for _, element := range elements {
		if element.IsNull() || element.IsUnknown() {
			continue
		}
		values = append(values, element.ValueString())
	}
	return values
}

// listFromStrings is the inverse of stringsFromList. A nil slice is a null
// list, so an optional attribute the API leaves out stays unset.
func listFromStrings(values []string) types.List {
	if values == nil {
		return types.ListNull(types.StringType)
	}
	elements := make([]attr.Value, 0, len(values))
	for _, value := range values {
		elements = append(elements, types.StringValue(value))
	}
	return types.ListValueMust(types.StringType, elements)
}

// scopePrivateKey is the private-state key holding the organisation and
// project a resource's object was created or last read in.
const scopePrivateKey = "ankra_scope"

type recordedScope struct {
	OrganisationID string `json:"organisation_id"`
	ProjectID      string `json:"project_id"`
}

type privateStateWriter interface {
	SetKey(ctx context.Context, key string, value []byte) diag.Diagnostics
}

type privateStateReader interface {
	GetKey(ctx context.Context, key string) ([]byte, diag.Diagnostics)
}

// trackScope records the provider's organisation and project in the
// resource's private state. Call it at the end of Create and of every
// successful Read, passing response.Private, so a later 404 can be told apart
// from a scope change.
func trackScope(ctx context.Context, apiClient *client.Client, private privateStateWriter) diag.Diagnostics {
	if apiClient == nil || private == nil {
		return nil
	}
	encoded, err := json.Marshal(recordedScope{OrganisationID: apiClient.OrganisationID, ProjectID: apiClient.ScopeProjectID()})
	if err != nil {
		return nil
	}
	return private.SetKey(ctx, scopePrivateKey, encoded)
}

// scopeMoved describes how the provider's scope differs from the one recorded
// for the object, or returns "" when it does not differ or nothing was
// recorded.
func scopeMoved(ctx context.Context, apiClient *client.Client, private privateStateReader) string {
	if apiClient == nil || private == nil {
		return ""
	}
	encoded, diagnostics := private.GetKey(ctx, scopePrivateKey)
	if diagnostics.HasError() || len(encoded) == 0 {
		return ""
	}
	var recorded recordedScope
	if err := json.Unmarshal(encoded, &recorded); err != nil {
		return ""
	}
	switch {
	case recorded.OrganisationID != apiClient.OrganisationID:
		return fmt.Sprintf("organisation_id was %q when Terraform last saw this object and is now %q", recorded.OrganisationID, apiClient.OrganisationID)
	case recorded.ProjectID != apiClient.ScopeProjectID():
		return fmt.Sprintf("project_id was %q when Terraform last saw this object and is now %q", recorded.ProjectID, apiClient.ScopeProjectID())
	}
	return ""
}

const scopeMovedDetail = "The platform answered not found, but the provider is now scoped differently from when Terraform last saw this object, " +
	"so it is most likely still there, in the old scope. It is kept in state rather than planned for recreation (or reported destroyed). " +
	"Point the provider back at the old scope to manage it, or run `terraform state rm` if you mean to stop managing it."

// removeWhenNotFound drops a resource from state when the platform reports it
// gone, which plans its recreation. It reports whether it handled err: when
// the provider's scope changed since the object was tracked, it records an
// error and keeps the resource instead, because the 404 only means the object
// is outside the new scope.
func removeWhenNotFound(ctx context.Context, err error, apiClient *client.Client, response *resource.ReadResponse) bool {
	if !client.IsNotFound(err) {
		return false
	}
	if moved := scopeMoved(ctx, apiClient, response.Private); moved != "" {
		response.Diagnostics.AddError("Object not visible in the provider's current scope", moved+". "+scopeMovedDetail)
		return true
	}
	response.State.RemoveResource(ctx)
	return true
}

// removeWhenTargetChanged handles a Read whose lookup found a different
// object than the one in state (readTargetChanged): it drops the resource,
// unless the provider's scope moved, in which case the original most likely
// lives on in the old scope and the resource is kept with an error.
func removeWhenTargetChanged(ctx context.Context, apiClient *client.Client, response *resource.ReadResponse) {
	if moved := scopeMoved(ctx, apiClient, response.Private); moved != "" {
		response.Diagnostics.AddError("Object not visible in the provider's current scope", moved+". "+scopeMovedDetail)
		return
	}
	response.State.RemoveResource(ctx)
}

// deletedWhenNotFound reports whether a Delete may treat err as "already
// gone". It returns false, and records an error, when the provider's scope
// changed since the object was tracked: destroy must not report success for
// an object that lives on in the old scope.
func deletedWhenNotFound(ctx context.Context, err error, apiClient *client.Client, private privateStateReader, diagnostics *diag.Diagnostics) bool {
	if !client.IsNotFound(err) {
		return false
	}
	if moved := scopeMoved(ctx, apiClient, private); moved != "" {
		diagnostics.AddError("Object not visible in the provider's current scope", moved+". "+scopeMovedDetail)
		return false
	}
	return true
}
