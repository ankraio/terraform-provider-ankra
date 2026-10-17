// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"ankra.io/terraform-provider-ankra/internal/client"
)

var (
	_ resource.Resource                = (*stackBackupProtectionResource)(nil)
	_ resource.ResourceWithConfigure   = (*stackBackupProtectionResource)(nil)
	_ resource.ResourceWithImportState = (*stackBackupProtectionResource)(nil)
)

// stackBackupNoPolicy is the unprotected reason the posture answers for a
// stack that carries no backup policy.
const stackBackupNoPolicy = "no_policy"

// stackBackupDefaultSchedule is the schedule the platform reports for a
// policy written without one.
const stackBackupDefaultSchedule = "daily"

type stackBackupProtectionResource struct {
	configuredClient
}

type stackBackupProtectionModel struct {
	ID              types.String               `tfsdk:"id"`
	ClusterID       types.String               `tfsdk:"cluster_id"`
	StackName       types.String               `tfsdk:"stack_name"`
	Vault           types.String               `tfsdk:"vault"`
	VaultName       types.String               `tfsdk:"vault_name"`
	Schedule        types.String               `tfsdk:"schedule"`
	Retention       *stackBackupRetentionModel `tfsdk:"retention"`
	Selection       *stackBackupSelectionModel `tfsdk:"selection"`
	BackupNow       types.Bool                 `tfsdk:"backup_now"`
	ProtectionState types.String               `tfsdk:"protection_state"`
}

type stackBackupRetentionModel struct {
	Hourly       types.Int64  `tfsdk:"hourly"`
	Daily        types.Int64  `tfsdk:"daily"`
	Weekly       types.Int64  `tfsdk:"weekly"`
	Monthly      types.Int64  `tfsdk:"monthly"`
	Yearly       types.Int64  `tfsdk:"yearly"`
	MinimumCount types.Int64  `tfsdk:"minimum_count"`
	MinimumAge   types.String `tfsdk:"minimum_age"`
}

type stackBackupSelectionModel struct {
	Databases               types.Bool `tfsdk:"databases"`
	PersistentVolumeClaims  types.List `tfsdk:"persistent_volume_claims"`
	ConfirmExcludeDatabases types.Bool `tfsdk:"confirm_exclude_databases"`
}

// NewStackBackupProtectionResource returns the
// ankra_stack_backup_protection resource, the counterpart of `ankra cluster
// stacks protect` and `unprotect`.
func NewStackBackupProtectionResource() resource.Resource {
	return &stackBackupProtectionResource{}
}

func (protectionResource *stackBackupProtectionResource) Metadata(_ context.Context, request resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_stack_backup_protection"
}

func (protectionResource *stackBackupProtectionResource) Schema(_ context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	retentionTier := func(description string) schema.Int64Attribute {
		return schema.Int64Attribute{MarkdownDescription: description + " 0 or unset disables the tier.", Optional: true}
	}
	response.Schema = schema.Schema{
		MarkdownDescription: "Protects a stack into a backup vault on a schedule: the backup policy on the stack, the counterpart of " +
			"`ankra cluster stacks protect`. Every change rewrites the whole policy. Destroying the resource unprotects the stack " +
			"(`ankra cluster stacks unprotect`); restore points already taken are kept. The stack itself stays owned by `ankra_cluster` " +
			"or the GitOps repository, which leave a stack's policy alone when they do not name one. The platform reports the vault and " +
			"schedule back, so a change to either outside Terraform plans a correction; `retention` and `selection` are not reported back.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "`<cluster_id>/<stack_name>`.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"cluster_id": clusterSettingsClusterIDAttribute(),
			"stack_name": schema.StringAttribute{
				MarkdownDescription: "Name of the stack to protect. Changing it replaces the resource.",
				Required:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
				Validators:          []validator.String{clusterSettingsNoSlashValidator{}},
			},
			"vault": schema.StringAttribute{
				MarkdownDescription: "Backup vault the restore points are written to, by name or id (for example `ankra_backup_vault.this.id`).",
				Required:            true,
			},
			"vault_name": schema.StringAttribute{
				MarkdownDescription: "Name of the vault the policy names.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stackBackupVaultNameUnlessVaultChanges{}},
			},
			"schedule": schema.StringAttribute{
				MarkdownDescription: "`hourly`, `daily`, `weekly` or a five-field cron expression. Unset, the platform default (daily) applies.",
				Optional:            true,
			},
			"retention": schema.SingleNestedAttribute{
				MarkdownDescription: "How many restore points each tier keeps. Unset keeps every restore point.",
				Optional:            true,
				Attributes: map[string]schema.Attribute{
					"hourly":        retentionTier("Hourly restore points kept."),
					"daily":         retentionTier("Daily restore points kept."),
					"weekly":        retentionTier("Weekly restore points kept."),
					"monthly":       retentionTier("Monthly restore points kept."),
					"yearly":        retentionTier("Yearly restore points kept."),
					"minimum_count": retentionTier("Restore points always kept, whatever their age."),
					"minimum_age": schema.StringAttribute{
						MarkdownDescription: "Age below which no restore point is pruned, for example `72h`.",
						Optional:            true,
					},
				},
			},
			"selection": schema.SingleNestedAttribute{
				MarkdownDescription: "Which of the stack's data the restore points carry. Unset, databases are captured and no volume is.",
				Optional:            true,
				Attributes: map[string]schema.Attribute{
					"databases": schema.BoolAttribute{
						MarkdownDescription: "Whether databases are captured. Defaults to `true`; `false` needs `confirm_exclude_databases`.",
						Optional:            true,
					},
					"persistent_volume_claims": schema.ListAttribute{
						MarkdownDescription: "Volumes to capture, as `namespace/name`.",
						Optional:            true,
						ElementType:         types.StringType,
					},
					"confirm_exclude_databases": schema.BoolAttribute{
						MarkdownDescription: "Acknowledges that restore points carry no database contents when `databases` is `false`.",
						Optional:            true,
					},
				},
			},
			"backup_now": schema.BoolAttribute{
				MarkdownDescription: "Take the first restore point when the resource is created instead of waiting for the schedule. " +
					"Only read on create: changing it later sends nothing and takes no restore point.",
				Optional: true,
			},
			"protection_state": schema.StringAttribute{
				MarkdownDescription: "The stack's backup posture: `protected`, `unprotected` (for example while the vault is not ready) or `unknown`.",
				Computed:            true,
			},
		},
	}
}

