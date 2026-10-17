// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"encoding/json"
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

func TestAccApplicationLifecycle(t *testing.T) {
	fake := newApplicationFake(t)

	withRegistry := fake.providerBlock() + `
resource "ankra_application" "web" {
  name                     = "web"
  app_repo_credential_name = "github-main"
  app_repo_owner           = "acme"
  app_repo_name            = "web"

  image_registry = {
    url             = "oci://harbor.acme.dev/apps"
    credential_name = "harbor"
    component_repositories = {
      api = "api-image"
    }
  }
}
`
	rebound := fake.providerBlock() + `
resource "ankra_application" "web" {
  name                     = "web"
  app_repo_credential_name = "github-second"
  app_repo_owner           = "acme"
  app_repo_name            = "web"
}

data "ankra_applications" "all" {
  depends_on = [ankra_application.web, ankra_application.api]
}

data "ankra_application" "web" {
  id = ankra_application.web.id
}

resource "ankra_application" "api" {
  name                     = "api"
  app_repo_credential_name = "github-main"
  app_repo_owner           = "acme"
  app_repo_name            = "api"
  app_repo_branch          = "develop"
}
`

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: withRegistry,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("ankra_application.web", "id"),
					resource.TestCheckResourceAttr("ankra_application.web", "app_repo_branch", "main"),
					resource.TestCheckResourceAttr("ankra_application.web", "state", "up"),
					resource.TestCheckResourceAttr("ankra_application.web", "container_image_url", "harbor.acme.dev/apps/web"),
					resource.TestCheckResourceAttr("ankra_application.web", "image_registry.credential_name", "harbor"),
					resource.TestCheckResourceAttr("ankra_application.web", "image_registry.flat_repositories", "false"),
					resource.TestCheckResourceAttr("ankra_application.web", "image_registry.component_repositories.api", "api-image"),
					func(_ *terraform.State) error {
						body := fake.lastBody("create application")
						if rawString(body["app_repo_branch"]) != "main" || rawString(body["app_repo_credential_name"]) != "github-main" {
							return fmt.Errorf("create body %v did not carry the branch and credential", body)
						}
						var registry map[string]any
						if err := json.Unmarshal(body["image_registry"], &registry); err != nil || registry["url"] != "oci://harbor.acme.dev/apps" {
							return fmt.Errorf("create body did not declare the registry: %s", body["image_registry"])
						}
						if fake.bodies["set image registry"] != nil {
							return fmt.Errorf("create sent a separate registry write")
						}
						return nil
					},
				),
			},
			{
				Config: rebound,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ankra_application.web", "app_repo_credential_name", "github-second"),
					resource.TestCheckNoResourceAttr("ankra_application.web", "image_registry.url"),
					resource.TestCheckResourceAttr("ankra_application.web", "container_image_url", "registry.ankra.app/org/web"),
					resource.TestCheckResourceAttr("ankra_application.api", "app_repo_branch", "develop"),
					resource.TestCheckResourceAttr("data.ankra_applications.all", "applications.#", "2"),
					resource.TestCheckResourceAttr("data.ankra_application.web", "name", "web"),
					resource.TestCheckResourceAttr("data.ankra_application.web", "deployment_state", "not_deployed"),
					func(_ *terraform.State) error {
						if credential := rawString(fake.lastBody("set repository credential")["credential_name"]); credential != "github-second" {
							return fmt.Errorf("credential rebind sent %q", credential)
						}
						if raw := string(fake.lastBody("set image registry")["image_registry"]); raw != "null" {
							return fmt.Errorf("removing the registry block sent image_registry=%s, want an explicit null", raw)
						}
						return nil
					},
				),
			},
			{
				ResourceName:      "ankra_application.web",
				ImportState:       true,
				ImportStateVerify: true,
			},
			{
				PreConfig: func() {
					for applicationID, application := range fake.applications {
						if application["name"] == "web" {
							delete(fake.applications, applicationID)
						}
					}
				},
				Config: rebound,
				Check: func(_ *terraform.State) error {
					for _, application := range fake.applications {
						if application["name"] == "web" {
							return nil
						}
					}
					return fmt.Errorf("application deleted outside Terraform was not recreated")
				},
			},
		},
	})
}

