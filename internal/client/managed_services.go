// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package client

import (
	"context"
	"net/http"
	"net/url"
)

const (
	servicePackagesPath  = "/api/v1/org/service-packages"
	serviceReviewsPath   = serviceAdmissionPath + "/reviews"
	serviceInstancesPath = serviceAdmissionPath + "/instances"
	serviceConsumersPath = serviceAdmissionPath + "/consumers"
	// servicePageLimit is the largest page the service routes answer.
	servicePageLimit = "50"
	// maxServicePages bounds a cursor walk, so a platform that keeps answering
	// a cursor cannot hold an apply forever.
	maxServicePages = 200
)

// Managed service states the platform reports.
const (
	ManagedServiceStateDeployed = "deployed"
	ManagedServiceStateServing  = "serving"
	ManagedServiceStateFailed   = "failed"
	ManagedServiceStateReleased = "released"
)

// Retirement states and the one outcome that means the service is gone.
const (
	ServiceRetirementStateSettled  = "settled"
	ServiceRetirementStateExpired  = "expired"
	ServiceRetirementOutcomeRetire = "retired"
)

// ServicePackage is one service package version the organisation can set a
// service up from: its own, shared with it, or first-party.
type ServicePackage struct {
	ID          string `json:"id"`
	PublisherID string `json:"publisher_id"`
	Name        string `json:"name"`
	Version     string `json:"version"`
	Capability  string `json:"capability"`
	Digest      string `json:"digest"`
	FirstParty  bool   `json:"first_party"`
	CreatedAt   string `json:"created_at"`
}

// ManagedServiceConsumer is one consumer a managed service is connected to.
type ManagedServiceConsumer struct {
	ID string `json:"id"`
}

// ManagedServiceRetirement is the retirement a managed service is under, if
// any.
type ManagedServiceRetirement struct {
	ID      string  `json:"id"`
	State   string  `json:"state"`
	Outcome *string `json:"outcome"`
}

// ManagedService is one managed service instance. Its id is the id of the
// review it was set up from.
type ManagedService struct {
	ID               string                    `json:"id"`
	Name             string                    `json:"name"`
	PackageVersionID string                    `json:"package_version_id"`
	Generation       int64                     `json:"generation"`
	Mode             string                    `json:"mode"`
	ClusterID        string                    `json:"cluster_id"`
	ClusterName      string                    `json:"cluster_name"`
	Region           string                    `json:"region"`
	DataBoundary     string                    `json:"data_boundary"`
	Consumers        []ManagedServiceConsumer  `json:"consumers"`
	State            string                    `json:"state"`
	DeploymentState  string                    `json:"deployment_state"`
	Readiness        string                    `json:"readiness"`
	CreatedAt        string                    `json:"created_at"`
	ReleasedAt       *string                   `json:"released_at"`
	Retirement       *ManagedServiceRetirement `json:"retirement"`
}

// ManagedServiceReviewRequest asks the platform to plan a managed service.
// The platform decodes it strictly: every field must be present and none may
// be null, so empty collections are sent as [] and {}.
type ManagedServiceReviewRequest struct {
	PackageVersionID string            `json:"package_version_id"`
	Name             string            `json:"name"`
	Mode             string            `json:"mode"`
	Region           string            `json:"region"`
	DataBoundary     string            `json:"data_boundary"`
	ClusterID        string            `json:"cluster_id"`
	ConsumerIDs      []string          `json:"consumer_ids"`
	Parameters       map[string]int64  `json:"parameters"`
	SecretReferences map[string]string `json:"secret_references"`
}

// ManagedServiceReview is a planned setup. Confirming it with its digest,
// inside its window, starts the install.
type ManagedServiceReview struct {
	ID          string  `json:"id"`
	Digest      string  `json:"digest"`
	ExecutionID *string `json:"execution_id"`
	ConfirmedAt *string `json:"confirmed_at"`
}

// ManagedServiceRetirementRequest plans the retirement of a service. The
// consumers named must be exactly the service's consumers, and the data loss
// must be acknowledged.
type ManagedServiceRetirementRequest struct {
	ExpectedGeneration    int64    `json:"expected_generation"`
	DisconnectConsumerIDs []string `json:"disconnect_consumer_ids"`
	AcknowledgeDataLoss   bool     `json:"acknowledge_data_loss"`
}

// ManagedServiceRetirementPlan is a planned or running retirement.
type ManagedServiceRetirementPlan struct {
	ID         string  `json:"id"`
	InstanceID string  `json:"instance_id"`
	Digest     string  `json:"digest"`
	State      string  `json:"state"`
	Phase      *string `json:"phase"`
	Outcome    *string `json:"outcome"`
	Reason     *string `json:"reason"`
}

// ServiceConsumerBinding is one consumer binding as the listing shows it.
type ServiceConsumerBinding struct {
	ID            string `json:"id"`
	ApplicationID string `json:"application_id"`
	ClusterID     string `json:"cluster_id"`
	Namespace     string `json:"namespace"`
	AllowPlanned  bool   `json:"allow_planned"`
	LocalOnly     bool   `json:"local_only"`
	Revision      int64  `json:"revision"`
	UpdatedAt     string `json:"updated_at"`
}

// ManagedServiceFilter narrows the managed service listing. Empty fields do
// not filter.
type ManagedServiceFilter struct {
	ApplicationID string
	ClusterID     string
	State         string
}

type serviceDigest struct {
	Digest string `json:"digest"`
}

