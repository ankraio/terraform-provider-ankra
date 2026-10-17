// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

// credentialFakeRecord is one stored credential. settings holds what the
// create body carried, secrets included; the listing never shows them.
type credentialFakeRecord struct {
	id         string
	name       string
	provider   string
	settings   map[string]any
	publicKey  string
	scope      string
	authMethod string
	// listedState is the lifecycle state the listing reports; empty reads as "up".
	listedState string
}

// credentialFake holds credentials the way the platform does: provider
// creates answer without the new id, the listing is metadata only, names are
// unique per provider, an SSH key is stored trimmed, and the Proxmox
// Tailscale update clears the join on an explicit null.
type credentialFake struct {
	*routeFake
	records map[string]*credentialFakeRecord
	// creates and tailscaleUpdates record the bodies the provider sent.
	creates          []map[string]any
	tailscaleUpdates []map[string]json.RawMessage
	deletes          []string
	created          int
	// listingForbidden answers the listing 403, as for a token without
	// credentials.read.
	listingForbidden bool
}

func newCredentialFake(t *testing.T) *credentialFake {
	fake := &credentialFake{routeFake: newRouteFake(t), records: map[string]*credentialFakeRecord{}}

	store := func(provider string, body map[string]any) *credentialFakeRecord {
		fake.created++
		name, _ := body["name"].(string)
		record := &credentialFakeRecord{
			id:       fmt.Sprintf("00000000-0000-4000-8000-%012d", fake.created),
			name:     name,
			provider: provider,
			settings: body,
		}
		fake.records[record.id] = record
		return record
	}
	nameTaken := func(provider, name string) bool {
		for _, record := range fake.records {
			if record.provider == provider && record.name == name {
				return true
			}
		}
		return false
	}

	fake.handle("GET /api/v1/org/credentials", func(writer http.ResponseWriter, request *http.Request) {
		if fake.listingForbidden {
			writeJSON(t, writer, http.StatusForbidden, map[string]any{"detail": "Missing permission: credentials.read"})
			return
		}
		provider := request.URL.Query().Get("provider")
		ids := make([]string, 0, len(fake.records))
		for id := range fake.records {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		listing := []map[string]any{}
		for _, id := range ids {
			record := fake.records[id]
			if provider != "" && record.provider != provider {
				continue
			}
			listedState := record.listedState
			if listedState == "" {
				listedState = "up"
			}
			row := map[string]any{
				"id": record.id, "name": record.name, "display_name": nil, "provider": record.provider,
				"organisation_id": "11111111-1111-4111-8111-111111111111", "system": false, "available": true,
				"state": listedState, "created_at": "2026-10-17T10:00:00+00:00", "updated_at": "2026-10-17T10:00:00+00:00",
			}
			if record.provider == "ssh-key" {
				row["ssh_public_key"] = record.publicKey
				row["fingerprint"] = "SHA256:fake"
				row["key_type"] = strings.Fields(record.publicKey)[0]
				row["comment"] = ""
			}
			if record.provider == "aws" {
				row["auth_method"] = record.authMethod
				if record.scope != "" {
					row["scope"] = record.scope
				}
			}
			listing = append(listing, row)
		}
		writeJSON(t, writer, http.StatusOK, listing)
	})
	fake.handle("POST /api/v1/credentials/{provider}", func(writer http.ResponseWriter, request *http.Request) {
		var body map[string]any
		decodeJSON(t, request, &body)
		fake.creates = append(fake.creates, body)
		provider := request.PathValue("provider")
		name, _ := body["name"].(string)
		if nameTaken(provider, name) {
			writeJSON(t, writer, http.StatusBadRequest, map[string]any{"detail": map[string]any{"errors": []any{map[string]any{
				"name": name, "errors": []any{map[string]any{"key": "name", "message": "already exists"}},
			}}}})
			return
		}
		store(provider, body)
		writeJSON(t, writer, http.StatusCreated, map[string]any{"success": true, "errors": []any{}})
	})
	fake.handle("POST /api/v1/credentials/hetzner/ssh-key", func(writer http.ResponseWriter, request *http.Request) {
		var body map[string]any
		decodeJSON(t, request, &body)
		fake.creates = append(fake.creates, body)
		publicKey, hasPublicKey := body["ssh_public_key"].(string)
		generate, _ := body["generate"].(bool)
		if hasPublicKey == generate {
			writeJSON(t, writer, http.StatusBadRequest, map[string]any{"detail": "Either provide ssh_public_key or set generate=true"})
			return
		}
		var privateKey any
		if generate {
			publicKey = "ssh-rsa AAAAgenerated"
			privateKey = "-----BEGIN RSA PRIVATE KEY-----\nfake\n-----END RSA PRIVATE KEY-----\n"
		}
		record := store("ssh-key", body)
		record.publicKey = strings.TrimSpace(publicKey)
		writeJSON(t, writer, http.StatusCreated, map[string]any{
			"success": true, "id": record.id, "name": record.name, "private_key": privateKey, "errors": []any{},
		})
	})
	fake.handle("POST /api/v1/credentials/aws/{method}", func(writer http.ResponseWriter, request *http.Request) {
		var body map[string]any
		decodeJSON(t, request, &body)
		fake.creates = append(fake.creates, body)
		record := store("aws", body)
		record.authMethod = request.PathValue("method")
		if scope, ok := body["scope"].(string); ok {
			record.scope = scope
		} else if record.authMethod == "role" {
			record.scope = "cost"
		}
		writeJSON(t, writer, http.StatusCreated, map[string]any{
			"id": record.id, "name": record.name, "provider": "aws", "system": false, "available": true,
		})
	})
	fake.handle("PUT /api/v1/credentials/proxmox/{id}/tailscale", func(writer http.ResponseWriter, request *http.Request) {
		record, exists := fake.records[request.PathValue("id")]
		if !exists || record.provider != "proxmox" {
			writeJSON(t, writer, http.StatusNotFound, map[string]any{"detail": "Proxmox VE credential not found"})
			return
		}
		var body map[string]json.RawMessage
		decodeJSON(t, request, &body)
		fake.tailscaleUpdates = append(fake.tailscaleUpdates, body)
		raw, present := body["tailscale"]
		if !present || string(raw) == "null" {
			delete(record.settings, "tailscale")
		} else {
			var tailscale map[string]any
			_ = json.Unmarshal(raw, &tailscale)
			record.settings["tailscale"] = tailscale
		}
		// The listing changes during the apply, so an Update that wrote the
		// fresh listing over the planned values would fail with
		// "inconsistent result after apply".
		record.listedState = "reconciling"
		writeJSON(t, writer, http.StatusOK, map[string]any{"success": true})
	})
	fake.handle("DELETE /api/v1/org/credentials/{id}", func(writer http.ResponseWriter, request *http.Request) {
		id := request.PathValue("id")
		if _, exists := fake.records[id]; !exists {
			writeJSON(t, writer, http.StatusNotFound, map[string]any{"detail": "Credential not found"})
			return
		}
		fake.deletes = append(fake.deletes, id)
		delete(fake.records, id)
		writeJSON(t, writer, http.StatusOK, map[string]any{"id": id})
	})
	return fake
}

func (fake *credentialFake) recordNamed(provider, name string) *credentialFakeRecord {
	for _, record := range fake.records {
		if record.provider == provider && record.name == name {
			return record
		}
	}
	return nil
}

func (fake *credentialFake) lastCreate() map[string]any {
	if len(fake.creates) == 0 {
		return nil
	}
	return fake.creates[len(fake.creates)-1]
}

func credentialExpectBody(fake *credentialFake, expected map[string]any) resource.TestCheckFunc {
	return func(_ *terraform.State) error {
		body := fake.lastCreate()
		if body == nil {
			return fmt.Errorf("no create body was sent")
		}
		expectedJSON, _ := json.Marshal(expected)
		bodyJSON, _ := json.Marshal(body)
		if string(expectedJSON) != string(bodyJSON) {
			return fmt.Errorf("create body %s, want %s", bodyJSON, expectedJSON)
		}
		return nil
	}
}

func TestAccHetznerCredentialLifecycle(t *testing.T) {
	fake := newCredentialFake(t)
	configuration := func(token string) string {
		return fake.providerBlock() + fmt.Sprintf(`
resource "ankra_hetzner_credential" "main" {
  name      = "hetzner-main"
  api_token = %q
}

data "ankra_credentials" "hetzner" {
  credential_provider = "hetzner"
  depends_on = [ankra_hetzner_credential.main]
}

data "ankra_credential" "by_name" {
  credential_provider = "hetzner"
  name       = ankra_hetzner_credential.main.name
  depends_on = [ankra_hetzner_credential.main]
}
`, token)
	}
	var firstID string

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: configuration("token-one"),
				Check: resource.ComposeAggregateTestCheckFunc(
					credentialExpectBody(fake, map[string]any{"api_token": "token-one", "name": "hetzner-main"}),
					resource.TestCheckResourceAttrSet("ankra_hetzner_credential.main", "id"),
					resource.TestCheckResourceAttr("ankra_hetzner_credential.main", "available", "true"),
					resource.TestCheckResourceAttr("ankra_hetzner_credential.main", "state", "up"),
					resource.TestCheckResourceAttr("ankra_hetzner_credential.main", "api_token", "token-one"),
					resource.TestCheckResourceAttr("data.ankra_credentials.hetzner", "credentials.#", "1"),
					resource.TestCheckResourceAttrPair("data.ankra_credential.by_name", "id", "ankra_hetzner_credential.main", "id"),
					resource.TestCheckResourceAttr("data.ankra_credential.by_name", "credential_provider", "hetzner"),
					func(state *terraform.State) error {
						firstID = state.RootModule().Resources["ankra_hetzner_credential.main"].Primary.ID
						return nil
					},
				),
			},
			{
				// The token API cannot rotate a token in place: a new token
				// replaces the credential.
				Config: configuration("token-two"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("ankra_hetzner_credential.main", plancheck.ResourceActionDestroyBeforeCreate),
					},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					credentialExpectBody(fake, map[string]any{"api_token": "token-two", "name": "hetzner-main"}),
					func(state *terraform.State) error {
						currentID := state.RootModule().Resources["ankra_hetzner_credential.main"].Primary.ID
						if currentID == firstID {
							return fmt.Errorf("token change kept credential %s", currentID)
						}
						if len(fake.deletes) != 1 || fake.deletes[0] != firstID {
							return fmt.Errorf("deleted %v, want [%s]", fake.deletes, firstID)
						}
						return nil
					},
				),
			},
			{
				ResourceName:            "ankra_hetzner_credential.main",
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"api_token"},
			},
			{
				// Deleted outside Terraform: the next plan recreates it.
				PreConfig: func() {
					if record := fake.recordNamed("hetzner", "hetzner-main"); record != nil {
						delete(fake.records, record.id)
					}
				},
				Config: configuration("token-two"),
				Check: func(_ *terraform.State) error {
					if fake.recordNamed("hetzner", "hetzner-main") == nil {
						return fmt.Errorf("credential deleted outside Terraform was not recreated")
					}
					return nil
				},
			},
		},
	})
}

