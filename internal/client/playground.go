// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package client

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"time"

	"github.com/hashicorp/terraform-plugin-log/tflog"
)

const playgroundPath = "/api/v1/org/clusters/playground"

// PlaygroundTrialPlanID is the free plan every un-ordered playground runs on.
// The platform refuses to resize from or to it.
const PlaygroundTrialPlanID = "trial"

// PlaygroundReadyPhase is the phase a playground reaches once it serves.
const PlaygroundReadyPhase = "ready"

// PlaygroundGonePhases are phases of an environment that is being or has
// been torn down. The status route keeps answering for them, so a read treats
// them as gone.
var PlaygroundGonePhases = []string{"deprovisioning", "removed"}

// playgroundFailurePhases end a readiness wait with an error.
var playgroundFailurePhases = []string{"failed", "deprovisioning", "removed"}

// PlaygroundOrderedPlan is the size an environment was ordered at.
type PlaygroundOrderedPlan struct {
	ID                string  `json:"id"`
	DisplayName       string  `json:"display_name"`
	Vcpus             int64   `json:"vcpus"`
	MemoryGB          float64 `json:"memory_gb"`
	StorageGB         int64   `json:"storage_gb"`
	MaxPods           int64   `json:"max_pods"`
	PriceMonthlyCents int64   `json:"price_monthly_cents"`
	Currency          string  `json:"currency"`
	OrderedAt         *string `json:"ordered_at"`
}

// PlaygroundStatus is GET .../playground/{cluster_id}/status.
type PlaygroundStatus struct {
	ClusterID     string                `json:"cluster_id"`
	Phase         string                `json:"phase"`
	StatusMessage *string               `json:"status_message"`
	ExpiresAt     string                `json:"expires_at"`
	StarterURL    *string               `json:"starter_url"`
	Plan          PlaygroundOrderedPlan `json:"plan"`
}

// PlaygroundPlan is one orderable size.
type PlaygroundPlan struct {
	ID                string  `json:"id"`
	DisplayName       string  `json:"display_name"`
	Summary           string  `json:"summary"`
	Vcpus             int64   `json:"vcpus"`
	MemoryGB          float64 `json:"memory_gb"`
	StorageGB         int64   `json:"storage_gb"`
	MaxPods           int64   `json:"max_pods"`
	PriceMonthlyCents int64   `json:"price_monthly_cents"`
	Currency          string  `json:"currency"`
	Available         bool    `json:"available"`
}

// PlaygroundMemoryQuota is the organisation's playground memory budget.
type PlaygroundMemoryQuota struct {
	LimitMiB int64 `json:"limit_mib"`
	UsedMiB  int64 `json:"used_mib"`
}

// PlaygroundPlanCatalog is GET .../playground/plans.
type PlaygroundPlanCatalog struct {
	Plans                      []PlaygroundPlan      `json:"plans"`
	DefaultPlanID              string                `json:"default_plan_id"`
	Currency                   string                `json:"currency"`
	OrganisationHasPaidPlan    bool                  `json:"organisation_has_paid_plan"`
	OrganisationHasPaymentCard bool                  `json:"organisation_has_payment_card"`
	Quota                      PlaygroundMemoryQuota `json:"quota"`
}

type playgroundPlanBody struct {
	Plan string `json:"plan"`
}

type playgroundCreated struct {
	ClusterID string `json:"cluster_id"`
}

func playgroundClusterPath(clusterID, suffix string) string {
	path := playgroundPath + "/" + url.PathEscape(clusterID)
	if suffix != "" {
		path += "/" + suffix
	}
	return path
}

// ListPlaygroundPlans returns the orderable playground sizes.
func (client *Client) ListPlaygroundPlans(ctx context.Context) (*PlaygroundPlanCatalog, error) {
	var catalog PlaygroundPlanCatalog
	if err := client.doRequest(ctx, http.MethodGet, playgroundPath+"/plans", nil, &catalog); err != nil {
		return nil, err
	}
	return &catalog, nil
}

// CreatePlayground orders a playground at the given plan and returns its
// cluster id.
func (client *Client) CreatePlayground(ctx context.Context, planID string) (string, error) {
	var created playgroundCreated
	if err := client.doRequest(ctx, http.MethodPost, playgroundPath, playgroundPlanBody{Plan: planID}, &created); err != nil {
		return "", err
	}
	if created.ClusterID == "" {
		return "", fmt.Errorf("the platform did not return a cluster_id for the playground")
	}
	return created.ClusterID, nil
}

// GetPlaygroundStatus reads a playground. One that does not exist is a
// NotFound APIError.
func (client *Client) GetPlaygroundStatus(ctx context.Context, clusterID string) (*PlaygroundStatus, error) {
	var status PlaygroundStatus
	if err := client.doRequest(ctx, http.MethodGet, playgroundClusterPath(clusterID, "status"), nil, &status); err != nil {
		return nil, err
	}
	return &status, nil
}

// ResizePlayground moves a paid playground to another paid plan in place.
func (client *Client) ResizePlayground(ctx context.Context, clusterID, planID string) error {
	return client.doRequest(ctx, http.MethodPost, playgroundClusterPath(clusterID, "resize"), playgroundPlanBody{Plan: planID}, nil)
}

// DestroyPlayground tears a playground down. One already gone is not an
// error.
func (client *Client) DestroyPlayground(ctx context.Context, clusterID string) error {
	return client.doRequest(ctx, http.MethodDelete, playgroundClusterPath(clusterID, ""), nil, nil, http.StatusNotFound)
}

// WaitForPlaygroundReady polls the status until the playground is ready, has
// failed, or ctx expires.
func (client *Client) WaitForPlaygroundReady(ctx context.Context, clusterID string, pollInterval time.Duration) (*PlaygroundStatus, error) {
	if pollInterval <= 0 {
		pollInterval = defaultPollInterval
	}
	lastPhase := ""
	for {
		status, err := client.GetPlaygroundStatus(ctx, clusterID)
		if err != nil {
			if ctx.Err() != nil {
				return nil, fmt.Errorf("timed out waiting for playground %s to be ready (last phase %q): %w", clusterID, lastPhase, ctx.Err())
			}
			return nil, err
		}
		lastPhase = status.Phase
		if status.Phase == PlaygroundReadyPhase {
			return status, nil
		}
		if slices.Contains(playgroundFailurePhases, status.Phase) {
			message := ""
			if status.StatusMessage != nil {
				message = ": " + *status.StatusMessage
			}
			return status, fmt.Errorf("playground %s entered phase %q%s", clusterID, status.Phase, message)
		}
		tflog.Debug(ctx, "waiting for playground", map[string]any{"cluster_id": clusterID, "phase": status.Phase})
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("timed out waiting for playground %s to be ready (last phase %q): %w", clusterID, lastPhase, ctx.Err())
		case <-time.After(pollInterval):
		}
	}
}
