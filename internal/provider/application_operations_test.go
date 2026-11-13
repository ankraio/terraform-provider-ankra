// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

const (
	operationsFakeApplicationID = "a1b2c3d4-0000-4000-8000-000000000001"
	operationsFakeEnvironmentID = "e1e2e3e4-0000-4000-8000-000000000002"
	operationsFakeRepositoryID  = "r1r2r3r4-0000-4000-8000-000000000003"
)

// applicationOperationsFake mirrors the application operation routes
// (go/internal/appsapi): autonomy layers are replaced whole and refused when
// the expected layer is stale, the setup auto-merge write needs an
// organisation admin, and operating expectations are adopted over the
// current revision.
type applicationOperationsFake struct {
	*routeFake
	applicationLayer   map[string]any
	environmentLayers  map[string]any
	layerWrites        []map[string]any
	autoFixBuilds      bool
	setupAutoMerge     any
	setupWrites        int
	notAdmin           bool
	environmentName    string
	renames            int
	expectations       map[string]any
	revision           int
	databaseBackup     bool
	applicationMode    any
	workspaceProfiles  json.RawMessage
	environmentMissing bool
}

func newApplicationOperationsFake(t *testing.T) *applicationOperationsFake {
	fake := &applicationOperationsFake{routeFake: newRouteFake(t), autoFixBuilds: true, environmentName: "prod-eu",
		environmentLayers: map[string]any{}, workspaceProfiles: json.RawMessage(`{}`)}
	base := "/api/v1/org/applications/{application_id}"
	known := func(writer http.ResponseWriter, request *http.Request) bool {
		if request.PathValue("application_id") != operationsFakeApplicationID {
			writeJSON(t, writer, http.StatusNotFound, map[string]any{"detail": "Application not found"})
			return false
		}
		return true
	}
	storeLayer := func(writer http.ResponseWriter, request *http.Request, stored any, keep func(map[string]any)) {
		var body map[string]any
		decodeJSON(t, request, &body)
		expected, _ := json.Marshal(body["expected"])
		current, _ := json.Marshal(stored)
		if string(expected) != string(current) {
			writeJSON(t, writer, http.StatusConflict, map[string]any{"detail": "The stored layer changed since it was read."})
			return
		}
		fake.layerWrites = append(fake.layerWrites, body)
		layer := map[string]any{"level": body["level"], "areas": body["areas"], "limits": body["limits"], "widened_by_admin": false}
		keep(layer)
		writeJSON(t, writer, http.StatusOK, layer)
	}
	fake.handle("GET "+base+"/autonomy", func(writer http.ResponseWriter, request *http.Request) {
		if known(writer, request) {
			writeJSON(t, writer, http.StatusOK, map[string]any{"application_id": operationsFakeApplicationID, "read_state": "ok",
				"layers": map[string]any{"application": fake.applicationLayer, "environments": fake.environmentLayers}})
		}
	})
	fake.handle("PUT "+base+"/autonomy", func(writer http.ResponseWriter, request *http.Request) {
		var stored any
		if fake.applicationLayer != nil {
			stored = fake.applicationLayer
		}
		storeLayer(writer, request, stored, func(layer map[string]any) { fake.applicationLayer = layer })
	})
	fake.handle("PUT "+base+"/environments/{environment_id}/autonomy", func(writer http.ResponseWriter, request *http.Request) {
		environmentID := request.PathValue("environment_id")
		storeLayer(writer, request, fake.environmentLayers[environmentID], func(layer map[string]any) { fake.environmentLayers[environmentID] = layer })
	})
	fake.handle("GET "+base+"/auto-fix-builds", func(writer http.ResponseWriter, request *http.Request) {
		if known(writer, request) {
			writeJSON(t, writer, http.StatusOK, map[string]any{"enabled": fake.autoFixBuilds})
		}
	})
	fake.handle("PUT "+base+"/auto-fix-builds", func(writer http.ResponseWriter, request *http.Request) {
		var body map[string]any
		decodeJSON(t, request, &body)
		fake.autoFixBuilds, _ = body["enabled"].(bool)
		writeJSON(t, writer, http.StatusOK, map[string]any{"enabled": fake.autoFixBuilds})
	})
	setupView := func() map[string]any {
		return map[string]any{"enabled": fake.setupAutoMerge, "organisation_enabled": true, "effective": fake.setupAutoMerge == nil || fake.setupAutoMerge == true}
	}
	fake.handle("GET "+base+"/auto-merge-setup-pull-requests", func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(t, writer, http.StatusOK, setupView())
	})
	fake.handle("PUT "+base+"/auto-merge-setup-pull-requests", func(writer http.ResponseWriter, request *http.Request) {
		if fake.notAdmin {
			writeJSON(t, writer, http.StatusForbidden, map[string]any{"detail": "Only organisation administrators can change this."})
			return
		}
		var body map[string]any
		decodeJSON(t, request, &body)
		fake.setupAutoMerge = body["enabled"]
		fake.setupWrites++
		writeJSON(t, writer, http.StatusOK, setupView())
	})
	fake.handle("GET "+base+"/environments", func(writer http.ResponseWriter, request *http.Request) {
		if !known(writer, request) {
			return
		}
		environments := []any{}
		if !fake.environmentMissing {
			environments = append(environments, map[string]any{"id": operationsFakeEnvironmentID, "name": fake.environmentName, "role": "production"})
		}
		writeJSON(t, writer, http.StatusOK, map[string]any{"application_id": operationsFakeApplicationID, "read_state": "ok", "read_reason": "",
			"environments": environments})
	})
	fake.handle("PATCH "+base+"/environments/{environment_id}", func(writer http.ResponseWriter, request *http.Request) {
		var body map[string]any
		decodeJSON(t, request, &body)
		fake.environmentName, _ = body["name"].(string)
		fake.renames++
		writeJSON(t, writer, http.StatusOK, map[string]any{"id": request.PathValue("environment_id"), "name": fake.environmentName})
	})
	expectationsView := func() map[string]any {
		var current any
		if fake.expectations != nil {
			current = map[string]any{"revision": fake.revision, "adopted_at": "2026-11-13T10:00:00Z", "adopted_by": "u", "expectations": fake.expectations}
		}
		return map[string]any{"application_id": operationsFakeApplicationID, "environment_id": operationsFakeEnvironmentID, "current": current,
			"enforcement": "expectations_only", "recovery_evidence": "unverified", "history": nil, "history_capped": false}
	}
	fake.handle("GET "+base+"/environments/{environment_id}/operating-expectations", func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(t, writer, http.StatusOK, expectationsView())
	})
	fake.handle("PUT "+base+"/environments/{environment_id}/operating-expectations", func(writer http.ResponseWriter, request *http.Request) {
		var body map[string]any
		decodeJSON(t, request, &body)
		expectedRevision, _ := body["expected_revision"].(float64)
		if int(expectedRevision) != fake.revision {
			writeJSON(t, writer, http.StatusConflict, map[string]any{"detail": "A newer revision was adopted."})
			return
		}
		delete(body, "expected_revision")
		fake.expectations = body
		fake.revision++
		writeJSON(t, writer, http.StatusOK, expectationsView())
	})
	backupsView := func() map[string]any {
		return map[string]any{"application_id": operationsFakeApplicationID, "application_name": "web", "database": map[string]any{"status": "detected"},
			"database_backup": fake.databaseBackup, "deployments": []any{}, "ready_vault_count": 1, "ready_vault_known": true, "warnings": []any{}}
	}
	fake.handle("GET "+base+"/backups", func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(t, writer, http.StatusOK, backupsView())
	})
	fake.handle("PUT "+base+"/backups/database-backup", func(writer http.ResponseWriter, request *http.Request) {
		var body map[string]any
		decodeJSON(t, request, &body)
		fake.databaseBackup, _ = body["enabled"].(bool)
		writeJSON(t, writer, http.StatusOK, backupsView())
	})
	provenanceView := func() map[string]any {
		effective := "warn"
		if mode, set := fake.applicationMode.(string); set {
			effective = mode
		}
		return map[string]any{"application_id": operationsFakeApplicationID, "mode": effective, "source": "application",
			"application_mode": fake.applicationMode, "organisation_mode": nil}
	}
	fake.handle("GET "+base+"/provenance-policy", func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(t, writer, http.StatusOK, provenanceView())
	})
	fake.handle("PUT "+base+"/provenance-policy", func(writer http.ResponseWriter, request *http.Request) {
		var body map[string]any
		decodeJSON(t, request, &body)
		fake.applicationMode = body["mode"]
		writeJSON(t, writer, http.StatusOK, provenanceView())
	})
	profilesPath := "/api/v1/org/pipeline-repositories/{repository_id}/workspace-profiles"
	fake.handle("GET "+profilesPath, func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(t, writer, http.StatusOK, map[string]any{"repository_id": operationsFakeRepositoryID, "profiles": fake.workspaceProfiles})
	})
	fake.handle("PUT "+profilesPath, func(writer http.ResponseWriter, request *http.Request) {
		var body struct {
			Profiles json.RawMessage `json:"profiles"`
		}
		decodeJSON(t, request, &body)
		fake.workspaceProfiles = body.Profiles
		writeJSON(t, writer, http.StatusOK, map[string]any{"repository_id": operationsFakeRepositoryID, "profiles": fake.workspaceProfiles})
	})
	return fake
}

