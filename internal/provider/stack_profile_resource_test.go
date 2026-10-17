// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

// stackProfileFake holds stack profiles the way the platform does
// (go/internal/profilesapi, usecase/stackprofiles): create answers 201 with
// the profile and version 1; a PATCH leaves absent and null fields alone,
// stores "" as an empty description, and any logo_url it carries deletes the
// uploaded logo; saving a version answers 201 and makes it current; the
// current version cannot be deprecated (409); delete answers 200
// {"deleted":true} and 404 when gone; share create answers 201 and refuses an
// unknown organisation with 404; the logo route takes a multipart "file" and
// answers the raw bytes.
type stackProfileFake struct {
	*routeFake
	profiles map[string]map[string]any
	versions map[string][]map[string]any
	shares   map[string][]map[string]any
	logos    map[string]stackProfileFakeLogo
	created  int
	// patches records each PATCH body.
	patches       []map[string]json.RawMessage
	logoUploads   int
	versionSaves  []map[string]json.RawMessage
	organisations map[string]string
	// projectOf is the project each profile was created in; a request from
	// another project sees 404, as the platform's scoped reads do.
	projectOf map[string]string
}

type stackProfileFakeLogo struct {
	content     []byte
	contentType string
}

const stackProfileFakeOrganisation = "99999999-0000-0000-0000-000000000000"

