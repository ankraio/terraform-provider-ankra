// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"errors"
	"fmt"

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
// created with. The platform never returns it.
type credentialStringSetting struct {
	name        string
	description string
	required    bool
	sensitive   bool
	// defaultValue, when set, is the value the platform applies when the
	// setting is left out.
	defaultValue string
	// listed reads the setting back from the credential listing, for the few
	// settings the listing does carry.
	listed func(credential *client.Credential) *string
	// validators reject at plan time values the platform refuses.
	validators []validator.String
}

// credentialBoolSetting is a boolean setting the platform defaults to false.
type credentialBoolSetting struct {
	name        string
	description string
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
			PlanModifiers:       []planmodifier.String{credentialReplaceString()},
			Validators:          setting.validators,
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
			"The platform never returns a credential's secrets or connection settings, so a change made to them outside Terraform is not detected; " +
			"the name, and the credential's existence, are. The token API cannot change a credential in place, so changing a setting replaces it." + credentialOperationalNotes,
		Attributes: attributes,
	}
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

// setListed writes the listing's view, including the settings the listing
// carries, into state.
func (credentialResource *providerCredentialResource) setListed(ctx context.Context, state *tfsdk.State, credential *client.Credential) diag.Diagnostics {
	diagnostics := credentialSetListed(ctx, state, credential)
	for _, setting := range credentialResource.kind.strings {
		if setting.listed == nil {
			continue
		}
		if value := setting.listed(credential); value != nil {
			diagnostics.Append(state.SetAttribute(ctx, path.Root(setting.name), types.StringValue(*value))...)
		}
	}
	return diagnostics
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
			response.Diagnostics.AddError("Unable to create "+kind.label+" credential", err.Error())
			return
		}
		created, err = apiClient.GetCredential(ctx, kind.provider, awsCredential.ID)
		if err != nil {
			response.State.Raw = request.Plan.Raw
			for _, attributeName := range []string{"display_name", "state", "created_at"} {
				response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root(attributeName), types.StringNull())...)
			}
			response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("available"), types.BoolNull())...)
			response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("id"), types.StringValue(awsCredential.ID))...)
			response.Diagnostics.AddError("Unable to read back "+kind.label+" credential",
				fmt.Sprintf("The credential %s was created and is recorded in state, but could not be read back: %s. "+
					"The token needs credentials.read as well as credentials.write. Terraform marks the resource tainted and replaces it on the next apply; "+
					"run `terraform untaint` on it to keep this credential instead.", awsCredential.ID, err))
			return
		}
	} else {
		var err error
		created, err = apiClient.CreateProviderCredential(ctx, kind.provider, body)
		var readBackError *client.CredentialReadBackError
		if errors.As(err, &readBackError) {
			response.Diagnostics.AddError("Unable to read back "+kind.label+" credential",
				fmt.Sprintf("The platform created the %s credential %q but it could not be read back: %s. "+
					"The token needs credentials.read as well as credentials.write. The credential exists, so applying again fails on its name: "+
					"find its id with `ankra credentials list --provider %s`, then run `terraform import ankra_%s.<name> <id>`.",
					kind.label, readBackError.Name, readBackError.Err, kind.provider, kind.typeSuffix))
			return
		}
		if err != nil {
			response.Diagnostics.AddError("Unable to create "+kind.label+" credential", err.Error())
			return
		}
	}

	response.State.Raw = request.Plan.Raw
	response.Diagnostics.Append(credentialResource.setListed(ctx, &response.State, created)...)
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
	if removeWhenNotFound(ctx, err, response) {
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
	response.Diagnostics.Append(credentialResource.setListed(ctx, &response.State, credential)...)
}

// Update runs only for the Tailscale join, which the platform changes in
// place, or to adopt the configured settings of a just-imported credential:
// every other change is planned as a replacement.
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
				response.Diagnostics.AddError("Unable to update "+kind.label+" credential Tailscale settings", err.Error())
				return
			}
		}
	}

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
	if err := apiClient.DeleteCredential(ctx, credentialID.ValueString()); err != nil {
		response.Diagnostics.AddError("Unable to delete "+credentialResource.kind.label+" credential", err.Error())
	}
}

// ImportState imports by credential id. The platform never returns the
// credential's secrets or settings, so the first apply after an import
// adopts the configured values without replacing the credential.
func (credentialResource *providerCredentialResource) ImportState(ctx context.Context, request resource.ImportStateRequest, response *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), request, response)
	response.Diagnostics.Append(response.Private.SetKey(ctx, credentialImportedKey, []byte("true"))...)
}

