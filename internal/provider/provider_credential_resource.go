// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64default"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"ankra.io/terraform-provider-ankra/internal/client"
)

var (
	_ resource.Resource                = (*providerCredentialResource)(nil)
	_ resource.ResourceWithConfigure   = (*providerCredentialResource)(nil)
	_ resource.ResourceWithImportState = (*providerCredentialResource)(nil)
)

// credentialStringSetting is one string setting a provider credential is
// created with. The platform never returns a secret, and returns only some
// connection settings.
type credentialStringSetting struct {
	name        string
	description string
	required    bool
	sensitive   bool
	// rotatable settings are secret material the platform replaces in place
	// (PUT /api/v1/org/credentials/{id}); every other setting is part of the
	// credential's identity and changing it replaces the credential.
	rotatable bool
	// defaultValue, when set, is the value the platform applies when the
	// setting is left out.
	defaultValue string
	// listed reads the setting back from the credential listing, for the
	// settings the listing carries. A nil answer means the listing did not
	// say, and the state is kept.
	listed func(credential *client.Credential) *string
	// normalise, when set, is how the platform stores the value, so a listed
	// value that only differs in spelling is not reported as drift.
	normalise func(value string) string
	// validators reject at plan time values the platform refuses.
	validators []validator.String
}

// credentialBoolSetting is a boolean setting the platform defaults to false.
type credentialBoolSetting struct {
	name        string
	description string
	// listed reads the setting back from the credential listing; nil means
	// the listing did not say.
	listed func(credential *client.Credential) *bool
}

// providerCredentialKind describes one kind of cloud provider credential:
// its listing filter, create route and settings.
type providerCredentialKind struct {
	typeSuffix  string
	provider    string
	label       string
	command     string
	description string
	// awsMethod, when set, creates through POST /api/v1/credentials/aws/<method>,
	// which answers with the new credential's id.
	awsMethod    string
	strings      []credentialStringSetting
	bools        []credentialBoolSetting
	hasJumphost  bool
	hasTailscale bool
}

type credentialJumphostModel struct {
	Host       types.String `tfsdk:"host"`
	Port       types.Int64  `tfsdk:"port"`
	Username   types.String `tfsdk:"username"`
	PrivateKey types.String `tfsdk:"private_key"`
}

type credentialTailscaleModel struct {
	LoginServer     types.String `tfsdk:"login_server"`
	AuthKey         types.String `tfsdk:"auth_key"`
	AcceptRoutes    types.Bool   `tfsdk:"accept_routes"`
	AdvertiseRoutes types.String `tfsdk:"advertise_routes"`
}

type providerCredentialResource struct {
	configuredClient
	kind providerCredentialKind
}

func newProviderCredentialResource(kind providerCredentialKind) func() resource.Resource {
	return func() resource.Resource {
		return &providerCredentialResource{kind: kind}
	}
}

func (credentialResource *providerCredentialResource) Metadata(_ context.Context, request resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_" + credentialResource.kind.typeSuffix
}

