// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfversion"
)

const (
	credentialFakeClusterID     = "1f2e3d4c-5b6a-4978-8695-a4b3c2d1e0f9"
	credentialFakeIngestID      = "2a3b4c5d-6e7f-4809-9a1b-2c3d4e5f6a7b"
	credentialFakeDatasourceID  = "3b4c5d6e-7f80-4911-8a2b-3c4d5e6f7a8b"
	credentialFakeDestinationID = "4c5d6e7f-8091-4a22-9b3c-4d5e6f7a8b9c"
	credentialFakeFamilyID      = "5d6e7f80-91a2-4b33-8c4d-5e6f7a8b9c0d"
	credentialFakeGrantID       = "6e7f8091-a2b3-4c44-9d5e-6f7a8b9c0d1e"
	credentialFakeVersionID     = "7f8091a2-b3c4-4d55-8e6f-7a8b9c0d1e2f"
	credentialFakeRecipientID   = "8091a2b3-c4d5-4e66-9f7a-8b9c0d1e2f3a"
	credentialFakeDigest        = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
)

func TestAccAlertIngestCredentialLifecycle(t *testing.T) {
	fake := newRouteFake(t)
	var credential map[string]any
	var rebinds []map[string]any
	fake.handle("GET /api/v1/org/alerts/ingest-credentials", func(writer http.ResponseWriter, _ *http.Request) {
		items := []any{}
		if credential != nil {
			items = append(items, credential)
		}
		writeJSON(t, writer, http.StatusOK, map[string]any{"items": items})
	})
	fake.handle("POST /api/v1/org/alerts/ingest-credentials", func(writer http.ResponseWriter, request *http.Request) {
		var body map[string]any
		decodeJSON(t, request, &body)
		credential = map[string]any{
			"id": credentialFakeIngestID, "name": body["name"], "cluster_id": body["cluster_id"], "scope": "cluster",
			"enabled": body["enabled"], "created_at": "2026-11-13T10:00:00Z", "updated_at": "2026-11-13T10:00:00Z", "last_used_at": nil,
		}
		writeJSON(t, writer, http.StatusOK, map[string]any{
			"item": credential, "token": "aitk_secret", "webhook_path": "/webhooks/alerts/" + credentialFakeIngestID,
		})
	})
	fake.handle("PATCH /api/v1/org/alerts/ingest-credentials/{id}", func(writer http.ResponseWriter, request *http.Request) {
		var body map[string]any
		decodeJSON(t, request, &body)
		rebinds = append(rebinds, body)
		previous := credential["cluster_id"]
		credential["cluster_id"] = body["cluster_id"]
		if scope, set := body["scope"]; set {
			credential["scope"] = scope
		}
		writeJSON(t, writer, http.StatusOK, map[string]any{"item": credential, "previous_cluster_id": previous})
	})
	fake.handle("DELETE /api/v1/org/alerts/ingest-credentials/{id}", func(writer http.ResponseWriter, _ *http.Request) {
		credential = nil
		writeJSON(t, writer, http.StatusOK, map[string]any{"success": true, "message": "revoked"})
	})
	config := func(binding string) string {
		return fake.providerBlock() + fmt.Sprintf(`
resource "ankra_alert_ingest_credential" "alertmanager" {
  name = "prod-alertmanager"
%s
}
`, binding)
	}
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy: func(_ *terraform.State) error {
			if credential != nil {
				return fmt.Errorf("destroy must revoke the credential")
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: config(fmt.Sprintf("  cluster_id = %q\n  scope      = \"mixed\"", credentialFakeClusterID)),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ankra_alert_ingest_credential.alertmanager", "token", "aitk_secret"),
					resource.TestCheckResourceAttr("ankra_alert_ingest_credential.alertmanager", "scope", "mixed"),
					resource.TestCheckResourceAttr("ankra_alert_ingest_credential.alertmanager", "enabled", "true"),
					resource.TestCheckResourceAttr("ankra_alert_ingest_credential.alertmanager", "webhook_path", "/webhooks/alerts/"+credentialFakeIngestID),
				),
			},
			{
				Config: config(`  scope = "platform"`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckNoResourceAttr("ankra_alert_ingest_credential.alertmanager", "cluster_id"),
					resource.TestCheckResourceAttr("ankra_alert_ingest_credential.alertmanager", "token", "aitk_secret"),
					func(_ *terraform.State) error {
						last := rebinds[len(rebinds)-1]
						if clusterID, set := last["cluster_id"]; !set || clusterID != nil {
							return fmt.Errorf("unpinning must send an explicit null cluster_id, sent %v", last)
						}
						return nil
					},
				),
			},
			{
				ResourceName:            "ankra_alert_ingest_credential.alertmanager",
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"token"},
			},
		},
	})
}

