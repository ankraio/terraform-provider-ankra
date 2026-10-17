// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package client

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

func clusterSettingsPath(clusterID string) string {
	return "/api/v1/clusters/" + url.PathEscape(clusterID)
}

func clusterSettingsImportedPath(clusterID string) string {
	return "/api/v1/org/clusters/imported/" + url.PathEscape(clusterID)
}

func clusterSettingsOrganisationPath(clusterID string) string {
	return "/api/v1/org/clusters/" + url.PathEscape(clusterID)
}

// ClusterAccessGrant is one kubectl access grant on a cluster.
type ClusterAccessGrant struct {
	ID              string  `json:"id"`
	ClusterID       string  `json:"cluster_id"`
	AnkraUserID     string  `json:"ankra_user_id"`
	UserEmail       *string `json:"user_email"`
	Scope           string  `json:"scope"`
	Namespace       *string `json:"namespace"`
	Role            string  `json:"role"`
	ReconcileStatus string  `json:"reconcile_status"`
	ExpiresAt       *string `json:"expires_at"`
	Reason          *string `json:"reason"`
	Source          *string `json:"source"`
}

// ClusterAccessGrantCreate is the grant body. Posting a grant that matches a
// live grant with an expiry re-times that grant in place (expiry and reason
// are replaced); without an expiry the platform answers 409.
type ClusterAccessGrantCreate struct {
	UserEmail string  `json:"user_email"`
	Scope     string  `json:"scope"`
	Namespace *string `json:"namespace,omitempty"`
	Role      string  `json:"role"`
	ExpiresAt *string `json:"expires_at,omitempty"`
	Reason    *string `json:"reason,omitempty"`
}

// ListClusterAccessGrants returns the cluster's grants that are not revoked.
// The listing still carries a grant whose expiry has passed until the
// reconciler retires it.
func (client *Client) ListClusterAccessGrants(ctx context.Context, clusterID string) ([]ClusterAccessGrant, error) {
	var envelope struct {
		Result []ClusterAccessGrant `json:"result"`
	}
	if err := client.doRequest(ctx, http.MethodGet, clusterSettingsPath(clusterID)+"/access/grants", nil, &envelope); err != nil {
		return nil, err
	}
	return envelope.Result, nil
}

// GetClusterAccessGrant finds one grant in the listing, the only read the
// platform offers. A missing grant is a NotFound APIError.
func (client *Client) GetClusterAccessGrant(ctx context.Context, clusterID, grantID string) (*ClusterAccessGrant, error) {
	grants, err := client.ListClusterAccessGrants(ctx, clusterID)
	if err != nil {
		return nil, err
	}
	for index := range grants {
		if grants[index].ID == grantID {
			return &grants[index], nil
		}
	}
	return nil, clusterSettingsNotFound(clusterSettingsPath(clusterID)+"/access/grants", "access grant "+grantID+" not found")
}

// CreateClusterAccessGrant grants, or re-times, kubectl access.
func (client *Client) CreateClusterAccessGrant(ctx context.Context, clusterID string, request ClusterAccessGrantCreate) (*ClusterAccessGrant, error) {
	var envelope struct {
		Grant ClusterAccessGrant `json:"grant"`
	}
	if err := client.doRequest(ctx, http.MethodPost, clusterSettingsPath(clusterID)+"/access/grants", request, &envelope); err != nil {
		return nil, err
	}
	return &envelope.Grant, nil
}

// DeleteClusterAccessGrant revokes a grant. A 404 is returned as an error:
// the caller tells a grant already gone from a cluster out of scope.
func (client *Client) DeleteClusterAccessGrant(ctx context.Context, clusterID, grantID string) error {
	return client.doRequest(ctx, http.MethodDelete, clusterSettingsPath(clusterID)+"/access/grants/"+url.PathEscape(grantID), nil, nil)
}

// ClusterSSHKeys is the set of SSH key credentials installed on a cluster's
// nodes.
type ClusterSSHKeys struct {
	SSHKeyCredentialIDs []string `json:"ssh_key_credential_ids"`
}

func clusterSettingsSSHKeysPath(kind, clusterID string) string {
	return "/api/v1/clusters/" + url.PathEscape(kind) + "/" + url.PathEscape(clusterID) + "/ssh-keys"
}

