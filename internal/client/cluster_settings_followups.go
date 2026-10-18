// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package client

import (
	"context"
	"net/http"
	"net/url"
)

// ClusterControlPlaneSelfHeal is the cluster's control-plane self-heal
// switch: whether Ankra may restart a NotReady control plane node through
// its provider.
type ClusterControlPlaneSelfHeal struct {
	ClusterID string `json:"cluster_id"`
	Enabled   bool   `json:"enabled"`
}

// GetClusterControlPlaneSelfHeal reads the cluster's control-plane self-heal
// switch.
func (client *Client) GetClusterControlPlaneSelfHeal(ctx context.Context, clusterID string) (*ClusterControlPlaneSelfHeal, error) {
	var view ClusterControlPlaneSelfHeal
	if err := client.doRequest(ctx, http.MethodGet, clusterSettingsOrganisationPath(clusterID)+"/control-plane-self-heal", nil, &view); err != nil {
		return nil, err
	}
	return &view, nil
}

// SetClusterControlPlaneSelfHeal turns the cluster's control-plane self-heal
// on or off.
func (client *Client) SetClusterControlPlaneSelfHeal(ctx context.Context, clusterID string, enabled bool) (*ClusterControlPlaneSelfHeal, error) {
	var view ClusterControlPlaneSelfHeal
	body := map[string]bool{"enabled": enabled}
	if err := client.doRequest(ctx, http.MethodPut, clusterSettingsOrganisationPath(clusterID)+"/control-plane-self-heal", body, &view); err != nil {
		return nil, err
	}
	return &view, nil
}

// ClusterAgentResources is the stored resource sizing of a cluster's agent.
// A member the cluster never set is nil (the chart default applies). The
// platform stores each quantity in its canonical Kubernetes spelling.
type ClusterAgentResources struct {
	MemoryLimit   *string `json:"memory_limit"`
	MemoryRequest *string `json:"memory_request"`
	CPULimit      *string `json:"cpu_limit"`
	CPURequest    *string `json:"cpu_request"`
	ApplyState    string  `json:"apply_state"`
}

// ClusterAgentResourcesUpdate is a partial write: a nil member is left out
// and keeps its stored value; an empty string clears it back to the chart
// default.
type ClusterAgentResourcesUpdate struct {
	MemoryLimit   *string `json:"memory_limit,omitempty"`
	MemoryRequest *string `json:"memory_request,omitempty"`
	CPULimit      *string `json:"cpu_limit,omitempty"`
	CPURequest    *string `json:"cpu_request,omitempty"`
}

// IsEmpty reports whether the update names no member.
func (update ClusterAgentResourcesUpdate) IsEmpty() bool {
	return update.MemoryLimit == nil && update.MemoryRequest == nil && update.CPULimit == nil && update.CPURequest == nil
}

// GetClusterAgentResources reads the cluster's agent resource sizing.
func (client *Client) GetClusterAgentResources(ctx context.Context, clusterID string) (*ClusterAgentResources, error) {
	var resources ClusterAgentResources
	if err := client.doRequest(ctx, http.MethodGet, clusterSettingsOrganisationPath(clusterID)+"/agent/resources", nil, &resources); err != nil {
		return nil, err
	}
	return &resources, nil
}

// UpdateClusterAgentResources stores a partial change to the cluster's agent
// resource sizing.
func (client *Client) UpdateClusterAgentResources(ctx context.Context, clusterID string, update ClusterAgentResourcesUpdate) (*ClusterAgentResources, error) {
	var resources ClusterAgentResources
	if err := client.doRequest(ctx, http.MethodPut, clusterSettingsOrganisationPath(clusterID)+"/agent/resources", update, &resources); err != nil {
		return nil, err
	}
	return &resources, nil
}

// ClusterLabels is the cluster's free-form labels, which dynamic cluster
// groups select on. ReservedKeys lists the keys no label may carry.
type ClusterLabels struct {
	ClusterID    string            `json:"cluster_id"`
	Labels       map[string]string `json:"labels"`
	ReservedKeys []string          `json:"reserved_keys"`
}

// ClusterLabelsChange sets (adds or overwrites) and removes labels. A key may
// not appear in both, and the platform refuses a change naming nothing.
type ClusterLabelsChange struct {
	Set    map[string]string `json:"set,omitempty"`
	Remove []string          `json:"remove,omitempty"`
}

