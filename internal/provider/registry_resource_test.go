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

// registryFake holds registry projects, robots and retention the way the
// platform does (go/internal/helmapi registryprojects.go, registryrobots.go,
// registrystorage.go): project create answers 201 and delete 200
// {"success":true}; a robot's secret is answered only by create (201) and
// rotate (200), never by get or list; retention saves answer 200 with the
// stored retention, refuse a stale expected_version with 409, and refuse rules
// without an organisation policy with 422.
type registryFake struct {
	*routeFake
	projects  map[string]map[string]any
	robots    map[string]map[string]any
	secrets   int
	created   int
	retention registryFakeRetention
	// robotBodies records each robot create body.
	robotBodies []map[string]json.RawMessage
	// retentionBodies records each retention save body.
	retentionBodies []map[string]json.RawMessage
	rotations       int
}

type registryFakeRetention struct {
	defaultPolicy map[string]any
	rules         []map[string]any
	version       int
}

func newRegistryFake(t *testing.T) *registryFake {
	fake := &registryFake{routeFake: newRouteFake(t), projects: map[string]map[string]any{}, robots: map[string]map[string]any{}}
	notFound := func(writer http.ResponseWriter, what string) {
		writeJSON(t, writer, http.StatusNotFound, map[string]any{"detail": what + " not found"})
	}

	fake.handle("GET /api/v1/org/registry-projects", func(writer http.ResponseWriter, _ *http.Request) {
		projects := []map[string]any{{"name": "default", "project": "org-acme", "host": "registry.ankra.cloud", "is_default": true, "created_at": "2026-01-01T00:00:00Z"}}
		names := make([]string, 0, len(fake.projects))
		for name := range fake.projects {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			projects = append(projects, fake.projects[name])
		}
		writeJSON(t, writer, http.StatusOK, map[string]any{"projects": projects, "total_count": len(projects), "extra_project_limit": 5})
	})
	fake.handle("POST /api/v1/org/registry-projects", func(writer http.ResponseWriter, request *http.Request) {
		var body map[string]string
		decodeJSON(t, request, &body)
		name := body["name"]
		if _, exists := fake.projects[name]; exists {
			writeJSON(t, writer, http.StatusConflict, map[string]any{"detail": "A registry project with that name already exists"})
			return
		}
		fake.created++
		fake.projects[name] = map[string]any{"name": name, "project": "org-acme-" + name, "host": "registry.ankra.cloud", "is_default": false,
			"repository_count": 0, "robot_count": 0, "created_at": fmt.Sprintf("2026-10-17T00:00:%02dZ", fake.created)}
		writeJSON(t, writer, http.StatusCreated, fake.projects[name])
	})
	fake.handle("DELETE /api/v1/org/registry-projects/{name}", func(writer http.ResponseWriter, request *http.Request) {
		name := request.PathValue("name")
		if _, exists := fake.projects[name]; !exists {
			notFound(writer, "Registry project")
			return
		}
		for _, robot := range fake.robots {
			if robot["project_name"] == name {
				writeJSON(t, writer, http.StatusConflict, map[string]any{"detail": "The registry project still has robot accounts bound to it"})
				return
			}
		}
		delete(fake.projects, name)
		writeJSON(t, writer, http.StatusOK, map[string]any{"success": true})
	})

	fake.handle("GET /api/v1/org/registry-robots", func(writer http.ResponseWriter, _ *http.Request) {
		robots := []map[string]any{}
		for _, robot := range fake.robots {
			robots = append(robots, robot)
		}
		writeJSON(t, writer, http.StatusOK, map[string]any{"robots": robots, "total_count": len(robots)})
	})
	fake.handle("POST /api/v1/org/registry-robots", func(writer http.ResponseWriter, request *http.Request) {
		var raw map[string]json.RawMessage
		decodeJSON(t, request, &raw)
		fake.robotBodies = append(fake.robotBodies, raw)
		var body struct {
			Name        string   `json:"name"`
			Project     *string  `json:"project"`
			Scope       *string  `json:"scope"`
			Permissions []string `json:"permissions"`
			Description *string  `json:"description"`
		}
		encoded, _ := json.Marshal(raw)
		_ = json.Unmarshal(encoded, &body)
		if body.Scope != nil && len(body.Permissions) > 0 {
			writeJSON(t, writer, http.StatusUnprocessableEntity, map[string]any{"detail": "Choose a scope or permissions, not both"})
			return
		}
		if _, exists := fake.robots[body.Name]; exists {
			writeJSON(t, writer, http.StatusConflict, map[string]any{"detail": "A robot with that name already exists"})
			return
		}
		project := "default"
		if body.Project != nil && *body.Project != "" {
			project = *body.Project
			if _, exists := fake.projects[project]; !exists {
				notFound(writer, "Registry project")
				return
			}
		}
		scope, permissions := "push", []string{"repository:pull", "repository:push"}
		switch {
		case len(body.Permissions) > 0:
			scope, permissions = registryFakeNormalisePermissions(body.Permissions)
		case body.Scope != nil && *body.Scope == "pull":
			scope, permissions = "pull", []string{"repository:pull"}
		}
		description := ""
		if body.Description != nil {
			description = *body.Description
		}
		fake.created++
		robot := map[string]any{"name": body.Name, "kind": "user", "managed": false, "robot_name": "robot$org-acme+" + body.Name,
			"host": "registry.ankra.cloud", "project": "org-acme", "project_name": project, "scope": scope, "permissions": permissions,
			"description": description, "credential_name": "registry-robot-" + body.Name,
			"created_at": fmt.Sprintf("2026-10-17T00:01:%02dZ", fake.created), "rotated_at": nil, "expires_at": nil}
		fake.robots[body.Name] = robot
		writeJSON(t, writer, http.StatusCreated, fake.withSecret(robot))
	})
	fake.handle("GET /api/v1/org/registry-robots/{name}", func(writer http.ResponseWriter, request *http.Request) {
		robot, exists := fake.robots[request.PathValue("name")]
		if !exists {
			notFound(writer, "Registry robot")
			return
		}
		writeJSON(t, writer, http.StatusOK, robot)
	})
	fake.handle("POST /api/v1/org/registry-robots/{name}/rotate", func(writer http.ResponseWriter, request *http.Request) {
		robot, exists := fake.robots[request.PathValue("name")]
		if !exists {
			notFound(writer, "Registry robot")
			return
		}
		fake.rotations++
		robot["rotated_at"] = fmt.Sprintf("2026-10-17T01:00:%02dZ", fake.rotations)
		writeJSON(t, writer, http.StatusOK, fake.withSecret(robot))
	})
	fake.handle("DELETE /api/v1/org/registry-robots/{name}", func(writer http.ResponseWriter, request *http.Request) {
		name := request.PathValue("name")
		if _, exists := fake.robots[name]; !exists {
			notFound(writer, "Registry robot")
			return
		}
		delete(fake.robots, name)
		writeJSON(t, writer, http.StatusOK, map[string]any{"success": true})
	})

	fake.handle("GET /api/v1/org/registry-storage/retention", func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(t, writer, http.StatusOK, fake.retentionDocument())
	})
	fake.handle("PUT /api/v1/org/registry-storage/retention", func(writer http.ResponseWriter, request *http.Request) {
		var body map[string]json.RawMessage
		decodeJSON(t, request, &body)
		fake.retentionBodies = append(fake.retentionBodies, body)
		if raw, present := body["expected_version"]; present {
			var expected string
			_ = json.Unmarshal(raw, &expected)
			if expected != fake.retentionVersion() {
				writeJSON(t, writer, http.StatusConflict, map[string]any{"detail": "Retention changed since you read it. Reload it and make your change again."})
				return
			}
		}
		next := fake.retention
		if raw, present := body["default"]; present {
			var policy map[string]any
			_ = json.Unmarshal(raw, &policy)
			next.defaultPolicy = policy
		}
		if raw, present := body["rules"]; present {
			var rules []map[string]any
			_ = json.Unmarshal(raw, &rules)
			next.rules = rules
		}
		if len(next.rules) > 0 && next.defaultPolicy == nil {
			writeJSON(t, writer, http.StatusUnprocessableEntity, map[string]any{"detail": "Set a project-wide policy (default) before adding repository rules: the platform default keeps nothing here"})
			return
		}
		next.version = fake.retention.version + 1
		fake.retention = next
		writeJSON(t, writer, http.StatusOK, map[string]any{"retention": fake.retentionDocument(), "harbor_applied": true, "harbor_apply_error": nil})
	})
	return fake
}