// GetClusterSSHKeys reads the SSH key credentials of a cluster of the given
// kind (hetzner, ovh, upcloud, ...).
func (client *Client) GetClusterSSHKeys(ctx context.Context, kind, clusterID string) (*ClusterSSHKeys, error) {
	var keys ClusterSSHKeys
	if err := client.doRequest(ctx, http.MethodGet, clusterSettingsSSHKeysPath(kind, clusterID), nil, &keys); err != nil {
		return nil, err
	}
	return &keys, nil
}

// SetClusterSSHKeys replaces the cluster's SSH key credentials.
func (client *Client) SetClusterSSHKeys(ctx context.Context, kind, clusterID string, credentialIDs []string) (*ClusterSSHKeys, error) {
	if credentialIDs == nil {
		credentialIDs = []string{}
	}
	var keys ClusterSSHKeys
	if err := client.doRequest(ctx, http.MethodPut, clusterSettingsSSHKeysPath(kind, clusterID), ClusterSSHKeys{SSHKeyCredentialIDs: credentialIDs}, &keys); err != nil {
		return nil, err
	}
	return &keys, nil
}

// ClusterCustomDNSZone is a zone a cluster serves with one of the
// organisation's DNS credentials. Source is "cluster" for the cluster's own
// declaration and "organisation" for one it inherits.
type ClusterCustomDNSZone struct {
	Zone           string  `json:"zone"`
	CredentialName string  `json:"credential_name"`
	Source         string  `json:"source"`
	Provider       *string `json:"provider"`
	Policy         *string `json:"policy"`
}

// ClusterCustomDNSZoneDeclaration is the declare body. Declaring a zone the
// cluster already serves re-points it to the named credential.
type ClusterCustomDNSZoneDeclaration struct {
	Zone           string  `json:"zone"`
	CredentialName string  `json:"credential_name"`
	Provider       *string `json:"provider,omitempty"`
}

// ListClusterCustomDNSZones returns the zones the cluster serves, its own and
// the organisation-wide ones it inherits.
func (client *Client) ListClusterCustomDNSZones(ctx context.Context, clusterID string) ([]ClusterCustomDNSZone, error) {
	var envelope struct {
		Zones []ClusterCustomDNSZone `json:"zones"`
	}
	if err := client.doRequest(ctx, http.MethodGet, clusterSettingsPath(clusterID)+"/custom-dns-zones", nil, &envelope); err != nil {
		return nil, err
	}
	return envelope.Zones, nil
}

// DeclareClusterCustomDNSZone declares, or re-points, a zone on the cluster
// and returns the stored binding with its normalised zone name.
func (client *Client) DeclareClusterCustomDNSZone(ctx context.Context, clusterID string, request ClusterCustomDNSZoneDeclaration) (*ClusterCustomDNSZone, error) {
	var zone ClusterCustomDNSZone
	if err := client.doRequest(ctx, http.MethodPost, clusterSettingsPath(clusterID)+"/custom-dns-zones", request, &zone); err != nil {
		return nil, err
	}
	return &zone, nil
}

// RemoveClusterCustomDNSZone withdraws a zone. A zone the cluster no longer
// serves answers 404, which the caller classifies.
func (client *Client) RemoveClusterCustomDNSZone(ctx context.Context, clusterID, zone string) error {
	return client.doRequest(ctx, http.MethodDelete, clusterSettingsPath(clusterID)+"/custom-dns-zones/"+url.PathEscape(zone), nil, nil)
}

// ClusterPowerSchedule is one scheduled stop or start of a cluster.
type ClusterPowerSchedule struct {
	ID             string  `json:"id"`
	Action         string  `json:"action"`
	StopMode       string  `json:"stop_mode"`
	PreserveState  bool    `json:"preserve_state"`
	ScheduleKind   string  `json:"schedule_kind"`
	RunAt          *string `json:"run_at"`
	CronExpression *string `json:"cron_expression"`
	Timezone       string  `json:"timezone"`
	Enabled        bool    `json:"enabled"`
	NextRunAt      *string `json:"next_run_at"`
	LastRunAt      *string `json:"last_run_at"`
	LastRunStatus  *string `json:"last_run_status"`
}

