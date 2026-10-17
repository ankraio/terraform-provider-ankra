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

func (fake *applicationFake) repositoryNamed(name string) (string, map[string]any) {
	for repositoryID, repository := range fake.repositories {
		if repository["name"] == name {
			return repositoryID, repository
		}
	}
	return "", nil
}

func TestAccPipelineRepositoryLifecycle(t *testing.T) {
	fake := newApplicationFake(t)

	pinned := fake.providerBlock() + `
resource "ankra_pipeline_repository" "platform" {
  provider_name   = "github"
  owner           = "acme"
  name            = "platform"
  credential_name = "github-main"
  cluster_id      = "cluster-ci"
}

resource "ankra_pipeline_repository" "docs" {
  provider_name = "gitlab"
  owner         = "acme"
  name          = "docs"
}

data "ankra_pipeline_repositories" "github" {
  provider_name = "github"
  depends_on    = [ankra_pipeline_repository.platform, ankra_pipeline_repository.docs]
}

data "ankra_pipeline_repositories" "all" {
  depends_on = [ankra_pipeline_repository.platform, ankra_pipeline_repository.docs]
}
`
	unpinned := fake.providerBlock() + `
resource "ankra_pipeline_repository" "platform" {
  provider_name   = "github"
  owner           = "acme"
  name            = "platform"
  credential_name = "github-main"
  workspaces_only = true
}
`

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: pinned,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("ankra_pipeline_repository.platform", "id"),
					resource.TestCheckResourceAttr("ankra_pipeline_repository.platform", "default_branch", "main"),
					resource.TestCheckResourceAttr("ankra_pipeline_repository.platform", "cluster_id", "cluster-ci"),
					resource.TestCheckNoResourceAttr("ankra_pipeline_repository.docs", "credential_name"),
					resource.TestCheckResourceAttr("data.ankra_pipeline_repositories.github", "repositories.#", "1"),
					resource.TestCheckResourceAttr("data.ankra_pipeline_repositories.all", "repositories.#", "2"),
				),
			},
			{
				Config: unpinned,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckNoResourceAttr("ankra_pipeline_repository.platform", "cluster_id"),
					resource.TestCheckResourceAttr("ankra_pipeline_repository.platform", "workspaces_only", "true"),
					func(_ *terraform.State) error {
						body := fake.lastBody("update repository")
						if string(body["cluster_id"]) != "null" || string(body["workspaces_only"]) != "true" {
							return fmt.Errorf("update body %v; want an explicit null cluster and workspaces_only", body)
						}
						if _, docs := fake.repositoryNamed("docs"); docs != nil {
							return fmt.Errorf("a removed repository is still connected")
						}
						return nil
					},
				),
			},
			{
				ResourceName:      "ankra_pipeline_repository.platform",
				ImportState:       true,
				ImportStateVerify: true,
			},
			{
				PreConfig: func() {
					repositoryID, _ := fake.repositoryNamed("platform")
					delete(fake.repositories, repositoryID)
				},
				Config: unpinned,
				Check: func(_ *terraform.State) error {
					if _, repository := fake.repositoryNamed("platform"); repository == nil {
						return fmt.Errorf("repository disconnected outside Terraform was not connected again")
					}
					return nil
				},
			},
		},
	})
}

func TestAccPipelineRepositoryAlreadyConnected(t *testing.T) {
	fake := newApplicationFake(t)
	fake.repositories["repository-existing"] = map[string]any{"id": "repository-existing", "provider": "github", "owner": "acme", "name": "platform"}

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: fake.providerBlock() + `
resource "ankra_pipeline_repository" "platform" {
  provider_name = "github"
  owner         = "ACME"
  name          = "platform"
}
`,
				ExpectError: regexp.MustCompile("terraform import"),
			},
		},
	})
}