func (credentialResource *providerCredentialResource) Schema(_ context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	kind := credentialResource.kind
	attributes := credentialListedAttributes()
	attributes["name"] = credentialNameAttribute()
	for _, setting := range kind.strings {
		attribute := schema.StringAttribute{
			MarkdownDescription: setting.description,
			Required:            setting.required,
			Optional:            !setting.required,
			Sensitive:           setting.sensitive,
			Validators:          setting.validators,
		}
		if !setting.rotatable {
			attribute.PlanModifiers = []planmodifier.String{credentialReplaceString()}
		}
		if setting.defaultValue != "" {
			attribute.Computed = true
			attribute.Default = stringdefault.StaticString(setting.defaultValue)
		}
		attributes[setting.name] = attribute
	}
	for _, setting := range kind.bools {
		attributes[setting.name] = schema.BoolAttribute{
			MarkdownDescription: setting.description + " Defaults to `false`.",
			Optional:            true,
			Computed:            true,
			Default:             booldefault.StaticBool(false),
			PlanModifiers:       []planmodifier.Bool{credentialReplaceBool()},
		}
	}
	if kind.hasJumphost {
		attributes["jumphost"] = schema.SingleNestedAttribute{
			MarkdownDescription: "SSH jumphost the platform tunnels through to reach the API. Changing it replaces the credential.",
			Optional:            true,
			PlanModifiers:       []planmodifier.Object{credentialReplaceObject()},
			Attributes: map[string]schema.Attribute{
				"host": schema.StringAttribute{
					MarkdownDescription: "Jumphost address.",
					Required:            true,
				},
				"port": schema.Int64Attribute{
					MarkdownDescription: "Jumphost SSH port. Defaults to `22`.",
					Optional:            true,
					Computed:            true,
					Default:             int64default.StaticInt64(22),
				},
				"username": schema.StringAttribute{
					MarkdownDescription: "Jumphost SSH user. Defaults to `root`.",
					Optional:            true,
					Computed:            true,
					Default:             stringdefault.StaticString("root"),
				},
				"private_key": schema.StringAttribute{
					MarkdownDescription: "SSH private key for the jumphost.",
					Required:            true,
					Sensitive:           true,
				},
			},
		}
	}
	if kind.hasTailscale {
		attributes["tailscale"] = schema.SingleNestedAttribute{
			MarkdownDescription: "Tailscale or Headscale join for the cluster VMs, needed when they sit on a node-local SDN the platform cannot reach otherwise. " +
				"Changed and removed in place, the counterpart of `ankra credentials proxmox tailscale set|clear`.",
			Optional: true,
			Attributes: map[string]schema.Attribute{
				"login_server": schema.StringAttribute{
					MarkdownDescription: "Headscale or Tailscale control server URL.",
					Required:            true,
				},
				"auth_key": schema.StringAttribute{
					MarkdownDescription: "Pre-authentication key the VMs join with.",
					Required:            true,
					Sensitive:           true,
				},
				"accept_routes": schema.BoolAttribute{
					MarkdownDescription: "Accept subnet routes advertised on the tailnet. Defaults to `false`.",
					Optional:            true,
					Computed:            true,
					Default:             booldefault.StaticBool(false),
				},
				"advertise_routes": schema.StringAttribute{
					MarkdownDescription: "Comma-separated CIDRs the VMs advertise to the tailnet.",
					Optional:            true,
				},
			},
		}
	}
	response.Schema = schema.Schema{
		MarkdownDescription: kind.description + " The counterpart of `" + kind.command + "` and `ankra credentials delete`.\n\n" +
			credentialResource.changeDescription() + credentialOperationalNotes,
		Attributes: attributes,
	}
}

// changeDescription documents which settings change in place, which replace
// the credential, and which are checked for drift.
func (credentialResource *providerCredentialResource) changeDescription() string {
	var rotatable, listed []string
	for _, setting := range credentialResource.kind.strings {
		if setting.rotatable {
			rotatable = append(rotatable, "`"+setting.name+"`")
		}
		if setting.listed != nil {
			listed = append(listed, "`"+setting.name+"`")
		}
	}
	for _, setting := range credentialResource.kind.bools {
		if setting.listed != nil {
			listed = append(listed, "`"+setting.name+"`")
		}
	}
	description := "**Changes.** Changing " + credentialJoinNames(rotatable, "or") + " rotates the secret in place: the platform checks the new value " +
		"with the provider and stores it only when the provider accepts it, otherwise the apply fails and the credential keeps its secret. " +
		"`display_name` also changes in place. The other settings identify what the credential connects to, so changing them replaces the credential.\n\n" +
		"**Drift.** The platform never returns a secret, so a secret changed outside Terraform is not detected; the name, the display name and the credential's existence are"
	if len(listed) > 0 {
		description += ", and so are " + credentialJoinNames(listed, "and") + ". A platform from before 2026-10-18 does not list these settings, and a setting the platform could not read is not listed; " +
			"either way the value in state is kept rather than reported as changed"
	}
	return description + "."
}

// createBody assembles the platform's create body from the plan.
func (credentialResource *providerCredentialResource) createBody(ctx context.Context, plan tfsdk.Plan) (map[string]any, diag.Diagnostics) {
	var diagnostics diag.Diagnostics
	kind := credentialResource.kind
	var name types.String
	diagnostics.Append(plan.GetAttribute(ctx, path.Root("name"), &name)...)
	body := map[string]any{"name": name.ValueString()}
	for _, setting := range kind.strings {
		var value types.String
		diagnostics.Append(plan.GetAttribute(ctx, path.Root(setting.name), &value)...)
		if !value.IsNull() && !value.IsUnknown() {
			body[setting.name] = value.ValueString()
		}
	}
	for _, setting := range kind.bools {
		var value types.Bool
		diagnostics.Append(plan.GetAttribute(ctx, path.Root(setting.name), &value)...)
		body[setting.name] = value.ValueBool()
	}
	if kind.hasJumphost {
		var jumphost *credentialJumphostModel
		diagnostics.Append(plan.GetAttribute(ctx, path.Root("jumphost"), &jumphost)...)
		if jumphost != nil {
			body["jumphost"] = map[string]any{
				"host":        jumphost.Host.ValueString(),
				"port":        jumphost.Port.ValueInt64(),
				"username":    jumphost.Username.ValueString(),
				"private_key": jumphost.PrivateKey.ValueString(),
			}
		}
	}
	if kind.hasTailscale {
		tailscale, tailscaleDiagnostics := credentialTailscaleFrom(ctx, plan.GetAttribute)
		diagnostics.Append(tailscaleDiagnostics...)
		if tailscale != nil {
			body["tailscale"] = tailscale
		}
	}
	return body, diagnostics
}

