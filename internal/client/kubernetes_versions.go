// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package client

import (
	"context"
	"fmt"
	"net/http"
)

// Kubernetes distributions the platform publishes a version catalogue for.
const (
	DistributionKubeadm = "kubeadm"
	DistributionK3s     = "k3s"
)

// KubernetesVersionEntry is one supported version of a distribution.
type KubernetesVersionEntry struct {
	Version     string  `json:"version"`
	Channel     string  `json:"channel"`
	PublishedAt *string `json:"published_at"`
}

// KubernetesVersionCatalogue is GET /api/v1/clusters/{distribution}-versions.
// StableVersion is nil when the catalogue names none.
type KubernetesVersionCatalogue struct {
	StableVersion *string                  `json:"stable_version"`
	Versions      []KubernetesVersionEntry `json:"versions"`
}

// ListKubernetesVersions returns the supported versions of a distribution.
func (client *Client) ListKubernetesVersions(ctx context.Context, distribution string) (*KubernetesVersionCatalogue, error) {
	if distribution != DistributionKubeadm && distribution != DistributionK3s {
		return nil, fmt.Errorf("unknown distribution %q: expected %q or %q", distribution, DistributionKubeadm, DistributionK3s)
	}
	var catalogue KubernetesVersionCatalogue
	if err := client.doRequest(ctx, http.MethodGet, "/api/v1/clusters/"+distribution+"-versions", nil, &catalogue); err != nil {
		return nil, err
	}
	return &catalogue, nil
}
