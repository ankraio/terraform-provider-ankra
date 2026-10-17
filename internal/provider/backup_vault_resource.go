// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"
	"strings"
	"time"

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
	_ resource.Resource                   = (*backupVaultResource)(nil)
	_ resource.ResourceWithConfigure      = (*backupVaultResource)(nil)
	_ resource.ResourceWithImportState    = (*backupVaultResource)(nil)
	_ resource.ResourceWithValidateConfig = (*backupVaultResource)(nil)
	_ resource.ResourceWithModifyPlan     = (*backupVaultResource)(nil)
)

// catalogueProvisioningPollInterval paces the wait for a vault or bucket the
// platform is creating. Tests shorten it.
var catalogueProvisioningPollInterval = 5 * time.Second

// catalogueProvisioningTimeout bounds that wait. Provisioning runs as a
// platform job, so a timeout leaves the object in state and a later plan
// picks up its outcome.
const catalogueProvisioningTimeout = 20 * time.Minute

// catalogueReplaceUnlessImported replaces the object when a create-only
// value the platform never returns (a key, or the input a snapshot was taken
// from) changes, except when state holds none: after an import the value is
// unknowable, and replacing a working object to learn it would be
// destructive.
func catalogueReplaceUnlessImported() planmodifier.String {
	return stringplanmodifier.RequiresReplaceIf(
		func(_ context.Context, request planmodifier.StringRequest, response *stringplanmodifier.RequiresReplaceIfFuncResponse) {
			response.RequiresReplace = !request.StateValue.IsNull()
		},
		"Changing this value replaces the object, except right after an import.",
		"Changing this value replaces the object, except right after an import.",
	)
}

// catalogueBoolReplaceUnlessImported is catalogueReplaceUnlessImported for
// booleans.
func catalogueBoolReplaceUnlessImported() planmodifier.Bool {
	return boolplanmodifier.RequiresReplaceIf(
		func(_ context.Context, request planmodifier.BoolRequest, response *boolplanmodifier.RequiresReplaceIfFuncResponse) {
			response.RequiresReplace = !request.StateValue.IsNull()
		},
		"Changing this value replaces the object, except right after an import.",
		"Changing this value replaces the object, except right after an import.",
	)
}

// catalogueWriteOnlyChanged reports whether a value the platform never
// returns changes, which replaces the object unless state never knew it.
func catalogueWriteOnlyChanged(planned, prior types.String) bool {
	return !prior.IsNull() && !planned.Equal(prior)
}

// catalogueAnyChanged reports whether any planned value differs from state,
// counting a value not known until apply as a change.
func catalogueAnyChanged(planned, prior []attr.Value) bool {
	for index := range planned {
		if planned[index].IsUnknown() || !planned[index].Equal(prior[index]) {
			return true
		}
	}
	return false
}

// catalogueWaitWhileProvisioning polls read until the object leaves the
// provisioning status. read keeps the caller's copy of the object current.
func catalogueWaitWhileProvisioning(ctx context.Context, status string, read func(context.Context) (string, error)) error {
	waitContext, cancel := context.WithTimeout(ctx, catalogueProvisioningTimeout)
	defer cancel()
	for status == client.BackupVaultStatusProvisioning {
		select {
		case <-waitContext.Done():
			return fmt.Errorf("still provisioning after %s", catalogueProvisioningTimeout)
		case <-time.After(catalogueProvisioningPollInterval):
		}
		next, err := read(waitContext)
		if err != nil {
			return err
		}
		status = next
	}
	return nil
}

type backupVaultResource struct {
	configuredClient
}