func TestAccApplicationDuplicateName(t *testing.T) {
	fake := newApplicationFake(t)
	fake.applications["app-existing"] = map[string]any{"id": "app-existing", "name": "web"}

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: fake.providerBlock() + `
resource "ankra_application" "web" {
  name                     = "web"
  app_repo_credential_name = "github-main"
  app_repo_owner           = "acme"
  app_repo_name            = "web"
}
`,
				ExpectError: regexp.MustCompile("already exists"),
			},
		},
	})
}

func TestAccApplicationSettingsLifecycle(t *testing.T) {
	fake := newApplicationFake(t)

	withLabel := fake.providerBlock() + `
resource "ankra_application_settings" "this" {
  ci_runner_label                = "self-hosted"
  auto_merge_setup_pull_requests = true
}
`
	withoutLabel := fake.providerBlock() + `
resource "ankra_application_settings" "this" {}
`

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: withLabel,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ankra_application_settings.this", "ci_runner_label", "self-hosted"),
					resource.TestCheckResourceAttr("ankra_application_settings.this", "auto_merge_setup_pull_requests", "true"),
				),
			},
			{
				Config: withoutLabel,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckNoResourceAttr("ankra_application_settings.this", "ci_runner_label"),
					resource.TestCheckResourceAttr("ankra_application_settings.this", "auto_merge_setup_pull_requests", "false"),
					func(_ *terraform.State) error {
						if raw := string(fake.lastBody("update settings")["ci_runner_label"]); raw != "null" {
							return fmt.Errorf("removing the label sent ci_runner_label=%s, want an explicit null", raw)
						}
						return nil
					},
				),
			},
			{
				ResourceName:      "ankra_application_settings.this",
				ImportState:       true,
				ImportStateId:     "application_settings",
				ImportStateVerify: true,
			},
			{
				PreConfig: func() {
					label := "drifted"
					fake.settingsLabel = &label
				},
				Config: withoutLabel,
				Check: func(_ *terraform.State) error {
					if fake.settingsLabel != nil {
						return fmt.Errorf("a label set outside Terraform was not cleared: %q", *fake.settingsLabel)
					}
					return nil
				},
			},
			{
				Config:  withLabel,
				Destroy: true,
				Check: func(_ *terraform.State) error {
					if fake.settingsLabel != nil || fake.settingsAutoMerge {
						return fmt.Errorf("destroy did not restore the defaults")
					}
					return nil
				},
			},
		},
	})
}

func TestAccApplicationAutoDeployLifecycle(t *testing.T) {
	fake := newApplicationFake(t)
	fake.applications["app-1"] = map[string]any{"id": "app-1", "name": "web", "app_repo_branch": "main"}
	fake.autoDeploy["app-1"] = true

	config := func(enabled bool) string {
		return fake.providerBlock() + fmt.Sprintf(`
resource "ankra_application_auto_deploy" "web" {
  application_id = "app-1"
  enabled        = %t
}
`, enabled)
	}

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: config(false),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ankra_application_auto_deploy.web", "enabled", "false"),
					resource.TestCheckResourceAttr("ankra_application_auto_deploy.web", "branch", "main"),
					func(_ *terraform.State) error {
						if fake.autoDeploy["app-1"] {
							return fmt.Errorf("auto-deploy still on")
						}
						return nil
					},
				),
			},
			{
				PreConfig: func() { fake.autoDeploy["app-1"] = true },
				Config:    config(false),
				Check: func(_ *terraform.State) error {
					if fake.autoDeploy["app-1"] {
						return fmt.Errorf("auto-deploy turned on outside Terraform was not turned off again")
					}
					return nil
				},
			},
			{
				Config: config(true),
				Check:  resource.TestCheckResourceAttr("ankra_application_auto_deploy.web", "enabled", "true"),
			},
			{
				ResourceName:                         "ankra_application_auto_deploy.web",
				ImportState:                          true,
				ImportStateId:                        "app-1",
				ImportStateVerify:                    true,
				ImportStateVerifyIdentifierAttribute: "application_id",
			},
		},
	})
}