// GetClusterLabels reads the cluster's labels.
func (client *Client) GetClusterLabels(ctx context.Context, clusterID string) (*ClusterLabels, error) {
	var labels ClusterLabels
	if err := client.doRequest(ctx, http.MethodGet, clusterSettingsOrganisationPath(clusterID)+"/labels", nil, &labels); err != nil {
		return nil, err
	}
	return &labels, nil
}

// ChangeClusterLabels applies a change to the cluster's labels and returns
// the resulting set.
func (client *Client) ChangeClusterLabels(ctx context.Context, clusterID string, change ClusterLabelsChange) (*ClusterLabels, error) {
	var labels ClusterLabels
	if err := client.doRequest(ctx, http.MethodPatch, clusterSettingsOrganisationPath(clusterID)+"/labels", change, &labels); err != nil {
		return nil, err
	}
	return &labels, nil
}

// StackBackupRetention is the grandfather-father-son retention of a stack's
// backup policy. A zero tier is disabled.
type StackBackupRetention struct {
	Hourly       int64  `json:"hourly"`
	Daily        int64  `json:"daily"`
	Weekly       int64  `json:"weekly"`
	Monthly      int64  `json:"monthly"`
	Yearly       int64  `json:"yearly"`
	MinimumCount int64  `json:"minimum_count"`
	MinimumAge   string `json:"minimum_age,omitempty"`
}

// StackBackupSelection is which of the stack's data assets the policy
// covers. Databases nil takes the default (captured); false needs
// ConfirmExcludeDatabases beside it.
type StackBackupSelection struct {
	Databases               *bool    `json:"databases,omitempty"`
	PersistentVolumeClaims  []string `json:"persistent_volume_claims,omitempty"`
	ConfirmExcludeDatabases bool     `json:"confirm_exclude_databases,omitempty"`
}

// StackProtect is the body of POST .../stacks/{stack_name}/protect. It
// rewrites the stack's whole backup policy.
type StackProtect struct {
	VaultID   string                `json:"vault_id"`
	Schedule  string                `json:"schedule,omitempty"`
	Retention *StackBackupRetention `json:"retention,omitempty"`
	Selection *StackBackupSelection `json:"selection,omitempty"`
	BackupNow bool                  `json:"backup_now,omitempty"`
}

// StackBackupPolicy is the policy a protect or unprotect answers with. Vault
// is the vault's name; Schedule is resolved.
type StackBackupPolicy struct {
	Enabled bool   `json:"enabled"`
	Vault   string `json:"vault"`
}

// StackProtection is what a protect or an unprotect answers with.
type StackProtection struct {
	StackName   string            `json:"stack_name"`
	Policy      StackBackupPolicy `json:"policy"`
	BackupStack string            `json:"backup_stack"`
	Warnings    []string          `json:"warnings"`
}

// StackBackupPosture is the backup posture of one stack. UnprotectedReason
// "no_policy" means the stack carries no backup policy. VaultName and
// Schedule are the policy's, as written.
type StackBackupPosture struct {
	StackName         string `json:"stack_name"`
	ProtectionState   string `json:"protection_state"`
	UnprotectedReason string `json:"unprotected_reason"`
	VaultName         string `json:"vault_name"`
	Schedule          string `json:"schedule"`
}

func stackPath(clusterID, stackName string) string {
	return clusterSettingsImportedPath(clusterID) + "/stacks/" + url.PathEscape(stackName)
}

// ProtectStack writes a backup policy onto a stack.
func (client *Client) ProtectStack(ctx context.Context, clusterID, stackName string, request StackProtect) (*StackProtection, error) {
	var protection StackProtection
	if err := client.doRequest(ctx, http.MethodPost, stackPath(clusterID, stackName)+"/protect", request, &protection); err != nil {
		return nil, err
	}
	return &protection, nil
}

// UnprotectStack clears a stack's backup policy. Restore points already
// taken are kept.
func (client *Client) UnprotectStack(ctx context.Context, clusterID, stackName string) error {
	body := map[string]string{"confirm": stackName}
	return client.doRequest(ctx, http.MethodDelete, stackPath(clusterID, stackName)+"/protect", body, nil)
}

// GetStackBackupPosture reads the backup posture of one stack. A stack the
// cluster does not have answers 404.
func (client *Client) GetStackBackupPosture(ctx context.Context, clusterID, stackName string) (*StackBackupPosture, error) {
	var posture StackBackupPosture
	if err := client.doRequest(ctx, http.MethodGet, stackPath(clusterID, stackName)+"/backup-posture", nil, &posture); err != nil {
		return nil, err
	}
	return &posture, nil
}