func newStackProfileFake(t *testing.T) *stackProfileFake {
	fake := &stackProfileFake{
		routeFake: newRouteFake(t), profiles: map[string]map[string]any{}, versions: map[string][]map[string]any{},
		shares: map[string][]map[string]any{}, logos: map[string]stackProfileFakeLogo{}, projectOf: map[string]string{},
		organisations: map[string]string{"partner-org": "88888888-0000-0000-0000-000000000001", "other-org": "88888888-0000-0000-0000-000000000002"},
	}
	notFound := func(writer http.ResponseWriter, detail string) {
		writeJSON(t, writer, http.StatusNotFound, map[string]any{"detail": detail})
	}
	nextIdentifier := func(prefix string) string {
		fake.created++
		return fmt.Sprintf("%s-0000-0000-0000-%012d", prefix, fake.created)
	}
	decode := func(request *http.Request) (map[string]json.RawMessage, map[string]any) {
		var raw map[string]json.RawMessage
		decodeJSON(t, request, &raw)
		body := map[string]any{}
		for key, value := range raw {
			var decoded any
			_ = json.Unmarshal(value, &decoded)
			body[key] = decoded
		}
		return raw, body
	}
	newVersion := func(profileID string, body map[string]any) map[string]any {
		number := len(fake.versions[profileID]) + 1
		channel, _ := body["channel"].(string)
		if channel == "" {
			channel = "stable"
		}
		changelog := body["changelog"]
		if changelog == "" {
			changelog = nil
		}
		version := map[string]any{"id": nextIdentifier("vvvvvvvv"), "version": number, "channel": channel, "changelog": changelog,
			"created_at": "2026-10-17T00:00:00Z", "deprecation": nil, "spec": map[string]any{}, "parameters": []any{}}
		fake.versions[profileID] = append(fake.versions[profileID], version)
		fake.profiles[profileID]["latest_version"] = number
		fake.profiles[profileID]["current_version"] = number
		return version
	}

	fake.handle("GET /api/v1/org/stack-profiles", func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Query().Get("page_size") == "" {
			t.Errorf("listing sent no page_size")
		}
		result := []map[string]any{}
		for _, profile := range fake.profiles {
			result = append(result, profile)
		}
		writeJSON(t, writer, http.StatusOK, map[string]any{"result": result, "total_count": len(result), "page": 1, "page_size": 100})
	})
	fake.handle("POST /api/v1/org/stack-profiles", func(writer http.ResponseWriter, request *http.Request) {
		_, body := decode(request)
		for _, required := range []string{"name", "source_cluster_id", "stack_name"} {
			if _, present := body[required]; !present {
				writeJSON(t, writer, http.StatusUnprocessableEntity, map[string]any{"detail": []any{map[string]any{"loc": []any{"body", required}, "type": "missing"}}})
				return
			}
		}
		category, _ := body["category"].(string)
		if category == "" {
			category = "general"
		}
		visibility, _ := body["visibility"].(string)
		if visibility == "" {
			visibility = "organisation"
		}
		tags, _ := body["tags"].([]any)
		if tags == nil {
			tags = []any{}
		}
		identifier := nextIdentifier("pppppppp")
		fake.profiles[identifier] = map[string]any{"id": identifier, "organisation_id": stackProfileFakeOrganisation, "name": body["name"],
			"description": body["description"], "logo_url": body["logo_url"], "has_uploaded_logo": false, "category": category, "tags": tags,
			"visibility": visibility, "source_cluster_id": body["source_cluster_id"], "is_owner": true, "shared_with_you": false}
		fake.projectOf[identifier] = request.Header.Get("X-Ankra-Project-Id")
		version := newVersion(identifier, body)
		writeJSON(t, writer, http.StatusCreated, map[string]any{"profile": fake.profiles[identifier], "version": version, "warnings": []string{}})
	})
	fake.handle("GET /api/v1/org/stack-profiles/{id}", func(writer http.ResponseWriter, request *http.Request) {
		profile, exists := fake.scopedProfile(request)
		if !exists {
			notFound(writer, "Stack profile not found")
			return
		}
		writeJSON(t, writer, http.StatusOK, map[string]any{"profile": profile, "versions": fake.versions[request.PathValue("id")],
			"update_status": map[string]any{"outdated_instantiation_count": 0, "has_update_available": false}})
	})
	fake.handle("PATCH /api/v1/org/stack-profiles/{id}", func(writer http.ResponseWriter, request *http.Request) {
		profile, exists := fake.scopedProfile(request)
		if !exists {
			notFound(writer, "Stack profile not found")
			return
		}
		raw, body := decode(request)
		fake.patches = append(fake.patches, raw)
		for _, key := range []string{"name", "description", "category", "visibility"} {
			if value, present := body[key]; present && value != nil {
				profile[key] = value
			}
		}
		if value, present := body["logo_url"]; present && value != nil {
			profile["logo_url"] = value
			if value == "" {
				profile["logo_url"] = nil
			}
			delete(fake.logos, request.PathValue("id"))
			profile["has_uploaded_logo"] = false
		}
		if value, present := body["tags"]; present && value != nil {
			profile["tags"] = value
		}
		writeJSON(t, writer, http.StatusOK, profile)
	})
	fake.handle("DELETE /api/v1/org/stack-profiles/{id}", func(writer http.ResponseWriter, request *http.Request) {
		if _, exists := fake.scopedProfile(request); !exists {
			notFound(writer, "Stack profile not found")
			return
		}
		delete(fake.profiles, request.PathValue("id"))
		writeJSON(t, writer, http.StatusOK, map[string]any{"deleted": true})
	})
	fake.handle("POST /api/v1/org/stack-profiles/{id}/current-version", func(writer http.ResponseWriter, request *http.Request) {
		profile, exists := fake.scopedProfile(request)
		if !exists {
			notFound(writer, "Stack profile not found")
			return
		}
		_, body := decode(request)
		requested, _ := body["version"].(float64)
		number := int(requested)
		versions := fake.versions[request.PathValue("id")]
		if number < 1 || number > len(versions) {
			notFound(writer, "Stack profile version not found")
			return
		}
		if versions[number-1]["deprecation"] != nil {
			writeJSON(t, writer, http.StatusConflict, map[string]any{"detail": "A deprecated version cannot be made current."})
			return
		}
		profile["current_version"] = number
		writeJSON(t, writer, http.StatusOK, profile)
	})
	fake.handle("POST /api/v1/org/stack-profiles/{id}/versions", func(writer http.ResponseWriter, request *http.Request) {
		if _, exists := fake.scopedProfile(request); !exists {
			notFound(writer, "Stack profile not found")
			return
		}
		raw, body := decode(request)
		fake.versionSaves = append(fake.versionSaves, raw)
		writeJSON(t, writer, http.StatusCreated, newVersion(request.PathValue("id"), body))
	})
	version := func(writer http.ResponseWriter, request *http.Request) map[string]any {
		profile, exists := fake.scopedProfile(request)
		if !exists {
			notFound(writer, "Stack profile not found")
			return nil
		}
		number, _ := strconv.Atoi(request.PathValue("version"))
		versions := fake.versions[request.PathValue("id")]
		if number < 1 || number > len(versions) {
			notFound(writer, "Stack profile version not found")
			return nil
		}
		_ = profile
		return versions[number-1]
	}
	fake.handle("GET /api/v1/org/stack-profiles/{id}/versions/{version}", func(writer http.ResponseWriter, request *http.Request) {
		if found := version(writer, request); found != nil {
			writeJSON(t, writer, http.StatusOK, found)
		}
	})
	fake.handle("POST /api/v1/org/stack-profiles/{id}/versions/{version}/deprecation", func(writer http.ResponseWriter, request *http.Request) {
		found := version(writer, request)
		if found == nil {
			return
		}
		if fake.profiles[request.PathValue("id")]["current_version"] == found["version"] {
			writeJSON(t, writer, http.StatusConflict, map[string]any{"detail": "The current version cannot be deprecated. Make another version current first."})
			return
		}
		_, body := decode(request)
		references, _ := body["references"].([]any)
		if references == nil {
			references = []any{}
		}
		found["deprecation"] = map[string]any{"reason": body["reason"], "note": body["note"], "references": references, "deprecated_at": "2026-10-17T02:00:00Z"}
		writeJSON(t, writer, http.StatusOK, found)
	})
	fake.handle("DELETE /api/v1/org/stack-profiles/{id}/versions/{version}/deprecation", func(writer http.ResponseWriter, request *http.Request) {
		if found := version(writer, request); found != nil {
			found["deprecation"] = nil
			writeJSON(t, writer, http.StatusOK, found)
		}
	})
	fake.handle("GET /api/v1/org/stack-profiles/{id}/shares", func(writer http.ResponseWriter, request *http.Request) {
		if _, exists := fake.scopedProfile(request); !exists {
			notFound(writer, "Stack profile not found")
			return
		}
		shares := fake.shares[request.PathValue("id")]
		if shares == nil {
			shares = []map[string]any{}
		}
		writeJSON(t, writer, http.StatusOK, map[string]any{"result": shares})
	})
	fake.handle("POST /api/v1/org/stack-profiles/{id}/shares", func(writer http.ResponseWriter, request *http.Request) {
		profileID := request.PathValue("id")
		if _, exists := fake.scopedProfile(request); !exists {
			notFound(writer, "Stack profile not found")
			return
		}
		_, body := decode(request)
		requestedSlug, _ := body["organisation_slug"].(string)
		slug := strings.ToLower(requestedSlug)
		organisationID, known := fake.organisations[slug]
		if !known {
			notFound(writer, "Organisation not found")
			return
		}
		for _, share := range fake.shares[profileID] {
			if share["organisation_slug"] == slug {
				writeJSON(t, writer, http.StatusConflict, map[string]any{"detail": "The profile is already shared with that organisation."})
				return
			}
		}
		share := map[string]any{"id": nextIdentifier("ssssssss"), "organisation_id": organisationID, "organisation_name": "Partner " + slug,
			"organisation_slug": slug, "created_at": "2026-10-17T00:00:00Z"}
		fake.shares[profileID] = append(fake.shares[profileID], share)
		writeJSON(t, writer, http.StatusCreated, share)
	})
	fake.handle("DELETE /api/v1/org/stack-profiles/{id}/shares/{share}", func(writer http.ResponseWriter, request *http.Request) {
		profileID := request.PathValue("id")
		if _, exists := fake.scopedProfile(request); !exists {
			notFound(writer, "Stack profile not found")
			return
		}
		for index, share := range fake.shares[profileID] {
			if share["id"] == request.PathValue("share") {
				fake.shares[profileID] = append(fake.shares[profileID][:index], fake.shares[profileID][index+1:]...)
				writeJSON(t, writer, http.StatusOK, map[string]any{"deleted": true})
				return
			}
		}
		notFound(writer, "Share not found")
	})
	fake.handle("PUT /api/v1/org/stack-profiles/{id}/logo", func(writer http.ResponseWriter, request *http.Request) {
		profile, exists := fake.scopedProfile(request)
		if !exists {
			notFound(writer, "Stack profile not found")
			return
		}
		file, header, err := request.FormFile("file")
		if err != nil {
			writeJSON(t, writer, http.StatusUnprocessableEntity, map[string]any{"detail": []any{map[string]any{"loc": []any{"body", "file"}, "type": "missing"}}})
			return
		}
		content, _ := io.ReadAll(file)
		contentType := header.Header.Get("Content-Type")
		if contentType != "image/png" && contentType != "image/jpeg" && contentType != "image/webp" {
			writeJSON(t, writer, http.StatusBadRequest, map[string]any{"detail": "Stack profile logos must be PNG, JPEG, or WebP images."})
			return
		}
		fake.logoUploads++
		fake.logos[request.PathValue("id")] = stackProfileFakeLogo{content: content, contentType: contentType}
		profile["logo_url"] = nil
		profile["has_uploaded_logo"] = true
		writeJSON(t, writer, http.StatusOK, profile)
	})
	fake.handle("GET /api/v1/org/stack-profiles/{id}/logo", func(writer http.ResponseWriter, request *http.Request) {
		logo, exists := fake.logos[request.PathValue("id")]
		if !exists {
			notFound(writer, "Stack profile logo not found")
			return
		}
		writer.Header().Set("Content-Type", logo.contentType)
		writer.WriteHeader(http.StatusOK)
		_, _ = writer.Write(logo.content)
	})
	fake.handle("DELETE /api/v1/org/stack-profiles/{id}/logo", func(writer http.ResponseWriter, request *http.Request) {
		profile, exists := fake.scopedProfile(request)
		if !exists {
			notFound(writer, "Stack profile not found")
			return
		}
		delete(fake.logos, request.PathValue("id"))
		profile["has_uploaded_logo"] = false
		writeJSON(t, writer, http.StatusOK, profile)
	})
	return fake
}