// TestAccHetznerCredentialImportAdopts imports a credential the platform
// already holds: the platform never returns its token, so the first apply
// must adopt the configured token rather than replace the credential, and a
// later token change must replace it again.
func TestAccHetznerCredentialImportAdopts(t *testing.T) {
	fake := newCredentialFake(t)
	fake.records["00000000-0000-4000-8000-000000000099"] = &credentialFakeRecord{
		id: "00000000-0000-4000-8000-000000000099", name: "existing", provider: "hetzner",
		settings: map[string]any{"name": "existing", "api_token": "unknown-to-terraform"},
	}
	configuration := func(token string) string {
		return fake.providerBlock() + fmt.Sprintf(`
resource "ankra_hetzner_credential" "existing" {
  name      = "existing"
  api_token = %q
}
`, token)
	}

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:             configuration("token-one"),
				ResourceName:       "ankra_hetzner_credential.existing",
				ImportState:        true,
				ImportStateId:      "00000000-0000-4000-8000-000000000099",
				ImportStatePersist: true,
				ImportStateCheck: func(states []*terraform.InstanceState) error {
					if token := states[0].Attributes["api_token"]; token != "" {
						return fmt.Errorf("import read back a token %q the platform never returns", token)
					}
					if name := states[0].Attributes["name"]; name != "existing" {
						return fmt.Errorf("import read name %q", name)
					}
					return nil
				},
			},
			{
				Config: configuration("token-one"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("ankra_hetzner_credential.existing", plancheck.ResourceActionUpdate),
					},
				},
				Check: func(_ *terraform.State) error {
					if len(fake.creates) != 0 || len(fake.deletes) != 0 {
						return fmt.Errorf("adopting an import sent creates %v and deletes %v", fake.creates, fake.deletes)
					}
					return nil
				},
			},
			{
				Config:   configuration("token-one"),
				PlanOnly: true,
			},
			{
				Config: configuration("token-two"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("ankra_hetzner_credential.existing", plancheck.ResourceActionDestroyBeforeCreate),
					},
				},
			},
		},
	})
}