// ClusterPowerScheduleRequest is the create and update body. An update is a
// full replace: enabled, stop_mode (for a stop) and timezone (for a cron
// schedule) must be restated, so every member is always sent.
type ClusterPowerScheduleRequest struct {
	Action                  string  `json:"action"`
	StopMode                string  `json:"stop_mode"`
	PreserveState           bool    `json:"preserve_state"`
	ScheduleKind            string  `json:"schedule_kind"`
	RunAt                   *string `json:"run_at,omitempty"`
	CronExpression          *string `json:"cron_expression,omitempty"`
	Timezone                string  `json:"timezone"`
	Enabled                 bool    `json:"enabled"`
	AcceptNodeLocalDataLoss bool    `json:"accept_node_local_data_loss"`
}

type clusterPowerScheduleListing struct {
	Schedules []ClusterPowerSchedule `json:"schedules"`
}

// ListClusterPowerSchedules returns the cluster's schedules, newest first.
func (client *Client) ListClusterPowerSchedules(ctx context.Context, clusterID string) ([]ClusterPowerSchedule, error) {
	var listing clusterPowerScheduleListing
	if err := client.doRequest(ctx, http.MethodGet, clusterSettingsImportedPath(clusterID)+"/power-schedules", nil, &listing); err != nil {
		return nil, err
	}
	return listing.Schedules, nil
}

// GetClusterPowerSchedule finds one schedule in the listing, the only read
// the platform offers. A missing schedule is a NotFound APIError.
func (client *Client) GetClusterPowerSchedule(ctx context.Context, clusterID, scheduleID string) (*ClusterPowerSchedule, error) {
	schedules, err := client.ListClusterPowerSchedules(ctx, clusterID)
	if err != nil {
		return nil, err
	}
	return findClusterPowerSchedule(schedules, clusterID, scheduleID)
}

func findClusterPowerSchedule(schedules []ClusterPowerSchedule, clusterID, scheduleID string) (*ClusterPowerSchedule, error) {
	for index := range schedules {
		if schedules[index].ID == scheduleID {
			return &schedules[index], nil
		}
	}
	return nil, clusterSettingsNotFound(clusterSettingsImportedPath(clusterID)+"/power-schedules", "power schedule "+scheduleID+" not found")
}

// clusterPowerScheduleCreateLocks serialises schedule creation per cluster
// within one provider process, so the listing taken before a create is not
// raced by a sibling create Terraform runs in parallel.
var (
	clusterPowerScheduleCreateLocksGuard sync.Mutex
	clusterPowerScheduleCreateLocks      = map[string]*sync.Mutex{}
)

// CreateClusterPowerSchedule creates a schedule. The platform answers the
// refreshed listing rather than the new row, so the schedule created is the
// one, among the ids the listing taken just before did not hold, that matches
// the request.
func (client *Client) CreateClusterPowerSchedule(ctx context.Context, clusterID string, request ClusterPowerScheduleRequest) (*ClusterPowerSchedule, error) {
	clusterPowerScheduleCreateLocksGuard.Lock()
	lock, exists := clusterPowerScheduleCreateLocks[clusterID]
	if !exists {
		lock = &sync.Mutex{}
		clusterPowerScheduleCreateLocks[clusterID] = lock
	}
	clusterPowerScheduleCreateLocksGuard.Unlock()
	lock.Lock()
	defer lock.Unlock()

	before, err := client.ListClusterPowerSchedules(ctx, clusterID)
	if err != nil {
		return nil, err
	}
	known := make(map[string]bool, len(before))
	for _, schedule := range before {
		known[schedule.ID] = true
	}
	var listing clusterPowerScheduleListing
	if err := client.doRequest(ctx, http.MethodPost, clusterSettingsImportedPath(clusterID)+"/power-schedules", request, &listing); err != nil {
		var apiError *APIError
		if errors.As(err, &apiError) {
			return nil, err
		}
		return nil, &ClusterPowerScheduleUntrackedError{ClusterID: clusterID, Request: request, Cause: err.Error()}
	}
	var created []ClusterPowerSchedule
	var newIDs []string
	for _, schedule := range listing.Schedules {
		if known[schedule.ID] {
			continue
		}
		newIDs = append(newIDs, schedule.ID)
		if clusterPowerScheduleMatches(schedule, request) {
			created = append(created, schedule)
		}
	}
	if len(created) != 1 {
		candidates := newIDs
		if len(created) > 1 {
			candidates = nil
			for _, schedule := range created {
				candidates = append(candidates, schedule.ID)
			}
		}
		return nil, &ClusterPowerScheduleUntrackedError{
			ClusterID: clusterID, Request: request, CandidateIDs: candidates,
			Cause: fmt.Sprintf("%d new schedules in the listing match the request", len(created)),
		}
	}
	return &created[0], nil
}

