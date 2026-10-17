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

// tokenFake holds personal and service tokens the way the token surface
// does (go/internal/cliapi/tokens.go, servicetokens.go, usecase/account,
// usecase/servicetokens) on top of the organisation roster, which tells the
// provider who it authenticates as. Personal tokens are the caller's own: the
// listing shows only tokens of the current caller. Create answers 200
// (personal) or 201 (service) with the value, the listing answers naive UTC
// timestamps and never the value, a revoked token keeps its name, revoke
// answers 409 for a token already revoked (and, personal, for one gone),
// delete answers 409 for a live token and 404 for a gone one, and rotate
// renames the old token "<name>-replaced-<stamp>" and mints a replacement
// under the old name. Deleting a service account's last token stands the
// account down.
type tokenFake struct {
	*organisationFake
	personal     map[string]map[string]any
	service      map[string]map[string]any
	createBodies []map[string]json.RawMessage
	standDowns   []string
	// failCreates makes the next personal creates fail, and failDeletes
	// refuses deleting the named tokens, to drive interrupted re-mints.
	failCreates int
	failDeletes map[string]bool
	created     int
}

func newTokenFake(t *testing.T) *tokenFake {
	fake := &tokenFake{organisationFake: newOrganisationFake(t), personal: map[string]map[string]any{}, service: map[string]map[string]any{}, failDeletes: map[string]bool{}}

	fake.handle("GET /api/v1/org/account/tokens", func(writer http.ResponseWriter, _ *http.Request) {
		visible := map[string]map[string]any{}
		for identifier, row := range fake.personal {
			if row["owner"] == fake.caller {
				visible[identifier] = row
			}
		}
		writeJSON(t, writer, http.StatusOK, tokenFakeListing(visible))
	})
	fake.handle("POST /api/v1/org/account/tokens", func(writer http.ResponseWriter, request *http.Request) {
		if fake.failCreates > 0 {
			fake.failCreates--
			writeJSON(t, writer, http.StatusForbidden, map[string]any{"detail": "minting refused"})
			return
		}
		var raw map[string]json.RawMessage
		decodeJSON(t, request, &raw)
		fake.createBodies = append(fake.createBodies, raw)
		var name, expiresAt string
		_ = json.Unmarshal(raw["name"], &name)
		_ = json.Unmarshal(raw["expires_at"], &expiresAt)
		for _, row := range fake.personal {
			if row["name"] == name && row["owner"] == fake.caller {
				writeJSON(t, writer, http.StatusConflict, map[string]any{"detail": "Token name already exists"})
				return
			}
		}
		row, answeredExpiry := fake.mintRow("eeeeeeee", expiresAt)
		row["name"] = name
		row["owner"] = fake.caller
		var scopes []any
		_ = json.Unmarshal(raw["scopes"], &scopes)
		row["scopes"] = tokenFakeScopes(scopes)
		var project any
		_ = json.Unmarshal(raw["project_id"], &project)
		row["project_id"] = project
		fake.personal[fmt.Sprint(row["id"])] = row
		writeJSON(t, writer, http.StatusOK, map[string]any{"id": row["id"], "token": "ankra_" + fmt.Sprint(row["id"]), "expires_at": answeredExpiry, "type": "Bearer"})
	})
	fake.handle("POST /api/v1/org/account/tokens/{token_id}/revoke", func(writer http.ResponseWriter, request *http.Request) {
		row, exists := fake.personal[request.PathValue("token_id")]
		if !exists || row["revoked"] == true || row["owner"] != fake.caller {
			writeJSON(t, writer, http.StatusConflict, map[string]any{"detail": "Failed to revoke user token"})
			return
		}
		row["revoked"] = true
		row["revoked_at"] = "2026-10-17T10:00:00"
		writeJSON(t, writer, http.StatusOK, map[string]any{"id": request.PathValue("token_id"), "revoked_at": "2026-10-17T10:00:00"})
	})
	fake.handle("POST /api/v1/org/account/tokens/{token_id}/rotate", func(writer http.ResponseWriter, request *http.Request) {
		row, exists := fake.personal[request.PathValue("token_id")]
		if !exists || row["owner"] != fake.caller {
			writeJSON(t, writer, http.StatusNotFound, map[string]any{"detail": "Token not found"})
			return
		}
		replacement := fake.rotate(row, "name", "eeeeeeee")
		fake.personal[fmt.Sprint(replacement["id"])] = replacement
		writeJSON(t, writer, http.StatusOK, map[string]any{"id": replacement["id"], "token": "ankra_rotated", "name": replacement["name"], "replaced_token_id": row["id"]})
	})
	fake.handle("DELETE /api/v1/org/account/tokens/{token_id}", func(writer http.ResponseWriter, request *http.Request) {
		if fake.failDeletes[request.PathValue("token_id")] {
			writeJSON(t, writer, http.StatusForbidden, map[string]any{"detail": "deletion refused"})
			return
		}
		row, exists := fake.personal[request.PathValue("token_id")]
		switch {
		case !exists || row["owner"] != fake.caller:
			writeJSON(t, writer, http.StatusNotFound, map[string]any{"detail": "Failed to delete user token"})
		case row["revoked"] != true:
			writeJSON(t, writer, http.StatusConflict, map[string]any{"detail": "Token must be revoked before deletion"})
		default:
			delete(fake.personal, request.PathValue("token_id"))
			writeJSON(t, writer, http.StatusOK, map[string]any{"id": request.PathValue("token_id"), "deleted_at": "2026-10-17T10:00:01"})
		}
	})

	fake.handle("GET /api/v1/org/organisation/service-tokens", func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(t, writer, http.StatusOK, map[string]any{"tokens": tokenFakeListing(fake.service)})
	})
	fake.handle("POST /api/v1/org/organisation/service-tokens", func(writer http.ResponseWriter, request *http.Request) {
		var raw map[string]json.RawMessage
		decodeJSON(t, request, &raw)
		fake.createBodies = append(fake.createBodies, raw)
		var name, label, roleSlug, expiresAt string
		_ = json.Unmarshal(raw["name"], &name)
		_ = json.Unmarshal(raw["label"], &label)
		_ = json.Unmarshal(raw["role_slug"], &roleSlug)
		_ = json.Unmarshal(raw["expires_at"], &expiresAt)
		if label == "" {
			label = fmt.Sprintf("token-20261017-0900%02d", fake.created)
		}
		for _, row := range fake.service {
			if row["account_name"] != name {
				continue
			}
			if row["role_slug"] != roleSlug {
				writeJSON(t, writer, http.StatusConflict, map[string]any{"detail": fmt.Sprintf("Service account %q already holds the %s role", name, row["role_slug"])})
				return
			}
			if row["label"] == label {
				writeJSON(t, writer, http.StatusConflict, map[string]any{"detail": fmt.Sprintf("Service account %q already has a token labelled %q", name, label)})
				return
			}
		}
		row, answeredExpiry := fake.mintRow("ffffffff", expiresAt)
		row["account_id"] = "acct-" + name
		row["account_name"] = name
		row["label"] = label
		row["role_slug"] = roleSlug
		var scopes []any
		_ = json.Unmarshal(raw["scopes"], &scopes)
		row["scopes"] = tokenFakeScopes(scopes)
		var project any
		_ = json.Unmarshal(raw["project_id"], &project)
		row["project_id"] = project
		fake.service[fmt.Sprint(row["id"])] = row
		writeJSON(t, writer, http.StatusCreated, map[string]any{
			"id": row["id"], "token": "ankra_service_" + fmt.Sprint(row["id"]), "account_id": row["account_id"], "account_name": name,
			"label": label, "role_slug": roleSlug, "expires_at": answeredExpiry, "type": "Bearer",
		})
	})
	fake.handle("POST /api/v1/org/organisation/service-tokens/{token_id}/revoke", func(writer http.ResponseWriter, request *http.Request) {
		row, exists := fake.service[request.PathValue("token_id")]
		switch {
		case !exists:
			writeJSON(t, writer, http.StatusNotFound, map[string]any{"detail": "Service token not found"})
		case row["revoked"] == true:
			writeJSON(t, writer, http.StatusConflict, map[string]any{"detail": "Service token is already revoked"})
		default:
			row["revoked"] = true
			writeJSON(t, writer, http.StatusOK, map[string]any{"id": request.PathValue("token_id"), "revoked_at": "2026-10-17T10:00:00"})
		}
	})
	fake.handle("POST /api/v1/org/organisation/service-tokens/{token_id}/rotate", func(writer http.ResponseWriter, request *http.Request) {
		row, exists := fake.service[request.PathValue("token_id")]
		if !exists {
			writeJSON(t, writer, http.StatusNotFound, map[string]any{"detail": "Service token not found"})
			return
		}
		replacement := fake.rotate(row, "label", "ffffffff")
		fake.service[fmt.Sprint(replacement["id"])] = replacement
		writeJSON(t, writer, http.StatusOK, map[string]any{"id": replacement["id"], "token": "ankra_rotated", "label": replacement["label"], "replaced_token_id": row["id"]})
	})
	fake.handle("DELETE /api/v1/org/organisation/service-tokens/{token_id}", func(writer http.ResponseWriter, request *http.Request) {
		row, exists := fake.service[request.PathValue("token_id")]
		switch {
		case !exists:
			writeJSON(t, writer, http.StatusNotFound, map[string]any{"detail": "Service token not found"})
		case row["revoked"] != true:
			writeJSON(t, writer, http.StatusConflict, map[string]any{"detail": "Service token must be revoked before deletion"})
		default:
			delete(fake.service, request.PathValue("token_id"))
			remaining := false
			for _, other := range fake.service {
				if other["account_name"] == row["account_name"] {
					remaining = true
				}
			}
			if !remaining {
				fake.standDowns = append(fake.standDowns, fmt.Sprint(row["account_name"]))
			}
			writeJSON(t, writer, http.StatusOK, map[string]any{"id": request.PathValue("token_id"), "deleted_at": "2026-10-17T10:00:01"})
		}
	})
	return fake
}