func TestAccApplicationAutonomyLifecycle(t *testing.T) {
	fake := newApplicationOperationsFake(t)
	config := func(areas, setup string) string {
		return fake.providerBlock() + fmt.Sprintf(`
resource "ankra_application_autonomy" "web" {
  application_id                = %q
  level                         = "together"
  areas                         = %s
  limits                        = { production_downtime_seconds = 0, may_change_data = false }
  auto_fix_builds               = false
  setup_pull_request_auto_merge = %q
}
`, operationsFakeApplicationID, areas, setup)
	}
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy: func(_ *terraform.State) error {
			if fake.applicationLayer == nil || fake.autoFixBuilds {
				return fmt.Errorf("destroy must leave every setting as it is")
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: config(`{ ship_changes = "ankra_runs_it" }`, "off"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ankra_application_autonomy.web", "id", operationsFakeApplicationID),
					resource.TestCheckResourceAttr("ankra_application_autonomy.web", "setup_pull_request_auto_merge_effective", "false"),
					func(_ *terraform.State) error {
						if len(fake.layerWrites) != 1 || fake.layerWrites[0]["expected"] != nil {
							return fmt.Errorf("the first write must expect no stored layer, wrote %v", fake.layerWrites)
						}
						return nil
					},
				),
			},
			{
				Config: config(`{ ship_changes = "together", keep_it_up = "ankra_runs_it" }`, "off"),
				Check: func(_ *terraform.State) error {
					if len(fake.layerWrites) != 2 || fake.layerWrites[1]["expected"] == nil {
						return fmt.Errorf("a change must start from the stored layer, wrote %v", fake.layerWrites)
					}
					if fake.setupWrites != 1 {
						return fmt.Errorf("an unchanged setup auto-merge must not be written again, written %d times", fake.setupWrites)
					}
					return nil
				},
			},
			{
				ResourceName:      "ankra_application_autonomy.web",
				ImportState:       true,
				ImportStateId:     operationsFakeApplicationID,
				ImportStateVerify: true,
			},
			{
				PreConfig:   func() { fake.notAdmin = true },
				Config:      config(`{ ship_changes = "together", keep_it_up = "ankra_runs_it" }`, "organisation"),
				ExpectError: regexp.MustCompile(`(?s)Unable to set setup pull request auto-merge.*organisation administrators`),
			},
			{
				Config:      config(`{ deploy_anything = "together" }`, "off"),
				ExpectError: regexp.MustCompile("Invalid area"),
			},
			{
				PreConfig: func() { fake.notAdmin = false },
				Config:    config(`{ ship_changes = "together", keep_it_up = "ankra_runs_it" }`, "off"),
			},
		},
	})
}