type credentialAttributeGetter func(ctx context.Context, attributePath path.Path, target any) diag.Diagnostics

func credentialTailscaleFrom(ctx context.Context, get credentialAttributeGetter) (*client.CredentialProxmoxTailscale, diag.Diagnostics) {
	var tailscale *credentialTailscaleModel
	diagnostics := get(ctx, path.Root("tailscale"), &tailscale)
	if tailscale == nil {
		return nil, diagnostics
	}
	return &client.CredentialProxmoxTailscale{
		LoginServer:     tailscale.LoginServer.ValueString(),
		AuthKey:         tailscale.AuthKey.ValueString(),
		AcceptRoutes:    tailscale.AcceptRoutes.ValueBool(),
		AdvertiseRoutes: tailscale.AdvertiseRoutes.ValueString(),
	}, diagnostics
}

// credentialJoinNames joins attribute names as English prose: "a", "a and b",
// "a, b and c".
func credentialJoinNames(names []string, conjunction string) string {
	if len(names) < 2 {
		return strings.Join(names, "")
	}
	return strings.Join(names[:len(names)-1], ", ") + " " + conjunction + " " + names[len(names)-1]
}

// setListedSettings writes the connection settings the listing carries into
// state, so a credential re-pointed outside Terraform shows as drift. A
// setting the listing leaves out is unknown, not changed, and keeps its state;
// a listed value that only differs in how the platform spells it keeps the
// configured spelling.
func (credentialResource *providerCredentialResource) setListedSettings(ctx context.Context, state *tfsdk.State, credential *client.Credential) diag.Diagnostics {
	var diagnostics diag.Diagnostics
	for _, setting := range credentialResource.kind.strings {
		if setting.listed == nil {
			continue
		}
		listedValue := setting.listed(credential)
		if listedValue == nil {
			continue
		}
		var current types.String
		diagnostics.Append(state.GetAttribute(ctx, path.Root(setting.name), &current)...)
		if credentialSettingMatches(setting, current, *listedValue) {
			continue
		}
		diagnostics.Append(state.SetAttribute(ctx, path.Root(setting.name), types.StringValue(*listedValue))...)
	}
	for _, setting := range credentialResource.kind.bools {
		if setting.listed == nil {
			continue
		}
		if listedValue := setting.listed(credential); listedValue != nil {
			diagnostics.Append(state.SetAttribute(ctx, path.Root(setting.name), types.BoolValue(*listedValue))...)
		}
	}
	return diagnostics
}

// credentialSettingMatches reports whether the state already holds the listed
// value, comparing the way the platform stores it. A null state matches only
// when the platform stores an unset value as the listed one.
func credentialSettingMatches(setting credentialStringSetting, current types.String, listedValue string) bool {
	if current.IsUnknown() {
		return false
	}
	if setting.normalise == nil {
		return !current.IsNull() && strings.TrimSpace(current.ValueString()) == strings.TrimSpace(listedValue)
	}
	return setting.normalise(current.ValueString()) == setting.normalise(listedValue)
}

// credentialNormaliseAPIURL compares API URLs without surrounding whitespace
// or a trailing slash.
func credentialNormaliseAPIURL(value string) string {
	return strings.TrimRight(strings.TrimSpace(value), "/")
}

// credentialAnkraCloudDefaultEndpoint is the endpoint the platform stores for
// an Ankra Cloud credential created without one.
const credentialAnkraCloudDefaultEndpoint = "https://cloud.ankra.app"