// mintRow builds a listing row; the listing renders expiries naive, while
// the create answer echoes the request's spelling.
func (fake *tokenFake) mintRow(prefix, expiresAt string) (map[string]any, string) {
	fake.created++
	naiveExpiry := "2027-01-15T10:00:00.123456"
	answeredExpiry := naiveExpiry + "Z"
	if expiresAt != "" {
		parsed, _ := organisationParseTimestamp(expiresAt)
		naiveExpiry = parsed.Format("2006-01-02T15:04:05.999999")
		answeredExpiry = expiresAt
	}
	return map[string]any{
		"id": fmt.Sprintf("%s-0000-0000-0000-%012d", prefix, fake.created), "expires_at": naiveExpiry, "days_left": 90,
		"created_at": "2026-10-17T09:00:00", "revoked_at": nil, "last_used_at": nil, "revoked": false, "project_name": nil,
	}, answeredExpiry
}

// rotate renames a token and mints its replacement under the old name.
func (fake *tokenFake) rotate(row map[string]any, nameKey, prefix string) map[string]any {
	replacement, _ := fake.mintRow(prefix, "")
	for key, value := range row {
		if key != "id" {
			replacement[key] = value
		}
	}
	row[nameKey] = fmt.Sprint(row[nameKey]) + "-replaced-20261017-0900"
	return replacement
}