// ClusterPowerScheduleUntrackedError is a create the platform may have
// committed but the provider could not identify. Applying again would create
// a second schedule, so the error names the candidates and how to import one.
type ClusterPowerScheduleUntrackedError struct {
	ClusterID    string
	Request      ClusterPowerScheduleRequest
	CandidateIDs []string
	Cause        string
}

func (untracked *ClusterPowerScheduleUntrackedError) Error() string {
	timing := "cron_expression " + clusterSettingsDereference(untracked.Request.CronExpression)
	if untracked.Request.ScheduleKind == "once" {
		timing = "run_at " + clusterSettingsDereference(untracked.Request.RunAt)
	}
	message := fmt.Sprintf("the power schedule (action %s, schedule_kind %s, %s) may have been created on cluster %s, "+
		"but the provider could not identify it (%s). Do not apply again before importing it, or the platform will hold two. ",
		untracked.Request.Action, untracked.Request.ScheduleKind, timing, untracked.ClusterID, untracked.Cause)
	if len(untracked.CandidateIDs) == 0 {
		return message + fmt.Sprintf("Find its id with `ankra cluster power-schedules list %s`, then run: "+
			"terraform import <resource address> %s/<schedule_id>", untracked.ClusterID, untracked.ClusterID)
	}
	commands := make([]string, 0, len(untracked.CandidateIDs))
	for _, candidate := range untracked.CandidateIDs {
		commands = append(commands, fmt.Sprintf("terraform import <resource address> %s/%s", untracked.ClusterID, candidate))
	}
	return message + "Candidates: " + strings.Join(untracked.CandidateIDs, ", ") + ". Import the right one: " + strings.Join(commands, " | ")
}

func clusterSettingsDereference(value *string) string {
	if value == nil {
		return "(none)"
	}
	return *value
}

// clusterPowerScheduleMatches reports whether a listed schedule is the one a
// create request describes, comparing run_at as an instant.
func clusterPowerScheduleMatches(schedule ClusterPowerSchedule, request ClusterPowerScheduleRequest) bool {
	if schedule.Action != request.Action || schedule.ScheduleKind != request.ScheduleKind || schedule.Enabled != request.Enabled {
		return false
	}
	if request.CronExpression != nil && (schedule.CronExpression == nil || *schedule.CronExpression != strings.TrimSpace(*request.CronExpression)) {
		return false
	}
	if request.RunAt != nil {
		if schedule.RunAt == nil {
			return false
		}
		requested, requestedError := time.Parse(time.RFC3339, strings.TrimSpace(*request.RunAt))
		listed, listedError := time.Parse(time.RFC3339, *schedule.RunAt)
		if requestedError == nil && listedError == nil && !requested.Equal(listed) {
			return false
		}
	}
	return true
}

// UpdateClusterPowerSchedule replaces a schedule and returns it as the
// refreshed listing shows it.
func (client *Client) UpdateClusterPowerSchedule(ctx context.Context, clusterID, scheduleID string, request ClusterPowerScheduleRequest) (*ClusterPowerSchedule, error) {
	var listing clusterPowerScheduleListing
	if err := client.doRequest(ctx, http.MethodPut, clusterSettingsImportedPath(clusterID)+"/power-schedules/"+url.PathEscape(scheduleID), request, &listing); err != nil {
		return nil, err
	}
	return findClusterPowerSchedule(listing.Schedules, clusterID, scheduleID)
}