// credentialNormaliseAnkraCloudEndpoint is how the platform stores an Ankra
// Cloud endpoint: scheme and host only, the default when unset.
func credentialNormaliseAnkraCloudEndpoint(value string) string {
	trimmed := strings.TrimRight(strings.TrimSpace(value), "/")
	if trimmed == "" {
		return credentialAnkraCloudDefaultEndpoint
	}
	parsed, err := url.Parse(trimmed)
	if err != nil || parsed.Host == "" {
		return trimmed
	}
	return parsed.Scheme + "://" + parsed.Host
}

// recordUnreadCreate keeps a credential that was created but could not be
// read back in state, so the next apply does not fail on its name, and fails
// the create, which marks it tainted.
func (credentialResource *providerCredentialResource) recordUnreadCreate(ctx context.Context, apiClient *client.Client, request resource.CreateRequest, response *resource.CreateResponse, credentialID string, readError error) {
	response.State.Raw = request.Plan.Raw
	for _, attributeName := range []string{"display_name", "state", "created_at"} {
		response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root(attributeName), types.StringNull())...)
	}
	response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("available"), types.BoolNull())...)
	response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("id"), types.StringValue(credentialID))...)
	response.Diagnostics.Append(trackScope(ctx, apiClient, response.Private)...)
	response.Diagnostics.AddError("Unable to read back "+credentialResource.kind.label+" credential",
		fmt.Sprintf("The credential %s was created and is recorded in state, but could not be read back: %s. "+
			"The token needs credentials.read as well as credentials.write. Terraform marks the resource tainted and replaces it on the next apply; "+
			"run `terraform untaint` on it to keep this credential instead.", credentialID, readError))
}

func (credentialResource *providerCredentialResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	apiClient := credentialResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	kind := credentialResource.kind
	body, diagnostics := credentialResource.createBody(ctx, request.Plan)
	response.Diagnostics.Append(diagnostics...)
	if response.Diagnostics.HasError() {
		return
	}

	var created *client.Credential
	if kind.awsMethod != "" {
		awsCredential, err := apiClient.CreateAwsCredential(ctx, kind.awsMethod, body)
		if err != nil {
			response.Diagnostics.AddError("Unable to create "+kind.label+" credential", credentialAPIErrorText(err))
			return
		}
		created, err = apiClient.GetCredential(ctx, kind.provider, awsCredential.ID)
		if err != nil {
			credentialResource.recordUnreadCreate(ctx, apiClient, request, response, awsCredential.ID, err)
			return
		}
	} else {
		var err error
		created, err = apiClient.CreateProviderCredential(ctx, kind.provider, body)
		var readBackError *client.CredentialReadBackError
		if errors.As(err, &readBackError) && readBackError.ID != "" {
			credentialResource.recordUnreadCreate(ctx, apiClient, request, response, readBackError.ID, readBackError.Err)
			return
		}
		if errors.As(err, &readBackError) {
			response.Diagnostics.AddError("Unable to read back "+kind.label+" credential",
				fmt.Sprintf("The platform created the %s credential %q but it could not be read back: %s. "+
					"The token needs credentials.read as well as credentials.write. The credential exists, so applying again fails on its name: "+
					"find its id with `ankra credentials list --provider %s`, then run `terraform import ankra_%s.<name> <id>`.",
					kind.label, readBackError.Name, readBackError.Err, kind.provider, kind.typeSuffix))
			return
		}
		if err != nil {
			response.Diagnostics.AddError("Unable to create "+kind.label+" credential", credentialAPIErrorText(err))
			return
		}
	}

	response.State.Raw = request.Plan.Raw
	var plannedDisplayName types.String
	response.Diagnostics.Append(request.Plan.GetAttribute(ctx, path.Root("display_name"), &plannedDisplayName)...)
	response.Diagnostics.Append(credentialSetListed(ctx, &response.State, created)...)
	response.Diagnostics.Append(trackScope(ctx, apiClient, response.Private)...)
	displayName, displayNameDiagnostics := credentialCreateDisplayName(ctx, apiClient, created.ID, plannedDisplayName, types.StringPointerValue(created.DisplayName))
	response.Diagnostics.Append(displayNameDiagnostics...)
	response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("display_name"), displayName)...)
}