type backupVaultModel struct {
	ID                       types.String `tfsdk:"id"`
	Name                     types.String `tfsdk:"name"`
	CredentialID             types.String `tfsdk:"credential_id"`
	Provider                 types.String `tfsdk:"storage_provider"`
	Endpoint                 types.String `tfsdk:"endpoint"`
	Region                   types.String `tfsdk:"region"`
	Bucket                   types.String `tfsdk:"bucket"`
	PathStyle                types.Bool   `tfsdk:"path_style"`
	AccessKeyID              types.String `tfsdk:"access_key_id"`
	SecretAccessKey          types.String `tfsdk:"secret_access_key"`
	DestroyProviderResources types.Bool   `tfsdk:"destroy_provider_resources"`
	Kind                     types.String `tfsdk:"kind"`
	Status                   types.String `tfsdk:"status"`
	ErrorExcerpt             types.String `tfsdk:"error_excerpt"`
	CreatedAt                types.String `tfsdk:"created_at"`
}

// NewBackupVaultResource returns the ankra_backup_vault resource, the
// counterpart of `ankra backup vaults create|provision|delete`.
func NewBackupVaultResource() resource.Resource {
	return &backupVaultResource{}
}

func (vaultResource *backupVaultResource) Metadata(_ context.Context, request resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_backup_vault"
}

func (vaultResource *backupVaultResource) Schema(_ context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	replaceString := []planmodifier.String{stringplanmodifier.RequiresReplace()}
	computedReplaceString := []planmodifier.String{stringplanmodifier.UseStateForUnknown(), stringplanmodifier.RequiresReplace()}
	computedString := []planmodifier.String{stringplanmodifier.UseStateForUnknown()}
	response.Schema = schema.Schema{
		MarkdownDescription: "A backup vault: the S3-compatible bucket cluster backups, restore points and migration data move " +
			"through. Either registers a bucket the organisation already has (`endpoint`, `bucket` and a key pair: " +
			"`ankra backup vaults create`) or has Ankra create the bucket on a provider credential (`credential_id` and " +
			"`region`: `ankra backup vaults provision`, after which Terraform waits for the bucket to be ready).\n\n" +
			"**Replacing a vault is not harmless.** The platform has no update for a vault, so changing any setting (even " +
			"`name`) replaces it, and a replacement deletes the old vault the way destroy does:\n\n" +
			"- with `destroy_provider_resources = true`, the provisioned bucket is emptied and deleted, with every backup in " +
			"it. Terraform therefore refuses to plan a replacement while state holds `true`: set it to `false` and apply first.\n" +
			"- a bring-your-own vault loses the migration import dumps it uploaded into your bucket and the keys Ankra " +
			"stored, and stack backup policies keep pointing at the old vault ID. Terraform therefore refuses to change the " +
			"keys of a bring-your-own vault; the platform cannot rotate them in place.\n\n" +
			"The platform refuses to delete a vault that holds the state a stopped cluster needs to start again. Keys are " +
			"never returned, so a key changed outside Terraform is not detected. Vaults are scoped to the provider's " +
			"project. Requires the organisation's `backups` feature.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Identifier of the vault.",
				Computed:            true,
				PlanModifiers:       computedString,
			},
			"name": schema.StringAttribute{
				MarkdownDescription: "Vault name, a lower-case label.",
				Required:            true,
				PlanModifiers:       replaceString,
				Validators:          []validator.String{catalogueLabelNameValidator},
			},
			"credential_id": schema.StringAttribute{
				MarkdownDescription: "Provider credential Ankra creates the bucket with. Set it to provision the bucket; leave it out " +
					"to register an existing bucket.",
				Optional:      true,
				PlanModifiers: replaceString,
			},
			"storage_provider": schema.StringAttribute{
				MarkdownDescription: "Object-storage provider of an existing bucket: `upcloud`, `digitalocean`, `scaleway`, `ovh`, " +
					"`hetzner`, `aws`, `gcp`, `azure` or `other` (the default). For a provisioned vault it comes from the credential.",
				Optional:      true,
				Computed:      true,
				PlanModifiers: computedReplaceString,
				Validators:    []validator.String{catalogueBackupVaultProviderValidator},
			},
			"endpoint": schema.StringAttribute{
				MarkdownDescription: "S3 endpoint of an existing bucket, an https URL with no path. Computed for a provisioned vault.",
				Optional:            true,
				Computed:            true,
				PlanModifiers:       computedReplaceString,
				Validators:          []validator.String{catalogueEndpointValidator},
			},
			"region": schema.StringAttribute{
				MarkdownDescription: "Bucket region. Required for a provisioned vault.",
				Optional:            true,
				Computed:            true,
				PlanModifiers:       computedReplaceString,
			},
			"bucket": schema.StringAttribute{
				MarkdownDescription: "Bucket name. Required for an existing bucket; for a provisioned vault the platform derives a " +
					"unique name when it is left out.",
				Optional:      true,
				Computed:      true,
				PlanModifiers: computedReplaceString,
				Validators:    []validator.String{catalogueBucketNameValidator},
			},
			"path_style": schema.BoolAttribute{
				MarkdownDescription: "Whether the bucket is addressed path-style rather than virtual-host-style.",
				Optional:            true,
				Computed:            true,
				PlanModifiers:       []planmodifier.Bool{boolplanmodifier.UseStateForUnknown(), boolplanmodifier.RequiresReplace()},
			},
			"access_key_id": schema.StringAttribute{
				MarkdownDescription: "Access key for the bucket. Required for an existing bucket; for a provisioned vault only on " +
					"Hetzner, whose API cannot mint Object Storage keys. Never returned by the platform.",
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
				MarkdownDescription: "On destroy **or replacement**, also empty and delete the bucket Ankra provisioned, with " +
					"every backup in it. Defaults to `false`, which only forgets the vault and leaves the bucket on the provider. " +
					"Only valid with `credential_id`. While it is `true` in state, Terraform refuses to plan a replacement. A vault " +
					"destroyed while still provisioning is always torn down.",
				Optional: true,
				Computed: true,
				Default:  booldefault.StaticBool(false),
			},
			"kind": schema.StringAttribute{
				MarkdownDescription: "`customer_s3` for an existing bucket, `ankra_provisioned` for one Ankra created.",
				Computed:            true,
				PlanModifiers:       computedString,
			},
			"status": schema.StringAttribute{
				MarkdownDescription: "`provisioning`, `ready`, or `error` when provisioning or the last verification failed.",
				Computed:            true,
			},
			"error_excerpt": schema.StringAttribute{
				MarkdownDescription: "Why the vault is in `error`.",
				Computed:            true,
			},
			"created_at": schema.StringAttribute{
				MarkdownDescription: "When the vault was created.",
				Computed:            true,
				PlanModifiers:       computedString,
			},
		},
	}
}