func TestAccApplicationEnvSecretLifecycle(t *testing.T) {
	fake := newApplicationFake(t)
	fake.applications["app-1"] = map[string]any{"id": "app-1", "name": "web"}

	config := func(value string) string {
		return fake.providerBlock() + fmt.Sprintf(`
resource "ankra_application_env_secret" "database" {
  application_id = "app-1"
  key            = "DATABASE_URL"
  value          = %q
}
`, value)
	}

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: config("postgres://one"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ankra_application_env_secret.database", "id", "app-1/DATABASE_URL"),
					resource.TestCheckResourceAttr("ankra_application_env_secret.database", "status", "set"),
					func(_ *terraform.State) error {
						if value := fake.envSecrets["app-1"]["DATABASE_URL"]; value != "postgres://one" {
							return fmt.Errorf("stored value %q", value)
						}
						return nil
					},
				),
			},
			{
				Config: config("postgres://two"),
				Check: func(_ *terraform.State) error {
					if value := fake.envSecrets["app-1"]["DATABASE_URL"]; value != "postgres://two" {
						return fmt.Errorf("value was not updated in place: %q", value)
					}
					return nil
				},
			},
			{
				ResourceName:            "ankra_application_env_secret.database",
				ImportState:             true,
				ImportStateId:           "app-1/DATABASE_URL",
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"value"},
			},
			{
				// The value vanished from the secret store: the key reads
				// value_missing, and the next apply writes it again.
				PreConfig: func() { fake.envSecrets["app-1"]["DATABASE_URL"] = "" },
				Config:    config("postgres://two"),
				Check: func(_ *terraform.State) error {
					if value := fake.envSecrets["app-1"]["DATABASE_URL"]; value != "postgres://two" {
						return fmt.Errorf("a missing value was not written again: %q", value)
					}
					return nil
				},
			},
			{
				PreConfig: func() { delete(fake.envSecrets["app-1"], "DATABASE_URL") },
				Config:    config("postgres://two"),
				Check: func(_ *terraform.State) error {
					if _, exists := fake.envSecrets["app-1"]["DATABASE_URL"]; !exists {
						return fmt.Errorf("secret deleted outside Terraform was not recreated")
					}
					return nil
				},
			},
		},
	})
}

func TestAccApplicationEnvSecretRejectsInvalidKey(t *testing.T) {
	fake := newApplicationFake(t)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: fake.providerBlock() + `
resource "ankra_application_env_secret" "bad" {
  application_id = "app-1"
  key            = "1-BAD/KEY"
  value          = "x"
}
`,
				ExpectError: regexp.MustCompile("Invalid environment secret key"),
			},
		},
	})
	if served := fake.served(); len(served) != 0 {
		t.Errorf("an invalid key reached the API: %v", served)
	}
}