func (credentialResource *providerCredentialResource) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	apiClient := credentialResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var credentialID types.String
	response.Diagnostics.Append(request.State.GetAttribute(ctx, path.Root("id"), &credentialID)...)
	if response.Diagnostics.HasError() {
		return
	}

	credential, err := apiClient.GetCredential(ctx, credentialResource.kind.provider, credentialID.ValueString())
	if removeWhenNotFound(ctx, err, apiClient, response) {
		return
	}
	if err != nil {
		response.Diagnostics.AddError("Unable to read "+credentialResource.kind.label+" credential", err.Error())
		return
	}
	if method := credentialResource.kind.awsMethod; method != "" && credential.AuthMethod != nil && *credential.AuthMethod != method {
		response.Diagnostics.AddError("Wrong AWS credential type",
			fmt.Sprintf("The credential %s authenticates with %q, not %q; manage it with the matching AWS credential resource.", credential.ID, *credential.AuthMethod, method))
		return
	}
	response.Diagnostics.Append(credentialSetListed(ctx, &response.State, credential)...)
	response.Diagnostics.Append(credentialResource.setListedSettings(ctx, &response.State, credential)...)
	response.Diagnostics.Append(trackScope(ctx, apiClient, response.Private)...)
}

// Update changes in place what the platform can change in place: the
// Tailscale join, the secret (a verified rotation) and the display name. It
// also adopts the configured settings of a just-imported credential. Every
// other change is planned as a replacement. The state starts from the prior
// one and takes each planned change only once the platform has made it, so a
// failed step leaves the state describing what the platform holds.
func (credentialResource *providerCredentialResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	apiClient := credentialResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	kind := credentialResource.kind
	var credentialID types.String
	response.Diagnostics.Append(request.State.GetAttribute(ctx, path.Root("id"), &credentialID)...)
	if response.Diagnostics.HasError() {
		return
	}
	response.State.Raw = request.State.Raw

	if kind.hasTailscale {
		var planned, current types.Object
		response.Diagnostics.Append(request.Plan.GetAttribute(ctx, path.Root("tailscale"), &planned)...)
		response.Diagnostics.Append(request.State.GetAttribute(ctx, path.Root("tailscale"), &current)...)
		if response.Diagnostics.HasError() {
			return
		}
		if !planned.Equal(current) {
			tailscale, diagnostics := credentialTailscaleFrom(ctx, request.Plan.GetAttribute)
			response.Diagnostics.Append(diagnostics...)
			if response.Diagnostics.HasError() {
				return
			}
			if err := apiClient.UpdateProxmoxCredentialTailscale(ctx, credentialID.ValueString(), tailscale); err != nil {
				response.Diagnostics.AddError("Unable to update "+kind.label+" credential Tailscale settings", credentialAPIErrorText(err))
				return
			}
			response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("tailscale"), planned)...)
		}
	}

	if !credentialResource.rotate(ctx, apiClient, credentialID.ValueString(), request, response) {
		return
	}

	var plannedDisplayName, currentDisplayName types.String
	response.Diagnostics.Append(request.Plan.GetAttribute(ctx, path.Root("display_name"), &plannedDisplayName)...)
	response.Diagnostics.Append(request.State.GetAttribute(ctx, path.Root("display_name"), &currentDisplayName)...)
	if response.Diagnostics.HasError() {
		return
	}
	displayName, displayNameDiagnostics := credentialApplyDisplayName(ctx, apiClient, credentialID.ValueString(), plannedDisplayName, currentDisplayName)
	response.Diagnostics.Append(displayNameDiagnostics...)
	if response.Diagnostics.HasError() {
		return
	}
	response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("display_name"), displayName)...)

	credential, err := apiClient.GetCredential(ctx, kind.provider, credentialID.ValueString())
	if err != nil {
		response.Diagnostics.AddError("Unable to read "+kind.label+" credential", err.Error())
		return
	}
	response.State.Raw = request.Plan.Raw
	response.Diagnostics.Append(credentialSetUnknownListed(ctx, request.Plan, &response.State, credential)...)
	credentialWarnWhenAdopting(ctx, request.Private, &response.Diagnostics, kind.label)
	response.Diagnostics.Append(response.Private.SetKey(ctx, credentialImportedKey, nil)...)
}