func (vaultResource *backupVaultResource) ValidateConfig(ctx context.Context, request resource.ValidateConfigRequest, response *resource.ValidateConfigResponse) {
	var config backupVaultModel
	response.Diagnostics.Append(request.Config.Get(ctx, &config)...)
	if response.Diagnostics.HasError() {
		return
	}
	if config.CredentialID.IsUnknown() {
		return
	}
	isProvisioned := !config.CredentialID.IsNull()
	if isProvisioned {
		for attribute, value := range map[string]bool{
			"endpoint":         !config.Endpoint.IsNull(),
			"storage_provider": !config.Provider.IsNull(),
			"path_style":       !config.PathStyle.IsNull(),
		} {
			if value {
				response.Diagnostics.AddAttributeError(path.Root(attribute), "Not settable on a provisioned vault",
					attribute+" comes from the provider credential when Ankra provisions the bucket.")
			}
		}
		if config.Region.IsNull() {
			response.Diagnostics.AddAttributeError(path.Root("region"), "Missing region", "A provisioned vault needs a region.")
		}
		return
	}
	for attribute, isMissing := range map[string]bool{
		"endpoint":          config.Endpoint.IsNull(),
		"bucket":            config.Bucket.IsNull(),
		"access_key_id":     config.AccessKeyID.IsNull(),
		"secret_access_key": config.SecretAccessKey.IsNull(),
	} {
		if isMissing {
			response.Diagnostics.AddAttributeError(path.Root(attribute), "Missing "+attribute,
				"Registering an existing bucket needs endpoint, bucket, access_key_id and secret_access_key. Set credential_id instead to have Ankra provision one.")
		}
	}
	if config.DestroyProviderResources.ValueBool() {
		response.Diagnostics.AddAttributeError(path.Root("destroy_provider_resources"), "Bucket not provisioned by Ankra",
			"destroy_provider_resources only applies to a vault Ankra provisioned; the platform refuses it for an existing bucket.")
	}
}