func TestAccDatasourceCredentialIsWriteOnly(t *testing.T) {
	fake := newRouteFake(t)
	var stored map[string]any
	creates := 0
	fake.handle("GET /api/v1/credentials/datasource", func(writer http.ResponseWriter, _ *http.Request) {
		listed := []any{}
		if stored != nil {
			listed = append(listed, map[string]any{"id": credentialFakeDatasourceID, "name": stored["name"], "provider": "datasource"})
		}
		writeJSON(t, writer, http.StatusOK, listed)
	})
	fake.handle("POST /api/v1/credentials/datasource", func(writer http.ResponseWriter, request *http.Request) {
		decodeJSON(t, request, &stored)
		creates++
		writeJSON(t, writer, http.StatusCreated, map[string]any{"success": true, "id": credentialFakeDatasourceID, "name": stored["name"], "errors": []any{}})
	})
	fake.handle("DELETE /api/v1/org/credentials/{id}", func(writer http.ResponseWriter, _ *http.Request) {
		stored = nil
		writeJSON(t, writer, http.StatusOK, map[string]any{"id": credentialFakeDatasourceID})
	})
	config := func(version int) string {
		return fake.providerBlock() + fmt.Sprintf(`
resource "ankra_datasource_credential" "mimir" {
  name              = "mimir-reader"
  auth_scheme       = "basic"
  username          = "reader"
  password_wo       = "s3cret"
  secret_wo_version = %d
}
`, version)
	}
	resource.Test(t, resource.TestCase{
		TerraformVersionChecks:   []tfversion.TerraformVersionCheck{tfversion.SkipBelow(tfversion.Version1_11_0)},
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy: func(_ *terraform.State) error {
			if stored != nil {
				return fmt.Errorf("destroy must delete the credential")
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: config(1),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ankra_datasource_credential.mimir", "id", credentialFakeDatasourceID),
					resource.TestCheckNoResourceAttr("ankra_datasource_credential.mimir", "password_wo"),
					func(_ *terraform.State) error {
						if stored["password"] != "s3cret" || stored["username"] != "reader" {
							return fmt.Errorf("the create must send the write-only password, sent %v", stored)
						}
						return nil
					},
				),
			},
			{
				Config: config(2),
				Check: func(_ *terraform.State) error {
					if creates != 2 {
						return fmt.Errorf("a new secret_wo_version must replace the credential, created %d times", creates)
					}
					return nil
				},
			},
			{
				ResourceName:            "ankra_datasource_credential.mimir",
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"auth_scheme", "username", "secret_wo_version"},
			},
		},
	})
}