// rotate replaces the secret in place when a rotatable setting changed from
// a value the state holds. A setting the state holds no value for (right
// after an import) is adopted instead, as before. The rotation sends every
// configured rotatable setting, so the provider checks them together. It
// answers false when the update must stop.
func (credentialResource *providerCredentialResource) rotate(ctx context.Context, apiClient *client.Client, credentialID string, request resource.UpdateRequest, response *resource.UpdateResponse) bool {
	kind := credentialResource.kind
	secretFields := map[string]string{}
	isRotationNeeded := false
	var rotated []credentialStringSetting
	for _, setting := range kind.strings {
		if !setting.rotatable {
			continue
		}
		var planned, current types.String
		response.Diagnostics.Append(request.Plan.GetAttribute(ctx, path.Root(setting.name), &planned)...)
		response.Diagnostics.Append(request.State.GetAttribute(ctx, path.Root(setting.name), &current)...)
		if planned.IsNull() || planned.IsUnknown() {
			continue
		}
		secretFields[setting.name] = planned.ValueString()
		rotated = append(rotated, setting)
		if !current.IsNull() && !planned.Equal(current) {
			isRotationNeeded = true
		}
	}
	if response.Diagnostics.HasError() {
		return false
	}
	if !isRotationNeeded {
		return true
	}

	rotation, err := apiClient.RotateCredential(ctx, credentialID, secretFields)
	if err != nil {
		detail := credentialAPIErrorText(err)
		var apiError *client.APIError
		if errors.As(err, &apiError) && (apiError.StatusCode == http.StatusNotFound || apiError.StatusCode == http.StatusMethodNotAllowed) {
			detail += "\n\nRotating a credential in place needs an Ankra platform from 2026-10-18 or later."
		}
		response.Diagnostics.AddError("Unable to rotate "+kind.label+" credential", detail)
		return false
	}
	if !rotation.Rotated {
		response.Diagnostics.AddError("The provider refused the new "+kind.label+" credential secret", credentialRotationRefusal(rotation.Result))
		return false
	}
	for _, setting := range rotated {
		var planned types.String
		response.Diagnostics.Append(request.Plan.GetAttribute(ctx, path.Root(setting.name), &planned)...)
		response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root(setting.name), planned)...)
	}
	return !response.Diagnostics.HasError()
}

// credentialRotationRefusal explains a rotation the provider refused, in the
// platform's words. The credential keeps its current secret.
func credentialRotationRefusal(result client.CredentialRotationResult) string {
	detail := "The platform checked the new secret with the provider before storing it, and the provider did not accept it, " +
		"so the credential keeps its current secret."
	if result.Message != nil && *result.Message != "" {
		detail += "\n\n" + *result.Message
	}
	if result.Code != nil && *result.Code != "" {
		detail += " (" + *result.Code + ")"
	}
	for _, hint := range result.Hints {
		detail += "\n- " + hint
	}
	return detail
}

func (credentialResource *providerCredentialResource) Delete(ctx context.Context, request resource.DeleteRequest, response *resource.DeleteResponse) {
	apiClient := credentialResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var credentialID types.String
	response.Diagnostics.Append(request.State.GetAttribute(ctx, path.Root("id"), &credentialID)...)
	if response.Diagnostics.HasError() {
		return
	}
	err := apiClient.DeleteCredential(ctx, credentialID.ValueString())
	if err == nil || deletedWhenNotFound(ctx, err, apiClient, request.Private, &response.Diagnostics) || response.Diagnostics.HasError() {
		return
	}
	response.Diagnostics.AddError("Unable to delete "+credentialResource.kind.label+" credential", credentialAPIErrorText(err))
}

// ImportState imports by credential id. The platform never returns the
// credential's secrets or most of its settings, so the first apply after an
// import adopts the configured values without replacing or rotating the
// credential. The settings the listing carries are read by the import.
func (credentialResource *providerCredentialResource) ImportState(ctx context.Context, request resource.ImportStateRequest, response *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), request, response)
	response.Diagnostics.Append(response.Private.SetKey(ctx, credentialImportedKey, []byte("true"))...)
}

const (
	credentialRotatesNote  = " Changing it rotates the secret in place."
	credentialReplacesNote = " Changing it replaces the credential."
	credentialDriftNote    = " A change made outside Terraform shows as drift."
)

func credentialAPITokenSetting(label string) credentialStringSetting {
	return credentialStringSetting{
		name:        "api_token",
		description: label + " API token." + credentialRotatesNote,
		required:    true,
		sensitive:   true,
		rotatable:   true,
	}
}

func credentialListedAPIURL(credential *client.Credential) *string { return credential.APIURL }

func credentialListedTLSInsecure(credential *client.Credential) *bool { return credential.TLSInsecure }

func credentialListedProjectID(credential *client.Credential) *string { return credential.ProjectID }

func credentialListedRegion(credential *client.Credential) *string { return credential.Region }

