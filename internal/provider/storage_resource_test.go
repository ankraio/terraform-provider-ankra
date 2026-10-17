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
	"time"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

// storageFake holds backup vaults and object-storage buckets the way the
// platform does (go/internal/backupapi, go/internal/objectstorageapi): a
// bring-your-own vault is created with 200 after a synchronous check, a
// provisioned vault and a bucket answer 202 in "provisioning" and turn
// "ready" on a later read, keys are never returned, delete answers 204 (404
// when gone) and refuses destroy_provider_resources on a bring-your-own
// vault with 400. Rows belong to the project the creating request carried
// in X-Ankra-Project-Id, and reads in another project answer 404.
type storageFake struct {
	*routeFake
	vaults  map[string]map[string]any
	buckets map[string]map[string]any
	// pendingReads is how many reads a provisioning row answers before it
	// turns ready.
	pendingReads map[string]int
	created      int
	bodies       []map[string]json.RawMessage
	deletes      []string
}

func newStorageFake(t *testing.T) *storageFake {
	fake := &storageFake{routeFake: newRouteFake(t), vaults: map[string]map[string]any{}, buckets: map[string]map[string]any{}, pendingReads: map[string]int{}}
	notFound := func(writer http.ResponseWriter, detail string) {
		writeJSON(t, writer, http.StatusNotFound, map[string]any{"detail": detail})
	}
	visible := func(rows map[string]map[string]any, request *http.Request, identifier string) map[string]any {
		row, exists := rows[identifier]
		if !exists || row["project"] != request.Header.Get("X-Ankra-Project-Id") {
			return nil
		}
		if fake.pendingReads[identifier] > 0 {
			fake.pendingReads[identifier]--
			if fake.pendingReads[identifier] == 0 {
				row["status"] = "ready"
			}
		}
		return row
	}
	public := func(row map[string]any) map[string]any {
		answer := map[string]any{}
		for key, value := range row {
			if key != "project" {
				answer[key] = value
			}
		}
		return answer
	}
	list := func(rows map[string]map[string]any, request *http.Request) []map[string]any {
		identifiers := make([]string, 0, len(rows))
		for identifier, row := range rows {
			if row["project"] == request.Header.Get("X-Ankra-Project-Id") {
				identifiers = append(identifiers, identifier)
			}
		}
		sort.Strings(identifiers)
		items := make([]map[string]any, 0, len(identifiers))
		for _, identifier := range identifiers {
			items = append(items, public(rows[identifier]))
		}
		return items
	}
	record := func(request *http.Request) map[string]any {
		var raw map[string]json.RawMessage
		decodeJSON(t, request, &raw)
		fake.bodies = append(fake.bodies, raw)
		body := map[string]any{}
		for key, value := range raw {
			var decoded any
			_ = json.Unmarshal(value, &decoded)
			body[key] = decoded
		}
		return body
	}
	newIdentifier := func(prefix string) string {
		fake.created++
		return fmt.Sprintf("%s-0000-0000-0000-%012d", prefix, fake.created)
	}

	fake.handle("GET /api/v1/org/backup-vaults", func(writer http.ResponseWriter, request *http.Request) {
		writeJSON(t, writer, http.StatusOK, map[string]any{"items": list(fake.vaults, request), "platform_default_in_use": false})
	})
	fake.handle("POST /api/v1/org/backup-vaults", func(writer http.ResponseWriter, request *http.Request) {
		body := record(request)
		provider, _ := body["provider"].(string)
		if provider == "" {
			provider = "other"
		}
		pathStyle, _ := body["path_style"].(bool)
		region, _ := body["region"].(string)
		identifier := newIdentifier("aaaaaaaa")
		fake.vaults[identifier] = map[string]any{"id": identifier, "name": body["name"], "kind": "customer_s3", "provider": provider,
			"endpoint": body["endpoint"], "region": region, "bucket": body["bucket"], "path_style": pathStyle, "status": "ready",
			"created_at": "2026-10-17T00:00:00Z", "updated_at": "2026-10-17T00:00:00Z", "project": request.Header.Get("X-Ankra-Project-Id")}
		writeJSON(t, writer, http.StatusOK, public(fake.vaults[identifier]))
	})
	fake.handle("POST /api/v1/org/backup-vaults/provision", func(writer http.ResponseWriter, request *http.Request) {
		body := record(request)
		identifier := newIdentifier("bbbbbbbb")
		bucket, _ := body["bucket"].(string)
		if bucket == "" {
			bucket = fmt.Sprintf("ankra-%s-x7k2", body["name"])
		}
		fake.vaults[identifier] = map[string]any{"id": identifier, "name": body["name"], "kind": "ankra_provisioned", "provider": "upcloud",
			"endpoint": "https://abc.upcloudobjects.com", "region": body["region"], "bucket": bucket, "path_style": true,
			"status": "provisioning", "provisioned_via_credential_id": body["credential_id"],
			"created_at": "2026-10-17T00:00:00Z", "updated_at": "2026-10-17T00:00:00Z", "project": request.Header.Get("X-Ankra-Project-Id")}
		fake.pendingReads[identifier] = 2
		writeJSON(t, writer, http.StatusAccepted, public(fake.vaults[identifier]))
	})
	fake.handle("GET /api/v1/org/backup-vaults/{id}", func(writer http.ResponseWriter, request *http.Request) {
		row := visible(fake.vaults, request, request.PathValue("id"))
		if row == nil {
			notFound(writer, "Backup vault not found.")
			return
		}
		writeJSON(t, writer, http.StatusOK, public(row))
	})
	fake.handle("DELETE /api/v1/org/backup-vaults/{id}", func(writer http.ResponseWriter, request *http.Request) {
		identifier := request.PathValue("id")
		fake.deletes = append(fake.deletes, request.URL.String())
		row, exists := fake.vaults[identifier]
		if !exists || row["project"] != request.Header.Get("X-Ankra-Project-Id") {
			notFound(writer, "Backup vault not found.")
			return
		}
		if request.URL.Query().Get("destroy_provider_resources") == "true" && row["kind"] != "ankra_provisioned" {
			writeJSON(t, writer, http.StatusBadRequest, map[string]any{"detail": "destroy_provider_resources applies only to Ankra-provisioned vaults."})
			return
		}
		delete(fake.vaults, identifier)
		writer.WriteHeader(http.StatusNoContent)
	})

	fake.handle("GET /api/v1/org/object-storage-buckets", func(writer http.ResponseWriter, request *http.Request) {
		writeJSON(t, writer, http.StatusOK, map[string]any{"items": list(fake.buckets, request)})
	})
	fake.handle("POST /api/v1/org/object-storage-buckets", func(writer http.ResponseWriter, request *http.Request) {
		body := record(request)
		identifier := newIdentifier("cccccccc")
		bucket, _ := body["bucket"].(string)
		if bucket == "" {
			bucket = fmt.Sprintf("ankra-%s-q9z1", body["name"])
		}
		region, _ := body["region"].(string)
		fake.buckets[identifier] = map[string]any{"id": identifier, "name": body["name"], "provider": "hetzner", "region": strings.ToLower(region),
			"bucket": bucket, "endpoint": "https://fsn1.your-objectstorage.com", "path_style": false, "credential_id": body["credential_id"],
			"status": "provisioning", "created_at": "2026-10-17T00:00:00Z", "updated_at": "2026-10-17T00:00:00Z",
			"project": request.Header.Get("X-Ankra-Project-Id")}
		fake.pendingReads[identifier] = 1
		writeJSON(t, writer, http.StatusAccepted, public(fake.buckets[identifier]))
	})
	fake.handle("GET /api/v1/org/object-storage-buckets/{id}", func(writer http.ResponseWriter, request *http.Request) {
		row := visible(fake.buckets, request, request.PathValue("id"))
		if row == nil {
			notFound(writer, "Object storage bucket not found.")
			return
		}
		writeJSON(t, writer, http.StatusOK, public(row))
	})
	fake.handle("DELETE /api/v1/org/object-storage-buckets/{id}", func(writer http.ResponseWriter, request *http.Request) {
		identifier := request.PathValue("id")
		fake.deletes = append(fake.deletes, request.URL.String())
		if row, exists := fake.buckets[identifier]; !exists || row["project"] != request.Header.Get("X-Ankra-Project-Id") {
			notFound(writer, "Object storage bucket not found.")
			return
		}
		delete(fake.buckets, identifier)
		writer.WriteHeader(http.StatusNoContent)
	})
	return fake
}

