// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"testing"

	"ankra.io/terraform-provider-ankra/internal/client"
)

func TestSecurityScopeOfSelectorNamesEveryScope(t *testing.T) {
	cases := map[string]client.SecurityDispositionSelector{
		securityScopeOrganisationAddon: {AddonSlug: "grafana"},
		securityScopeNamespace:         {ClusterID: "cluster-1", WorkloadNamespace: "crew"},
		securityScopeWorkload:          {ClusterID: "cluster-1", WorkloadKind: "deployment", WorkloadNamespace: "crew", WorkloadName: "crewai"},
		securityScopeImage:             {ImageDigest: "sha256:abc"},
	}
	for want, selector := range cases {
		if got := securityScopeOfSelector(selector); got != want {
			t.Fatalf("scope of %+v = %q, want %q", selector, got, want)
		}
	}
}