// credentialListedTLSInsecureSetting is the tls_insecure setting of the
// credentials whose listing carries it.
var credentialListedTLSInsecureSetting = credentialBoolSetting{
	name:        "tls_insecure",
	description: "Skip TLS certificate verification for the API URL." + credentialReplacesNote + credentialDriftNote,
	listed:      credentialListedTLSInsecure,
}

func credentialAWSRegionSetting() credentialStringSetting {
	return credentialStringSetting{
		name:         "region",
		description:  "Default AWS region. Defaults to `us-east-1`." + credentialReplacesNote + credentialDriftNote,
		defaultValue: "us-east-1",
		listed:       credentialListedRegion,
	}
}

var providerCredentialKinds = []providerCredentialKind{
	{
		typeSuffix:  "hetzner_credential",
		provider:    "hetzner",
		label:       "Hetzner",
		command:     "ankra credentials hetzner create",
		description: "A Hetzner Cloud API token credential, referenced by `ankra_hetzner_cluster.credential_id`.",
		strings:     []credentialStringSetting{credentialAPITokenSetting("Hetzner Cloud")},
	},
	{
		typeSuffix:  "digitalocean_credential",
		provider:    "digitalocean",
		label:       "DigitalOcean",
		command:     "ankra credentials digitalocean create",
		description: "A DigitalOcean API token credential, referenced by `ankra_digitalocean_cluster.credential_id`.",
		strings:     []credentialStringSetting{credentialAPITokenSetting("DigitalOcean")},
	},
	{
		typeSuffix:  "upcloud_credential",
		provider:    "upcloud",
		label:       "UpCloud",
		command:     "ankra credentials upcloud create",
		description: "An UpCloud API token credential, referenced by `ankra_upcloud_cluster.credential_id`.",
		strings:     []credentialStringSetting{credentialAPITokenSetting("UpCloud")},
	},
	{
		typeSuffix:  "ovh_credential",
		provider:    "ovh",
		label:       "OVHcloud",
		command:     "ankra credentials ovh create",
		description: "An OVHcloud API credential, referenced by `ankra_ovh_cluster.credential_id`.",
		strings: []credentialStringSetting{
			{name: "application_key", description: "OVH application key." + credentialRotatesNote, required: true, sensitive: true, rotatable: true},
			{name: "application_secret", description: "OVH application secret." + credentialRotatesNote, required: true, sensitive: true, rotatable: true},
			{name: "consumer_key", description: "OVH consumer key." + credentialRotatesNote, required: true, sensitive: true, rotatable: true},
			{name: "project_id", description: "OVH Public Cloud project id." + credentialReplacesNote + credentialDriftNote, required: true, listed: credentialListedProjectID},
		},
	},
	{
		typeSuffix:  "scaleway_credential",
		provider:    "scaleway",
		label:       "Scaleway",
		command:     "ankra credentials scaleway create",
		description: "A Scaleway API key credential, referenced by `ankra_scaleway_cluster.credential_id`. Scaleway is behind a rollout flag; an organisation without it gets a 404.",
		strings: []credentialStringSetting{
			{name: "access_key", description: "Scaleway API access key." + credentialRotatesNote, required: true, rotatable: true},
			{name: "secret_key", description: "Scaleway API secret key." + credentialRotatesNote, required: true, sensitive: true, rotatable: true},
			{name: "project_id", description: "Scaleway project id." + credentialReplacesNote + credentialDriftNote, required: true, listed: credentialListedProjectID},
		},
	},
	{
		typeSuffix:  "azure_credential",
		provider:    "azure",
		label:       "Azure",
		command:     "ankra credentials azure create",
		description: "An Azure service principal credential.",
		strings: []credentialStringSetting{
			{name: "subscription_id", description: "Azure subscription id the service principal manages." + credentialReplacesNote, required: true},
			{name: "tenant_id", description: "Microsoft Entra tenant id." + credentialReplacesNote, required: true},
			{name: "client_id", description: "Service principal application (client) id." + credentialRotatesNote, required: true, rotatable: true},
			{name: "client_secret", description: "Service principal client secret." + credentialRotatesNote, required: true, sensitive: true, rotatable: true},
		},
	},
	{
		typeSuffix:  "ankracloud_credential",
		provider:    "ankracloud",
		label:       "Ankra Cloud",
		command:     "ankra credentials ankracloud create",
		description: "An Ankra Cloud API token credential. Ankra Cloud is behind a rollout flag; an organisation without it gets a 404.",
		strings: []credentialStringSetting{
			credentialAPITokenSetting("Ankra Cloud"),
			{
				name: "endpoint",
				description: "Ankra Cloud API endpoint, https only. The platform uses `https://cloud.ankra.app` when it is unset, and stores the scheme and host only." +
					credentialReplacesNote + credentialDriftNote,
				validators: []validator.String{credentialHTTPSValidator{}},
				listed:     func(credential *client.Credential) *string { return credential.Endpoint },
				normalise:  credentialNormaliseAnkraCloudEndpoint,
			},
		},
	},
	{
		typeSuffix:  "proxmox_credential",
		provider:    "proxmox",
		label:       "Proxmox VE",
		command:     "ankra credentials proxmox create",
		description: "A Proxmox VE API token credential.",
		strings: []credentialStringSetting{
			{
				name: "api_url",
				description: "Proxmox VE API URL, such as `https://pve.example:8006`, without a user name or password." +
					credentialReplacesNote + credentialDriftNote,
				required:   true,
				listed:     credentialListedAPIURL,
				normalise:  credentialNormaliseAPIURL,
				validators: []validator.String{credentialAPIURLValidator{}},
			},
			{
				name: "internal_api_url",
				description: "API URL the cluster VMs use to reach Proxmox VE, when it differs from `api_url`, without a user name or password." +
					credentialReplacesNote,
				validators: []validator.String{credentialAPIURLValidator{}},
			},
			{name: "token_id", description: "API token id, such as `root@pam!ankra`." + credentialRotatesNote, required: true, rotatable: true},
			{name: "token_secret", description: "API token secret." + credentialRotatesNote, required: true, sensitive: true, rotatable: true},
		},
		bools:        []credentialBoolSetting{credentialListedTLSInsecureSetting},
		hasJumphost:  true,
		hasTailscale: true,
	},
	{
		typeSuffix:  "morpheus_credential",
		provider:    "morpheus",
		label:       "HPE Morpheus",
		command:     "ankra credentials morpheus create",
		description: "An HPE Morpheus API credential.",
		strings: []credentialStringSetting{
			{
				name:        "api_url",
				description: "HPE Morpheus API URL, without a user name or password." + credentialReplacesNote + credentialDriftNote,
				required:    true,
				listed:      credentialListedAPIURL,
				normalise:   credentialNormaliseAPIURL,
				validators:  []validator.String{credentialAPIURLValidator{}},
			},
			{name: "access_token", description: "HPE Morpheus access token." + credentialRotatesNote, required: true, sensitive: true, rotatable: true},
		},
		bools:       []credentialBoolSetting{credentialListedTLSInsecureSetting},
		hasJumphost: true,
	},
	{
		typeSuffix:  "aws_role_credential",
		provider:    client.CredentialAwsProvider,
		label:       "AWS",
		command:     "ankra credentials aws create-role",
		description: "An AWS credential the platform uses by assuming an IAM role. Create the role with the trust policy `ankra credentials aws onboarding` prints, using the same external id.",
		awsMethod:   "role",
		strings: []credentialStringSetting{
			{name: "role_arn", description: "ARN of the IAM role the platform assumes. Changing it rotates the credential in place.", required: true, rotatable: true},
			{name: "external_id", description: "External id the role's trust policy requires. Changing it rotates the credential in place.", required: true, rotatable: true},
			credentialAWSRegionSetting(),
			{
				name:         "scope",
				description:  "Scope the role was onboarded under: `cost`, `provisioning` or `self_managed`. Defaults to `cost`." + credentialReplacesNote + credentialDriftNote,
				defaultValue: "cost",
				listed:       func(credential *client.Credential) *string { return credential.Scope },
				validators:   []validator.String{credentialOneOfValidator{allowed: []string{"cost", "provisioning", "self_managed"}}},
			},
		},
	},
	{
		typeSuffix:  "aws_keys_credential",
		provider:    client.CredentialAwsProvider,
		label:       "AWS",
		command:     "ankra credentials aws create-keys",
		description: "An AWS credential made of an IAM access key pair.",
		awsMethod:   "keys",
		strings: []credentialStringSetting{
			{name: "access_key_id", description: "IAM access key id." + credentialRotatesNote, required: true, rotatable: true},
			{name: "secret_access_key", description: "IAM secret access key." + credentialRotatesNote, required: true, sensitive: true, rotatable: true},
			credentialAWSRegionSetting(),
		},
	},
}
