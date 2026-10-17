// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

// TestAccProviderCredentialReadBackForbidden covers a token that may create
// credentials but not list them: the generic create cannot learn the new id,
// so its error names the import to run.
func TestAccProviderCredentialReadBackForbidden(t *testing.T) {
	fake := newCredentialFake(t)
	fake.listingForbidden = true
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: fake.providerBlock() + `
resource "ankra_hetzner_credential" "main" {
  name      = "hetzner-main"
  api_token = "token"
}
`,
				ExpectError: regexp.MustCompile(`(?s)credentials\.read.*ankra credentials list\s+--provider\s+hetzner.*terraform\s+import\s+ankra_hetzner_credential\.<name>\s+<id>`),
			},
		},
	})
	if fake.recordNamed("hetzner", "hetzner-main") == nil {
		t.Errorf("the platform should hold the created credential")
	}
}

// TestAccAwsCredentialReadBackForbidden covers the same failure on the AWS
// create, which returns the id: the id is kept in state, so the next apply
// replaces the credential instead of failing on its name.
func TestAccAwsCredentialReadBackForbidden(t *testing.T) {
	fake := newCredentialFake(t)
	fake.listingForbidden = true
	configuration := fake.providerBlock() + `
resource "ankra_aws_keys_credential" "main" {
  name              = "aws-keys"
  access_key_id     = "AKIA"
  secret_access_key = "secret"
}
`
	var orphanID string
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:      configuration,
				ExpectError: regexp.MustCompile("recorded in state"),
			},
			{
				PreConfig: func() {
					fake.listingForbidden = false
					if record := fake.recordNamed("aws", "aws-keys"); record != nil {
						orphanID = record.id
					}
				},
				Config: configuration,
				Check: func(_ *terraform.State) error {
					if orphanID == "" {
						return fmt.Errorf("the first create stored nothing")
					}
					if len(fake.deletes) != 1 || fake.deletes[0] != orphanID {
						return fmt.Errorf("deleted %v, want the tainted %s", fake.deletes, orphanID)
					}
					return nil
				},
			},
		},
	})
}

// TestAccSSHKeyCredentialImportWithFileKey imports a key whose configuration
// reads it with file(), so it ends in a newline the platform stripped. The
// first apply records the configured form without a replacement or any
// request beyond the listing, and the plan after it is empty. Terraform
// requires the plan to carry the configured value verbatim, so that one
// recording apply cannot be skipped.
func TestAccSSHKeyCredentialImportWithFileKey(t *testing.T) {
	fake := newCredentialFake(t)
	fake.records["00000000-0000-4000-8000-000000000077"] = &credentialFakeRecord{
		id: "00000000-0000-4000-8000-000000000077", name: "laptop", provider: "ssh-key",
		settings: map[string]any{"name": "laptop"}, publicKey: "ssh-ed25519 AAAAC3laptop me@laptop",
	}
	configuration := fake.providerBlock() + `
resource "ankra_ssh_key_credential" "laptop" {
  name           = "laptop"
  ssh_public_key = "ssh-ed25519 AAAAC3laptop me@laptop\n"
}
`
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:             configuration,
				ResourceName:       "ankra_ssh_key_credential.laptop",
				ImportState:        true,
				ImportStateId:      "00000000-0000-4000-8000-000000000077",
				ImportStatePersist: true,
			},
			{
				Config: configuration,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("ankra_ssh_key_credential.laptop", plancheck.ResourceActionUpdate),
					},
				},
				Check: func(_ *terraform.State) error {
					if len(fake.creates) != 0 || len(fake.deletes) != 0 {
						return fmt.Errorf("adopting the import sent creates %v and deletes %v", fake.creates, fake.deletes)
					}
					return nil
				},
			},
			{
				Config: configuration,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
		},
	})
}