func TestAccSecurityEvidenceDestinationLifecycle(t *testing.T) {
	fake := newRouteFake(t)
	var destination map[string]any
	var sentCredential map[string]any
	fake.handle("GET /api/v1/org/security/evidence/destinations", func(writer http.ResponseWriter, _ *http.Request) {
		result := []any{}
		if destination != nil {
			result = append(result, destination)
		}
		writeJSON(t, writer, http.StatusOK, map[string]any{"result": result})
	})
	fake.handle("POST /api/v1/org/security/evidence/destinations", func(writer http.ResponseWriter, request *http.Request) {
		var body map[string]any
		decodeJSON(t, request, &body)
		configBody, _ := body["config"].(map[string]any)
		if address, _ := configBody["url"].(string); !strings.HasPrefix(address, "https://") {
			writeJSON(t, writer, http.StatusBadRequest, map[string]any{"detail": "Destination URLs must use https."})
			return
		}
		sentCredential, _ = body["credential"].(map[string]any)
		destination = map[string]any{
			"id": credentialFakeDestinationID, "organisation_id": "org", "name": body["name"], "kind": body["kind"], "config": body["config"],
			"enabled": true, "credential_configured": true, "created_at": "2026-11-13T10:00:00Z", "created_by": nil,
			"updated_at": "2026-11-13T10:00:00Z", "revoked_at": nil,
		}
		writeJSON(t, writer, http.StatusCreated, destination)
	})
	fake.handle("POST /api/v1/org/security/evidence/destinations/{id}/revoke", func(writer http.ResponseWriter, _ *http.Request) {
		destination["revoked_at"] = "2026-11-13T11:00:00Z"
		destination["enabled"] = false
		writeJSON(t, writer, http.StatusOK, destination)
	})
	config := func(url string) string {
		return fake.providerBlock() + fmt.Sprintf(`
resource "ankra_security_evidence_destination" "siem" {
  name                  = "wazuh-relay"
  kind                  = "webhook"
  config                = { url = %q }
  credential_wo         = { signing_secret = "whsec" }
  credential_wo_version = 1
}
`, url)
	}
	resource.Test(t, resource.TestCase{
		TerraformVersionChecks:   []tfversion.TerraformVersionCheck{tfversion.SkipBelow(tfversion.Version1_11_0)},
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy: func(_ *terraform.State) error {
			if destination == nil || destination["revoked_at"] == nil {
				return fmt.Errorf("destroy must revoke the destination")
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config:      config("http://siem.example"),
				ExpectError: regexp.MustCompile(`(?s)Unable to create the security evidence destination.*https`),
			},
			{
				Config: config("https://siem.example/evidence"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ankra_security_evidence_destination.siem", "credential_configured", "true"),
					resource.TestCheckNoResourceAttr("ankra_security_evidence_destination.siem", "config.document_id"),
					func(_ *terraform.State) error {
						if sentCredential["signing_secret"] != "whsec" {
							return fmt.Errorf("the create must send the write-only credential, sent %v", sentCredential)
						}
						return nil
					},
				),
			},
			{
				ResourceName:            "ankra_security_evidence_destination.siem",
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"credential_wo_version"},
			},
		},
	})
}