func tokenFakeScopes(scopes []any) []any {
	if scopes == nil {
		return []any{}
	}
	return scopes
}

func tokenFakeListing(rows map[string]map[string]any) []map[string]any {
	identifiers := make([]string, 0, len(rows))
	for identifier := range rows {
		identifiers = append(identifiers, identifier)
	}
	sort.Strings(identifiers)
	listing := []map[string]any{}
	for _, identifier := range identifiers {
		listing = append(listing, rows[identifier])
	}
	return listing
}

func (fake *tokenFake) personalNamed(name string) []map[string]any {
	found := []map[string]any{}
	for _, row := range fake.personal {
		if row["name"] == name {
			found = append(found, row)
		}
	}
	return found
}

func TestAccAPITokenLifecycle(t *testing.T) {
	fake := newTokenFake(t)
	config := func(name, expiry string) string {
		return fake.providerBlock() + fmt.Sprintf(`
resource "ankra_api_token" "mcp" {
  name       = %q
  expires_at = %q
  scopes     = ["mcp:read"]
}
`, name, expiry)
	}
	var firstID string

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy: func(_ *terraform.State) error {
			if len(fake.personal) != 0 {
				return fmt.Errorf("tokens left after destroy: %v", fake.personal)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: config("laptop", "2027-01-01T00:00:00Z"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("ankra_api_token.mcp", "token"),
					resource.TestCheckResourceAttr("ankra_api_token.mcp", "expires_at", "2027-01-01T00:00:00Z"),
					resource.TestCheckResourceAttr("ankra_api_token.mcp", "owner_user_id", organisationTestCaller),
					resource.TestCheckResourceAttr("ankra_api_token.mcp", "organisation_id", organisationTestCurrent),
					resource.TestCheckNoResourceAttr("ankra_api_token.mcp", "project_id"),
					func(state *terraform.State) error {
						firstID = state.RootModule().Resources["ankra_api_token.mcp"].Primary.ID
						body := fake.createBodies[0]
						if string(body["project_id"]) != "null" {
							return fmt.Errorf("project_id must be sent as an explicit null, got %s", body["project_id"])
						}
						if string(body["scopes"]) != `["mcp:read"]` {
							return fmt.Errorf("scopes sent as %s", body["scopes"])
						}
						return nil
					},
				),
			},
			{
				// A new expiry with the same name re-mints in place: renew to free
				// the name, retire the renewal, mint, then retire the old token.
				Config: config("laptop", "2027-06-01T00:00:00Z"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ankra_api_token.mcp", "expires_at", "2027-06-01T00:00:00Z"),
					func(state *terraform.State) error {
						if state.RootModule().Resources["ankra_api_token.mcp"].Primary.ID == firstID {
							return fmt.Errorf("token was not re-minted")
						}
						if _, exists := fake.personal[firstID]; exists {
							return fmt.Errorf("old token %s was not retired", firstID)
						}
						if len(fake.personal) != 1 || len(fake.personalNamed("laptop")) != 1 {
							return fmt.Errorf("expected exactly the new token, got %v", fake.personal)
						}
						return tokenFakeOrder(fake.served(), "POST /api/v1/org/account/tokens/"+firstID+"/rotate",
							"POST /api/v1/org/account/tokens", "DELETE /api/v1/org/account/tokens/"+firstID)
					},
				),
			},
			{
				Config: config("laptop-2", "2027-06-01T00:00:00Z"),
				Check: func(_ *terraform.State) error {
					if len(fake.personal) != 1 || len(fake.personalNamed("laptop-2")) != 1 {
						return fmt.Errorf("rename did not leave exactly one token: %v", fake.personal)
					}
					return nil
				},
			},
			{
				ResourceName:            "ankra_api_token.mcp",
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"token"},
			},
			{
				Config:      strings.Replace(config("laptop-2", "2027-06-01T00:00:00Z"), `["mcp:read"]`, `[]`, 1),
				ExpectError: regexp.MustCompile("Empty scopes"),
			},
			{
				PreConfig: func() {
					for _, row := range fake.personal {
						row["name"] = "laptop-2-replaced-20261017-0900"
					}
				},
				Config:      config("laptop-2", "2027-06-01T00:00:00Z"),
				ExpectError: regexp.MustCompile("Token renewed outside Terraform"),
			},
			{
				PreConfig: func() {
					for _, row := range fake.personal {
						row["name"] = "laptop-2"
					}
					fake.caller = "ci-service-account"
					row := fake.organisations[organisationTestCurrent]
					row.members = append(row.members, map[string]any{"id": "ci-service-account", "status": "active", "role": "member"})
				},
				Config:      config("laptop-2", "2027-06-01T00:00:00Z"),
				ExpectError: regexp.MustCompile("API token belongs to another identity"),
			},
			{
				PreConfig: func() {
					fake.caller = organisationTestCaller
					for _, row := range fake.personal {
						row["revoked"] = true
					}
				},
				Config:             config("laptop-2", "2027-06-01T00:00:00Z"),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
			{
				Config: config("laptop-2", "2027-06-01T00:00:00Z"),
				Check: func(_ *terraform.State) error {
					rows := fake.personalNamed("laptop-2")
					if len(rows) != 1 || rows[0]["revoked"] == true {
						return fmt.Errorf("revoked token was not replaced by a live one: %v", fake.personal)
					}
					return nil
				},
			},
			{
				PreConfig: func() {
					for identifier := range fake.personal {
						delete(fake.personal, identifier)
					}
				},
				Config: config("laptop-2", "2027-06-01T00:00:00Z"),
				Check:  resource.TestCheckResourceAttrSet("ankra_api_token.mcp", "token"),
			},
		},
	})
}

