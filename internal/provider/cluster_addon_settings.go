// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"ankra.io/terraform-provider-ankra/internal/client"
)

// addonSettingsModel is the settings block of an ankra_cluster addon.
type addonSettingsModel struct {
	RevisionHistoryLimit        types.Int64                  `tfsdk:"revision_history_limit"`
	StrictHealthCheck           types.Bool                   `tfsdk:"strict_health_check"`
	RollbackOnFailure           types.Bool                   `tfsdk:"rollback_on_failure"`
	HelmWaitTimeoutSeconds      types.Int64                  `tfsdk:"helm_wait_timeout_seconds"`
	KeepFailedResourcesForDebug types.Bool                   `tfsdk:"keep_failed_resources_for_debug"`
	SyncPolicy                  *addonSyncPolicyModel        `tfsdk:"sync_policy"`
	SyncWindow                  *addonSyncWindowModel        `tfsdk:"sync_window"`
	RetryPolicy                 *addonRetryPolicyModel       `tfsdk:"retry_policy"`
	IgnoreDifferences           []addonIgnoreDifferenceModel `tfsdk:"ignore_differences"`
}

type addonSyncPolicyModel struct {
	Automated   types.Bool `tfsdk:"automated"`
	AutoPrune   types.Bool `tfsdk:"auto_prune"`
	SelfHeal    types.Bool `tfsdk:"self_heal"`
	SyncOptions types.List `tfsdk:"sync_options"`
}

type addonSyncWindowModel struct {
	Enabled  types.Bool   `tfsdk:"enabled"`
	Schedule types.String `tfsdk:"schedule"`
	Duration types.String `tfsdk:"duration"`
	Timezone types.String `tfsdk:"timezone"`
}

type addonRetryPolicyModel struct {
	Limit              types.Int64  `tfsdk:"limit"`
	BackoffDuration    types.String `tfsdk:"backoff_duration"`
	BackoffFactor      types.Int64  `tfsdk:"backoff_factor"`
	BackoffMaxDuration types.String `tfsdk:"backoff_max_duration"`
}

type addonIgnoreDifferenceModel struct {
	Group                 types.String `tfsdk:"group"`
	Kind                  types.String `tfsdk:"kind"`
	Name                  types.String `tfsdk:"name"`
	Namespace             types.String `tfsdk:"namespace"`
	JSONPointers          types.List   `tfsdk:"json_pointers"`
	JQPathExpressions     types.List   `tfsdk:"jq_path_expressions"`
	ManagedFieldsManagers types.List   `tfsdk:"managed_fields_managers"`
}

// addonSettingsBlock is the schema of the settings block, the counterpart
// of `ankra cluster addons settings set`.
func addonSettingsBlock() schema.SingleNestedBlock {
	optionalBool := func(description string) schema.BoolAttribute {
		return schema.BoolAttribute{MarkdownDescription: description, Optional: true}
	}
	optionalInt := func(description string) schema.Int64Attribute {
		return schema.Int64Attribute{MarkdownDescription: description, Optional: true}
	}
	optionalText := func(description string) schema.StringAttribute {
		return schema.StringAttribute{MarkdownDescription: description, Optional: true}
	}
	optionalStrings := func(description string) schema.ListAttribute {
		return schema.ListAttribute{MarkdownDescription: description, Optional: true, ElementType: types.StringType}
	}
	return schema.SingleNestedBlock{
		MarkdownDescription: "How the platform syncs the addon, the counterpart of `ankra cluster addons settings set`. " +
			"Without the block the addon keeps the settings it has (set in the portal or the CLI). With it, the settings are " +
			"written on every apply and each one the block leaves out takes the platform default, so an empty `settings {}` resets " +
			"every setting to its default. Like the rest of `stacks`, the provider does not read the settings back, so a change made " +
			"outside Terraform is not detected until the next apply that changes the stack rewrites it.",
		Attributes: map[string]schema.Attribute{
			"revision_history_limit":          optionalInt("How many release revisions are kept. Default 10."),
			"strict_health_check":             optionalBool("Whether the addon counts as healthy only when every resource is. Default `false`."),
			"rollback_on_failure":             optionalBool("Whether a failed upgrade rolls back. Default `true`."),
			"helm_wait_timeout_seconds":       optionalInt("How long Helm waits for the release to become ready, in seconds."),
			"keep_failed_resources_for_debug": optionalBool("Whether resources of a failed install are kept for debugging. Default `false`."),
		},
		Blocks: map[string]schema.Block{
			"sync_policy": schema.SingleNestedBlock{
				MarkdownDescription: "Automated sync behaviour.",
				Attributes: map[string]schema.Attribute{
					"automated":    optionalBool("Whether the addon syncs automatically. Default `true`."),
					"auto_prune":   optionalBool("Whether resources removed from the chart are deleted. Default `true`."),
					"self_heal":    optionalBool("Whether changes made in the cluster are reverted. Default `true`."),
					"sync_options": optionalStrings("Sync options. Default `CreateNamespace=true` and `ServerSideApply=true`."),
				},
			},
			"sync_window": schema.SingleNestedBlock{
				MarkdownDescription: "When the addon may sync.",
				Attributes: map[string]schema.Attribute{
					"enabled":  optionalBool("Whether the window applies. Default `false`."),
					"schedule": optionalText("Cron expression the window opens on."),
					"duration": optionalText("How long the window stays open, for example `1h`."),
					"timezone": optionalText("Time zone of the schedule. Default `UTC`."),
				},
			},
			"retry_policy": schema.SingleNestedBlock{
				MarkdownDescription: "How a failed sync is retried.",
				Attributes: map[string]schema.Attribute{
					"limit":                optionalInt("How many times a failed sync is retried. Default 5."),
					"backoff_duration":     optionalText("Wait before the first retry. Default `5s`."),
					"backoff_factor":       optionalInt("Factor the wait grows by. Default 2."),
					"backoff_max_duration": optionalText("Longest wait between retries. Default `3m`."),
				},
			},
			"ignore_differences": schema.ListNestedBlock{
				MarkdownDescription: "Differences the sync ignores.",
				NestedObject: schema.NestedBlockObject{
					Attributes: map[string]schema.Attribute{
						"group":                   optionalText("API group of the resources."),
						"kind":                    optionalText("Kind of the resources."),
						"name":                    optionalText("Name of the resource."),
						"namespace":               optionalText("Namespace of the resource."),
						"json_pointers":           optionalStrings("JSON pointers of the fields to ignore."),
						"jq_path_expressions":     optionalStrings("jq path expressions of the fields to ignore."),
						"managed_fields_managers": optionalStrings("Field managers whose changes are ignored."),
					},
				},
			},
		},
	}
}

