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
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

const dnsFakeZone = "acme.ankra.cc"

// dnsFake holds the organisation DNS surfaces the way the platform does:
// organisation records (go/internal/dnsrecordsapi: create 200 with the FQDN,
// PATCH ttl absent keeps and null resets, delete 204 marks the row deleting),
// organisation-wide custom zones (upsert on declare), the root domain on the
// ai-environment settings (partial PUT), DNS and Cloudflare credentials
// (create 201, secrets never returned, the Cloudflare listing carries no id,
// deleted through the organisation credential route), and Cloudflare records
// (create 201, a bare label qualified with the zone, an empty comment
// stamped "Managed by Ankra", PATCH fields absent kept).
type dnsFake struct {
	*routeFake
	records            map[string]map[string]any
	customZones        map[string]map[string]any
	rootDomain         any
	credentials        map[string]map[string]any
	cloudflareRecords  map[string]map[string]any
	created            int
	recordPatches      []map[string]json.RawMessage
	environmentBodies  []map[string]json.RawMessage
	cloudflarePatches  []map[string]json.RawMessage
	credentialDeletes  []string
	cloudflareCreateIn []map[string]any
}

func newDNSFake(t *testing.T) *dnsFake {
	fake := &dnsFake{
		routeFake:         newRouteFake(t),
		records:           map[string]map[string]any{},
		customZones:       map[string]map[string]any{},
		credentials:       map[string]map[string]any{},
		cloudflareRecords: map[string]map[string]any{},
	}
	nextID := func(prefix int) string {
		fake.created++
		return fmt.Sprintf("%08d-0000-0000-0000-%012d", prefix, fake.created)
	}
	notFound := func(writer http.ResponseWriter, detail string) {
		writeJSON(t, writer, http.StatusNotFound, map[string]any{"detail": detail})
	}

	fake.handle("GET /api/v1/org/dns/zone", func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(t, writer, http.StatusOK, map[string]any{"fqdn": dnsFakeZone, "state": "active"})
	})
	fake.handle("GET /api/v1/org/dns/records", func(writer http.ResponseWriter, _ *http.Request) {
		items := []map[string]any{}
		for _, row := range fake.records {
			items = append(items, row)
		}
		sort.Slice(items, func(left, right int) bool { return dnsFakeText(items[left]["id"]) < dnsFakeText(items[right]["id"]) })
		writeJSON(t, writer, http.StatusOK, map[string]any{"items": items})
	})
	fake.handle("POST /api/v1/org/dns/records", func(writer http.ResponseWriter, request *http.Request) {
		var body map[string]any
		decodeJSON(t, request, &body)
		row := map[string]any{
			"id": nextID(1), "name": strings.ToLower(dnsFakeText(body["name"])) + "." + dnsFakeZone,
			"record_type": strings.ToUpper(dnsFakeText(body["record_type"])), "content": body["content"],
			"state": "pending", "created_by": "user", "created_at": "2026-10-17T00:00:00Z", "updated_at": "2026-10-17T00:00:00Z",
		}
		if ttl, set := body["ttl"]; set && ttl != nil {
			row["ttl"] = ttl
		}
		fake.records[dnsFakeText(row["id"])] = row
		writeJSON(t, writer, http.StatusOK, row)
	})
	fake.handle("PATCH /api/v1/org/dns/records/{id}", func(writer http.ResponseWriter, request *http.Request) {
		row, exists := fake.records[request.PathValue("id")]
		if !exists {
			notFound(writer, "DNS record not found.")
			return
		}
		var body map[string]json.RawMessage
		decodeJSON(t, request, &body)
		fake.recordPatches = append(fake.recordPatches, body)
		row["content"] = alertFakeString(body["content"])
		if raw, present := body["ttl"]; present {
			var ttl any
			_ = json.Unmarshal(raw, &ttl)
			if ttl == nil {
				delete(row, "ttl")
			} else {
				row["ttl"] = ttl
			}
		}
		row["state"] = "pending"
		writeJSON(t, writer, http.StatusOK, row)
	})
	fake.handle("DELETE /api/v1/org/dns/records/{id}", func(writer http.ResponseWriter, request *http.Request) {
		row, exists := fake.records[request.PathValue("id")]
		if !exists {
			notFound(writer, "DNS record not found.")
			return
		}
		row["state"] = "deleting"
		writer.WriteHeader(http.StatusNoContent)
	})

	fake.handle("GET /api/v1/org/custom-dns-zones", func(writer http.ResponseWriter, _ *http.Request) {
		zones := []map[string]any{}
		for _, row := range fake.customZones {
			zones = append(zones, row)
		}
		writeJSON(t, writer, http.StatusOK, map[string]any{"success": true, "zones": zones})
	})
	fake.handle("POST /api/v1/org/custom-dns-zones", func(writer http.ResponseWriter, request *http.Request) {
		var body map[string]any
		decodeJSON(t, request, &body)
		zone := strings.TrimSuffix(strings.ToLower(dnsFakeText(body["zone"])), ".")
		credentialName := dnsFakeText(body["credential_name"])
		family := ""
		for _, credential := range fake.credentials {
			if credential["name"] == credentialName {
				family = map[string]string{"dns": "webhook", "cloudflare": "cloudflare"}[dnsFakeText(credential["provider"])]
			}
		}
		if family == "" {
			writeJSON(t, writer, http.StatusBadRequest, map[string]any{"detail": "credential not found"})
			return
		}
		if asserted, set := body["provider"]; set && asserted != family {
			writeJSON(t, writer, http.StatusBadRequest, map[string]any{"detail": "credential family mismatch"})
			return
		}
		row := map[string]any{"zone": zone, "credential_name": credentialName, "provider": family, "policy": "upsert-only"}
		fake.customZones[zone] = row
		writeJSON(t, writer, http.StatusOK, row)
	})
	fake.handle("DELETE /api/v1/org/custom-dns-zones/{zone}", func(writer http.ResponseWriter, request *http.Request) {
		if _, exists := fake.customZones[request.PathValue("zone")]; !exists {
			notFound(writer, "Zone is not declared for the organisation")
			return
		}
		delete(fake.customZones, request.PathValue("zone"))
		writeJSON(t, writer, http.StatusOK, map[string]any{"success": true, "zone": request.PathValue("zone")})
	})

	environment := func() map[string]any {
		return map[string]any{"dns_root_domain": fake.rootDomain, "dns_root_domain_default": "ankra.cc", "staging_cluster_id": nil}
	}
	fake.handle("GET /api/v1/org/ai-environment", func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(t, writer, http.StatusOK, environment())
	})
	fake.handle("PUT /api/v1/org/ai-environment", func(writer http.ResponseWriter, request *http.Request) {
		var body map[string]json.RawMessage
		decodeJSON(t, request, &body)
		fake.environmentBodies = append(fake.environmentBodies, body)
		if raw, present := body["dns_root_domain"]; present {
			var domain any
			_ = json.Unmarshal(raw, &domain)
			fake.rootDomain = domain
		}
		writeJSON(t, writer, http.StatusOK, environment())
	})

	fake.handle("GET /api/v1/credentials/dns", func(writer http.ResponseWriter, _ *http.Request) {
		listing := []map[string]any{}
		for _, row := range fake.credentials {
			if row["provider"] == "dns" {
				listing = append(listing, map[string]any{"id": row["id"], "name": row["name"], "provider": "dns", "created_at": "2026-10-17T00:00:00Z"})
			}
		}
		writeJSON(t, writer, http.StatusOK, listing)
	})
	fake.handle("POST /api/v1/credentials/dns", func(writer http.ResponseWriter, request *http.Request) {
		var body map[string]any
		decodeJSON(t, request, &body)
		if fake.credentialNamed(dnsFakeText(body["name"])) != nil {
			writeJSON(t, writer, http.StatusBadRequest, map[string]any{"detail": "A credential with this name already exists"})
			return
		}
		row := map[string]any{"id": nextID(2), "name": body["name"], "provider": "dns", "secret": body["webhook_provider_url"]}
		fake.credentials[dnsFakeText(row["id"])] = row
		writeJSON(t, writer, http.StatusCreated, map[string]any{"success": true, "id": row["id"], "name": row["name"], "errors": []any{}})
	})
	fake.handle("GET /api/v1/org/credentials", func(writer http.ResponseWriter, request *http.Request) {
		listing := []map[string]any{}
		for _, row := range fake.credentials {
			if row["provider"] == request.URL.Query().Get("provider") {
				listing = append(listing, map[string]any{"id": row["id"], "name": row["name"], "provider": row["provider"]})
			}
		}
		writeJSON(t, writer, http.StatusOK, listing)
	})
	fake.handle("DELETE /api/v1/org/credentials/{id}", func(writer http.ResponseWriter, request *http.Request) {
		if _, exists := fake.credentials[request.PathValue("id")]; !exists {
			notFound(writer, "Credential not found")
			return
		}
		fake.credentialDeletes = append(fake.credentialDeletes, request.PathValue("id"))
		delete(fake.credentials, request.PathValue("id"))
		writeJSON(t, writer, http.StatusOK, map[string]any{"id": request.PathValue("id")})
	})

	fake.handle("GET /api/v1/org/cloudflare/credentials", func(writer http.ResponseWriter, _ *http.Request) {
		items := []map[string]any{}
		for _, row := range fake.credentials {
			if row["provider"] == "cloudflare" {
				items = append(items, map[string]any{
					"name": row["name"], "account_id": row["account_id"], "account_name": "Acme", "token_id": "tok-1",
					"token_kind": "user", "is_expired": false, "state": "verified", "needs_reconnect": false, "created_at": "2026-10-17T00:00:00Z",
				})
			}
		}
		writeJSON(t, writer, http.StatusOK, map[string]any{"items": items})
	})
	fake.handle("POST /api/v1/org/cloudflare/credentials", func(writer http.ResponseWriter, request *http.Request) {
		var body map[string]any
		decodeJSON(t, request, &body)
		accountID := dnsFakeText(body["account_id"])
		if accountID == "" {
			accountID = "account-resolved"
		}
		row := map[string]any{"id": nextID(3), "name": body["name"], "provider": "cloudflare", "secret": body["api_token"], "account_id": accountID}
		fake.credentials[dnsFakeText(row["id"])] = row
		writeJSON(t, writer, http.StatusCreated, map[string]any{
			"success": true, "id": row["id"], "name": row["name"], "errors": []any{},
			"verification": map[string]any{"status": "active", "is_active": true, "zone_count": 1, "zone_names": []string{"example.com"}},
		})
	})
	fake.handle("GET /api/v1/org/cloudflare/domains", func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(t, writer, http.StatusOK, map[string]any{"is_truncated": false, "items": []map[string]any{{
			"id": "zone-1", "name": "example.com", "status": "active", "is_active": true, "paused": false, "type": "full",
			"credential_name": "cloudflare", "account_id": "account-resolved", "name_servers": []string{"ada.ns.cloudflare.com"},
		}}})
	})
	fake.handle("GET /api/v1/org/cloudflare/domains/{zone}/records", func(writer http.ResponseWriter, request *http.Request) {
		query := request.URL.Query()
		items := []map[string]any{}
		for _, row := range fake.cloudflareRecords {
			if row["zone_id"] != request.PathValue("zone") {
				continue
			}
			if name := query.Get("name"); name != "" && row["name"] != name {
				continue
			}
			if recordType := query.Get("record_type"); recordType != "" && row["record_type"] != recordType {
				continue
			}
			items = append(items, row)
		}
		writeJSON(t, writer, http.StatusOK, map[string]any{"domain": map[string]any{"id": request.PathValue("zone")}, "items": items, "is_truncated": false})
	})
	fake.handle("POST /api/v1/org/cloudflare/domains/{zone}/records", func(writer http.ResponseWriter, request *http.Request) {
		var body map[string]any
		decodeJSON(t, request, &body)
		fake.cloudflareCreateIn = append(fake.cloudflareCreateIn, body)
		name := dnsFakeText(body["name"])
		if !strings.HasSuffix(name, "example.com") {
			name += ".example.com"
		}
		comment := dnsFakeText(body["comment"])
		if comment == "" {
			comment = "Managed by Ankra"
		}
		row := map[string]any{
			"id": nextID(4), "zone_id": request.PathValue("zone"), "zone_name": "example.com", "name": name,
			"record_type": strings.ToUpper(dnsFakeText(body["record_type"])), "content": body["content"], "ttl": body["ttl"],
			"is_ttl_automatic": body["ttl"] == float64(1), "proxied": body["proxied"], "proxiable": true,
			"comment": comment, "tags": []string{}, "managed_by": "ankra",
		}
		fake.cloudflareRecords[dnsFakeText(row["id"])] = row
		writeJSON(t, writer, http.StatusCreated, row)
	})
	fake.handle("PATCH /api/v1/org/cloudflare/domains/{zone}/records/{id}", func(writer http.ResponseWriter, request *http.Request) {
		row, exists := fake.cloudflareRecords[request.PathValue("id")]
		if !exists {
			notFound(writer, "DNS record not found")
			return
		}
		var body map[string]json.RawMessage
		decodeJSON(t, request, &body)
		fake.cloudflarePatches = append(fake.cloudflarePatches, body)
		for _, key := range []string{"content", "ttl", "proxied", "priority", "comment"} {
			if raw, present := body[key]; present {
				var value any
				_ = json.Unmarshal(raw, &value)
				row[key] = value
			}
		}
		if row["comment"] == "" {
			row["comment"] = "Managed by Ankra"
		}
		writeJSON(t, writer, http.StatusOK, row)
	})
	fake.handle("DELETE /api/v1/org/cloudflare/domains/{zone}/records/{id}", func(writer http.ResponseWriter, request *http.Request) {
		if _, exists := fake.cloudflareRecords[request.PathValue("id")]; !exists {
			notFound(writer, "DNS record not found")
			return
		}
		delete(fake.cloudflareRecords, request.PathValue("id"))
		writer.WriteHeader(http.StatusNoContent)
	})
	return fake
}