func TestAccApplicationDeploymentLifecycle(t *testing.T) {
	fake := newApplicationFake(t)
	fake.applications["app-1"] = map[string]any{"id": "app-1", "name": "web"}

	withHost := fake.providerBlock() + `
resource "ankra_application_deployment" "production" {
  application_id = "app-1"
  cluster_id     = "cluster-1"
  inputs = {
    ingress_host = "web.acme.dev"
    replicas     = "2"
  }
}
`
	highAvailability := fake.providerBlock() + `
resource "ankra_application_deployment" "production" {
  application_id = "app-1"
  cluster_id     = "cluster-1"
  deploy_mode    = "high_availability"
  inputs = {
    ingress_host = "web.acme.dev"
  }
}
`

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: withHost,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("ankra_application_deployment.production", "id"),
					resource.TestCheckResourceAttr("ankra_application_deployment.production", "namespace", "web"),
					resource.TestCheckResourceAttr("ankra_application_deployment.production", "deploy_mode", "quick"),
					resource.TestCheckResourceAttr("ankra_application_deployment.production", "stack_name", "web-stack"),
				),
			},
			{
				Config: highAvailability,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ankra_application_deployment.production", "deploy_mode", "high_availability"),
					func(_ *terraform.State) error {
						body := fake.lastBody("deploy")
						if rawString(body["namespace"]) != "web" || rawString(body["deploy_mode"]) != "high_availability" {
							return fmt.Errorf("redeploy body %v", body)
						}
						var resets []string
						_ = json.Unmarshal(body["reset_inputs"], &resets)
						if len(resets) != 1 || resets[0] != "replicas" {
							return fmt.Errorf("a removed input was not reset: %v", resets)
						}
						if len(fake.installations["app-1"]) != 1 {
							return fmt.Errorf("redeploy created a second deployment")
						}
						if _, kept := fake.installations["app-1"][0]["inputs"].(map[string]any)["replicas"]; kept {
							return fmt.Errorf("platform still holds the removed input")
						}
						return nil
					},
				),
			},
			{
				ResourceName:            "ankra_application_deployment.production",
				ImportState:             true,
				ImportStateId:           "app-1/cluster-1/web",
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"inputs", "status"},
			},
			{
				PreConfig:   func() { fake.installations["app-1"][0]["status"] = "removing" },
				Config:      highAvailability,
				ExpectError: regexp.MustCompile("being removed"),
			},
			{
				PreConfig: func() { fake.installations["app-1"] = nil },
				Config:    highAvailability,
				Check: func(_ *terraform.State) error {
					if len(fake.installations["app-1"]) != 1 {
						return fmt.Errorf("deployment removed outside Terraform was not recreated")
					}
					return nil
				},
			},
			{
				Config:  highAvailability,
				Destroy: true,
				Check: func(_ *terraform.State) error {
					body := fake.lastBody("remove deployment")
					if rawString(body["cluster_id"]) != "cluster-1" || rawString(body["namespace"]) != "web" {
						return fmt.Errorf("removal body %v", body)
					}
					if len(fake.installations["app-1"]) != 0 {
						return fmt.Errorf("deployment still present after destroy")
					}
					return nil
				},
			},
		},
	})
}

func TestAccApplicationAIConfigLifecycle(t *testing.T) {
	fake := newApplicationFake(t)
	fake.applications["app-1"] = map[string]any{"id": "app-1", "name": "web", "app_repo_owner": "acme", "app_repo_name": "web"}

	twoLanes := fake.providerBlock() + `
resource "ankra_application_ai_config" "web" {
  application_id = "app-1"
  lanes = {
    pull_request = {
      enabled          = true
      model            = "claude-opus"
      ignore_paths     = []
      max_runs_per_day = 10
    }
    code = {
      enabled = false
    }
  }
}
`
	oneLane := fake.providerBlock() + `
resource "ankra_application_ai_config" "web" {
  application_id = "app-1"
  lanes = {
    pull_request = {
      enabled  = true
      autonomy = "propose_fix"
    }
  }
}
`

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: twoLanes,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ankra_application_ai_config.web", "repo_full_name", "acme/web"),
					resource.TestCheckResourceAttr("ankra_application_ai_config.web", "lanes.pull_request.model", "claude-opus"),
					resource.TestCheckResourceAttr("ankra_application_ai_config.web", "lanes.pull_request.ignore_paths.#", "0"),
					resource.TestCheckNoResourceAttr("ankra_application_ai_config.web", "lanes.pull_request.max_reviews_per_pull_request"),
					resource.TestCheckResourceAttr("ankra_application_ai_config.web", "lanes.code.enabled", "false"),
					func(_ *terraform.State) error {
						override := fake.aiOverrides["app-1"]["pull_request"]
						if override == nil || override.settings["model"] != "claude-opus" {
							return fmt.Errorf("pull_request override not stored: %+v", override)
						}
						if _, present := override.settings["autonomy"]; present {
							return fmt.Errorf("an unset field was sent instead of inherited")
						}
						return nil
					},
				),
			},
			{
				Config: oneLane,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ankra_application_ai_config.web", "lanes.pull_request.autonomy", "propose_fix"),
					resource.TestCheckNoResourceAttr("ankra_application_ai_config.web", "lanes.code.enabled"),
					func(_ *terraform.State) error {
						if _, kept := fake.aiOverrides["app-1"]["code"]; kept {
							return fmt.Errorf("a lane removed from configuration still overrides the organisation")
						}
						if _, kept := fake.aiOverrides["app-1"]["pull_request"].settings["model"]; kept {
							return fmt.Errorf("the lane override was patched, not replaced")
						}
						return nil
					},
				),
			},
			{
				PreConfig: func() { delete(fake.aiOverrides["app-1"], "pull_request") },
				Config:    oneLane,
				Check: func(_ *terraform.State) error {
					if fake.aiOverrides["app-1"]["pull_request"] == nil {
						return fmt.Errorf("an override cleared outside Terraform was not restored")
					}
					return nil
				},
			},
			{
				ResourceName:                         "ankra_application_ai_config.web",
				ImportState:                          true,
				ImportStateId:                        "app-1",
				ImportStateVerify:                    true,
				ImportStateVerifyIdentifierAttribute: "application_id",
				ImportStateVerifyIgnore:              []string{"lanes.pull_request.autonomy"},
			},
			{
				Config:  oneLane,
				Destroy: true,
				Check: func(_ *terraform.State) error {
					if len(fake.aiOverrides["app-1"]) != 0 {
						return fmt.Errorf("destroy left overrides: %v", fake.aiOverrides["app-1"])
					}
					return nil
				},
			},
		},
	})
}