func (protectionResource *stackBackupProtectionResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	var plan stackBackupProtectionModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}
	apiClient := protectionResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	if !protectionResource.protect(ctx, apiClient, &plan, plan.BackupNow.ValueBool(), &response.Diagnostics) {
		return
	}
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
	response.Diagnostics.Append(trackScope(ctx, apiClient, response.Private)...)
}

func (protectionResource *stackBackupProtectionResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	var plan stackBackupProtectionModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}
	apiClient := protectionResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	if !protectionResource.protect(ctx, apiClient, &plan, false, &response.Diagnostics) {
		return
	}
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
}

// protect writes the planned policy onto the stack, then reads the posture
// so the state carries what the platform reports.
func (protectionResource *stackBackupProtectionResource) protect(ctx context.Context, apiClient *client.Client, plan *stackBackupProtectionModel, backupNow bool, diagnostics *diag.Diagnostics) bool {
	clusterID, stackName := plan.ClusterID.ValueString(), plan.StackName.ValueString()
	body := client.StackProtect{
		VaultID:   plan.Vault.ValueString(),
		Schedule:  plan.Schedule.ValueString(),
		Retention: stackBackupRetentionToAPI(plan.Retention),
		Selection: stackBackupSelectionToAPI(ctx, plan.Selection, diagnostics),
		BackupNow: backupNow,
	}
	if diagnostics.HasError() {
		return false
	}
	protection, err := apiClient.ProtectStack(ctx, clusterID, stackName, body)
	if err != nil {
		diagnostics.AddError("Unable to protect stack", clusterSettingsErrorText(err, clusterID))
		return false
	}
	for _, warning := range protection.Warnings {
		diagnostics.AddWarning("Stack protected with a warning", warning)
	}
	plan.ID = types.StringValue(clusterID + "/" + stackName)
	plan.VaultName = types.StringValue(protection.Policy.Vault)
	posture, err := apiClient.GetStackBackupPosture(ctx, clusterID, stackName)
	if err != nil {
		diagnostics.AddError("Unable to read the stack's backup posture", clusterSettingsErrorText(err, clusterID))
		return false
	}
	plan.ProtectionState = types.StringValue(posture.ProtectionState)
	return true
}

func (protectionResource *stackBackupProtectionResource) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	apiClient := protectionResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state stackBackupProtectionModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	clusterID, stackName := state.ClusterID.ValueString(), state.StackName.ValueString()
	posture, err := apiClient.GetStackBackupPosture(ctx, clusterID, stackName)
	if clusterSettingsReadNotFound(ctx, err, apiClient, clusterID, response) {
		return
	}
	if err != nil {
		response.Diagnostics.AddError("Unable to read the stack's backup posture", err.Error())
		return
	}
	if posture.UnprotectedReason == stackBackupNoPolicy {
		// The stack no longer carries a policy: protect it again.
		response.State.RemoveResource(ctx)
		return
	}
	if posture.VaultName == "" {
		// The stack carries a policy the platform cannot read right now
		// (protection_state unknown). Keep what state holds rather than
		// overwrite that policy on the next apply.
		if state.Vault.IsNull() {
			response.Diagnostics.AddError("Backup policy not readable",
				"The platform reports the stack's backup protection as "+posture.ProtectionState+" and cannot read its policy, so it cannot be imported now. Try again later.")
			return
		}
		state.ID = types.StringValue(clusterID + "/" + stackName)
		state.ProtectionState = types.StringValue(posture.ProtectionState)
		response.Diagnostics.Append(response.State.Set(ctx, &state)...)
		return
	}
	applyStackBackupPosture(&state, posture)
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
	response.Diagnostics.Append(trackScope(ctx, apiClient, response.Private)...)
}