// ModifyPlan refuses the replacements that would destroy data: any
// replacement while destroy_provider_resources is true, and a key change on a
// bring-your-own vault.
func (vaultResource *backupVaultResource) ModifyPlan(ctx context.Context, request resource.ModifyPlanRequest, response *resource.ModifyPlanResponse) {
	if request.State.Raw.IsNull() || request.Plan.Raw.IsNull() {
		return
	}
	var plan, state backupVaultModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	isKeyChanged := catalogueWriteOnlyChanged(plan.AccessKeyID, state.AccessKeyID) ||
		catalogueWriteOnlyChanged(plan.SecretAccessKey, state.SecretAccessKey)
	if isKeyChanged && state.CredentialID.IsNull() {
		response.Diagnostics.AddAttributeError(path.Root("access_key_id"), "Keys of a bring-your-own vault cannot change in place",
			"The platform has no way to update a vault's keys, so Terraform would replace the vault. Replacing a "+
				"bring-your-own vault deletes the migration import dumps it uploaded into your bucket, drops the keys Ankra "+
				"stored, and leaves stack backup policies pointing at the old vault ID. Keep the old keys valid and in "+
				"configuration, or destroy the vault deliberately and create a new one.")
		return
	}
	isReplaced := isKeyChanged || catalogueAnyChanged(
		[]attr.Value{plan.Name, plan.CredentialID, plan.Provider, plan.Endpoint, plan.Region, plan.Bucket, plan.PathStyle},
		[]attr.Value{state.Name, state.CredentialID, state.Provider, state.Endpoint, state.Region, state.Bucket, state.PathStyle})
	if isReplaced && state.DestroyProviderResources.ValueBool() {
		response.Diagnostics.AddAttributeError(path.Root("destroy_provider_resources"), "Replacement would delete the bucket",
			"This change replaces the vault, and destroy_provider_resources is true in state, so the replacement would empty "+
				"and delete the provisioned bucket with every backup in it. Set destroy_provider_resources = false and apply "+
				"first, then make this change.")
	}
}

func (vaultResource *backupVaultResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	apiClient := vaultResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var plan backupVaultModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}

	var vault *client.BackupVault
	var err error
	if !plan.CredentialID.IsNull() {
		vault, err = apiClient.ProvisionBackupVault(ctx, client.BackupVaultProvision{
			Name:            plan.Name.ValueString(),
			CredentialID:    plan.CredentialID.ValueString(),
			Region:          plan.Region.ValueString(),
			Bucket:          stringPointer(plan.Bucket),
			AccessKeyID:     stringPointer(plan.AccessKeyID),
			SecretAccessKey: stringPointer(plan.SecretAccessKey),
		})
	} else {
		vault, err = apiClient.CreateBackupVault(ctx, client.BackupVaultCreate{
			Name:            plan.Name.ValueString(),
			Provider:        stringPointer(plan.Provider),
			Endpoint:        plan.Endpoint.ValueString(),
			Region:          stringPointer(plan.Region),
			Bucket:          plan.Bucket.ValueString(),
			PathStyle:       boolPointer(plan.PathStyle),
			AccessKeyID:     plan.AccessKeyID.ValueString(),
			SecretAccessKey: plan.SecretAccessKey.ValueString(),
		})
	}
	if err != nil {
		response.Diagnostics.AddError("Unable to create backup vault", err.Error())
		return
	}
	backupVaultToState(vault, &plan)
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
	response.Diagnostics.Append(trackScope(ctx, apiClient, response.Private)...)
	if response.Diagnostics.HasError() {
		return
	}

	waitError := catalogueWaitWhileProvisioning(ctx, vault.Status, func(waitContext context.Context) (string, error) {
		current, readError := apiClient.GetBackupVault(waitContext, vault.ID)
		if readError != nil {
			return "", readError
		}
		vault = current
		return current.Status, nil
	})
	backupVaultToState(vault, &plan)
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
	response.Diagnostics.Append(trackScope(ctx, apiClient, response.Private)...)
	if waitError != nil {
		response.Diagnostics.AddError("Backup vault did not finish provisioning", waitError.Error())
		return
	}
	if vault.Status == client.BackupVaultStatusError {
		response.Diagnostics.AddError("Backup vault is in error",
			fmt.Sprintf("The platform could not reach bucket %q: %s", vault.Bucket, plan.ErrorExcerpt.ValueString()))
	}
}