// DeleteClusterPowerSchedule deletes a schedule. A 404 is returned for the
// caller to classify.
func (client *Client) DeleteClusterPowerSchedule(ctx context.Context, clusterID, scheduleID string) error {
	return client.doRequest(ctx, http.MethodDelete, clusterSettingsImportedPath(clusterID)+"/power-schedules/"+url.PathEscape(scheduleID), nil, nil)
}

// ClusterAgent is the part of a cluster's agent record the provider reads.
type ClusterAgent struct {
	AgentVersion       *string `json:"agent_version"`
	DisableAutoUpgrade bool    `json:"disable_auto_upgrade"`
	IsOnline           bool    `json:"is_online"`
}

// GetClusterAgent reads the cluster's agent. A cluster without a registered
// agent answers 404.
func (client *Client) GetClusterAgent(ctx context.Context, clusterID string) (*ClusterAgent, error) {
	var agent ClusterAgent
	if err := client.doRequest(ctx, http.MethodGet, clusterSettingsImportedPath(clusterID)+"/agent", nil, &agent); err != nil {
		return nil, err
	}
	return &agent, nil
}

// SetClusterAgentAutoUpgrade stores the agent's automatic-upgrade opt-out:
// enabled false stores disable_auto_upgrade true.
func (client *Client) SetClusterAgentAutoUpgrade(ctx context.Context, clusterID string, enabled bool) error {
	body := map[string]bool{"disable_auto_upgrade": !enabled}
	return client.doRequest(ctx, http.MethodPatch, clusterSettingsImportedPath(clusterID)+"/agent/settings", body, nil)
}

// ClusterAgentCISettings is the stored CI settings of a cluster's agent. The
// optional members are nil on a platform older than the setting.
type ClusterAgentCISettings struct {
	CIWorkerCount          int64   `json:"ci_worker_count"`
	CIStorageClass         string  `json:"ci_storage_class"`
	CIRunReservation       *string `json:"ci_run_reservation"`
	CIImagePrepull         *string `json:"ci_image_prepull"`
	CIBuildAppArmorProfile *string `json:"ci_build_apparmor_profile"`
	ApplyState             string  `json:"apply_state"`
}

// ClusterAgentCISettingsUpdate is a partial write: a nil member is left out
// and the platform keeps its stored value. The placement is not part of it.
type ClusterAgentCISettingsUpdate struct {
	CIWorkerCount          *int64  `json:"ci_worker_count,omitempty"`
	CIStorageClass         *string `json:"ci_storage_class,omitempty"`
	CIRunReservation       *string `json:"ci_run_reservation,omitempty"`
	CIImagePrepull         *string `json:"ci_image_prepull,omitempty"`
	CIBuildAppArmorProfile *string `json:"ci_build_apparmor_profile,omitempty"`
}

// GetClusterAgentCISettings reads the cluster's agent CI settings.
func (client *Client) GetClusterAgentCISettings(ctx context.Context, clusterID string) (*ClusterAgentCISettings, error) {
	var settings ClusterAgentCISettings
	if err := client.doRequest(ctx, http.MethodGet, clusterSettingsOrganisationPath(clusterID)+"/agent/ci-settings", nil, &settings); err != nil {
		return nil, err
	}
	return &settings, nil
}

// UpdateClusterAgentCISettings stores a partial change to the cluster's agent
// CI settings.
func (client *Client) UpdateClusterAgentCISettings(ctx context.Context, clusterID string, update ClusterAgentCISettingsUpdate) (*ClusterAgentCISettings, error) {
	var settings ClusterAgentCISettings
	if err := client.doRequest(ctx, http.MethodPut, clusterSettingsOrganisationPath(clusterID)+"/agent/ci-settings", update, &settings); err != nil {
		return nil, err
	}
	return &settings, nil
}

// ClusterLogShipping is the cluster's hosted log shipping switch.
type ClusterLogShipping struct {
	ShippingEnabled     bool `json:"shipping_enabled"`
	Available           bool `json:"available"`
	AgentSupportsSwitch bool `json:"agent_supports_switch"`
}