type cursorPage[Item any] struct {
	Items      []Item  `json:"items"`
	NextCursor *string `json:"next_cursor"`
}

// walkCursor follows next_cursor from the first page to the last.
func walkCursor[Item any](ctx context.Context, client *Client, path string, query url.Values) ([]Item, error) {
	items := []Item{}
	query.Set("limit", servicePageLimit)
	for page := 0; page < maxServicePages; page++ {
		var response cursorPage[Item]
		if err := client.doRequest(ctx, http.MethodGet, path+"?"+query.Encode(), nil, &response); err != nil {
			return nil, err
		}
		items = append(items, response.Items...)
		if response.NextCursor == nil || *response.NextCursor == "" || len(response.Items) == 0 {
			return items, nil
		}
		query.Set("after", *response.NextCursor)
	}
	return items, nil
}

// ListServicePackages returns every service package version the
// organisation can use.
func (client *Client) ListServicePackages(ctx context.Context) ([]ServicePackage, error) {
	return walkCursor[ServicePackage](ctx, client, servicePackagesPath, url.Values{})
}

// ListManagedServices returns the organisation's managed services, released
// ones included, on clusters the token can read.
func (client *Client) ListManagedServices(ctx context.Context, filter ManagedServiceFilter) ([]ManagedService, error) {
	query := url.Values{}
	if filter.ApplicationID != "" {
		query.Set("application_id", filter.ApplicationID)
	}
	if filter.ClusterID != "" {
		query.Set("cluster_id", filter.ClusterID)
	}
	if filter.State != "" {
		query.Set("state", filter.State)
	}
	return walkCursor[ManagedService](ctx, client, serviceInstancesPath, query)
}

// ListServiceConsumerBindings returns one application's consumer bindings.
func (client *Client) ListServiceConsumerBindings(ctx context.Context, applicationID string) ([]ServiceConsumerBinding, error) {
	query := url.Values{}
	query.Set("application_id", applicationID)
	return walkCursor[ServiceConsumerBinding](ctx, client, serviceConsumersPath, query)
}

// GetManagedService reads one managed service. A released service is still
// returned, with state "released".
func (client *Client) GetManagedService(ctx context.Context, serviceID string) (*ManagedService, error) {
	var service ManagedService
	if err := client.doRequest(ctx, http.MethodGet, serviceInstancesPath+"/"+url.PathEscape(serviceID), nil, &service); err != nil {
		return nil, err
	}
	return &service, nil
}

// ReviewManagedService plans a managed service. The review expires after ten
// minutes.
func (client *Client) ReviewManagedService(ctx context.Context, request ManagedServiceReviewRequest) (*ManagedServiceReview, error) {
	if request.ConsumerIDs == nil {
		request.ConsumerIDs = []string{}
	}
	if request.Parameters == nil {
		request.Parameters = map[string]int64{}
	}
	if request.SecretReferences == nil {
		request.SecretReferences = map[string]string{}
	}
	var review ManagedServiceReview
	if err := client.doRequest(ctx, http.MethodPost, serviceReviewsPath, request, &review); err != nil {
		return nil, err
	}
	return &review, nil
}

// ConfirmManagedService confirms a review by its digest, which starts the
// install. Confirming an already confirmed review answers the same receipt.
func (client *Client) ConfirmManagedService(ctx context.Context, reviewID, digest string) (*ManagedServiceReview, error) {
	var review ManagedServiceReview
	if err := client.doRequest(ctx, http.MethodPost, serviceReviewsPath+"/"+url.PathEscape(reviewID)+"/confirm", serviceDigest{Digest: digest}, &review); err != nil {
		return nil, err
	}
	return &review, nil
}

func serviceRetirementsPath(serviceID string) string {
	return serviceInstancesPath + "/" + url.PathEscape(serviceID) + "/retirements"
}

// PlanManagedServiceRetirement plans the retirement of a service, which
// deletes its data once confirmed.
func (client *Client) PlanManagedServiceRetirement(ctx context.Context, serviceID string, request ManagedServiceRetirementRequest) (*ManagedServiceRetirementPlan, error) {
	if request.DisconnectConsumerIDs == nil {
		request.DisconnectConsumerIDs = []string{}
	}
	var retirement ManagedServiceRetirementPlan
	if err := client.doRequest(ctx, http.MethodPost, serviceRetirementsPath(serviceID), request, &retirement); err != nil {
		return nil, err
	}
	return &retirement, nil
}

// ConfirmManagedServiceRetirement confirms a retirement by its digest.
func (client *Client) ConfirmManagedServiceRetirement(ctx context.Context, serviceID, retirementID, digest string) (*ManagedServiceRetirementPlan, error) {
	var retirement ManagedServiceRetirementPlan
	path := serviceRetirementsPath(serviceID) + "/" + url.PathEscape(retirementID) + "/confirm"
	if err := client.doRequest(ctx, http.MethodPost, path, serviceDigest{Digest: digest}, &retirement); err != nil {
		return nil, err
	}
	return &retirement, nil
}

// GetManagedServiceRetirement reads one retirement.
func (client *Client) GetManagedServiceRetirement(ctx context.Context, serviceID, retirementID string) (*ManagedServiceRetirementPlan, error) {
	var retirement ManagedServiceRetirementPlan
	path := serviceRetirementsPath(serviceID) + "/" + url.PathEscape(retirementID)
	if err := client.doRequest(ctx, http.MethodGet, path, nil, &retirement); err != nil {
		return nil, err
	}
	return &retirement, nil
}
