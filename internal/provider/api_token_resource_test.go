// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

// tokenFake holds personal and service tokens the way the token surface
// does (go/internal/cliapi/tokens.go, servicetokens.go): the personal create
// answers 200 with the value and an RFC 3339 expiry, the listing answers
// naive UTC timestamps and never the value, a revoked token keeps its name,
// revoke answers 409 for a token already revoked or gone, and delete answers
// 409 for a live token and 404 for a gone one. Service tokens are the same
// with a {"tokens": [...]} listing, a 201 create, and 404 on revoking a gone
// token.
type tokenFake struct {
	*routeFake
	personal     map[string]map[string]any
	service      map[string]map[string]any
	createBodies []map[string]any
	created      int
}

func newTokenFake(t *testing.T) *tokenFake {
	fake := &tokenFake{routeFake: newRouteFake(t), personal: map[string]map[string]any{}, service: map[string]map[string]any{}}

	fake.handle("GET /api/v1/org/account/tokens", func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(t, writer, http.StatusOK, tokenFakeListing(fake.personal))
	})
	fake.handle("POST /api/v1/org/account/tokens", func(writer http.ResponseWriter, request *http.Request) {
		var body map[string]any
		decodeJSON(t, request, &body)
		fake.createBodies = append(fake.createBodies, body)
		for _, row := range fake.personal {
			if row["name"] == body["name"] {
				writeJSON(t, writer, http.StatusConflict, map[string]any{"detail": "Token name already exists"})
				return
			}
		}
		fake.created++
		identifier := fmt.Sprintf("eeeeeeee-0000-0000-0000-%012d", fake.created)
		expiresAt, _ := body["expires_at"].(string)
		naiveExpiry := "2027-01-15T10:00:00.123456"
		answeredExpiry := naiveExpiry + "Z"
		if expiresAt != "" {
			parsed, _ := organisationParseTimestamp(expiresAt)
			naiveExpiry = parsed.Format("2006-01-02T15:04:05")
			answeredExpiry = expiresAt
		}
		scopes := []any{}
		if listed, ok := body["scopes"].([]any); ok {
			scopes = listed
		}
		fake.personal[identifier] = map[string]any{
			"id": identifier, "name": body["name"], "expires_at": naiveExpiry, "days_left": 90,
			"created_at": "2026-10-17T09:00:00", "revoked_at": nil, "last_used_at": nil, "revoked": false,
			"scopes": scopes, "project_id": nil, "project_name": nil,
		}
		writeJSON(t, writer, http.StatusOK, map[string]any{"id": identifier, "token": "ankra_secret" + identifier[len(identifier)-2:], "expires_at": answeredExpiry, "type": "Bearer"})
	})
	fake.handle("POST /api/v1/org/account/tokens/{token_id}/revoke", func(writer http.ResponseWriter, request *http.Request) {
		row, exists := fake.personal[request.PathValue("token_id")]
		if !exists || row["revoked"] == true {
			writeJSON(t, writer, http.StatusConflict, map[string]any{"detail": "Failed to revoke user token"})
			return
		}
		row["revoked"] = true
		row["revoked_at"] = "2026-10-17T10:00:00"
		writeJSON(t, writer, http.StatusOK, map[string]any{"id": request.PathValue("token_id"), "revoked_at": "2026-10-17T10:00:00"})
	})
	fake.handle("DELETE /api/v1/org/account/tokens/{token_id}", func(writer http.ResponseWriter, request *http.Request) {
		row, exists := fake.personal[request.PathValue("token_id")]
		switch {
		case !exists:
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
		var body map[string]any
		decodeJSON(t, request, &body)
		fake.createBodies = append(fake.createBodies, body)
		label, _ := body["label"].(string)
		if label == "" {
			label = "token-20261017-090000"
		}
		fake.created++
		identifier := fmt.Sprintf("ffffffff-0000-0000-0000-%012d", fake.created)
		fake.service[identifier] = map[string]any{
			"id": identifier, "account_id": "acct-1", "account_name": body["name"], "label": label, "role_slug": body["role_slug"],
			"expires_at": "2027-01-15T10:00:00", "days_left": 90, "created_at": "2026-10-17T09:00:00", "revoked_at": nil,
			"last_used_at": nil, "revoked": false, "scopes": []any{}, "created_by": nil, "project_id": nil, "project_name": nil,
		}
		writeJSON(t, writer, http.StatusCreated, map[string]any{
			"id": identifier, "token": "ankra_service", "account_id": "acct-1", "account_name": body["name"], "label": label,
			"role_slug": body["role_slug"], "expires_at": "2027-01-15T10:00:00Z", "type": "Bearer",
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
	fake.handle("DELETE /api/v1/org/organisation/service-tokens/{token_id}", func(writer http.ResponseWriter, request *http.Request) {
		row, exists := fake.service[request.PathValue("token_id")]
		switch {
		case !exists:
			writeJSON(t, writer, http.StatusNotFound, map[string]any{"detail": "Service token not found"})
		case row["revoked"] != true:
			writeJSON(t, writer, http.StatusConflict, map[string]any{"detail": "Service token must be revoked before deletion"})
		default:
			delete(fake.service, request.PathValue("token_id"))
			writeJSON(t, writer, http.StatusOK, map[string]any{"id": request.PathValue("token_id"), "deleted_at": "2026-10-17T10:00:01"})
		}
	})
	return fake
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

func TestAccAPITokenLifecycle(t *testing.T) {
	fake := newTokenFake(t)
	config := func(name string) string {
		return fake.providerBlock() + fmt.Sprintf(`
resource "ankra_api_token" "mcp" {
  name       = %q
  expires_at = "2027-01-01T00:00:00Z"
  scopes     = ["mcp:read"]
}
`, name)
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
				Config: config("laptop"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("ankra_api_token.mcp", "token"),
					resource.TestCheckResourceAttr("ankra_api_token.mcp", "expires_at", "2027-01-01T00:00:00Z"),
					resource.TestCheckResourceAttr("ankra_api_token.mcp", "created_at", "2026-10-17T09:00:00Z"),
					func(state *terraform.State) error {
						firstID = state.RootModule().Resources["ankra_api_token.mcp"].Primary.ID
						body := fake.createBodies[0]
						scopes, _ := body["scopes"].([]any)
						if body["expires_at"] != "2027-01-01T00:00:00Z" || len(scopes) != 1 || scopes[0] != "mcp:read" {
							return fmt.Errorf("create body %v does not match the backend contract", body)
						}
						return nil
					},
				),
			},
			{
				Config: config("laptop-2"),
				Check: func(state *terraform.State) error {
					if _, exists := fake.personal[firstID]; exists {
						return fmt.Errorf("replaced token %s was not revoked and deleted", firstID)
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
				PreConfig: func() {
					for _, row := range fake.personal {
						row["revoked"] = true
					}
				},
				Config:             config("laptop-2"),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
			{
				Config:      config("laptop-2"),
				ExpectError: regexp.MustCompile(`(?s)already exists.*revoked token keeps its name`),
			},
			{
				PreConfig: func() {
					for identifier := range fake.personal {
						delete(fake.personal, identifier)
					}
				},
				Config: config("laptop-2"),
				Check:  resource.TestCheckResourceAttrSet("ankra_api_token.mcp", "token"),
			},
		},
	})
}

func TestAccServiceTokenLifecycle(t *testing.T) {
	fake := newTokenFake(t)
	config := fake.providerBlock() + `
resource "ankra_service_token" "ci" {
  name      = "ci-deployer"
  role_slug = "member"
}
`

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
				Config: config,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ankra_service_token.ci", "token", "ankra_service"),
					resource.TestCheckResourceAttr("ankra_service_token.ci", "label", "token-20261017-090000"),
					resource.TestCheckResourceAttr("ankra_service_token.ci", "expires_at", "2027-01-15T10:00:00Z"),
					func(_ *terraform.State) error {
						body := fake.createBodies[0]
						if _, present := body["label"]; present {
							return fmt.Errorf("an unset label must be left out so the platform names it, got %v", body)
						}
						if body["name"] != "ci-deployer" || body["role_slug"] != "member" {
							return fmt.Errorf("create body %v does not match the backend contract", body)
						}
						return nil
					},
				),
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
				Config: config,
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