func TestAccProxmoxCredentialTailscale(t *testing.T) {
	fake := newCredentialFake(t)
	base := `
resource "ankra_proxmox_credential" "lab" {
  name         = "lab"
  api_url      = "https://pve.example:8006"
  token_id     = "root@pam!ankra"
  token_secret = "secret"

  jumphost = {
    host        = "bastion.example"
    private_key = "jumphost-key"
  }
%s}
`
	withTailscale := func(advertise string) string {
		return fake.providerBlock() + fmt.Sprintf(base, fmt.Sprintf(`
  tailscale = {
    login_server     = "https://headscale.example"
    auth_key         = "tskey"
    advertise_routes = %q
  }
`, advertise))
	}
	withoutTailscale := fake.providerBlock() + fmt.Sprintf(base, "")
	var createdID string

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: withTailscale("10.20.0.0/16"),
				Check: resource.ComposeAggregateTestCheckFunc(
					credentialExpectBody(fake, map[string]any{
						"api_url":  "https://pve.example:8006",
						"jumphost": map[string]any{"host": "bastion.example", "port": 22, "private_key": "jumphost-key", "username": "root"},
						"name":     "lab",
						"tailscale": map[string]any{
							"accept_routes": false, "advertise_routes": "10.20.0.0/16",
							"auth_key": "tskey", "login_server": "https://headscale.example",
						},
						"tls_insecure": false,
						"token_id":     "root@pam!ankra",
						"token_secret": "secret",
					}),
					resource.TestCheckResourceAttr("ankra_proxmox_credential.lab", "jumphost.port", "22"),
					func(state *terraform.State) error {
						createdID = state.RootModule().Resources["ankra_proxmox_credential.lab"].Primary.ID
						return nil
					},
				),
			},
			{
				// The Tailscale join changes in place.
				Config: withTailscale("10.30.0.0/16"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("ankra_proxmox_credential.lab", plancheck.ResourceActionUpdate),
					},
				},
				Check: func(state *terraform.State) error {
					if currentID := state.RootModule().Resources["ankra_proxmox_credential.lab"].Primary.ID; currentID != createdID {
						return fmt.Errorf("tailscale change replaced the credential")
					}
					if len(fake.tailscaleUpdates) != 1 {
						return fmt.Errorf("sent %d tailscale updates, want 1", len(fake.tailscaleUpdates))
					}
					stored, _ := fake.records[createdID].settings["tailscale"].(map[string]any)
					if stored["advertise_routes"] != "10.30.0.0/16" || stored["auth_key"] != "tskey" {
						return fmt.Errorf("platform holds tailscale %v", stored)
					}
					return nil
				},
			},
			{
				// Removing the block clears the join with an explicit null.
				Config: withoutTailscale,
				Check: func(_ *terraform.State) error {
					if len(fake.tailscaleUpdates) != 2 {
						return fmt.Errorf("sent %d tailscale updates, want 2", len(fake.tailscaleUpdates))
					}
					if raw, present := fake.tailscaleUpdates[1]["tailscale"]; !present || string(raw) != "null" {
						return fmt.Errorf("clear sent tailscale %q, want an explicit null", raw)
					}
					if _, held := fake.records[createdID].settings["tailscale"]; held {
						return fmt.Errorf("platform still holds a tailscale join")
					}
					return nil
				},
			},
			{
				ResourceName:            "ankra_proxmox_credential.lab",
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"api_url", "token_id", "token_secret", "tls_insecure", "jumphost"},
			},
			{
				PreConfig: func() { delete(fake.records, createdID) },
				Config:    withoutTailscale,
				Check: func(_ *terraform.State) error {
					if fake.recordNamed("proxmox", "lab") == nil {
						return fmt.Errorf("credential deleted outside Terraform was not recreated")
					}
					return nil
				},
			},
		},
	})
}