func (fake *stackProfileFake) scopedProfile(request *http.Request) (map[string]any, bool) {
	identifier := request.PathValue("id")
	profile, exists := fake.profiles[identifier]
	if !exists || fake.projectOf[identifier] != request.Header.Get("X-Ankra-Project-Id") {
		return nil, false
	}
	return profile, true
}

func (fake *stackProfileFake) onlyProfileID() string {
	for identifier := range fake.profiles {
		return identifier
	}
	return ""
}

func TestAccStackProfileLifecycle(t *testing.T) {
	fake := newStackProfileFake(t)
	firstLogo := base64.StdEncoding.EncodeToString([]byte("first-logo-bytes"))
	first := fake.providerBlock() + fmt.Sprintf(`
resource "ankra_stack_profile" "platform" {
  name              = "platform-base"
  description       = "ingress, certs and monitoring"
  tags              = ["ingress", "observability"]
  source_cluster_id = "c1111111-0000-0000-0000-000000000001"
  stack_name        = "platform"
}

resource "ankra_stack_profile_version" "second" {
  profile_id        = ankra_stack_profile.platform.id
  source_cluster_id = "c1111111-0000-0000-0000-000000000001"
  stack_name        = "platform"
  changelog         = ""
}

resource "ankra_stack_profile_share" "partner" {
  profile_id        = ankra_stack_profile.platform.id
  organisation_slug = "Partner-Org"
}

resource "ankra_stack_profile_logo" "logo" {
  profile_id     = ankra_stack_profile.platform.id
  content_base64 = %q
  content_type   = "image/png"
}

data "ankra_stack_profiles" "all" {
  depends_on = [ankra_stack_profile_version.second]
}
`, firstLogo)
	second := fake.providerBlock() + fmt.Sprintf(`
resource "ankra_stack_profile" "platform" {
  name              = "platform-baseline"
  tags              = []
  current_version   = 1
  source_cluster_id = "c1111111-0000-0000-0000-000000000001"
  stack_name        = "platform"
}

resource "ankra_stack_profile_version" "second" {
  profile_id        = ankra_stack_profile.platform.id
  source_cluster_id = "c1111111-0000-0000-0000-000000000001"
  stack_name        = "platform"
  changelog         = "bump cert-manager"
  deprecation = {
    reason     = "cve"
    references = ["CVE-2026-1234"]
  }
}

resource "ankra_stack_profile_share" "partner" {
  profile_id        = ankra_stack_profile.platform.id
  organisation_slug = "partner-org"
}

resource "ankra_stack_profile_logo" "logo" {
  profile_id     = ankra_stack_profile.platform.id
  content_base64 = %q
  content_type   = "image/png"
}
`, firstLogo)
	profileImportID := func(_ *terraform.State) (string, error) { return fake.onlyProfileID(), nil }

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: first,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ankra_stack_profile.platform", "category", "general"),
					resource.TestCheckResourceAttr("ankra_stack_profile.platform", "visibility", "organisation"),
					resource.TestCheckResourceAttr("ankra_stack_profile_version.second", "version", "2"),
					resource.TestCheckResourceAttr("ankra_stack_profile_version.second", "channel", "stable"),
					resource.TestCheckResourceAttr("ankra_stack_profile_share.partner", "organisation_id", "88888888-0000-0000-0000-000000000001"),
					resource.TestCheckResourceAttrSet("ankra_stack_profile_logo.logo", "content_sha256"),
					resource.TestCheckResourceAttr("data.ankra_stack_profiles.all", "profiles.#", "1"),
					resource.TestCheckResourceAttr("data.ankra_stack_profiles.all", "profiles.0.current_version", "2"),
					func(_ *terraform.State) error {
						if fake.profiles[fake.onlyProfileID()]["has_uploaded_logo"] != true {
							return fmt.Errorf("logo was not uploaded")
						}
						body := fake.versionSaves[0]
						if string(body["include_addon_configurations"]) != "true" || string(body["stack_name"]) != `"platform"` {
							return fmt.Errorf("save-version body was %v", body)
						}
						return nil
					},
				),
			},
			{
				Config: second,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ankra_stack_profile.platform", "name", "platform-baseline"),
					resource.TestCheckNoResourceAttr("ankra_stack_profile.platform", "description"),
					resource.TestCheckResourceAttr("ankra_stack_profile.platform", "tags.#", "0"),
					resource.TestCheckResourceAttr("ankra_stack_profile.platform", "current_version", "1"),
					resource.TestCheckResourceAttr("ankra_stack_profile_version.second", "deprecation.reason", "cve"),
					func(_ *terraform.State) error {
						patch := fake.patches[len(fake.patches)-1]
						if string(patch["description"]) != `""` {
							return fmt.Errorf("a removed description was sent as %s; only \"\" clears it", patch["description"])
						}
						if string(patch["tags"]) != "[]" {
							return fmt.Errorf("emptied tags were sent as %s", patch["tags"])
						}
						if _, present := patch["logo_url"]; present {
							return fmt.Errorf("the PATCH carried logo_url, which deletes the uploaded logo")
						}
						if fake.profiles[fake.onlyProfileID()]["has_uploaded_logo"] != true {
							return fmt.Errorf("the profile update removed the uploaded logo")
						}
						return nil
					},
				),
			},
			{
				ResourceName:            "ankra_stack_profile.platform",
				ImportState:             true,
				ImportStateIdFunc:       profileImportID,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"source_cluster_id", "stack_name", "include_addon_configurations"},
			},
			{
				ResourceName: "ankra_stack_profile_version.second",
				ImportState:  true,
				ImportStateIdFunc: func(state *terraform.State) (string, error) {
					return fake.onlyProfileID() + "/2", nil
				},
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"source_cluster_id", "stack_name", "include_addon_configurations", "changelog"},
			},
			{
				ResourceName: "ankra_stack_profile_share.partner",
				ImportState:  true,
				ImportStateIdFunc: func(state *terraform.State) (string, error) {
					return fake.onlyProfileID() + "/partner-org", nil
				},
				ImportStateVerify: true,
			},
			{
				ResourceName:      "ankra_stack_profile_logo.logo",
				ImportState:       true,
				ImportStateIdFunc: profileImportID,
				ImportStateVerify: true,
			},
			{
				// Changed and removed outside Terraform: the logo is uploaded
				// again and the share granted again.
				PreConfig: func() {
					profileID := fake.onlyProfileID()
					fake.logos[profileID] = stackProfileFakeLogo{content: []byte("someone-else"), contentType: "image/png"}
					fake.shares[profileID] = nil
				},
				Config: second,
				Check: func(_ *terraform.State) error {
					profileID := fake.onlyProfileID()
					if string(fake.logos[profileID].content) != "first-logo-bytes" || fake.logoUploads != 2 {
						return fmt.Errorf("logo changed outside Terraform was not restored")
					}
					if len(fake.shares[profileID]) != 1 {
						return fmt.Errorf("share revoked outside Terraform was not granted again")
					}
					return nil
				},
			},
			{
				// Profile deleted outside Terraform: the next apply recreates it.
				PreConfig: func() { delete(fake.profiles, fake.onlyProfileID()) },
				Config:    first,
				Check: func(_ *terraform.State) error {
					if len(fake.profiles) != 1 {
						return fmt.Errorf("profile deleted outside Terraform was not recreated")
					}
					return nil
				},
			},
		},
		CheckDestroy: func(_ *terraform.State) error {
			if len(fake.profiles) != 0 {
				return fmt.Errorf("profiles left after destroy")
			}
			return nil
		},
	})
}