func (fake *registryFake) withSecret(robot map[string]any) map[string]any {
	fake.secrets++
	answer := map[string]any{}
	for key, value := range robot {
		answer[key] = value
	}
	answer["secret"] = fmt.Sprintf("secret-%d", fake.secrets)
	answer["docker_login"] = fmt.Sprintf("docker login registry.ankra.cloud -u %s -p secret-%d", robot["robot_name"], fake.secrets)
	return answer
}

func (fake *registryFake) retentionVersion() string {
	return fmt.Sprintf("version-%d", fake.retention.version)
}

func (fake *registryFake) retentionDocument() map[string]any {
	platformDefault := map[string]any{"keep_days": 0, "keep_latest": 0}
	inForce, source := platformDefault, "default"
	if fake.retention.defaultPolicy != nil {
		inForce, source = fake.retention.defaultPolicy, "organisation"
	}
	rules := append(make([]map[string]any, 0, len(fake.retention.rules)), fake.retention.rules...)
	sort.Slice(rules, func(left, right int) bool {
		return fmt.Sprint(rules[left]["repository_pattern"]) < fmt.Sprint(rules[right]["repository_pattern"])
	})
	return map[string]any{"organisation_id": "org-acme", "default": inForce, "platform_default": platformDefault, "source": source,
		"rules": rules, "version": fake.retentionVersion(), "recently_pulled_days": 7}
}

