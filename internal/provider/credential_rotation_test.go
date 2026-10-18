// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

// TestAccCredentialRotationRefusedKeepsTheSecret pins that a secret the
// provider refuses fails the apply, sends no replacement, and leaves the
// state holding the secret the platform still uses, so the next plan asks
// for the rotation again.
func TestAccCredentialRotationRefusedKeepsTheSecret(t *testing.T) {
	fake := newCredentialFake(t)
	configuration := func(token string) string {
		return fake.providerBlock() + fmt.Sprintf(`
resource "ankra_upcloud_credential" "main" {
  name      = "upcloud-main"
  api_token = %q
}
`, token)
	}
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{Config: configuration("token-one")},
			{
				Config:      configuration(credentialFakeRefusedSecret),
				ExpectError: regexp.MustCompile(`(?s)provider refused.*keeps its current secret.*The provider refused the token.*Check the token's permissions`),
			},
			{
				Config: configuration("token-one"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				Check: func(_ *terraform.State) error {
					record := fake.recordNamed("upcloud", "upcloud-main")
					if record == nil || record.settings["api_token"] != "token-one" {
						return fmt.Errorf("the platform no longer holds token-one: %v", record)
					}
					if len(fake.creates) != 1 || len(fake.deletes) != 0 {
						return fmt.Errorf("a refused rotation sent creates %v and deletes %v", fake.creates, fake.deletes)
					}
					return nil
				},
			},
		},
	})
}

// TestAccCredentialRotationSendsTheSecretSet pins the rotation body: every
// configured rotatable field and nothing else, so the provider checks the
// pair together and no identity setting is sent; the jumphost and API URL
// stay as they are.
func TestAccCredentialRotationSendsTheSecretSet(t *testing.T) {
	fake := newCredentialFake(t)
	configuration := func(tokenSecret string) string {
		return fake.providerBlock() + fmt.Sprintf(`
resource "ankra_proxmox_credential" "lab" {
  name         = "lab"
  api_url      = "https://pve.example:8006"
  token_id     = "root@pam!ankra"
  token_secret = %q

  jumphost = {
    host        = "bastion.example"
    private_key = "jumphost-key"
  }
}

resource "ankra_ovh_credential" "ovh" {
  name               = "ovh"
  application_key    = "ak"
  application_secret = %q
  consumer_key       = "ck"
  project_id         = "project"
}
`, tokenSecret, tokenSecret)
	}
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{Config: configuration("secret-one")},
			{
				Config: configuration("secret-two"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("ankra_proxmox_credential.lab", plancheck.ResourceActionUpdate),
						plancheck.ExpectResourceAction("ankra_ovh_credential.ovh", plancheck.ResourceActionUpdate),
					},
				},
				Check: func(_ *terraform.State) error {
					if len(fake.rotations) != 2 {
						return fmt.Errorf("sent %d rotations, want 2: %v", len(fake.rotations), fake.rotations)
					}
					expected := map[string]string{
						"root@pam!ankra|secret-two": "proxmox",
						"ak|secret-two|ck":          "ovh",
					}
					for _, rotation := range fake.rotations {
						var key string
						if _, isProxmox := rotation["token_id"]; isProxmox {
							key = fmt.Sprintf("%v|%v", rotation["token_id"], rotation["token_secret"])
							if len(rotation) != 2 {
								return fmt.Errorf("proxmox rotation sent %v", rotation)
							}
						} else {
							key = fmt.Sprintf("%v|%v|%v", rotation["application_key"], rotation["application_secret"], rotation["consumer_key"])
							if len(rotation) != 3 {
								return fmt.Errorf("ovh rotation sent %v", rotation)
							}
						}
						if _, known := expected[key]; !known {
							return fmt.Errorf("unexpected rotation %v", rotation)
						}
					}
					if len(fake.creates) != 2 || len(fake.deletes) != 0 {
						return fmt.Errorf("rotation sent creates %v and deletes %v", fake.creates, fake.deletes)
					}
					return nil
				},
			},
		},
	})
}