func (fake *dnsFake) credentialNamed(name string) map[string]any {
	for _, row := range fake.credentials {
		if row["name"] == name {
			return row
		}
	}
	return nil
}

func dnsTestConfig(fake *dnsFake, content string, ttl string, comment string, rootDomain string, credentialURL string) string {
	ttlLine := ""
	if ttl != "" {
		ttlLine = "ttl = " + ttl
	}
	commentLine := ""
	if comment != "" {
		commentLine = fmt.Sprintf("comment = %q", comment)
	}
	return fake.providerBlock() + fmt.Sprintf(`
resource "ankra_organisation_dns_record" "logs" {
  name        = "logs"
  record_type = "CNAME"
  content     = %[1]q
  %[2]s
}

resource "ankra_dns_credential" "webhook" {
  name                 = "webhook"
  webhook_provider_url = %[5]q
}

resource "ankra_cloudflare_credential" "cloudflare" {
  name      = "cloudflare"
  api_token = "cf-token"
}

resource "ankra_organisation_custom_dns_zone" "example" {
  zone            = "example.org"
  credential_name = ankra_dns_credential.webhook.name
}

resource "ankra_organisation_domain" "this" {
  dns_root_domain = %[4]q
}

data "ankra_cloudflare_domains" "zones" {
  credential_name = ankra_cloudflare_credential.cloudflare.name
}

resource "ankra_cloudflare_dns_record" "www" {
  credential_name = ankra_cloudflare_credential.cloudflare.name
  zone_id         = data.ankra_cloudflare_domains.zones.domains[0].id
  name            = "www"
  record_type     = "A"
  content         = %[1]q == "a.example.net" ? "192.0.2.1" : "192.0.2.2"
  %[2]s
  %[3]s
}

data "ankra_organisation_dns_zone" "zone" {}

data "ankra_organisation_dns_records" "all" {
  depends_on = [ankra_organisation_dns_record.logs]
}

data "ankra_organisation_custom_dns_zones" "all" {
  depends_on = [ankra_organisation_custom_dns_zone.example]
}

data "ankra_dns_credentials" "all" {
  depends_on = [ankra_dns_credential.webhook]
}

data "ankra_cloudflare_credentials" "all" {
  depends_on = [ankra_cloudflare_credential.cloudflare]
}
`, content, ttlLine, commentLine, rootDomain, credentialURL)
}