func TestAccServiceTokenLifecycle(t *testing.T) {
	fake := newTokenFake(t)
	config := func(role, label, expiry string) string {
		attributes := fmt.Sprintf("  role_slug = %q\n", role)
		if label != "" {
			attributes += fmt.Sprintf("  label = %q\n", label)
		}
		if expiry != "" {
			attributes += fmt.Sprintf("  expires_at = %q\n", expiry)
		}
		return fake.providerBlock() + `
resource "ankra_service_token" "ci" {
  name = "ci-deployer"
` + attributes + `}
`
	}

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy: func(_ *terraform.State) error {
			if len(fake.service) != 0 {
				return fmt.Errorf("service tokens left after destroy: %v", fake.service)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: config("member", "", ""),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("ankra_service_token.ci", "token"),
					resource.TestCheckResourceAttr("ankra_service_token.ci", "label", "token-20261017-090000"),
					resource.TestCheckResourceAttr("ankra_service_token.ci", "expires_at", "2027-01-15T10:00:00.123456Z"),
					func(_ *terraform.State) error {
						body := fake.createBodies[0]
						if _, present := body["label"]; present {
							return fmt.Errorf("an unset label must be left out so the platform names it, got %v", body)
						}
						if string(body["project_id"]) != "null" || string(body["scopes"]) != "null" {
							return fmt.Errorf("project_id and scopes must be sent as explicit nulls, got %s and %s", body["project_id"], body["scopes"])
						}
						return nil
					},
				),
			},
			{
				// Same label, new expiry: renewed to free the label, so the account
				// always holds a token and is never stood down.
				Config: config("member", "", "2027-03-01T00:00:00Z"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ankra_service_token.ci", "expires_at", "2027-03-01T00:00:00Z"),
					resource.TestCheckResourceAttr("ankra_service_token.ci", "label", "token-20261017-090000"),
					func(_ *terraform.State) error {
						if len(fake.standDowns) != 0 || len(fake.service) != 1 {
							return fmt.Errorf("re-mint stood the account down (%v) or left extra tokens (%v)", fake.standDowns, fake.service)
						}
						return nil
					},
				),
			},
			{
				Config: config("member", "pipelines", "2027-03-01T00:00:00Z"),
				Check: func(_ *terraform.State) error {
					if len(fake.standDowns) != 0 || len(fake.service) != 1 {
						return fmt.Errorf("label change stood the account down (%v) or left extra tokens (%v)", fake.standDowns, fake.service)
					}
					return nil
				},
			},
			{
				Config:      config("admin", "pipelines", "2027-03-01T00:00:00Z"),
				ExpectError: regexp.MustCompile("Service account role cannot change here"),
			},
			{
				PreConfig: func() {
					for _, row := range fake.service {
						row["role_slug"] = "operator"
					}
				},
				Config:      config("member", "pipelines", "2027-03-01T00:00:00Z"),
				ExpectError: regexp.MustCompile("Service account role cannot change here"),
			},
			{
				PreConfig: func() {
					for _, row := range fake.service {
						row["role_slug"] = "member"
					}
				},
				Config:      strings.Replace(config("member", "pipelines", "2027-03-01T00:00:00Z"), `label = "pipelines"`, `label = ""`, 1),
				ExpectError: regexp.MustCompile("Invalid token name"),
			},
			{
				ResourceName:            "ankra_service_token.ci",
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"token"},
			},
			{
				PreConfig: func() {
					for identifier := range fake.service {
						delete(fake.service, identifier)
					}
				},
				Config: config("member", "pipelines", "2027-03-01T00:00:00Z"),
				Check: func(_ *terraform.State) error {
					if len(fake.service) != 1 {
						return fmt.Errorf("service token deleted outside Terraform was not recreated")
					}
					return nil
				},
			},
		},
	})
}