// TestAccCredentialIdentityChangeReplaces pins that a setting the platform
// cannot change in place still replaces the credential.
func TestAccCredentialIdentityChangeReplaces(t *testing.T) {
	fake := newCredentialFake(t)
	configuration := func(projectID string) string {
		return fake.providerBlock() + fmt.Sprintf(`
resource "ankra_scaleway_credential" "main" {
  name       = "scaleway"
  access_key = "SCW"
  secret_key = "secret"
  project_id = %q
}
`, projectID)
	}
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{Config: configuration("project-one")},
			{
				Config: configuration("project-two"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("ankra_scaleway_credential.main", plancheck.ResourceActionDestroyBeforeCreate),
					},
				},
				Check: func(_ *terraform.State) error {
					if len(fake.rotations) != 0 {
						return fmt.Errorf("an identity change sent rotations %v", fake.rotations)
					}
					return nil
				},
			},
		},
	})
}

// TestAccCredentialDisplayName pins the display name: set at create and
// changed in place through the display-name route, a label changed in the
// portal shows as drift and is set back, and removing it from the
// configuration keeps the label without a request.
func TestAccCredentialDisplayName(t *testing.T) {
	fake := newCredentialFake(t)
	configuration := func(displayName string) string {
		attribute := ""
		if displayName != "" {
			attribute = fmt.Sprintf("display_name = %q", displayName)
		}
		return fake.providerBlock() + fmt.Sprintf(`
resource "ankra_hetzner_credential" "main" {
  name      = "hetzner-main"
  api_token = "token"
  %[1]s
}

resource "ankra_ssh_key_credential" "laptop" {
  name           = "laptop"
  ssh_public_key = "ssh-ed25519 AAAAC3laptop me@laptop"
  %[1]s
}
`, attribute)
	}
	expectLabels := func(label string, updates int) resource.TestCheckFunc {
		return func(_ *terraform.State) error {
			for _, record := range fake.records {
				if record.displayName == nil || *record.displayName != label {
					return fmt.Errorf("credential %s is labelled %v, want %q", record.name, record.displayName, label)
				}
			}
			if len(fake.displayNameUpdates) != updates {
				return fmt.Errorf("sent %d display name updates, want %d", len(fake.displayNameUpdates), updates)
			}
			if len(fake.creates) != 2 || len(fake.deletes) != 0 || len(fake.rotations) != 0 {
				return fmt.Errorf("a display name change sent creates %v, deletes %v and rotations %v", fake.creates, fake.deletes, fake.rotations)
			}
			return nil
		}
	}
	inPlace := resource.ConfigPlanChecks{
		PreApply: []plancheck.PlanCheck{
			plancheck.ExpectResourceAction("ankra_hetzner_credential.main", plancheck.ResourceActionUpdate),
			plancheck.ExpectResourceAction("ankra_ssh_key_credential.laptop", plancheck.ResourceActionUpdate),
		},
	}
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: configuration("Production"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ankra_hetzner_credential.main", "display_name", "Production"),
					resource.TestCheckResourceAttr("ankra_ssh_key_credential.laptop", "display_name", "Production"),
					expectLabels("Production", 2),
				),
			},
			{
				Config:           configuration("Staging"),
				ConfigPlanChecks: inPlace,
				Check:            expectLabels("Staging", 4),
			},
			{
				PreConfig: func() {
					relabelled := "Renamed in the portal"
					for _, record := range fake.records {
						record.displayName = &relabelled
					}
				},
				Config:           configuration("Staging"),
				ConfigPlanChecks: inPlace,
				Check:            expectLabels("Staging", 6),
			},
			{
				Config: configuration(""),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				Check: expectLabels("Staging", 6),
			},
		},
	})
}