func TestAccStackProfileVersionCannotDeprecateCurrent(t *testing.T) {
	fake := newStackProfileFake(t)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config: fake.providerBlock() + `
resource "ankra_stack_profile" "platform" {
  name              = "platform-base"
  source_cluster_id = "c1111111-0000-0000-0000-000000000001"
  stack_name        = "platform"
}

resource "ankra_stack_profile_version" "second" {
  profile_id        = ankra_stack_profile.platform.id
  source_cluster_id = "c1111111-0000-0000-0000-000000000001"
  stack_name        = "platform"
  deprecation       = { reason = "critical_bug" }
}
`,
			PlanOnly:    true,
			ExpectError: regexp.MustCompile(`A new version cannot be deprecated`),
		}},
	})
}

func TestAccStackProfileRejectsInvalidConfigAtPlan(t *testing.T) {
	fake := newRouteFake(t)
	for _, config := range []string{
		`resource "ankra_stack_profile" "bad" {
  name              = "x"
  visibility        = "everyone"
  source_cluster_id = "c1"
  stack_name        = "s"
}`,
		`resource "ankra_stack_profile_logo" "bad" {
  profile_id     = "p"
  content_base64 = "AA=="
  content_type   = "image/gif"
}`,
	} {
		resource.Test(t, resource.TestCase{
			ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
			Steps: []resource.TestStep{{
				Config:      fake.providerBlock() + config,
				PlanOnly:    true,
				ExpectError: regexp.MustCompile(`Invalid value`),
			}},
		})
	}
	if served := fake.served(); len(served) != 0 {
		t.Errorf("a refused configuration still reached the platform: %v", served)
	}
}