func storageFastPolling(t *testing.T) {
	previous := catalogueProvisioningPollInterval
	catalogueProvisioningPollInterval = 10 * time.Millisecond
	t.Cleanup(func() { catalogueProvisioningPollInterval = previous })
}

func (fake *storageFake) vaultNamed(name string) (string, map[string]any) {
	for identifier, row := range fake.vaults {
		if row["name"] == name {
			return identifier, row
		}
	}
	return "", nil
}

func TestAccBackupVaultLifecycle(t *testing.T) {
	storageFastPolling(t)
	fake := newStorageFake(t)
	config := func(destroyProvisioned bool) string {
		return fake.providerBlock() + fmt.Sprintf(`
resource "ankra_backup_vault" "own" {
  name              = "production-backups"
  endpoint          = "https://fsn1.your-objectstorage.com"
  bucket            = "acme-backups"
  region            = "fsn1"
  access_key_id     = "AKIA123"
  secret_access_key = "s3cret"
}

resource "ankra_backup_vault" "provisioned" {
  name                       = "offsite"
  credential_id              = "11111111-0000-0000-0000-000000000001"
  region                     = "europe-1"
  destroy_provider_resources = %t
}

data "ankra_backup_vaults" "all" {
  depends_on = [ankra_backup_vault.own, ankra_backup_vault.provisioned]
}
`, destroyProvisioned)
	}

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: config(false),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ankra_backup_vault.own", "kind", "customer_s3"),
					resource.TestCheckResourceAttr("ankra_backup_vault.own", "storage_provider", "other"),
					resource.TestCheckResourceAttr("ankra_backup_vault.own", "secret_access_key", "s3cret"),
					resource.TestCheckResourceAttr("ankra_backup_vault.provisioned", "status", "ready"),
					resource.TestCheckResourceAttr("ankra_backup_vault.provisioned", "bucket", "ankra-offsite-x7k2"),
					resource.TestCheckResourceAttr("ankra_backup_vault.provisioned", "endpoint", "https://abc.upcloudobjects.com"),
					resource.TestCheckResourceAttr("data.ankra_backup_vaults.all", "vaults.#", "2"),
					func(_ *terraform.State) error {
						for _, body := range fake.bodies {
							if _, present := body["credential_id"]; present {
								if _, sent := body["endpoint"]; sent {
									return fmt.Errorf("provision body carried an endpoint: %v", body)
								}
								continue
							}
							if string(body["secret_access_key"]) != `"s3cret"` || string(body["endpoint"]) != `"https://fsn1.your-objectstorage.com"` {
								return fmt.Errorf("create body was %v", body)
							}
							if _, present := body["path_style"]; present {
								return fmt.Errorf("an unset path_style was sent as %s", body["path_style"])
							}
						}
						return nil
					},
				),
			},
			{
				// Only the destroy behaviour changes: no replacement.
				Config: config(true),
				Check: func(_ *terraform.State) error {
					if len(fake.bodies) != 2 {
						return fmt.Errorf("changing destroy_provider_resources re-created a vault (%d creates)", len(fake.bodies))
					}
					return nil
				},
			},
			{
				ResourceName:            "ankra_backup_vault.own",
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"access_key_id", "secret_access_key"},
			},
			{
				ResourceName:            "ankra_backup_vault.provisioned",
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"destroy_provider_resources"},
			},
			{
				PreConfig: func() {
					identifier, _ := fake.vaultNamed("production-backups")
					delete(fake.vaults, identifier)
				},
				Config: config(true),
				Check: func(_ *terraform.State) error {
					if _, row := fake.vaultNamed("production-backups"); row == nil {
						return fmt.Errorf("vault deleted outside Terraform was not recreated")
					}
					return nil
				},
			},
		},
		CheckDestroy: func(_ *terraform.State) error {
			if len(fake.vaults) != 0 {
				return fmt.Errorf("vaults left after destroy: %v", fake.vaults)
			}
			destroyed := 0
			for _, deleted := range fake.deletes {
				if regexp.MustCompile(`destroy_provider_resources=true`).MatchString(deleted) {
					destroyed++
				}
			}
			if destroyed != 1 {
				return fmt.Errorf("expected exactly the provisioned vault to be destroyed with its bucket, got %v", fake.deletes)
			}
			return nil
		},
	})
}