func TestAccSSHKeyCredentialLifecycle(t *testing.T) {
	fake := newCredentialFake(t)
	configuration := fake.providerBlock() + `
resource "ankra_ssh_key_credential" "laptop" {
  name           = "laptop"
  ssh_public_key = "ssh-ed25519 AAAAC3laptop me@laptop\n"
}

resource "ankra_ssh_key_credential" "generated" {
  name     = "generated"
  generate = true
}
`
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: configuration,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ankra_ssh_key_credential.laptop", "fingerprint", "SHA256:fake"),
					resource.TestCheckResourceAttr("ankra_ssh_key_credential.laptop", "key_type", "ssh-ed25519"),
					resource.TestCheckNoResourceAttr("ankra_ssh_key_credential.laptop", "private_key"),
					resource.TestCheckResourceAttr("ankra_ssh_key_credential.generated", "ssh_public_key", "ssh-rsa AAAAgenerated"),
					resource.TestCheckResourceAttrSet("ankra_ssh_key_credential.generated", "private_key"),
					func(_ *terraform.State) error {
						for _, body := range fake.creates {
							if body["name"] == "generated" {
								if body["generate"] != true || body["ssh_public_key"] != nil {
									return fmt.Errorf("generate create sent %v", body)
								}
							}
							if body["name"] == "laptop" && body["generate"] != false {
								return fmt.Errorf("public key create sent %v", body)
							}
						}
						return nil
					},
				),
			},
			{
				// The platform stores the key trimmed; the configured trailing
				// newline must not plan a replacement.
				Config:             configuration,
				PlanOnly:           true,
				ExpectNonEmptyPlan: false,
			},
			{
				ResourceName:            "ankra_ssh_key_credential.laptop",
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"generate", "ssh_public_key"},
			},
			{
				PreConfig: func() {
					if record := fake.recordNamed("ssh-key", "laptop"); record != nil {
						delete(fake.records, record.id)
					}
				},
				Config: configuration,
				Check: func(_ *terraform.State) error {
					if fake.recordNamed("ssh-key", "laptop") == nil {
						return fmt.Errorf("credential deleted outside Terraform was not recreated")
					}
					return nil
				},
			},
		},
	})
}