func TestAccServiceCredentialRotatesInPlace(t *testing.T) {
	fake := newRouteFake(t)
	var activeValueID any
	var values []map[string]any
	var grants []map[string]any
	shares := []string{}
	fake.handle("POST /api/v1/org/service-admission/credentials", func(writer http.ResponseWriter, request *http.Request) {
		var body map[string]any
		decodeJSON(t, request, &body)
		writeJSON(t, writer, http.StatusCreated, map[string]any{"id": credentialFakeFamilyID, "name": body["name"], "created_at": "2026-11-13T10:00:00Z", "active_value_id": nil})
	})
	fake.handle("GET /api/v1/org/service-admission/credentials/{id}", func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(t, writer, http.StatusOK, map[string]any{"id": credentialFakeFamilyID, "name": "billing-db", "created_at": "2026-11-13T10:00:00Z", "active_value_id": activeValueID})
	})
	fake.handle("POST /api/v1/org/service-admission/credentials/{id}/values", func(writer http.ResponseWriter, request *http.Request) {
		var body map[string]any
		decodeJSON(t, request, &body)
		if body["expected_value_id"] != activeValueID {
			writeJSON(t, writer, http.StatusConflict, map[string]any{"detail": "expected_value_id is stale"})
			return
		}
		values = append(values, body)
		activeValueID = fmt.Sprintf("9a000000-0000-4000-8000-00000000000%d", len(values))
		for _, grant := range grants {
			grant["revoked_at"] = "2026-11-13T11:00:00Z"
		}
		writeJSON(t, writer, http.StatusCreated, map[string]any{
			"value":    map[string]any{"id": activeValueID, "state": "active", "created_at": "2026-11-13T10:00:00Z", "activated_at": nil, "retired_at": nil, "retained": false},
			"replaced": []any{}, "granted": []any{},
		})
	})
	fake.handle("GET /api/v1/org/service-admission/credentials/{id}/grants", func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(t, writer, http.StatusOK, map[string]any{"items": grants, "next_cursor": nil})
	})
	fake.handle("POST /api/v1/org/service-admission/credential-grants", func(writer http.ResponseWriter, request *http.Request) {
		var body map[string]any
		decodeJSON(t, request, &body)
		body["id"] = fmt.Sprintf("%s-%d", credentialFakeGrantID[:30], len(grants))
		body["created_at"] = "2026-11-13T10:00:00Z"
		body["revoked_at"] = nil
		delete(body, "family_id")
		grants = append(grants, body)
		writeJSON(t, writer, http.StatusCreated, body)
	})
	fake.handle("DELETE /api/v1/org/service-admission/credential-grants/{id}", func(writer http.ResponseWriter, request *http.Request) {
		for _, grant := range grants {
			if grant["id"] == request.PathValue("id") {
				grant["revoked_at"] = "2026-11-13T12:00:00Z"
			}
		}
		writer.WriteHeader(http.StatusNoContent)
	})
	fake.handle("GET /api/v1/org/service-packages/{version}/shares", func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(t, writer, http.StatusOK, map[string]any{"organisation_ids": shares})
	})
	fake.handle("PUT /api/v1/org/service-packages/{version}/shares/{organisation}", func(writer http.ResponseWriter, request *http.Request) {
		shares = append(shares, request.PathValue("organisation"))
		writer.WriteHeader(http.StatusNoContent)
	})
	fake.handle("DELETE /api/v1/org/service-packages/{version}/shares/{organisation}", func(writer http.ResponseWriter, _ *http.Request) {
		shares = []string{}
		writer.WriteHeader(http.StatusNoContent)
	})
	config := func(version int) string {
		return fake.providerBlock() + fmt.Sprintf(`
resource "ankra_service_credential" "billing" {
  name             = "billing-db"
  value_wo         = "postgres://billing"
  value_wo_version = %d
}

resource "ankra_service_credential_grant" "billing" {
  family_id      = ankra_service_credential.billing.id
  value_id       = ankra_service_credential.billing.active_value_id
  package_digest = %q
  input_name     = "DATABASE_URL"
  mode           = "hosted"
}

resource "ankra_service_package_share" "partner" {
  version_id      = %q
  organisation_id = %q
}
`, version, credentialFakeDigest, credentialFakeVersionID, credentialFakeRecipientID)
	}
	resource.Test(t, resource.TestCase{
		TerraformVersionChecks:   []tfversion.TerraformVersionCheck{tfversion.SkipBelow(tfversion.Version1_11_0)},
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy: func(_ *terraform.State) error {
			if len(shares) != 0 {
				return fmt.Errorf("destroy must revoke the share")
			}
			for _, grant := range grants {
				if grant["revoked_at"] == nil {
					return fmt.Errorf("destroy must revoke the grant %v", grant["id"])
				}
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: config(1),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ankra_service_credential.billing", "active_value_id", "9a000000-0000-4000-8000-000000000001"),
					resource.TestCheckNoResourceAttr("ankra_service_credential.billing", "value_wo"),
					resource.TestCheckResourceAttr("ankra_service_package_share.partner", "id", credentialFakeVersionID+"/"+credentialFakeRecipientID),
				),
			},
			{
				Config: config(2),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ankra_service_credential.billing", "active_value_id", "9a000000-0000-4000-8000-000000000002"),
					resource.TestCheckResourceAttr("ankra_service_credential_grant.billing", "value_id", "9a000000-0000-4000-8000-000000000002"),
					func(_ *terraform.State) error {
						if len(values) != 2 || values[1]["value"] != "postgres://billing" {
							return fmt.Errorf("a new value_wo_version must write the value in place, wrote %v", values)
						}
						return nil
					},
				),
			},
			{
				ResourceName:      "ankra_service_package_share.partner",
				ImportState:       true,
				ImportStateId:     credentialFakeVersionID + "/" + credentialFakeRecipientID,
				ImportStateVerify: true,
			},
			{
				Config: fake.providerBlock() + `
resource "ankra_service_credential_grant" "bad" {
  family_id      = "f"
  value_id       = "v"
  package_digest = "latest"
  input_name     = "database_url"
  mode           = "hosted"
}
`,
				ExpectError: regexp.MustCompile(`package_digest`),
			},
			{
				Config: config(2),
			},
		},
	})
}