func (vaultResource *backupVaultResource) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	apiClient := vaultResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state backupVaultModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}

	vault, err := apiClient.GetBackupVault(ctx, state.ID.ValueString())
	if removeWhenNotFound(ctx, err, apiClient, response) {
		return
	}
	if err != nil {
		response.Diagnostics.AddError("Unable to read backup vault", err.Error())
		return
	}
	backupVaultToState(vault, &state)
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
	response.Diagnostics.Append(trackScope(ctx, apiClient, response.Private)...)
}

// Update only ever changes destroy_provider_resources, which lives in state:
// every other configurable attribute replaces the vault.
func (vaultResource *backupVaultResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	var plan, state backupVaultModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	plan.Status = state.Status
	plan.ErrorExcerpt = state.ErrorExcerpt
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
}

func (vaultResource *backupVaultResource) Delete(ctx context.Context, request resource.DeleteRequest, response *resource.DeleteResponse) {
	apiClient := vaultResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state backupVaultModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	err := apiClient.DeleteBackupVault(ctx, state.ID.ValueString(), state.DestroyProviderResources.ValueBool())
	if err == nil || deletedWhenNotFound(ctx, err, apiClient, request.Private, &response.Diagnostics) || response.Diagnostics.HasError() {
		return
	}
	response.Diagnostics.AddError("Unable to delete backup vault", err.Error())
}

// ImportState imports by vault ID. Keys are never returned: after an import
// they are taken from configuration without replacing the vault.
func (vaultResource *backupVaultResource) ImportState(ctx context.Context, request resource.ImportStateRequest, response *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), request, response)
}

// backupVaultToState copies what the platform reports. Keys are never
// reported and stay as configured. A region the platform normalised only in
// case keeps its configured spelling.
func backupVaultToState(vault *client.BackupVault, state *backupVaultModel) {
	state.ID = types.StringValue(vault.ID)
	state.Name = types.StringValue(vault.Name)
	state.Kind = types.StringValue(vault.Kind)
	state.Provider = types.StringValue(vault.Provider)
	state.Endpoint = types.StringValue(vault.Endpoint)
	if !strings.EqualFold(state.Region.ValueString(), vault.Region) || state.Region.IsUnknown() {
		state.Region = optionalString(vault.Region)
	}
	state.Bucket = types.StringValue(vault.Bucket)
	state.PathStyle = types.BoolValue(vault.PathStyle)
	state.Status = types.StringValue(vault.Status)
	state.ErrorExcerpt = types.StringPointerValue(vault.ErrorExcerpt)
	state.CreatedAt = types.StringValue(vault.CreatedAt)
	if vault.ProvisionedViaCredentialID != nil {
		state.CredentialID = types.StringValue(*vault.ProvisionedViaCredentialID)
	} else if !state.CredentialID.IsUnknown() {
		state.CredentialID = types.StringNull()
	}
	if state.DestroyProviderResources.IsNull() || state.DestroyProviderResources.IsUnknown() {
		state.DestroyProviderResources = types.BoolValue(false)
	}
}