// TestAccCredentialListedSettingsDrift pins drift detection on the
// connection settings the listing carries: a re-pointed credential plans a
// replacement, a setting the listing leaves out keeps the state, and the
// platform's own spelling of the configured value plans nothing.
func TestAccCredentialListedSettingsDrift(t *testing.T) {
	fake := newCredentialFake(t)
	configuration := fake.providerBlock() + `
resource "ankra_morpheus_credential" "morpheus" {
  name         = "morpheus"
  api_url      = "https://morpheus.example/"
  access_token = "token"
}

resource "ankra_ankracloud_credential" "default_endpoint" {
  name      = "default-endpoint"
  api_token = "token"
}

resource "ankra_ankracloud_credential" "own_endpoint" {
  name      = "own-endpoint"
  api_token = "token"
  endpoint  = "https://cloud.example.com/"
}

resource "ankra_aws_keys_credential" "aws" {
  name              = "aws"
  access_key_id     = "AKIA"
  secret_access_key = "secret"
}
`
	morpheus := func() *credentialFakeRecord { return fake.recordNamed("morpheus", "morpheus") }
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				PreConfig: func() { fake.listingOmitsSettings = false },
				Config:    configuration,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ankra_morpheus_credential.morpheus", "api_url", "https://morpheus.example/"),
					resource.TestCheckNoResourceAttr("ankra_ankracloud_credential.default_endpoint", "endpoint"),
					resource.TestCheckResourceAttr("ankra_ankracloud_credential.own_endpoint", "endpoint", "https://cloud.example.com/"),
				),
			},
			{
				// The platform lists the endpoint and URL in its own spelling.
				Config: configuration,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
			{
				// Re-pointed outside Terraform, while the listing does not say:
				// unknown is not a change.
				PreConfig: func() {
					fake.listingOmitsSettings = true
					morpheus().settings["api_url"] = "https://elsewhere.example"
					morpheus().settings["tls_insecure"] = true
				},
				Config: configuration,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
			{
				// Once listed, the re-pointed credential is replaced.
				PreConfig: func() { fake.listingOmitsSettings = false },
				Config:    configuration,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("ankra_morpheus_credential.morpheus", plancheck.ResourceActionDestroyBeforeCreate),
						plancheck.ExpectResourceAction("ankra_ankracloud_credential.default_endpoint", plancheck.ResourceActionNoop),
						plancheck.ExpectResourceAction("ankra_aws_keys_credential.aws", plancheck.ResourceActionNoop),
					},
				},
				Check: func(_ *terraform.State) error {
					record := morpheus()
					if record == nil || record.settings["api_url"] != "https://morpheus.example/" || record.settings["tls_insecure"] != false {
						return fmt.Errorf("the replacement holds %v", record)
					}
					return nil
				},
			},
			{
				// A region changed outside Terraform is detected too.
				PreConfig: func() { fake.recordNamed("aws", "aws").settings["region"] = "eu-north-1" },
				Config:    configuration,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("ankra_aws_keys_credential.aws", plancheck.ResourceActionDestroyBeforeCreate),
					},
				},
			},
		},
	})
}

// TestAccCredentialImportReadsListedSettings pins that an import reads the
// listed connection settings, so a configuration naming another API URL
// plans a replacement instead of adopting it unchecked.
func TestAccCredentialImportReadsListedSettings(t *testing.T) {
	fake := newCredentialFake(t)
	const credentialID = "00000000-0000-4000-8000-000000000055"
	fake.records[credentialID] = &credentialFakeRecord{
		id: credentialID, name: "lab", provider: "proxmox",
		settings: map[string]any{"name": "lab", "api_url": "https://pve.example:8006", "tls_insecure": true},
	}
	configuration := func(apiURL string) string {
		return fake.providerBlock() + fmt.Sprintf(`
resource "ankra_proxmox_credential" "lab" {
  name         = "lab"
  api_url      = %q
  token_id     = "root@pam!ankra"
  token_secret = "secret"
  tls_insecure = true
}
`, apiURL)
	}
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:             configuration("https://pve.example:8006"),
				ResourceName:       "ankra_proxmox_credential.lab",
				ImportState:        true,
				ImportStateId:      credentialID,
				ImportStatePersist: true,
				ImportStateCheck: func(states []*terraform.InstanceState) error {
					if states[0].Attributes["api_url"] != "https://pve.example:8006" || states[0].Attributes["tls_insecure"] != "true" {
						return fmt.Errorf("import read %v", states[0].Attributes)
					}
					return nil
				},
			},
			{
				Config:             configuration("https://other.example:8006"),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
			{
				Config: configuration("https://pve.example:8006"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("ankra_proxmox_credential.lab", plancheck.ResourceActionUpdate),
					},
				},
				Check: func(_ *terraform.State) error {
					if len(fake.creates) != 0 || len(fake.deletes) != 0 || len(fake.rotations) != 0 {
						return fmt.Errorf("adopting the import sent creates %v, deletes %v and rotations %v", fake.creates, fake.deletes, fake.rotations)
					}
					return nil
				},
			},
			{
				Config: configuration("https://other.example:8006"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("ankra_proxmox_credential.lab", plancheck.ResourceActionDestroyBeforeCreate),
					},
				},
			},
		},
	})
}

