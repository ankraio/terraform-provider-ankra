// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

const (
	applicationScopeProject      = "11111111-2222-3333-4444-555555555555"
	applicationScopeOtherProject = "99999999-8888-7777-6666-555555555555"
)

// TestAccApplicationScopeMoveKeepsTheResources pins that moving the provider
// to another project, where the platform answers 404 for the application, its
// deployment, its env secret and a repository's schedule, neither drops them
// from state (which would plan duplicates) nor lets destroy report success
// while they live on in the original project.
func TestAccApplicationScopeMoveKeepsTheResources(t *testing.T) {
	fake := newApplicationFake(t)
	inProject := func(project string) string {
		return fmt.Sprintf(`
provider "ankra" {
  token      = "test-token"
  base_url   = %q
  project_id = %q
}

resource "ankra_application" "web" {
  name                     = "web"
  app_repo_credential_name = "github-main"
  app_repo_owner           = "acme"
  app_repo_name            = "web"
}

resource "ankra_application_env_secret" "token" {
  application_id = ankra_application.web.id
  key            = "API_TOKEN"
  value          = "secret"
}

resource "ankra_application_deployment" "production" {
  application_id = ankra_application.web.id
  cluster_id     = "cluster-1"
}

resource "ankra_pipeline_repository" "platform" {
  provider_name = "github"
  owner         = "acme"
  name          = "platform"
}

resource "ankra_pipeline_schedule" "nightly" {
  repository_id = ankra_pipeline_repository.platform.id
  cron          = "0 3 * * *"
}
`, fake.server.URL, project)
	}
	unchanged := func(_ *terraform.State) error {
		if len(fake.applications) != 1 || len(fake.repositories) != 1 {
			return fmt.Errorf("a scope move created duplicates: %d applications, %d repositories", len(fake.applications), len(fake.repositories))
		}
		for applicationID := range fake.applications {
			if len(fake.installations[applicationID]) != 1 || fake.envSecrets[applicationID]["API_TOKEN"] != "secret" {
				return fmt.Errorf("the deployment or env secret in the original project is gone")
			}
		}
		for repositoryID := range fake.repositories {
			if len(fake.schedules[repositoryID]) != 1 {
				return fmt.Errorf("the schedule in the original project is gone")
			}
		}
		return nil
	}

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{Config: inProject(applicationScopeProject), Check: unchanged},
			{
				Config:      inProject(applicationScopeOtherProject),
				ExpectError: regexp.MustCompile(`Object not visible in the provider's current scope`),
			},
			{
				Config:      inProject(applicationScopeOtherProject),
				Destroy:     true,
				ExpectError: regexp.MustCompile(`Object not visible in the provider's current scope`),
			},
			{Config: inProject(applicationScopeProject), Check: unchanged},
		},
	})
}