func TestAccRegistryProjectAndRobotLifecycle(t *testing.T) {
	fake := newRegistryFake(t)
	config := func(trigger string) string {
		return fake.providerBlock() + fmt.Sprintf(`
resource "ankra_registry_project" "ci" {
  name = "ci-images"
}

resource "ankra_registry_robot" "puller" {
  name              = "cluster-puller"
  project           = ankra_registry_project.ci.name
  scope             = "pull"
  description       = "pulls CI images"
  rotation_triggers = { generation = %q }
}

data "ankra_registry_projects" "all" {
  depends_on = [ankra_registry_project.ci]
}

data "ankra_registry_robots" "all" {
  depends_on = [ankra_registry_robot.puller]
}
`, trigger)
	}
	var firstSecret string

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: config("1"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ankra_registry_project.ci", "project", "org-acme-ci-images"),
					resource.TestCheckResourceAttr("ankra_registry_robot.puller", "scope", "pull"),
					resource.TestCheckResourceAttr("ankra_registry_robot.puller", "permissions.#", "1"),
					resource.TestCheckResourceAttr("ankra_registry_robot.puller", "secret", "secret-1"),
					resource.TestCheckResourceAttr("ankra_registry_robot.puller", "credential_name", "registry-robot-cluster-puller"),
					resource.TestCheckResourceAttr("data.ankra_registry_projects.all", "projects.#", "2"),
					resource.TestCheckResourceAttr("data.ankra_registry_robots.all", "robots.0.project", "ci-images"),
					resource.TestCheckNoResourceAttr("data.ankra_registry_robots.all", "robots.0.secret"),
					func(state *terraform.State) error {
						firstSecret = state.RootModule().Resources["ankra_registry_robot.puller"].Primary.Attributes["secret"]
						body := fake.robotBodies[0]
						if _, present := body["permissions"]; present {
							return fmt.Errorf("create sent permissions alongside a scope: %s", body["permissions"])
						}
						if string(body["project"]) != `"ci-images"` || string(body["scope"]) != `"pull"` {
							return fmt.Errorf("create body was %v", body)
						}
						return nil
					},
				),
			},
			{
				// A new trigger rotates in place: same robot, new secret.
				Config: config("2"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ankra_registry_robot.puller", "secret", "secret-2"),
					resource.TestCheckResourceAttrSet("ankra_registry_robot.puller", "rotated_at"),
					func(state *terraform.State) error {
						if fake.rotations != 1 || len(fake.robotBodies) != 1 {
							return fmt.Errorf("expected one rotation and no re-create, got %d rotations and %d creates", fake.rotations, len(fake.robotBodies))
						}
						if state.RootModule().Resources["ankra_registry_robot.puller"].Primary.Attributes["secret"] == firstSecret {
							return fmt.Errorf("rotation kept the old secret")
						}
						return nil
					},
				),
			},
			{
				ResourceName:            "ankra_registry_robot.puller",
				ImportState:             true,
				ImportStateId:           "cluster-puller",
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"secret", "docker_login", "rotation_triggers"},
			},
			{
				ResourceName:      "ankra_registry_project.ci",
				ImportState:       true,
				ImportStateId:     "ci-images",
				ImportStateVerify: true,
			},
			{
				// Deleted outside Terraform: the next apply mints it again.
				PreConfig: func() { delete(fake.robots, "cluster-puller") },
				Config:    config("2"),
				Check: func(_ *terraform.State) error {
					if _, exists := fake.robots["cluster-puller"]; !exists || len(fake.robotBodies) != 2 {
						return fmt.Errorf("robot deleted outside Terraform was not recreated")
					}
					return nil
				},
			},
			{
				// The name now belongs to a robot minted elsewhere: Read must
				// not adopt it, so the plan proposes creating ours.
				PreConfig: func() {
					fake.robots["cluster-puller"]["created_at"] = "2026-10-18T00:00:00Z"
				},
				Config:             config("2"),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
		},
	})
}

