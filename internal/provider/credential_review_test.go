// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"fmt"
	"net/http"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

// TestAccProviderCredentialReadBackForbidden covers a token that may create
// credentials but not list them, on a platform that answers the create
// without the new id: the create cannot learn the id, so its error names the
// import to run.
func TestAccProviderCredentialReadBackForbidden(t *testing.T) {
	fake := newCredentialFake(t)
	fake.listingForbidden = true
	fake.createOmitsID = true
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

// TestAccProviderCredentialCreateUsesTheAnsweredID pins that a create reads
// the new credential by the id the platform answers, not by its name: a
// credential of the same name in another project the listing also shows
// must not be picked up.
func TestAccProviderCredentialCreateUsesTheAnsweredID(t *testing.T) {
	fake := newCredentialFake(t)
	const decoyID = "00000000-0000-4000-8000-000000000000"
	fake.records[decoyID] = &credentialFakeRecord{
		id: decoyID, name: "hetzner-main", provider: "hetzner", settings: map[string]any{"name": "hetzner-main"},
	}
	fake.handle("POST /api/v1/credentials/hetzner", func(writer http.ResponseWriter, request *http.Request) {
		var body map[string]any
		decodeJSON(t, request, &body)
		fake.creates = append(fake.creates, body)
		const createdID = "00000000-0000-4000-8000-000000000001"
		fake.records[createdID] = &credentialFakeRecord{id: createdID, name: "hetzner-main", provider: "hetzner", settings: body}
		writeJSON(t, writer, http.StatusCreated, map[string]any{"success": true, "id": createdID, "name": "hetzner-main", "errors": []any{}})
	})
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
				Check: resource.TestCheckResourceAttr("ankra_hetzner_credential.main", "id", "00000000-0000-4000-8000-000000000001"),
			},
		},
	})
}

// TestAccProviderCredentialCreateOnAnOlderPlatform pins the fallback for a
// platform that answers a create without the id: the credential is found by
// its name.
func TestAccProviderCredentialCreateOnAnOlderPlatform(t *testing.T) {
	fake := newCredentialFake(t)
	fake.createOmitsID = true
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: fake.providerBlock() + `
resource "ankra_digitalocean_credential" "main" {
  name      = "do-main"
  api_token = "token"
}
`,
				Check: func(state *terraform.State) error {
					record := fake.recordNamed("digitalocean", "do-main")
					if record == nil || state.RootModule().Resources["ankra_digitalocean_credential.main"].Primary.ID != record.id {
						return fmt.Errorf("the credential was not found by name")
					}
					return nil
				},
			},
		},
	})
}

// TestAccProviderCredentialReadBackForbiddenKeepsTheAnsweredID covers a token
// that may create credentials but not list them, on a platform that answers
// the create with the id: the id is kept in state, so the next apply
// replaces the credential instead of failing on its name.
func TestAccProviderCredentialReadBackForbiddenKeepsTheAnsweredID(t *testing.T) {
	fake := newCredentialFake(t)
	fake.listingForbidden = true
	configuration := fake.providerBlock() + `
resource "ankra_hetzner_credential" "main" {
  name      = "hetzner-main"
  api_token = "token"
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
					if record := fake.recordNamed("hetzner", "hetzner-main"); record != nil {
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

// TestAccCredentialProjectSwitchKeepsTheCredential pins that moving the
// provider to another project, where the platform answers the credential as
// not found, neither plans a recreation (which would fail on the
// organisation-wide name anyway) nor lets destroy report success.
func TestAccCredentialProjectSwitchKeepsTheCredential(t *testing.T) {
	const firstProject = "aaaaaaaa-1111-4111-8111-111111111111"
	const secondProject = "bbbbbbbb-2222-4222-8222-222222222222"
	fake := newCredentialFake(t)
	inProject := func(project string) string {
		return fmt.Sprintf(`
provider "ankra" {
  token      = "test-token"
  base_url   = %q
  project_id = %q
}

resource "ankra_hetzner_credential" "main" {
  name      = "hetzner-main"
  api_token = "token"
}

resource "ankra_ssh_key_credential" "laptop" {
  name           = "laptop"
  ssh_public_key = "ssh-ed25519 AAAAC3laptop me@laptop"
}
`, fake.server.URL, project)
	}
	stillInFirstProject := func() error {
		if len(fake.records) != 2 {
			return fmt.Errorf("the platform holds %d credentials, want the original 2", len(fake.records))
		}
		for _, record := range fake.records {
			if record.project != firstProject {
				return fmt.Errorf("credential %s is in project %q", record.name, record.project)
			}
		}
		return nil
	}

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{Config: inProject(firstProject)},
			{
				Config:      inProject(secondProject),
				ExpectError: regexp.MustCompile(`Object not visible in the provider's current scope`),
			},
			{
				Config:      inProject(secondProject),
				Destroy:     true,
				ExpectError: regexp.MustCompile(`Object not visible in the provider's current scope`),
			},
			{
				Config: inProject(firstProject),
				Check: func(_ *terraform.State) error {
					if len(fake.deletes) != 0 {
						return fmt.Errorf("deleted %v under the wrong project", fake.deletes)
					}
					return stillInFirstProject()
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