func TestAccApplicationEnvironmentLifecycle(t *testing.T) {
	fake := newApplicationOperationsFake(t)
	config := func(name string, budget int) string {
		return fake.providerBlock() + fmt.Sprintf(`
resource "ankra_application_environment" "production" {
  application_id = %q
  environment_id = %q
  name           = %q
  level          = "you_run_it"

  operating_expectations = {
    availability_percent = 99.9
    data_region          = "eu"
    recovery_minutes     = 60
    data_loss_minutes    = 15
    retention_days       = 30
    monthly_budget_cents = %d
  }
}
`, operationsFakeApplicationID, operationsFakeEnvironmentID, name, budget)
	}
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: config("production", 50000),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ankra_application_environment.production", "role", "production"),
					resource.TestCheckResourceAttr("ankra_application_environment.production", "operating_expectations_revision", "1"),
					func(_ *terraform.State) error {
						if fake.environmentName != "production" || fake.environmentLayers[operationsFakeEnvironmentID] == nil {
							return fmt.Errorf("the create must rename the environment and store its layer")
						}
						return nil
					},
				),
			},
			{
				Config: config("production", 80000),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ankra_application_environment.production", "operating_expectations_revision", "2"),
					func(_ *terraform.State) error {
						if fake.renames != 1 || len(fake.layerWrites) != 1 {
							return fmt.Errorf("only the changed expectations may be written: %d renames, %d layer writes", fake.renames, len(fake.layerWrites))
						}
						return nil
					},
				),
			},
			{
				ResourceName:            "ankra_application_environment.production",
				ImportState:             true,
				ImportStateId:           operationsFakeApplicationID + "/" + operationsFakeEnvironmentID,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"operating_expectations"},
			},
			{
				PreConfig: func() { fake.environmentMissing = true },
				Config:    config("production", 80000),
				ExpectError: regexp.MustCompile(
					`(?s)Environment not found.*no environment`),
			},
		},
	})
}