// TestAccStackProfileShareScopeMoveIsNotRevoked pins that moving the provider
// to another project neither drops the share from state nor lets destroy
// report success: the partner organisation keeps access until the grant is
// revoked from the project that owns the profile.
func TestAccStackProfileShareScopeMoveIsNotRevoked(t *testing.T) {
	fake := newStackProfileFake(t)
	inProject := func(project string) string {
		return fmt.Sprintf(`
provider "ankra" {
  token      = "test-token"
  base_url   = %q
  project_id = %q
}

resource "ankra_stack_profile" "platform" {
  name              = "platform-base"
  source_cluster_id = "c1111111-0000-0000-0000-000000000001"
  stack_name        = "platform"
}

resource "ankra_stack_profile_share" "partner" {
  profile_id        = ankra_stack_profile.platform.id
  organisation_slug = "partner-org"
}
`, fake.server.URL, project)
	}
	const owningProject = "11111111-2222-3333-4444-555555555555"
	const otherProject = "66666666-7777-8888-9999-000000000000"

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{Config: inProject(owningProject)},
			{
				Config:      inProject(otherProject),
				ExpectError: regexp.MustCompile(`Object not visible in the provider's current scope`),
			},
			{
				Config:      inProject(otherProject),
				Destroy:     true,
				ExpectError: regexp.MustCompile(`Object not visible in the provider's current scope`),
			},
			{
				Config: inProject(owningProject),
				Check: func(_ *terraform.State) error {
					if len(fake.profiles) != 1 || len(fake.shares[fake.onlyProfileID()]) != 1 {
						return fmt.Errorf("the scope move created or lost objects: %d profiles", len(fake.profiles))
					}
					return nil
				},
			},
		},
	})
}