// Delete unprotects the stack. A stack that is already gone counts as done,
// unless the provider's scope moved since the policy was written.
func (protectionResource *stackBackupProtectionResource) Delete(ctx context.Context, request resource.DeleteRequest, response *resource.DeleteResponse) {
	apiClient := protectionResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state stackBackupProtectionModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	clusterID := state.ClusterID.ValueString()
	err := apiClient.UnprotectStack(ctx, clusterID, state.StackName.ValueString())
	if err == nil {
		return
	}
	if scopeError := clusterSettingsScopeError(err, clusterID); scopeError != nil {
		response.Diagnostics.AddError("Unable to unprotect stack", scopeError.Error())
		return
	}
	if deletedWhenNotFound(ctx, err, apiClient, request.Private, &response.Diagnostics) || response.Diagnostics.HasError() {
		return
	}
	response.Diagnostics.AddError("Unable to unprotect stack", err.Error())
}

// ImportState imports by "<cluster_id>/<stack_name>"; the vault is read
// back by name.
func (protectionResource *stackBackupProtectionResource) ImportState(ctx context.Context, request resource.ImportStateRequest, response *resource.ImportStateResponse) {
	clusterSettingsImportComposite(ctx, request, response, "stack_name")
	if response.Diagnostics.HasError() {
		return
	}
	response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("id"), request.ID)...)
}

// applyStackBackupPosture copies what the posture reports into the model.
// The configured vault is kept while it still names the vault the policy
// names (it may be an id); a different vault replaces it with that vault's
// name, so the next plan corrects it.
func applyStackBackupPosture(model *stackBackupProtectionModel, posture *client.StackBackupPosture) {
	model.ID = types.StringValue(model.ClusterID.ValueString() + "/" + model.StackName.ValueString())
	if model.VaultName.ValueString() != posture.VaultName || model.Vault.IsNull() || model.Vault.ValueString() == "" {
		model.Vault = types.StringValue(posture.VaultName)
	}
	model.VaultName = types.StringValue(posture.VaultName)
	reported := strings.TrimSpace(posture.Schedule)
	unsetReadsAsDefault := model.Schedule.IsNull() && reported == stackBackupDefaultSchedule
	if reported != model.Schedule.ValueString() && !unsetReadsAsDefault {
		model.Schedule = optionalString(reported)
	}
	model.ProtectionState = types.StringValue(posture.ProtectionState)
}

// stackBackupVaultNameUnlessVaultChanges keeps vault_name from state while
// the planned vault is the one in state, and leaves it unknown when the
// vault changes, since only the platform resolves an id to a name.
type stackBackupVaultNameUnlessVaultChanges struct{}

func (stackBackupVaultNameUnlessVaultChanges) Description(_ context.Context) string {
	return "keeps the vault name while the vault does not change"
}

func (modifier stackBackupVaultNameUnlessVaultChanges) MarkdownDescription(ctx context.Context) string {
	return modifier.Description(ctx)
}

func (stackBackupVaultNameUnlessVaultChanges) PlanModifyString(ctx context.Context, request planmodifier.StringRequest, response *planmodifier.StringResponse) {
	if request.StateValue.IsNull() || !request.PlanValue.IsUnknown() {
		return
	}
	var plannedVault, storedVault types.String
	response.Diagnostics.Append(request.Plan.GetAttribute(ctx, path.Root("vault"), &plannedVault)...)
	response.Diagnostics.Append(request.State.GetAttribute(ctx, path.Root("vault"), &storedVault)...)
	if response.Diagnostics.HasError() || plannedVault.IsUnknown() || !plannedVault.Equal(storedVault) {
		return
	}
	response.PlanValue = request.StateValue
}

func stackBackupRetentionToAPI(retention *stackBackupRetentionModel) *client.StackBackupRetention {
	if retention == nil {
		return nil
	}
	return &client.StackBackupRetention{
		Hourly:       retention.Hourly.ValueInt64(),
		Daily:        retention.Daily.ValueInt64(),
		Weekly:       retention.Weekly.ValueInt64(),
		Monthly:      retention.Monthly.ValueInt64(),
		Yearly:       retention.Yearly.ValueInt64(),
		MinimumCount: retention.MinimumCount.ValueInt64(),
		MinimumAge:   retention.MinimumAge.ValueString(),
	}
}

func stackBackupSelectionToAPI(ctx context.Context, selection *stackBackupSelectionModel, diagnostics *diag.Diagnostics) *client.StackBackupSelection {
	if selection == nil {
		return nil
	}
	return &client.StackBackupSelection{
		Databases:               boolPointer(selection.Databases),
		PersistentVolumeClaims:  stringsFromList(ctx, selection.PersistentVolumeClaims, diagnostics),
		ConfirmExcludeDatabases: selection.ConfirmExcludeDatabases.ValueBool(),
	}
}