func TestAccApplicationBackupProvenanceAndWorkspaceProfiles(t *testing.T) {
	fake := newApplicationOperationsFake(t)
	config := func(mode, image string) string {
		return fake.providerBlock() + fmt.Sprintf(`
resource "ankra_application_database_backup" "web" {
  application_id = %[1]q
  enabled        = true
}

resource "ankra_application_provenance_policy" "web" {
  application_id = %[1]q
  mode           = %[2]q
}

resource "ankra_pipeline_workspace_profiles" "web" {
  repository_id = %[3]q
  profiles_json = jsonencode({ default = { image = %[4]q, volume_size = "20Gi" } })
}
`, operationsFakeApplicationID, mode, operationsFakeRepositoryID, image)
	}
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy: func(_ *terraform.State) error {
			if string(fake.workspaceProfiles) != "{}" {
				return fmt.Errorf("destroy must clear the workspace profiles, found %s", fake.workspaceProfiles)
			}
			if !fake.databaseBackup || fake.applicationMode != "enforce" {
				return fmt.Errorf("destroy must leave the backup default and the provenance mode as they are")
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: config("warn", "ghcr.io/ankraio/workspace:1"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ankra_application_database_backup.web", "database_status", "detected"),
					resource.TestCheckResourceAttr("ankra_application_provenance_policy.web", "effective_mode", "warn"),
				),
			},
			{
				Config: config("enforce", "ghcr.io/ankraio/workspace:2"),
				Check:  resource.TestCheckResourceAttr("ankra_application_provenance_policy.web", "mode", "enforce"),
			},
			{
				ResourceName:      "ankra_pipeline_workspace_profiles.web",
				ImportState:       true,
				ImportStateId:     operationsFakeRepositoryID,
				ImportStateVerify: true,
			},
			{
				PreConfig: func() { fake.applicationMode = nil },
				Config:    config("enforce", "ghcr.io/ankraio/workspace:2"),
				Check: func(_ *terraform.State) error {
					if fake.applicationMode != "enforce" {
						return fmt.Errorf("a mode cleared outside Terraform must be set again")
					}
					return nil
				},
			},
		},
	})
}

func TestAccApplicationAutonomyRefusesToDropAStoredLayer(t *testing.T) {
	fake := newApplicationOperationsFake(t)
	fake.applicationLayer = map[string]any{"level": "together", "areas": map[string]any{}, "limits": map[string]any{"may_change_data": false},
		"widened_by_admin": false}
	config := func(limits string) string {
		return fake.providerBlock() + fmt.Sprintf(`
resource "ankra_application_autonomy" "web" {
  application_id = %q
  level          = "ankra_runs_it"
%s
}
`, operationsFakeApplicationID, limits)
	}
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:      config(""),
				ExpectError: regexp.MustCompile(`(?s)A stored autonomy layer already exists.*terraform import`),
			},
			{
				Config: config("  limits = { may_change_data = false }"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ankra_application_autonomy.web", "limits.may_change_data", "false"),
					resource.TestCheckNoResourceAttr("ankra_application_autonomy.web", "areas"),
					func(_ *terraform.State) error {
						if len(fake.layerWrites) != 1 || fake.layerWrites[0]["expected"] == nil {
							return fmt.Errorf("the create must start from the stored layer, wrote %v", fake.layerWrites)
						}
						return nil
					},
				),
			},
		},
	})
}