// TestAccBackupVaultStaysInItsProject pins project scoping: a vault created
// in one project is not visible from another, so moving the provider to
// another project plans a new vault there instead of adopting anything.
func TestAccBackupVaultStaysInItsProject(t *testing.T) {
	fake := newStorageFake(t)
	config := func(project string) string {
		return fmt.Sprintf(`
provider "ankra" {
  token      = "test-token"
  base_url   = %q
  project_id = %q
}

resource "ankra_backup_vault" "own" {
  name              = "production-backups"
  endpoint          = "https://fsn1.your-objectstorage.com"
  bucket            = "acme-backups"
  access_key_id     = "AKIA123"
  secret_access_key = "s3cret"
}
`, fake.server.URL, project)
	}
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{Config: config("11111111-2222-3333-4444-555555555555")},
			{
				Config:             config("66666666-7777-8888-9999-000000000000"),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
		},
	})
}

func TestAccBackupVaultRejectsInvalidConfigAtPlan(t *testing.T) {
	fake := newRouteFake(t)
	for _, body := range []string{
		`name = "Production"
  endpoint = "https://s3.example.com"
  bucket = "b-1"
  access_key_id = "a"
  secret_access_key = "b"`,
		`name = "ok"
  bucket = "b-1"`,
		`name = "ok"
  endpoint = "https://s3.example.com/"
  bucket = "b-1"
  access_key_id = "a"
  secret_access_key = "b"`,
		`name = "ok"
  endpoint = "https://s3.example.com"
  bucket = "b-1"
  access_key_id = "a"
  secret_access_key = "b"
  destroy_provider_resources = true`,
		`name = "ok"
  credential_id = "11111111-0000-0000-0000-000000000001"`,
	} {
		resource.Test(t, resource.TestCase{
			ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
			Steps: []resource.TestStep{{
				Config:      fake.providerBlock() + "resource \"ankra_backup_vault\" \"bad\" {\n  " + body + "\n}\n",
				PlanOnly:    true,
				ExpectError: regexp.MustCompile(`Invalid value|Missing|Bucket not provisioned by Ankra`),
			}},
		})
	}
	if served := fake.served(); len(served) != 0 {
		t.Errorf("a refused configuration still reached the platform: %v", served)
	}
}