// GetClusterLogShipping reads the cluster's hosted log shipping switch.
func (client *Client) GetClusterLogShipping(ctx context.Context, clusterID string) (*ClusterLogShipping, error) {
	var state ClusterLogShipping
	if err := client.doRequest(ctx, http.MethodGet, clusterSettingsOrganisationPath(clusterID)+"/hosted-logs", nil, &state); err != nil {
		return nil, err
	}
	return &state, nil
}

// SetClusterLogShipping turns hosted log shipping on or off.
func (client *Client) SetClusterLogShipping(ctx context.Context, clusterID string, enabled bool) (*ClusterLogShipping, error) {
	var state ClusterLogShipping
	body := map[string]bool{"shipping_enabled": enabled}
	if err := client.doRequest(ctx, http.MethodPut, clusterSettingsOrganisationPath(clusterID)+"/hosted-logs", body, &state); err != nil {
		return nil, err
	}
	return &state, nil
}

const clusterMeshesPath = "/api/v1/org/cluster-meshes"

// ClusterMeshMember is one cluster in a mesh.
type ClusterMeshMember struct {
	ClusterID         string `json:"cluster_id"`
	CiliumClusterID   int64  `json:"cilium_cluster_id"`
	CiliumClusterName string `json:"cilium_cluster_name"`
	Status            string `json:"status"`
}

// ClusterMesh is an organisation's Cilium ClusterMesh.
type ClusterMesh struct {
	ID      string              `json:"id"`
	Slug    string              `json:"slug"`
	Name    string              `json:"name"`
	Status  string              `json:"status"`
	Members []ClusterMeshMember `json:"members"`
}

// ListClusterMeshes returns every mesh of the organisation.
func (client *Client) ListClusterMeshes(ctx context.Context) ([]ClusterMesh, error) {
	var envelope struct {
		ClusterMeshes []ClusterMesh `json:"cluster_meshes"`
	}
	if err := client.doRequest(ctx, http.MethodGet, clusterMeshesPath, nil, &envelope); err != nil {
		return nil, err
	}
	return envelope.ClusterMeshes, nil
}

// GetClusterMesh reads one mesh with its members.
func (client *Client) GetClusterMesh(ctx context.Context, meshID string) (*ClusterMesh, error) {
	var envelope struct {
		ClusterMesh ClusterMesh `json:"cluster_mesh"`
	}
	if err := client.doRequest(ctx, http.MethodGet, clusterMeshesPath+"/"+url.PathEscape(meshID), nil, &envelope); err != nil {
		return nil, err
	}
	return &envelope.ClusterMesh, nil
}

// CreateClusterMesh declares a new, empty mesh.
func (client *Client) CreateClusterMesh(ctx context.Context, name string) (*ClusterMesh, error) {
	var envelope struct {
		ClusterMesh ClusterMesh `json:"cluster_mesh"`
	}
	if err := client.doRequest(ctx, http.MethodPost, clusterMeshesPath, map[string]string{"name": name}, &envelope); err != nil {
		return nil, err
	}
	return &envelope.ClusterMesh, nil
}

// DeleteClusterMesh deletes a mesh, which the platform allows only once it
// has no members. A 404 is returned for the caller to classify.
func (client *Client) DeleteClusterMesh(ctx context.Context, meshID string) error {
	return client.doRequest(ctx, http.MethodDelete, clusterMeshesPath+"/"+url.PathEscape(meshID), nil, nil)
}

// JoinClusterMesh admits a cluster to a mesh.
func (client *Client) JoinClusterMesh(ctx context.Context, meshID, clusterID string) error {
	return client.doRequest(ctx, http.MethodPost, clusterMeshesPath+"/"+url.PathEscape(meshID)+"/members", map[string]string{"cluster_id": clusterID}, nil)
}

// LeaveClusterMesh removes a cluster from a mesh. A 404 is returned for the
// caller to classify.
func (client *Client) LeaveClusterMesh(ctx context.Context, meshID, clusterID string) error {
	return client.doRequest(ctx, http.MethodDelete, clusterMeshesPath+"/"+url.PathEscape(meshID)+"/members/"+url.PathEscape(clusterID), nil, nil)
}

func clusterSettingsNotFound(path, body string) error {
	return &APIError{Method: http.MethodGet, Path: path, StatusCode: http.StatusNotFound, Body: body}
}