func TestAccOrganisationDNSLifecycle(t *testing.T) {
	fake := newDNSFake(t)
	initial := dnsTestConfig(fake, "a.example.net", "300", "", "apps.example.com", "https://dns.example.com/hook?token=one")
	changed := dnsTestConfig(fake, "b.example.net", "", "edge", "apps2.example.com", "https://dns.example.com/hook?token=one")
	rotated := dnsTestConfig(fake, "b.example.net", "", "edge", "apps2.example.com", "https://dns.example.com/hook?token=two")

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: initial,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ankra_organisation_dns_record.logs", "fqdn", "logs."+dnsFakeZone),
					resource.TestCheckResourceAttr("ankra_organisation_dns_record.logs", "ttl", "300"),
					resource.TestCheckResourceAttr("ankra_organisation_dns_record.logs", "state", "pending"),
					resource.TestCheckResourceAttr("ankra_organisation_custom_dns_zone.example", "provider_kind", "webhook"),
					resource.TestCheckResourceAttr("ankra_organisation_custom_dns_zone.example", "policy", "upsert-only"),
					resource.TestCheckResourceAttr("ankra_organisation_domain.this", "dns_root_domain_default", "ankra.cc"),
					resource.TestCheckResourceAttr("ankra_cloudflare_credential.cloudflare", "account_id", "account-resolved"),
					resource.TestCheckResourceAttr("ankra_cloudflare_credential.cloudflare", "state", "verified"),
					resource.TestCheckResourceAttr("ankra_cloudflare_dns_record.www", "fqdn", "www.example.com"),
					resource.TestCheckResourceAttr("ankra_cloudflare_dns_record.www", "comment", "Managed by Ankra"),
					resource.TestCheckResourceAttr("ankra_cloudflare_dns_record.www", "ttl", "300"),
					resource.TestCheckResourceAttr("data.ankra_organisation_dns_zone.zone", "fqdn", dnsFakeZone),
					resource.TestCheckResourceAttr("data.ankra_organisation_dns_records.all", "records.#", "1"),
					resource.TestCheckResourceAttr("data.ankra_organisation_custom_dns_zones.all", "zones.#", "1"),
					resource.TestCheckResourceAttr("data.ankra_dns_credentials.all", "credentials.#", "1"),
					resource.TestCheckResourceAttr("data.ankra_cloudflare_credentials.all", "credentials.0.name", "cloudflare"),
					resource.TestCheckResourceAttr("data.ankra_cloudflare_domains.zones", "domains.0.name_servers.#", "1"),
					func(_ *terraform.State) error {
						if fake.rootDomain != "apps.example.com" {
							return fmt.Errorf("root domain = %v", fake.rootDomain)
						}
						for _, body := range fake.environmentBodies {
							if len(body) != 1 {
								return fmt.Errorf("environment PUT sent %d members; only dns_root_domain may be sent", len(body))
							}
						}
						if secret := fake.credentialNamed("webhook")["secret"]; secret != "https://dns.example.com/hook?token=one" {
							return fmt.Errorf("webhook secret = %v", secret)
						}
						return nil
					},
				),
			},
			{
				Config: changed,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ankra_organisation_dns_record.logs", "content", "b.example.net"),
					resource.TestCheckNoResourceAttr("ankra_organisation_dns_record.logs", "ttl"),
					resource.TestCheckResourceAttr("ankra_cloudflare_dns_record.www", "ttl", "1"),
					resource.TestCheckResourceAttr("ankra_cloudflare_dns_record.www", "comment", "edge"),
					func(_ *terraform.State) error {
						last := fake.recordPatches[len(fake.recordPatches)-1]
						if raw, present := last["ttl"]; !present || string(raw) != "null" {
							return fmt.Errorf("record update sent ttl %q; an explicit null is what resets it to Auto", raw)
						}
						if fake.rootDomain != "apps2.example.com" {
							return fmt.Errorf("root domain = %v", fake.rootDomain)
						}
						patch := fake.cloudflarePatches[len(fake.cloudflarePatches)-1]
						for _, key := range []string{"ttl", "proxied", "comment"} {
							if _, present := patch[key]; !present {
								return fmt.Errorf("Cloudflare update omitted %s, so the record would keep a stale value", key)
							}
						}
						return nil
					},
				),
			},
			{
				ResourceName:      "ankra_organisation_dns_record.logs",
				ImportState:       true,
				ImportStateVerify: true,
			},
			{
				ResourceName:      "ankra_organisation_custom_dns_zone.example",
				ImportState:       true,
				ImportStateId:     "example.org",
				ImportStateVerify: true,
			},
			{
				ResourceName:      "ankra_organisation_domain.this",
				ImportState:       true,
				ImportStateVerify: true,
			},
			{
				ResourceName:                         "ankra_dns_credential.webhook",
				ImportState:                          true,
				ImportStateId:                        "webhook",
				ImportStateVerify:                    true,
				ImportStateVerifyIdentifierAttribute: "name",
				ImportStateVerifyIgnore:              []string{"webhook_provider_url"},
			},
			{
				ResourceName:                         "ankra_cloudflare_credential.cloudflare",
				ImportState:                          true,
				ImportStateId:                        "cloudflare",
				ImportStateVerify:                    true,
				ImportStateVerifyIdentifierAttribute: "name",
				ImportStateVerifyIgnore:              []string{"api_token"},
			},
			{
				ResourceName:      "ankra_cloudflare_dns_record.www",
				ImportState:       true,
				ImportStateIdFunc: dnsCloudflareRecordImportID,
				ImportStateVerify: true,
				// The import reads the qualified name; configuration holds the label.
				ImportStateVerifyIgnore: []string{"name"},
			},
			{
				// A new webhook URL replaces the credential: the platform cannot change it.
				Config: rotated,
				Check: func(_ *terraform.State) error {
					if len(fake.credentialDeletes) != 1 {
						return fmt.Errorf("rotating the webhook URL deleted %d credentials, want 1", len(fake.credentialDeletes))
					}
					if secret := fake.credentialNamed("webhook")["secret"]; secret != "https://dns.example.com/hook?token=two" {
						return fmt.Errorf("webhook secret = %v", secret)
					}
					return nil
				},
			},
			{
				// Deleted outside Terraform: the next plan recreates them. A record
				// still being deleted counts as gone.
				PreConfig: func() {
					for _, row := range fake.records {
						row["state"] = "deleting"
					}
					for zone := range fake.customZones {
						delete(fake.customZones, zone)
					}
					for recordID := range fake.cloudflareRecords {
						delete(fake.cloudflareRecords, recordID)
					}
					fake.rootDomain = nil
				},
				Config: rotated,
				Check: func(_ *terraform.State) error {
					active := 0
					for _, row := range fake.records {
						if row["state"] != "deleting" {
							active++
						}
					}
					if active != 1 || len(fake.customZones) != 1 || len(fake.cloudflareRecords) != 1 || fake.rootDomain != "apps2.example.com" {
						return fmt.Errorf("objects deleted outside Terraform were not recreated: records %d, zones %d, cloudflare %d, domain %v",
							active, len(fake.customZones), len(fake.cloudflareRecords), fake.rootDomain)
					}
					return nil
				},
			},
			{
				// Replaced outside Terraform by another credential under the same
				// name: it is not adopted, so the create is refused for the name
				// rather than the provider taking over (and later deleting) it.
				PreConfig: func() {
					webhook := fake.credentialNamed("webhook")
					delete(fake.credentials, dnsFakeText(webhook["id"]))
					fake.credentials["impostor"] = map[string]any{"id": "impostor", "name": "webhook", "provider": "dns", "secret": "theirs"}
				},
				Config:      rotated,
				ExpectError: regexp.MustCompile("Unable to create DNS credential"),
			},
		},
		CheckDestroy: func(_ *terraform.State) error {
			if fake.rootDomain != nil {
				return fmt.Errorf("destroy left the root domain at %v instead of the default", fake.rootDomain)
			}
			if len(fake.credentials) != 1 || fake.credentials["impostor"] == nil {
				return fmt.Errorf("destroy should leave only the credential it does not own, left %v", fake.credentials)
			}
			return nil
		},
	})
}

func dnsCloudflareRecordImportID(state *terraform.State) (string, error) {
	record := state.RootModule().Resources["ankra_cloudflare_dns_record.www"]
	if record == nil {
		return "", fmt.Errorf("record not in state")
	}
	return "cloudflare/" + record.Primary.Attributes["zone_id"] + "/" + record.Primary.ID, nil
}

func TestAccCloudflareDNSRecordRejectsProxiedTTL(t *testing.T) {
	fake := newRouteFake(t)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: fake.providerBlock() + `
resource "ankra_cloudflare_dns_record" "www" {
  zone_id     = "zone-1"
  name        = "www"
  record_type = "TXT"
  content     = "hello"
  proxied     = true
  ttl         = 300
}
`,
				ExpectError: regexp.MustCompile("Only A, AAAA and CNAME records can be proxied"),
			},
		},
	})
	if served := fake.served(); len(served) != 0 {
		t.Errorf("an invalid configuration reached the platform: %v", served)
	}
}

// dnsFakeText reads a fake row member as text.
func dnsFakeText(value any) string {
	text, _ := value.(string)
	return text
}