func TestAccObjectStorageBucketLifecycle(t *testing.T) {
	storageFastPolling(t)
	fake := newStorageFake(t)
	config := fake.providerBlock() + `
resource "ankra_object_storage_bucket" "assets" {
  name          = "registry-storage"
  credential_id = "22222222-0000-0000-0000-000000000002"
  region        = "FSN1"
}

data "ankra_object_storage_buckets" "all" {
  depends_on = [ankra_object_storage_bucket.assets]
}
`
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: config,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ankra_object_storage_bucket.assets", "status", "ready"),
					// The platform lowercases the region; state keeps the configured spelling.
					resource.TestCheckResourceAttr("ankra_object_storage_bucket.assets", "region", "FSN1"),
					resource.TestCheckResourceAttr("ankra_object_storage_bucket.assets", "bucket", "ankra-registry-storage-q9z1"),
					resource.TestCheckResourceAttr("data.ankra_object_storage_buckets.all", "buckets.0.region", "fsn1"),
					func(_ *terraform.State) error {
						body := fake.bodies[0]
						for _, key := range []string{"bucket", "access_key_id", "secret_access_key"} {
							if _, present := body[key]; present {
								return fmt.Errorf("an unset %s was sent as %s", key, body[key])
							}
						}
						return nil
					},
				),
			},
			{
				ResourceName:            "ankra_object_storage_bucket.assets",
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"region"},
			},
			{
				PreConfig: func() {
					for identifier := range fake.buckets {
						delete(fake.buckets, identifier)
					}
				},
				Config: config,
				Check: func(_ *terraform.State) error {
					if len(fake.buckets) != 1 {
						return fmt.Errorf("bucket deleted outside Terraform was not recreated")
					}
					return nil
				},
			},
		},
		CheckDestroy: func(_ *terraform.State) error {
			if len(fake.buckets) != 0 {
				return fmt.Errorf("buckets left after destroy")
			}
			last := fake.deletes[len(fake.deletes)-1]
			if regexp.MustCompile(`destroy_provider_resources`).MatchString(last) {
				return fmt.Errorf("destroy asked to destroy the provider bucket by default: %s", last)
			}
			return nil
		},
	})
}