func TestAccPipelineDefinitionScheduleAndTrainLifecycle(t *testing.T) {
	fake := newApplicationFake(t)
	fake.repositories["repository-1"] = map[string]any{"id": "repository-1", "provider": "github", "owner": "acme", "name": "platform", "application_id": nil}
	fake.repositories["repository-app"] = map[string]any{"id": "repository-app", "provider": "github", "owner": "acme", "name": "web", "application_id": "app-1"}

	initial := fake.providerBlock() + `
resource "ankra_pipeline_definition" "platform" {
  repository_id = "repository-1"
  spec_yaml     = <<-EOT
    # build on every push
    version: 1
    stages:
      - name: test
  EOT
}

resource "ankra_pipeline_schedule" "nightly" {
  application_id = "app-1"
  cron           = "0  3 * * *"
  inputs = {
    suite = "full"
  }
}

resource "ankra_pipeline_merge_train" "platform" {
  repository_id = "repository-1"
  enabled       = true
  max_cars      = 3
}
`
	changed := fake.providerBlock() + `
resource "ankra_pipeline_definition" "platform" {
  repository_id = "repository-1"
  spec_yaml     = <<-EOT
    version: 1
    stages:
      - name: lint
  EOT
}

resource "ankra_pipeline_schedule" "nightly" {
  application_id = "app-1"
  cron           = "0 4 * * *"
  timezone       = "Europe/Oslo"
  ref            = "release"
  enabled        = false
}

resource "ankra_pipeline_merge_train" "platform" {
  repository_id = "repository-1"
  enabled       = true
}
`

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: initial,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("ankra_pipeline_definition.platform", "spec_hash"),
					resource.TestCheckResourceAttr("ankra_pipeline_definition.platform", "source", "generated"),
					resource.TestCheckResourceAttr("ankra_pipeline_schedule.nightly", "cron", "0  3 * * *"),
					resource.TestCheckResourceAttr("ankra_pipeline_schedule.nightly", "timezone", "UTC"),
					resource.TestCheckResourceAttr("ankra_pipeline_schedule.nightly", "enabled", "true"),
					resource.TestCheckResourceAttr("ankra_pipeline_merge_train.platform", "max_cars", "3"),
					func(_ *terraform.State) error {
						if len(fake.schedules["repository-app"]) != 1 {
							return fmt.Errorf("the application selector did not reach the linked repository")
						}
						return nil
					},
				),
			},
			{
				Config: changed,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ankra_pipeline_schedule.nightly", "ref", "release"),
					resource.TestCheckResourceAttr("ankra_pipeline_schedule.nightly", "enabled", "false"),
					resource.TestCheckNoResourceAttr("ankra_pipeline_schedule.nightly", "inputs"),
					resource.TestCheckResourceAttr("ankra_pipeline_merge_train.platform", "max_cars", "3"),
					func(_ *terraform.State) error {
						if string(fake.lastBody("update schedule")["inputs"]) != "{}" {
							return fmt.Errorf("removed inputs were not cleared: %s", fake.lastBody("update schedule")["inputs"])
						}
						if sent := string(fake.lastBody("set merge train")["max_cars"]); sent != "" && sent != "3" {
							return fmt.Errorf("an unset max_cars changed the stored value: %s", sent)
						}
						return nil
					},
				),
			},
			{
				ResourceName:                         "ankra_pipeline_merge_train.platform",
				ImportState:                          true,
				ImportStateId:                        "repository/repository-1",
				ImportStateVerify:                    true,
				ImportStateVerifyIdentifierAttribute: "repository_id",
			},
			{
				ResourceName: "ankra_pipeline_schedule.nightly",
				ImportState:  true,
				ImportStateIdFunc: func(state *terraform.State) (string, error) {
					return "application/app-1/" + state.RootModule().Resources["ankra_pipeline_schedule.nightly"].Primary.ID, nil
				},
				ImportStateVerify: true,
			},
			{
				ResourceName:                         "ankra_pipeline_definition.platform",
				ImportState:                          true,
				ImportStateId:                        "repository/repository-1",
				ImportStateVerify:                    true,
				ImportStateVerifyIdentifierAttribute: "repository_id",
				ImportStateVerifyIgnore:              []string{"spec_yaml"},
			},
			{
				// A push of the committed file recorded a different
				// definition: Terraform puts its own back.
				PreConfig: func() {
					fake.definitions["repository-1"]["spec_hash"] = "pushed"
					fake.definitions["repository-1"]["spec_yaml"] = "version: 1\nstages:\n  - name: pushed\n"
				},
				Config: changed,
				Check: func(_ *terraform.State) error {
					if fake.definitions["repository-1"]["spec_hash"] == "pushed" {
						return fmt.Errorf("a definition changed outside Terraform was not put back")
					}
					return nil
				},
			},
			{
				PreConfig: func() {
					fake.schedules["repository-app"] = nil
					fake.trains["repository-1"]["enabled"] = false
				},
				Config: changed,
				Check: func(_ *terraform.State) error {
					if len(fake.schedules["repository-app"]) != 1 {
						return fmt.Errorf("schedule deleted outside Terraform was not recreated")
					}
					if fake.trains["repository-1"]["enabled"] != true {
						return fmt.Errorf("merge train turned off outside Terraform was not turned on")
					}
					return nil
				},
			},
			{
				Config:  changed,
				Destroy: true,
				Check: func(_ *terraform.State) error {
					if fake.trains["repository-1"]["enabled"] != false {
						return fmt.Errorf("destroy left the merge train on")
					}
					if len(fake.schedules["repository-app"]) != 0 {
						return fmt.Errorf("destroy left the schedule")
					}
					if _, kept := fake.definitions["repository-1"]; !kept {
						return fmt.Errorf("the definition cannot be deleted and must stay")
					}
					return nil
				},
			},
		},
	})
}

func TestAccPipelineSelectorValidation(t *testing.T) {
	fake := newApplicationFake(t)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: fake.providerBlock() + `
resource "ankra_pipeline_merge_train" "both" {
  application_id = "app-1"
  repository_id  = "repository-1"
  enabled        = true
}
`,
				ExpectError: regexp.MustCompile("Invalid pipeline selector"),
			},
			{
				Config: fake.providerBlock() + `
resource "ankra_pipeline_merge_train" "too_many" {
  repository_id = "repository-1"
  enabled       = true
  max_cars      = 9
}
`,
				ExpectError: regexp.MustCompile("Value out of range"),
			},
		},
	})
	if served := fake.served(); len(served) != 0 {
		t.Errorf("an invalid configuration reached the API: %v", served)
	}
}