func TestAccSSHKeyCredentialRejectsAmbiguousConfig(t *testing.T) {
	fake := newCredentialFake(t)
	for _, settings := range []string{
		"ssh_public_key = \"ssh-ed25519 AAAA\"\n  generate = true",
		"generate = false",
	} {
		resource.Test(t, resource.TestCase{
			ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config: fake.providerBlock() + fmt.Sprintf(`
resource "ankra_ssh_key_credential" "invalid" {
  name = "invalid"
  %s
}
`, settings),
					PlanOnly:    true,
					ExpectError: regexp.MustCompile("Set exactly one of ssh_public_key and generate"),
				},
			},
		})
	}
	if served := fake.served(); len(served) != 0 {
		t.Errorf("an invalid configuration reached the platform: %v", served)
	}
}

func TestAccAwsRoleCredentialRejectsUnknownScope(t *testing.T) {
	fake := newCredentialFake(t)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: fake.providerBlock() + `
resource "ankra_aws_role_credential" "invalid" {
  name        = "invalid"
  role_arn    = "arn:aws:iam::123456789012:role/ankra"
  external_id = "external-123"
  scope       = "everything"
}
`,
				PlanOnly:    true,
				ExpectError: regexp.MustCompile("value must be one of"),
			},
		},
	})
}

func TestAccAwsRoleCredentialLifecycle(t *testing.T) {
	fake := newCredentialFake(t)
	configuration := fake.providerBlock() + `
resource "ankra_aws_role_credential" "cost" {
  name        = "aws-cost"
  role_arn    = "arn:aws:iam::123456789012:role/ankra"
  external_id = "external-123"
}
`
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: configuration,
				Check: resource.ComposeAggregateTestCheckFunc(
					credentialExpectBody(fake, map[string]any{
						"external_id": "external-123", "name": "aws-cost",
						"region": "us-east-1", "role_arn": "arn:aws:iam::123456789012:role/ankra", "scope": "cost",
					}),
					resource.TestCheckResourceAttr("ankra_aws_role_credential.cost", "scope", "cost"),
					resource.TestCheckResourceAttr("ankra_aws_role_credential.cost", "region", "us-east-1"),
				),
			},
			{
				ResourceName:            "ankra_aws_role_credential.cost",
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"role_arn", "external_id", "region"},
			},
			{
				// Scope changed outside Terraform is detected from the listing.
				PreConfig: func() { fake.recordNamed("aws", "aws-cost").scope = "provisioning" },
				Config:    configuration,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("ankra_aws_role_credential.cost", plancheck.ResourceActionDestroyBeforeCreate),
					},
				},
			},
		},
	})
}

