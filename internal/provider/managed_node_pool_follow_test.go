// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"fmt"
	"net/http"
	"regexp"
	"testing"

	testresource "github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

const poolReplaceShortTimeout = `  timeouts { update = "300ms" }`

// TestAccManagedNodePoolFollowsRunningReplacement pins how a replacement the
// platform reports "already in progress" is followed: another size's
// replacement fails fast, a followed attempt that fails is asked for again
// and then followed to its end, and a replaced pool that vanishes without its
// replacement fails fast.
func TestAccManagedNodePoolFollowsRunningReplacement(t *testing.T) {
	lanesFastPolling(t)
	fake := newPoolReplaceFake(t)
	medium := managedReplacementPoolName("workers", "s-4vcpu-8gb")
	address := "ankra_managed_node_pool.workers"
	testresource.Test(t, testresource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []testresource.TestStep{
			{Config: poolReplaceConfig(fake, "s-2vcpu-4gb", 1, "")},
			{
				PreConfig:   func() { fake.stalled = true },
				Config:      poolReplaceConfig(fake, "s-4vcpu-8gb", 1, poolReplaceShortTimeout),
				ExpectError: regexp.MustCompile(`(?s)had\s+not\s+finished`),
			},
			{
				// The replacement to s-4vcpu-8gb is still running and its
				// pool is listed; asking for another size fails at once.
				PreConfig: func() {
					fake.pools[medium] = map[string]any{"name": medium, "size": "s-4vcpu-8gb", "count": 1, "labels": map[string]any{}, "taints": []any{}}
				},
				Config:      poolReplaceConfig(fake, "s-8vcpu-16gb", 1, ""),
				ExpectError: regexp.MustCompile(`(?s)already\s+running.*set\s+size\s+back\s+to\s+s-4vcpu-8gb`),
			},
			{
				// The running attempt fails while it is followed: the
				// replacement is asked for again and the new attempt finishes.
				PreConfig: func() {
					delete(fake.pools, medium)
					fake.failOnConflict = true
					fake.replaceCalls = nil
				},
				Config: poolReplaceConfig(fake, "s-4vcpu-8gb", 1, ""),
				Check: testresource.ComposeAggregateTestCheckFunc(
					testresource.TestCheckResourceAttr(address, "current_pool_name", medium),
					lanesCheck(func() error {
						want := "workers -> " + medium + " (s-4vcpu-8gb)"
						return lanesExpect("replace calls", fake.replaceCalls, []string{want, want})
					}),
				),
			},
			{
				PreConfig: func() {
					fake.failOnConflict = false
					fake.stalled = true
				},
				Config:      poolReplaceConfig(fake, "s-8vcpu-16gb", 1, poolReplaceShortTimeout),
				ExpectError: regexp.MustCompile(`(?s)had\s+not\s+finished`),
			},
			{
				PreConfig:   func() { fake.dropOnConflict = true },
				Config:      poolReplaceConfig(fake, "s-8vcpu-16gb", 1, ""),
				ExpectError: regexp.MustCompile(`(?s)no\s+longer\s+listed`),
			},
		},
	})
}

// TestAccManagedNodePoolReplacementPlatformErrors pins the errors of the
// wait and of the route: a platform without the replace route is named as
// such; an operation the token cannot read is left for the listing; a
// listing that keeps failing ends the wait; and destroy during a running
// replacement deletes the replacement pool too.
func TestAccManagedNodePoolReplacementPlatformErrors(t *testing.T) {
	lanesFastPolling(t)
	fake := newPoolReplaceFake(t)
	medium := managedReplacementPoolName("workers", "s-4vcpu-8gb")
	large := managedReplacementPoolName("workers", "s-8vcpu-16gb")
	extraLarge := managedReplacementPoolName("workers", "s-16vcpu-32gb")
	address := "ankra_managed_node_pool.workers"
	testresource.Test(t, testresource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []testresource.TestStep{
			{Config: poolReplaceConfig(fake, "s-2vcpu-4gb", 1, "")},
			{
				PreConfig:   func() { fake.replaceStatus = http.StatusNotFound },
				Config:      poolReplaceConfig(fake, "s-4vcpu-8gb", 1, ""),
				ExpectError: regexp.MustCompile(`(?s)size changes are not supported.*does\s+not\s+support.*Upgrade\s+the\s+platform`),
			},
			{
				PreConfig: func() {
					fake.replaceStatus = 0
					fake.executionStatus = http.StatusForbidden
				},
				Config: poolReplaceConfig(fake, "s-4vcpu-8gb", 1, ""),
				Check:  testresource.TestCheckResourceAttr(address, "current_pool_name", medium),
			},
			{
				PreConfig: func() {
					fake.executionStatus = 0
					fake.failListingOnAccept = true
				},
				Config:      poolReplaceConfig(fake, "s-8vcpu-16gb", 1, ""),
				ExpectError: regexp.MustCompile(`(?s)listing\s+failed\s+4\s+times`),
			},
			{
				// The listing recovers and the replacement has finished: the
				// refresh finds it.
				PreConfig: func() { fake.listFailing, fake.failListingOnAccept = false, false },
				Config:    poolReplaceConfig(fake, "s-8vcpu-16gb", 1, ""),
				Check:     testresource.TestCheckResourceAttr(address, "current_pool_name", large),
			},
			{
				PreConfig:   func() { fake.stalled = true },
				Config:      poolReplaceConfig(fake, "s-16vcpu-32gb", 1, poolReplaceShortTimeout),
				ExpectError: regexp.MustCompile(`(?s)had\s+not\s+finished`),
			},
			{
				// The running replacement's pool is listed when destroy runs.
				PreConfig: func() {
					fake.pools[extraLarge] = map[string]any{"name": extraLarge, "size": "s-16vcpu-32gb", "count": 1, "labels": map[string]any{}, "taints": []any{}}
				},
				RefreshState:       true,
				ExpectNonEmptyPlan: true,
			},
		},
		CheckDestroy: func(*terraform.State) error {
			if err := lanesExpect("deletes", fake.deletes, []string{large, extraLarge}); err != nil {
				return err
			}
			return lanesExpect("pools left", len(fake.pools), 0)
		},
	})
}

// TestAccManagedNodePoolImportByReplacementName pins that a pool imported by
// the name of a replacement this resource created takes the configured name
// in place instead of planning a destroy.
func TestAccManagedNodePoolImportByReplacementName(t *testing.T) {
	fake := newPoolReplaceFake(t)
	medium := managedReplacementPoolName("workers", "s-4vcpu-8gb")
	fake.pools[medium] = map[string]any{"name": medium, "size": "S-4VCPU-8GB", "count": 1, "labels": map[string]any{}, "taints": []any{}}
	address := "ankra_managed_node_pool.workers"
	config := poolReplaceConfig(fake, "S-4VCPU-8GB", 1, "") + fmt.Sprintf(`
import {
  to = %s
  id = "doks/%s/%s"
}
`, address, poolReplaceClusterID, medium)
	testresource.Test(t, testresource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []testresource.TestStep{
			{
				Config: config,
				ConfigPlanChecks: testresource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction(address, plancheck.ResourceActionUpdate),
				}},
				Check: testresource.ComposeAggregateTestCheckFunc(
					testresource.TestCheckResourceAttr(address, "name", "workers"),
					testresource.TestCheckResourceAttr(address, "current_pool_name", medium),
				),
			},
			{Config: config, PlanOnly: true},
		},
		CheckDestroy: func(*terraform.State) error {
			return lanesExpect("deletes", fake.deletes, []string{medium})
		},
	})
}