func TestAccRegistryRobotRejectsInvalidConfigAtPlan(t *testing.T) {
	fake := newRouteFake(t)
	for _, body := range []string{
		`name = "Bad_Name"`,
		`name = "ok-name"
  scope = "admin"`,
		`name = "ok-name"
  scope = "push"
  permissions = ["repository:delete"]`,
		`name = "ok-name"
  description = "trailing "`,
	} {
		resource.Test(t, resource.TestCase{
			ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
			Steps: []resource.TestStep{{
				Config:      fake.providerBlock() + "resource \"ankra_registry_robot\" \"bad\" {\n  " + body + "\n}\n",
				PlanOnly:    true,
				ExpectError: regexp.MustCompile(`Invalid value|Conflicting robot grants`),
			}},
		})
	}
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config:      fake.providerBlock() + "resource \"ankra_registry_project\" \"bad\" {\n  name = \"default\"\n}\n",
			PlanOnly:    true,
			ExpectError: regexp.MustCompile(`Invalid value`),
		}},
	})
	if served := fake.served(); len(served) != 0 {
		t.Errorf("a refused configuration still reached the platform: %v", served)
	}
}

func TestAccRegistryRetentionLifecycle(t *testing.T) {
	fake := newRegistryFake(t)
	withRules := fake.providerBlock() + `
resource "ankra_registry_retention" "this" {
  default = {
    keep_days   = 30
    keep_latest = 10
  }
  rules = [
    { repository_pattern = "ankra-ci/**", keep_days = 0, keep_latest = 3 },
  ]
}
`
	withoutRules := fake.providerBlock() + `
resource "ankra_registry_retention" "this" {
  default = {
    keep_days   = 14
    keep_latest = 5
  }
}
`

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: withRules,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ankra_registry_retention.this", "source", "organisation"),
					resource.TestCheckResourceAttr("ankra_registry_retention.this", "rules.#", "1"),
					resource.TestCheckResourceAttr("ankra_registry_retention.this", "default_in_force.keep_days", "30"),
					func(_ *terraform.State) error {
						body := fake.retentionBodies[0]
						if string(body["expected_version"]) != `"version-0"` {
							return fmt.Errorf("the first save sent expected_version %s, want the version it read before creating", body["expected_version"])
						}
						if !strings.Contains(string(body["rules"]), `"ankra-ci/**"`) {
							return fmt.Errorf("create sent rules %s", body["rules"])
						}
						return nil
					},
				),
			},
			{
				Config: withoutRules,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckNoResourceAttr("ankra_registry_retention.this", "rules"),
					resource.TestCheckResourceAttr("ankra_registry_retention.this", "default.keep_latest", "5"),
					func(_ *terraform.State) error {
						body := fake.retentionBodies[len(fake.retentionBodies)-1]
						if string(body["rules"]) != "[]" {
							return fmt.Errorf("a removed rule list was sent as %s, which would leave the rules in place", body["rules"])
						}
						if string(body["expected_version"]) != `"version-1"` {
							return fmt.Errorf("update sent expected_version %s, want the version it read", body["expected_version"])
						}
						return nil
					},
				),
			},
			{
				ResourceName:      "ankra_registry_retention.this",
				ImportState:       true,
				ImportStateId:     "registry_retention",
				ImportStateVerify: true,
			},
			{
				// A rule added outside Terraform is drift and is removed.
				PreConfig: func() {
					fake.retention.rules = []map[string]any{{"repository_pattern": "storefront/**", "application_id": nil, "keep_days": 0, "keep_latest": 1}}
					fake.retention.version++
				},
				Config: withoutRules,
				Check: func(_ *terraform.State) error {
					if len(fake.retention.rules) != 0 {
						return fmt.Errorf("rule added outside Terraform survived the apply")
					}
					return nil
				},
			},
		},
		CheckDestroy: func(_ *terraform.State) error {
			body := fake.retentionBodies[len(fake.retentionBodies)-1]
			if string(body["default"]) != "null" || string(body["rules"]) != "[]" {
				return fmt.Errorf("destroy sent default %s and rules %s instead of restoring the platform default", body["default"], body["rules"])
			}
			if fake.retention.defaultPolicy != nil {
				return fmt.Errorf("destroy left the organisation policy in place")
			}
			return nil
		},
	})
}