// addonSettingsToAPI converts the settings block; nil when the block is
// absent, so the platform keeps the addon's stored settings.
func addonSettingsToAPI(ctx context.Context, settings *addonSettingsModel, diagnostics *diag.Diagnostics) *client.AddonSettings {
	if settings == nil {
		return nil
	}
	apiSettings := &client.AddonSettings{
		RevisionHistoryLimit:        int64Pointer(settings.RevisionHistoryLimit),
		StrictHealthCheck:           boolPointer(settings.StrictHealthCheck),
		RollbackOnFailure:           boolPointer(settings.RollbackOnFailure),
		HelmWaitTimeoutSeconds:      int64Pointer(settings.HelmWaitTimeoutSeconds),
		KeepFailedResourcesForDebug: boolPointer(settings.KeepFailedResourcesForDebug),
	}
	if policy := settings.SyncPolicy; policy != nil {
		apiSettings.SyncPolicy = &client.AddonSyncPolicy{
			Automated: boolPointer(policy.Automated),
			AutoPrune: boolPointer(policy.AutoPrune),
			SelfHeal:  boolPointer(policy.SelfHeal),
		}
		if !policy.SyncOptions.IsNull() && !policy.SyncOptions.IsUnknown() {
			options := stringsFromList(ctx, policy.SyncOptions, diagnostics)
			apiSettings.SyncPolicy.SyncOptions = &options
		}
	}
	if window := settings.SyncWindow; window != nil {
		apiSettings.SyncWindow = &client.AddonSyncWindow{
			Enabled:  boolPointer(window.Enabled),
			Schedule: stringPointer(window.Schedule),
			Duration: stringPointer(window.Duration),
			Timezone: stringPointer(window.Timezone),
		}
	}
	if retry := settings.RetryPolicy; retry != nil {
		apiSettings.RetryPolicy = &client.AddonRetryPolicy{Limit: int64Pointer(retry.Limit)}
		backoff := client.AddonRetryBackoff{
			Duration:    stringPointer(retry.BackoffDuration),
			Factor:      int64Pointer(retry.BackoffFactor),
			MaxDuration: stringPointer(retry.BackoffMaxDuration),
		}
		if backoff != (client.AddonRetryBackoff{}) {
			apiSettings.RetryPolicy.Backoff = &backoff
		}
	}
	for _, difference := range settings.IgnoreDifferences {
		apiSettings.IgnoreDifferences = append(apiSettings.IgnoreDifferences, client.AddonIgnoreDifference{
			Group:                 stringPointer(difference.Group),
			Kind:                  stringPointer(difference.Kind),
			Name:                  stringPointer(difference.Name),
			Namespace:             stringPointer(difference.Namespace),
			JSONPointers:          stringsFromList(ctx, difference.JSONPointers, diagnostics),
			JQPathExpressions:     stringsFromList(ctx, difference.JQPathExpressions, diagnostics),
			ManagedFieldsManagers: stringsFromList(ctx, difference.ManagedFieldsManagers, diagnostics),
		})
	}
	return apiSettings
}