func TestAccApplicationDemoConfigLifecycle(t *testing.T) {
	fake := newApplicationFake(t)
	fake.applications["app-1"] = map[string]any{"id": "app-1", "name": "web"}

	protected := fake.providerBlock() + `
resource "ankra_application_demo_config" "web" {
  application_id      = "app-1"
  database            = true
  migrate_command     = "npm run migrate"
  database_extensions = ["pgcrypto"]
  protected           = true
  env = [
    { name = "MODE", value = "demo" },
    { name = "TOKEN", value = "$${SECRET_SLOT:11111111-1111-1111-1111-111111111111}", secret = true },
  ]
}
`
	plain := fake.providerBlock() + `
resource "ankra_application_demo_config" "web" {
  application_id = "app-1"
  protected      = true
  env = [
    { name = "MODE", value = "preview" },
  ]
}
`

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: protected,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ankra_application_demo_config.web", "generated_password", "minted-app-1"),
					resource.TestCheckResourceAttr("ankra_application_demo_config.web", "protected_username", "demo"),
					resource.TestCheckResourceAttr("ankra_application_demo_config.web", "env.1.secret", "true"),
					resource.TestCheckResourceAttr("ankra_application_demo_config.web", "database_extensions.0", "pgcrypto"),
				),
			},
			{
				Config: plain,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ankra_application_demo_config.web", "generated_password", "minted-app-1"),
					resource.TestCheckNoResourceAttr("ankra_application_demo_config.web", "migrate_command"),
					resource.TestCheckResourceAttr("ankra_application_demo_config.web", "database", "false"),
					func(_ *terraform.State) error {
						body := fake.lastBody("update demo-config")
						if string(body["base_stack"]) != "null" || rawString(body["migrate_command"]) != "" || string(body["database_extensions"]) != "[]" {
							return fmt.Errorf("removed settings were not cleared explicitly: %v", body)
						}
						if _, sent := body["stack_profile"]; sent {
							return fmt.Errorf("the unmanaged stack profile was written")
						}
						return nil
					},
				),
			},
			{
				ResourceName:                         "ankra_application_demo_config.web",
				ImportState:                          true,
				ImportStateId:                        "app-1",
				ImportStateVerify:                    true,
				ImportStateVerifyIdentifierAttribute: "application_id",
				ImportStateVerifyIgnore:              []string{"generated_password"},
			},
			{
				Config:  plain,
				Destroy: true,
				Check: func(_ *terraform.State) error {
					stored := fake.demos["app-1"]
					if environment, _ := stored["env"].([]any); stored["protected"] != false || len(environment) != 0 {
						return fmt.Errorf("destroy did not restore the defaults: %v", stored)
					}
					return nil
				},
			},
		},
	})
}