func TestAccRegistryRetentionRulesNeedDefault(t *testing.T) {
	fake := newRegistryFake(t)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config: fake.providerBlock() + `
resource "ankra_registry_retention" "this" {
  rules = [{ repository_pattern = "ankra-ci/**", keep_days = 0, keep_latest = 3 }]
}
`,
			ExpectError: regexp.MustCompile(`project-wide policy`),
		}},
	})
}

// registryFakeNormalisePermissions mirrors normaliseRegistryRobotPermissions:
// lower-case and trim each grant, add repository:pull to push, deduplicate,
// answer in catalogue order, and name the preset the result amounts to.
func registryFakeNormalisePermissions(requested []string) (string, []string) {
	catalogue := []string{"repository:pull", "repository:push", "repository:delete", "artifact:read", "artifact:delete", "tag:create", "tag:delete"}
	selected := map[string]bool{}
	for _, permission := range requested {
		selected[strings.ToLower(strings.TrimSpace(permission))] = true
	}
	if selected["repository:push"] {
		selected["repository:pull"] = true
	}
	ordered := []string{}
	for _, permission := range catalogue {
		if selected[permission] {
			ordered = append(ordered, permission)
		}
	}
	switch strings.Join(ordered, ",") {
	case "repository:pull":
		return "pull", ordered
	case "repository:pull,repository:push":
		return "push", ordered
	}
	return "custom", ordered
}

// TestAccRegistryRobotPermissionsConverge pins the platform's normalisation:
// a grant list written in another case, or without the repository:pull the
// platform adds to push, must neither fail the apply with an inconsistent
// result nor plan a replacement (which would revoke the login) afterwards.
func TestAccRegistryRobotPermissionsConverge(t *testing.T) {
	fake := newRegistryFake(t)
	config := fake.providerBlock() + `
resource "ankra_registry_robot" "ci" {
  name              = "ci-pusher"
  permissions       = ["Repository:Push", "artifact:delete"]
  rotation_triggers = {}
}
`
	withoutTriggers := fake.providerBlock() + `
resource "ankra_registry_robot" "ci" {
  name        = "ci-pusher"
  permissions = ["Repository:Push", "artifact:delete"]
}
`
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: config,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ankra_registry_robot.ci", "permissions.#", "2"),
					resource.TestCheckResourceAttr("ankra_registry_robot.ci", "scope", "custom"),
				),
			},
			{
				// Dropping an empty trigger map is not a rotation.
				Config: withoutTriggers,
				Check: func(_ *terraform.State) error {
					if fake.rotations != 0 || len(fake.robotBodies) != 1 {
						return fmt.Errorf("got %d rotations and %d creates, want none and one", fake.rotations, len(fake.robotBodies))
					}
					return nil
				},
			},
		},
	})
}

func TestAccRegistryRetentionRefusesToOverwrite(t *testing.T) {
	fake := newRegistryFake(t)
	fake.retention = registryFakeRetention{defaultPolicy: map[string]any{"keep_days": 90, "keep_latest": 20}, version: 3}
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config: fake.providerBlock() + `
resource "ankra_registry_retention" "this" {
  default = { keep_days = 7, keep_latest = 1 }
}
`,
			ExpectError: regexp.MustCompile(`Registry retention already set`),
		}},
	})
	if len(fake.retentionBodies) != 0 || fake.retention.defaultPolicy["keep_days"] != 90 {
		t.Errorf("create overwrote the organisation's existing retention")
	}
}