func TestAccAwsKeysCredentialRejectsRoleImport(t *testing.T) {
	fake := newCredentialFake(t)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: fake.providerBlock() + `
resource "ankra_aws_role_credential" "role" {
  name        = "aws-role"
  role_arn    = "arn:aws:iam::123456789012:role/ankra"
  external_id = "external-123"
}
`,
			},
			{
				Config: fake.providerBlock() + `
resource "ankra_aws_role_credential" "role" {
  name        = "aws-role"
  role_arn    = "arn:aws:iam::123456789012:role/ankra"
  external_id = "external-123"
}

resource "ankra_aws_keys_credential" "keys" {
  name              = "aws-role"
  access_key_id     = "AKIA"
  secret_access_key = "secret"
}
`,
				ResourceName: "ankra_aws_keys_credential.keys",
				ImportState:  true,
				ImportStateIdFunc: func(state *terraform.State) (string, error) {
					return state.RootModule().Resources["ankra_aws_role_credential.role"].Primary.ID, nil
				},
				ExpectError: regexp.MustCompile("Wrong AWS credential type"),
			},
		},
	})
}

// TestAccProviderCredentialCreateBodies checks the create body of every
// remaining provider credential against the platform's request fields.
func TestAccProviderCredentialCreateBodies(t *testing.T) {
	cases := []struct {
		resourceType string
		provider     string
		settings     string
		expected     map[string]any
	}{
		{"digitalocean_credential", "digitalocean", `api_token = "do-token"`, map[string]any{"api_token": "do-token"}},
		{"upcloud_credential", "upcloud", `api_token = "uc-token"`, map[string]any{"api_token": "uc-token"}},
		{
			"ovh_credential", "ovh",
			`
  application_key    = "ak"
  application_secret = "as"
  consumer_key       = "ck"
  project_id         = "project"`,
			map[string]any{"application_key": "ak", "application_secret": "as", "consumer_key": "ck", "project_id": "project"},
		},
		{
			"scaleway_credential", "scaleway",
			`
  access_key = "SCW"
  secret_key = "secret"
  project_id = "project"`,
			map[string]any{"access_key": "SCW", "secret_key": "secret", "project_id": "project"},
		},
		{
			"azure_credential", "azure",
			`
  subscription_id = "subscription"
  tenant_id       = "tenant"
  client_id       = "client"
  client_secret   = "secret"`,
			map[string]any{"subscription_id": "subscription", "tenant_id": "tenant", "client_id": "client", "client_secret": "secret"},
		},
		{"ankracloud_credential", "ankracloud", `api_token = "ac-token"`, map[string]any{"api_token": "ac-token"}},
		{
			"morpheus_credential", "morpheus",
			`
  api_url      = "https://morpheus.example"
  access_token = "token"
  tls_insecure = true`,
			map[string]any{"api_url": "https://morpheus.example", "access_token": "token", "tls_insecure": true},
		},
		{
			"aws_keys_credential", "aws",
			`
  access_key_id     = "AKIA"
  secret_access_key = "secret"
  region            = "eu-north-1"`,
			map[string]any{"access_key_id": "AKIA", "secret_access_key": "secret", "region": "eu-north-1"},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.resourceType, func(t *testing.T) {
			fake := newCredentialFake(t)
			expected := map[string]any{"name": "example"}
			for key, value := range testCase.expected {
				expected[key] = value
			}
			resource.Test(t, resource.TestCase{
				ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
				CheckDestroy: func(_ *terraform.State) error {
					if len(fake.records) != 0 {
						return fmt.Errorf("%d credentials left after destroy", len(fake.records))
					}
					return nil
				},
				Steps: []resource.TestStep{
					{
						Config: fake.providerBlock() + fmt.Sprintf(`
resource "ankra_%s" "example" {
  name = "example"
  %s
}
`, testCase.resourceType, testCase.settings),
						Check: resource.ComposeAggregateTestCheckFunc(
							credentialExpectBody(fake, expected),
							resource.TestCheckResourceAttrSet("ankra_"+testCase.resourceType+".example", "id"),
							func(_ *terraform.State) error {
								if fake.recordNamed(testCase.provider, "example") == nil {
									return fmt.Errorf("no %s credential was stored", testCase.provider)
								}
								return nil
							},
						),
					},
				},
			})
		})
	}
}