// TestAccAPITokenInterruptedRemint pins N1 and N4: a mint that fails after
// the provider renewed the old token to free its name is finished by the next
// apply instead of wedging every plan, and an old token that could not be
// retired is retried until it is gone.
func TestAccAPITokenInterruptedRemint(t *testing.T) {
	fake := newTokenFake(t)
	config := func(name, expiry string) string {
		return fake.providerBlock() + fmt.Sprintf(`
resource "ankra_api_token" "ci" {
  name       = %q
  expires_at = %q
}
`, name, expiry)
	}
	var firstID string

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy: func(_ *terraform.State) error {
			if len(fake.personal) != 0 {
				return fmt.Errorf("tokens left after destroy: %v", fake.personal)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: config("ci", "2027-01-01T00:00:00Z"),
				Check: func(state *terraform.State) error {
					firstID = state.RootModule().Resources["ankra_api_token.ci"].Primary.ID
					return nil
				},
			},
			{
				PreConfig:   func() { fake.failCreates = 1 },
				Config:      config("ci", "2027-06-01T00:00:00Z"),
				ExpectError: regexp.MustCompile("Unable to mint the replacement token"),
			},
			{
				// The renewed old token is Terraform's own: no "renewed outside
				// Terraform" error, the apply finishes the re-mint.
				Config: config("ci", "2027-06-01T00:00:00Z"),
				Check: func(_ *terraform.State) error {
					if _, exists := fake.personal[firstID]; exists {
						return fmt.Errorf("the renewed old token %s was not retired", firstID)
					}
					if rows := fake.personalNamed("ci"); len(rows) != 1 || len(fake.personal) != 1 {
						return fmt.Errorf("expected exactly one live token named ci, got %v", fake.personal)
					}
					return nil
				},
			},
			{
				PreConfig: func() {
					for identifier := range fake.personal {
						fake.failDeletes[identifier] = true
					}
				},
				// A rename mints first; the old token cannot be deleted yet, so
				// the next plan still has work to do.
				Config:             config("ci-renamed", "2027-06-01T00:00:00Z"),
				ExpectNonEmptyPlan: true,
			},
			{
				Config:             config("ci-renamed", "2027-06-01T00:00:00Z"),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
			{
				PreConfig: func() { fake.failDeletes = map[string]bool{} },
				Config:    config("ci-renamed", "2027-06-01T00:00:00Z"),
				Check: func(_ *terraform.State) error {
					if len(fake.personal) != 1 || len(fake.personalNamed("ci-renamed")) != 1 {
						return fmt.Errorf("the old token was not retired on the next apply: %v", fake.personal)
					}
					return nil
				},
			},
			{
				Config:   config("ci-renamed", "2027-06-01T00:00:00Z"),
				PlanOnly: true,
			},
		},
	})
}