// TestAccCredentialPlanTimeValidation pins that an API URL carrying a user
// name or password, and a display name the platform would refuse or
// rewrite, fail the plan before any request.
func TestAccCredentialPlanTimeValidation(t *testing.T) {
	cases := map[string]struct {
		configuration string
		expected      string
	}{
		"proxmox api_url with userinfo": {`
resource "ankra_proxmox_credential" "invalid" {
  name         = "invalid"
  api_url      = "https://root:hunter2@pve.example:8006"
  token_id     = "root@pam!ankra"
  token_secret = "secret"
}
`, "user name or password"},
		"proxmox internal_api_url with a user": {`
resource "ankra_proxmox_credential" "invalid" {
  name             = "invalid"
  api_url          = "https://pve.example:8006"
  internal_api_url = "https://root@10.0.0.2:8006"
  token_id         = "root@pam!ankra"
  token_secret     = "secret"
}
`, "user name or password"},
		"morpheus api_url with userinfo": {`
resource "ankra_morpheus_credential" "invalid" {
  name         = "invalid"
  api_url      = "https://admin:hunter2@morpheus.example"
  access_token = "token"
}
`, "user name or password"},
		"empty display name": {`
resource "ankra_hetzner_credential" "invalid" {
  name         = "invalid"
  api_token    = "token"
  display_name = ""
}
`, "Invalid display name"},
		"display name with surrounding whitespace": {`
resource "ankra_ssh_key_credential" "invalid" {
  name           = "invalid"
  ssh_public_key = "ssh-ed25519 AAAA"
  display_name   = " Production "
}
`, "Invalid display name"},
		"display name too long": {fmt.Sprintf(`
resource "ankra_hetzner_credential" "invalid" {
  name         = "invalid"
  api_token    = "token"
  display_name = %q
}
`, strings.Repeat("x", 101)), "Invalid display name"},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			fake := newCredentialFake(t)
			resource.Test(t, resource.TestCase{
				ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
				Steps: []resource.TestStep{
					{
						Config:      fake.providerBlock() + testCase.configuration,
						PlanOnly:    true,
						ExpectError: regexp.MustCompile(testCase.expected),
					},
				},
			})
			if served := fake.served(); len(served) != 0 {
				t.Errorf("an invalid configuration reached the platform: %v", served)
			}
		})
	}
}

// TestAccCredentialUpgradeFromPreviousReleasePlansNoChange pins the upgrade
// path: credentials created by 0.3.0 plan no change once this provider reads
// them, including the settings the listing now carries and a display name
// set in the portal.
func TestAccCredentialUpgradeFromPreviousReleasePlansNoChange(t *testing.T) {
	fake := newCredentialFake(t)
	configuration := fake.providerBlock() + `
resource "ankra_hetzner_credential" "hetzner" {
  name      = "hetzner"
  api_token = "token"
}

resource "ankra_proxmox_credential" "proxmox" {
  name         = "proxmox"
  api_url      = "https://pve.example:8006/"
  token_id     = "root@pam!ankra"
  token_secret = "secret"
}

resource "ankra_ankracloud_credential" "ankracloud" {
  name      = "ankracloud"
  api_token = "token"
}

resource "ankra_ovh_credential" "ovh" {
  name               = "ovh"
  application_key    = "ak"
  application_secret = "as"
  consumer_key       = "ck"
  project_id         = "project"
}

resource "ankra_aws_role_credential" "aws" {
  name        = "aws"
  role_arn    = "arn:aws:iam::123456789012:role/ankra"
  external_id = "external-123"
}

resource "ankra_ssh_key_credential" "laptop" {
  name           = "laptop"
  ssh_public_key = "ssh-ed25519 AAAAC3laptop me@laptop"
}
`
	resource.Test(t, resource.TestCase{
		Steps: []resource.TestStep{
			{
				ExternalProviders: map[string]resource.ExternalProvider{
					"ankra": {Source: "ankraio/ankra", VersionConstraint: "0.3.0"},
				},
				Config: configuration,
			},
			{
				PreConfig: func() {
					label := "Set in the portal"
					fake.recordNamed("hetzner", "hetzner").displayName = &label
				},
				ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
				Config:                   configuration,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ankra_hetzner_credential.hetzner", "display_name", "Set in the portal"),
					func(_ *terraform.State) error {
						if len(fake.rotations) != 0 || len(fake.displayNameUpdates) != 0 || len(fake.deletes) != 0 {
							return fmt.Errorf("the upgrade sent rotations %v, display name updates %v and deletes %v", fake.rotations, fake.displayNameUpdates, fake.deletes)
						}
						return nil
					},
				),
			},
		},
	})
}