func credentialAPITokenSetting(label string) credentialStringSetting {
	return credentialStringSetting{
		name:        "api_token",
		description: label + " API token. Changing it replaces the credential.",
		required:    true,
		sensitive:   true,
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
			{name: "application_key", description: "OVH application key. Changing it replaces the credential.", required: true, sensitive: true},
			{name: "application_secret", description: "OVH application secret. Changing it replaces the credential.", required: true, sensitive: true},
			{name: "consumer_key", description: "OVH consumer key. Changing it replaces the credential.", required: true, sensitive: true},
			{name: "project_id", description: "OVH Public Cloud project id. Changing it replaces the credential.", required: true},
		},
	},
	{
		typeSuffix:  "scaleway_credential",
		provider:    "scaleway",
		label:       "Scaleway",
		command:     "ankra credentials scaleway create",
		description: "A Scaleway API key credential, referenced by `ankra_scaleway_cluster.credential_id`. Scaleway is behind a rollout flag; an organisation without it gets a 404.",
		strings: []credentialStringSetting{
			{name: "access_key", description: "Scaleway API access key. Changing it replaces the credential.", required: true},
			{name: "secret_key", description: "Scaleway API secret key. Changing it replaces the credential.", required: true, sensitive: true},
			{name: "project_id", description: "Scaleway project id. Changing it replaces the credential.", required: true},
		},
	},
	{
		typeSuffix:  "azure_credential",
		provider:    "azure",
		label:       "Azure",
		command:     "ankra credentials azure create",
		description: "An Azure service principal credential.",
		strings: []credentialStringSetting{
			{name: "subscription_id", description: "Azure subscription id the service principal manages. Changing it replaces the credential.", required: true},
			{name: "tenant_id", description: "Microsoft Entra tenant id. Changing it replaces the credential.", required: true},
			{name: "client_id", description: "Service principal application (client) id. Changing it replaces the credential.", required: true},
			{name: "client_secret", description: "Service principal client secret. Changing it replaces the credential.", required: true, sensitive: true},
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
			{name: "endpoint", description: "Ankra Cloud API endpoint, https only. The platform uses `https://cloud.ankra.app` when it is unset. Changing it replaces the credential.", validators: []validator.String{credentialHTTPSValidator{}}},
		},
	},
	{
		typeSuffix:  "proxmox_credential",
		provider:    "proxmox",
		label:       "Proxmox VE",
		command:     "ankra credentials proxmox create",
		description: "A Proxmox VE API token credential.",
		strings: []credentialStringSetting{
			{name: "api_url", description: "Proxmox VE API URL, such as `https://pve.example:8006`. Changing it replaces the credential.", required: true},
			{name: "internal_api_url", description: "API URL the cluster VMs use to reach Proxmox VE, when it differs from `api_url`. Changing it replaces the credential."},
			{name: "token_id", description: "API token id, such as `root@pam!ankra`. Changing it replaces the credential.", required: true},
			{name: "token_secret", description: "API token secret. Changing it replaces the credential.", required: true, sensitive: true},
		},
		bools:        []credentialBoolSetting{{name: "tls_insecure", description: "Skip TLS certificate verification for the API URL. Changing it replaces the credential."}},
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
			{name: "api_url", description: "HPE Morpheus API URL. Changing it replaces the credential.", required: true},
			{name: "access_token", description: "HPE Morpheus access token. Changing it replaces the credential.", required: true, sensitive: true},
		},
		bools:       []credentialBoolSetting{{name: "tls_insecure", description: "Skip TLS certificate verification for the API URL. Changing it replaces the credential."}},
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
			{name: "role_arn", description: "ARN of the IAM role the platform assumes. Changing it replaces the credential.", required: true},
			{name: "external_id", description: "External id the role's trust policy requires. Changing it replaces the credential.", required: true},
			{name: "region", description: "Default AWS region. Defaults to `us-east-1`. Changing it replaces the credential.", defaultValue: "us-east-1"},
			{
				name:         "scope",
				description:  "Scope the role was onboarded under: `cost`, `provisioning` or `self_managed`. Defaults to `cost`. Changing it replaces the credential.",
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
			{name: "access_key_id", description: "IAM access key id. Changing it replaces the credential.", required: true},
			{name: "secret_access_key", description: "IAM secret access key. Changing it replaces the credential.", required: true, sensitive: true},
			{name: "region", description: "Default AWS region. Defaults to `us-east-1`. Changing it replaces the credential.", defaultValue: "us-east-1"},
		},
	},
}