// TestAccServiceTokenRevokedInPortal pins N2: a service token revoked in the
// portal is re-minted in place without ever leaving its account without a
// token, so the account is not stood down and keeps its role.
func TestAccServiceTokenRevokedInPortal(t *testing.T) {
	fake := newTokenFake(t)
	config := fake.providerBlock() + `
resource "ankra_service_token" "ci" {
  name      = "ci-deployer"
  role_slug = "deployer"
  label     = "pipelines"
}
`

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{Config: config},
			{
				PreConfig: func() {
					for _, row := range fake.service {
						row["revoked"] = true
					}
				},
				Config: config,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("ankra_service_token.ci", plancheck.ResourceActionUpdate)},
				},
				Check: func(_ *terraform.State) error {
					if len(fake.standDowns) != 0 {
						return fmt.Errorf("the account was stood down while its revoked token was re-minted: %v", fake.standDowns)
					}
					if len(fake.service) != 1 {
						return fmt.Errorf("expected exactly the re-minted token, got %v", fake.service)
					}
					for _, row := range fake.service {
						if row["label"] != "pipelines" || row["revoked"] == true {
							return fmt.Errorf("the re-minted token is %v", row)
						}
					}
					return nil
				},
			},
		},
	})
}

// tokenFakeOrder checks that the expected requests were served in order.
func tokenFakeOrder(served []string, expected ...string) error {
	position := 0
	for _, request := range served {
		if position < len(expected) && request == expected[position] {
			position++
		}
	}
	if position != len(expected) {
		return fmt.Errorf("requests out of order: wanted %v in sequence, served %v", expected, served)
	}
	return nil
}
