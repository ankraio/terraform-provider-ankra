// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func TestAccSecurityPlanTimeValidation(t *testing.T) {
	fake := newSecurityFake(t)

	cases := []struct {
		config        string
		expectedError string
	}{
		{`
resource "ankra_security_disposition" "invalid" {
  occurrence_id = "not-a-uuid"
  disposition   = "acknowledged"
  reason        = "ok"
}
`, "is not a UUID"},
		{`
resource "ankra_security_disposition" "invalid" {
  occurrence_id = "11111111-1111-4111-8111-111111111111"
  disposition   = "accepted_risk"
  reason        = "ok"
}
`, "Missing review deadline"},
		{`
resource "ankra_security_disposition" "invalid" {
  occurrence_id = "11111111-1111-4111-8111-111111111111"
  disposition   = "Acknowledged"
  reason        = "ok"
}
`, "is not accepted"},
		{`
resource "ankra_security_disposition" "invalid" {
  occurrence_id = "11111111-1111-4111-8111-111111111111"
  disposition   = "acknowledged"
  reason        = "  "
}
`, "Invalid reason"},
		{`
resource "ankra_security_report_schedule" "invalid" {
  cluster_id = "5e3c2a10-7d4b-4f6e-9a21-3b4c5d6e7f80"
  frequency  = "daily"
  recipients = ["security@example.com"]
}
`, "is not accepted"},
		{`
resource "ankra_security_report_schedule" "invalid" {
  cluster_id = "5e3c2a10-7d4b-4f6e-9a21-3b4c5d6e7f80"
  frequency  = "weekly"
}
`, "Missing recipients"},
		{`
resource "ankra_security_report_schedule" "invalid" {
  cluster_id = "5e3c2a10-7d4b-4f6e-9a21-3b4c5d6e7f80"
  frequency  = "weekly"
  recipients = ["not an address"]
}
`, "Invalid recipient"},
	}
	for _, testCase := range cases {
		resource.Test(t, resource.TestCase{
			ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
			Steps: []resource.TestStep{{
				Config:      fake.providerBlock() + testCase.config,
				PlanOnly:    true,
				ExpectError: regexp.MustCompile(testCase.expectedError),
			}},
		})
	}
	if served := fake.served(); len(served) != 0 {
		t.Errorf("plan-time refusals still reached the platform: %v", served)
	}
}
