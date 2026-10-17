// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"testing"
)

// applicationLaneOrder mirrors gatewayprofiles.LaneOrder.
var applicationLaneOrder = []string{"pull_request", "code", "actions", "skills", "previews"}

// applicationFakeOverride is one stored AI lane override, sparse like
// gateway_binding_repo_profiles: a nil enabled or settings inherits.
type applicationFakeOverride struct {
	enabled  *bool
	settings map[string]any
}

// applicationFake stands in for the applications and pipelines surfaces of
// cluster-api (go/internal/appsapi, go/internal/pipelineapi) with the status
// codes and bodies those handlers answer: create answers 200 with an id, or
// 200 with errors and no id for a duplicate name; an unknown application is a
// 404 on every subresource; delete answers 200 {success:false} for an unknown
// id; env-secret delete answers 200 {"deleted":true} and 404 for a missing
// key; a removal answers 202, 409 while it runs and 422 for nothing deployed;
// a repository connect answers 201 and 409 for a duplicate; disconnect and
// schedule delete answer 204; the definition store keeps a canonical render
// rather than the text it was sent.
type applicationFake struct {
	*routeFake
	t                 *testing.T
	sequence          int
	applications      map[string]map[string]any
	registries        map[string]map[string]any
	settingsLabel     *string
	settingsAutoMerge bool
	autoDeploy        map[string]bool
	envSecrets        map[string]map[string]string
	aiOverrides       map[string]map[string]*applicationFakeOverride
	demos             map[string]map[string]any
	installations     map[string][]map[string]any
	repositories      map[string]map[string]any
	definitions       map[string]map[string]any
	schedules         map[string]map[string]map[string]any
	trains            map[string]map[string]any
	bodies            map[string][]map[string]json.RawMessage
}

func newApplicationFake(t *testing.T) *applicationFake {
	fake := &applicationFake{
		routeFake:     newRouteFake(t),
		t:             t,
		applications:  map[string]map[string]any{},
		registries:    map[string]map[string]any{},
		autoDeploy:    map[string]bool{},
		envSecrets:    map[string]map[string]string{},
		aiOverrides:   map[string]map[string]*applicationFakeOverride{},
		demos:         map[string]map[string]any{},
		installations: map[string][]map[string]any{},
		repositories:  map[string]map[string]any{},
		definitions:   map[string]map[string]any{},
		schedules:     map[string]map[string]map[string]any{},
		trains:        map[string]map[string]any{},
		bodies:        map[string][]map[string]json.RawMessage{},
	}
	fake.registerApplicationRoutes()
	fake.registerApplicationConfigurationRoutes()
	fake.registerApplicationDeploymentRoutes()
	fake.registerPipelineRoutes()
	return fake
}

func (fake *applicationFake) nextID(prefix string) string {
	fake.sequence++
	return fmt.Sprintf("%s-%04d", prefix, fake.sequence)
}

// record decodes and keeps a request body under its route name, so a test can
// assert what the provider sent.
func (fake *applicationFake) record(route string, request *http.Request) map[string]json.RawMessage {
	var body map[string]json.RawMessage
	decodeJSON(fake.t, request, &body)
	if body == nil {
		body = map[string]json.RawMessage{}
	}
	fake.bodies[route] = append(fake.bodies[route], body)
	return body
}

// lastBody is the most recent body recorded for a route.
func (fake *applicationFake) lastBody(route string) map[string]json.RawMessage {
	recorded := fake.bodies[route]
	if len(recorded) == 0 {
		return nil
	}
	return recorded[len(recorded)-1]
}

func rawString(raw json.RawMessage) string {
	var text string
	_ = json.Unmarshal(raw, &text)
	return text
}

func (fake *applicationFake) notFound(writer http.ResponseWriter, detail string) {
	writeJSON(fake.t, writer, http.StatusNotFound, map[string]any{"detail": detail})
}

// application resolves the path's application, answering the platform's 404
// when it is unknown.
func (fake *applicationFake) application(writer http.ResponseWriter, request *http.Request) (string, map[string]any) {
	applicationID := request.PathValue("application_id")
	application, exists := fake.applications[applicationID]
	if !exists {
		fake.notFound(writer, "Application not found")
		return "", nil
	}
	return applicationID, application
}

func (fake *applicationFake) applicationView(applicationID string) map[string]any {
	application := fake.applications[applicationID]
	view := map[string]any{}
	for key, value := range application {
		if key == "app_repo_credential_name" {
			continue
		}
		view[key] = value
	}
	imageURL := "registry.ankra.app/org/" + fmt.Sprint(application["app_repo_name"])
	if registry, declared := fake.registries[applicationID]; declared {
		imageURL = strings.TrimPrefix(fmt.Sprint(registry["url"]), "oci://") + "/" + fmt.Sprint(application["app_repo_name"])
	}
	view["container_image_url"] = imageURL
	view["deployment_state"] = "not_deployed"
	return view
}

func (fake *applicationFake) registerApplicationRoutes() {
	t := fake.t
	fake.handle("POST /api/v1/org/applications", func(writer http.ResponseWriter, request *http.Request) {
		body := fake.record("create application", request)
		name := rawString(body["name"])
		for _, existing := range fake.applications {
			if existing["name"] == name {
				writeJSON(t, writer, http.StatusOK, map[string]any{"id": nil, "errors": []any{map[string]any{
					"name": name, "kind": "application",
					"errors": []any{map[string]any{"key": "name", "message": "Application with name '" + name + "' already exists."}},
				}}})
				return
			}
		}
		applicationID := fake.nextID("app")
		branch := rawString(body["app_repo_branch"])
		if branch == "" {
			branch = "main"
		}
		fake.applications[applicationID] = map[string]any{
			"id": applicationID, "name": name, "state": "creating",
			"app_repo_owner": rawString(body["app_repo_owner"]), "app_repo_name": rawString(body["app_repo_name"]),
			"app_repo_branch": branch, "app_repo_credential_name": rawString(body["app_repo_credential_name"]),
			"app_repo_url":    "https://github.com/" + rawString(body["app_repo_owner"]) + "/" + rawString(body["app_repo_name"]),
			"pipeline_source": nil, "created_at": "2026-10-17T00:00:00Z", "updated_at": "2026-10-17T00:00:00Z",
		}
		if raw, present := body["image_registry"]; present && string(raw) != "null" {
			var registry map[string]any
			_ = json.Unmarshal(raw, &registry)
			fake.registries[applicationID] = registry
		}
		fake.autoDeploy[applicationID] = true
		writeJSON(t, writer, http.StatusOK, map[string]any{"id": applicationID, "errors": []any{}})
	})
	fake.handle("GET /api/v1/org/applications", func(writer http.ResponseWriter, request *http.Request) {
		identifiers := make([]string, 0, len(fake.applications))
		search := request.URL.Query().Get("search")
		for applicationID, application := range fake.applications {
			if search == "" || strings.Contains(fmt.Sprint(application["name"]), search) {
				identifiers = append(identifiers, applicationID)
			}
		}
		sort.Sort(sort.Reverse(sort.StringSlice(identifiers)))
		pageSize := 1
		page := 1
		_, _ = fmt.Sscan(request.URL.Query().Get("page"), &page)
		totalPages := (len(identifiers) + pageSize - 1) / pageSize
		result := []any{}
		if start := (page - 1) * pageSize; start < len(identifiers) {
			for _, applicationID := range identifiers[start:min(start+pageSize, len(identifiers))] {
				item := fake.applicationView(applicationID)
				delete(item, "deployment_state")
				result = append(result, item)
			}
		}
		writeJSON(t, writer, http.StatusOK, map[string]any{"result": result, "pagination": map[string]any{
			"page": page, "page_size": pageSize, "total_pages": totalPages, "total_count": len(identifiers)}})
	})
	fake.handle("GET /api/v1/org/applications/{application_id}", func(writer http.ResponseWriter, request *http.Request) {
		applicationID, application := fake.application(writer, request)
		if application == nil {
			return
		}
		application["state"] = "up"
		writeJSON(t, writer, http.StatusOK, fake.applicationView(applicationID))
	})
	fake.handle("DELETE /api/v1/org/applications/{application_id}", func(writer http.ResponseWriter, request *http.Request) {
		applicationID := request.PathValue("application_id")
		if _, exists := fake.applications[applicationID]; !exists {
			writeJSON(t, writer, http.StatusOK, map[string]any{"success": false, "message": "Application not found."})
			return
		}
		delete(fake.applications, applicationID)
		delete(fake.installations, applicationID)
		writeJSON(t, writer, http.StatusOK, map[string]any{"success": true, "message": "Application deleted successfully."})
	})
	fake.handle("GET /api/v1/org/applications/{application_id}/repository-credential", func(writer http.ResponseWriter, request *http.Request) {
		_, application := fake.application(writer, request)
		if application == nil {
			return
		}
		writeJSON(t, writer, http.StatusOK, map[string]any{"credential_name": application["app_repo_credential_name"], "resolved": true, "message": ""})
	})
	fake.handle("PUT /api/v1/org/applications/{application_id}/repository-credential", func(writer http.ResponseWriter, request *http.Request) {
		_, application := fake.application(writer, request)
		if application == nil {
			return
		}
		body := fake.record("set repository credential", request)
		name := strings.TrimSpace(rawString(body["credential_name"]))
		if name == "" {
			writeJSON(t, writer, http.StatusBadRequest, map[string]any{"detail": "credential_name must name a GitHub credential of this organisation."})
			return
		}
		application["app_repo_credential_name"] = name
		writeJSON(t, writer, http.StatusOK, map[string]any{"credential_name": name, "resolved": true, "message": ""})
	})
	fake.handle("GET /api/v1/org/applications/{application_id}/image-registry", func(writer http.ResponseWriter, request *http.Request) {
		applicationID, application := fake.application(writer, request)
		if application == nil {
			return
		}
		writeJSON(t, writer, http.StatusOK, fake.registryView(applicationID))
	})
	fake.handle("PUT /api/v1/org/applications/{application_id}/image-registry", func(writer http.ResponseWriter, request *http.Request) {
		applicationID, application := fake.application(writer, request)
		if application == nil {
			return
		}
		body := fake.record("set image registry", request)
		raw, present := body["image_registry"]
		if !present {
			writeJSON(t, writer, http.StatusUnprocessableEntity, map[string]any{"detail": []any{map[string]any{"loc": []any{"body", "image_registry"}, "msg": "Field required"}}})
			return
		}
		if string(raw) == "null" {
			delete(fake.registries, applicationID)
		} else {
			var registry map[string]any
			_ = json.Unmarshal(raw, &registry)
			fake.registries[applicationID] = registry
		}
		writeJSON(t, writer, http.StatusOK, fake.registryView(applicationID))
	})
}

func (fake *applicationFake) registryView(applicationID string) map[string]any {
	registry, declared := fake.registries[applicationID]
	view := map[string]any{
		"declared": declared, "url": "", "credential_name": "", "api_url": "", "pull_secret_name": "",
		"username_secret_name": "", "password_secret_name": "", "manage_actions_secrets": false,
		"admin_credential_name": "", "flat_repositories": false, "component_repositories": map[string]any{},
		"host": "registry.ankra.app", "project": "org", "image_repository": "", "components": []any{}, "robot": map[string]any{},
	}
	if !declared {
		return view
	}
	for key, value := range registry {
		if key == "project_public" {
			continue
		}
		view[key] = value
	}
	if view["component_repositories"] == nil {
		view["component_repositories"] = map[string]any{}
	}
	return view
}

func (fake *applicationFake) registerApplicationConfigurationRoutes() {
	t := fake.t
	fake.handle("GET /api/v1/org/applications/settings", func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(t, writer, http.StatusOK, fake.settingsView())
	})
	fake.handle("PUT /api/v1/org/applications/settings", func(writer http.ResponseWriter, request *http.Request) {
		body := fake.record("update settings", request)
		if raw, present := body["ci_runner_label"]; present {
			if string(raw) == "null" {
				fake.settingsLabel = nil
			} else {
				label := rawString(raw)
				fake.settingsLabel = &label
			}
		}
		if raw, present := body["auto_merge_setup_pull_requests"]; present {
			_ = json.Unmarshal(raw, &fake.settingsAutoMerge)
		}
		writeJSON(t, writer, http.StatusOK, fake.settingsView())
	})
	fake.handle("GET /api/v1/org/applications/{application_id}/auto-deploy", func(writer http.ResponseWriter, request *http.Request) {
		applicationID, application := fake.application(writer, request)
		if application == nil {
			return
		}
		writeJSON(t, writer, http.StatusOK, map[string]any{"enabled": fake.autoDeploy[applicationID], "branch": application["app_repo_branch"], "environments": []any{}})
	})
	fake.handle("PUT /api/v1/org/applications/{application_id}/auto-deploy", func(writer http.ResponseWriter, request *http.Request) {
		applicationID, application := fake.application(writer, request)
		if application == nil {
			return
		}
		body := fake.record("set auto-deploy", request)
		raw, present := body["enabled"]
		if !present {
			writeJSON(t, writer, http.StatusUnprocessableEntity, map[string]any{"detail": "enabled is required"})
			return
		}
		var enabled bool
		_ = json.Unmarshal(raw, &enabled)
		fake.autoDeploy[applicationID] = enabled
		writeJSON(t, writer, http.StatusOK, map[string]any{"enabled": enabled, "branch": application["app_repo_branch"], "environments": []any{}})
	})
	fake.handle("GET /api/v1/org/applications/{application_id}/env-secrets", func(writer http.ResponseWriter, request *http.Request) {
		applicationID, application := fake.application(writer, request)
		if application == nil {
			return
		}
		keys := []string{}
		for key := range fake.envSecrets[applicationID] {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		secrets := []any{}
		for _, key := range keys {
			status := "set"
			if fake.envSecrets[applicationID][key] == "" {
				status = "value_missing"
			}
			secrets = append(secrets, map[string]any{"key": key, "required": false, "status": status, "updated_at": "2026-10-17T00:00:00Z", "updated_by": nil})
		}
		writeJSON(t, writer, http.StatusOK, map[string]any{"secret_name": "app-env", "secret_names": []any{}, "secrets": secrets,
			"missing_keys": []any{}, "requirements_state": "known"})
	})
	fake.handle("PUT /api/v1/org/applications/{application_id}/env-secrets/{secret_key}", func(writer http.ResponseWriter, request *http.Request) {
		applicationID, application := fake.application(writer, request)
		if application == nil {
			return
		}
		body := fake.record("set env secret", request)
		if _, present := body["value"]; !present {
			writeJSON(t, writer, http.StatusUnprocessableEntity, map[string]any{"detail": "value is required"})
			return
		}
		key := request.PathValue("secret_key")
		if fake.envSecrets[applicationID] == nil {
			fake.envSecrets[applicationID] = map[string]string{}
		}
		fake.envSecrets[applicationID][key] = rawString(body["value"])
		writeJSON(t, writer, http.StatusOK, map[string]any{"key": key, "required": false, "status": "set", "updated_at": "2026-10-17T00:00:00Z", "updated_by": "user"})
	})
	fake.handle("DELETE /api/v1/org/applications/{application_id}/env-secrets/{secret_key}", func(writer http.ResponseWriter, request *http.Request) {
		applicationID, application := fake.application(writer, request)
		if application == nil {
			return
		}
		key := request.PathValue("secret_key")
		if _, exists := fake.envSecrets[applicationID][key]; !exists {
			fake.notFound(writer, "Environment secret not found")
			return
		}
		delete(fake.envSecrets[applicationID], key)
		writeJSON(t, writer, http.StatusOK, map[string]any{"deleted": true})
	})
	fake.handle("GET /api/v1/org/applications/{application_id}/ai-config", func(writer http.ResponseWriter, request *http.Request) {
		applicationID, application := fake.application(writer, request)
		if application == nil {
			return
		}
		writeJSON(t, writer, http.StatusOK, fake.aiConfigView(applicationID))
	})
	fake.handle("PUT /api/v1/org/applications/{application_id}/ai-config", func(writer http.ResponseWriter, request *http.Request) {
		applicationID, application := fake.application(writer, request)
		if application == nil {
			return
		}
		fake.record("update ai-config", request)
		body := fake.lastBody("update ai-config")
		var lanes []struct {
			Lane     string         `json:"lane"`
			Override bool           `json:"override"`
			Enabled  *bool          `json:"enabled"`
			Settings map[string]any `json:"settings"`
		}
		_ = json.Unmarshal(body["lanes"], &lanes)
		if fake.aiOverrides[applicationID] == nil {
			fake.aiOverrides[applicationID] = map[string]*applicationFakeOverride{}
		}
		for _, patch := range lanes {
			if !applicationIsAILane(patch.Lane) {
				writeJSON(t, writer, http.StatusBadRequest, map[string]any{"detail": "Unknown AI lane."})
				return
			}
			if !patch.Override {
				delete(fake.aiOverrides[applicationID], patch.Lane)
				continue
			}
			override := fake.aiOverrides[applicationID][patch.Lane]
			if override == nil {
				override = &applicationFakeOverride{}
				fake.aiOverrides[applicationID][patch.Lane] = override
			}
			if patch.Enabled != nil {
				override.enabled = patch.Enabled
			}
			if patch.Settings != nil {
				settings := patch.Settings
				if patch.Lane == "pull_request" {
					budget, _ := settings["budget"].(map[string]any)
					if budget == nil {
						budget = map[string]any{}
					}
					if _, pinned := budget["max_reviews_per_pull_request"]; !pinned {
						budget["max_reviews_per_pull_request"] = float64(5)
					}
					settings["budget"] = budget
				}
				override.settings = settings
			}
		}
		writeJSON(t, writer, http.StatusOK, fake.aiConfigView(applicationID))
	})
	fake.handle("GET /api/v1/org/applications/{application_id}/demo-config", func(writer http.ResponseWriter, request *http.Request) {
		applicationID, application := fake.application(writer, request)
		if application == nil {
			return
		}
		writeJSON(t, writer, http.StatusOK, fake.demoView(applicationID))
	})
	fake.handle("PUT /api/v1/org/applications/{application_id}/demo-config", func(writer http.ResponseWriter, request *http.Request) {
		applicationID, application := fake.application(writer, request)
		if application == nil {
			return
		}
		body := fake.record("update demo-config", request)
		stored := fake.demos[applicationID]
		if stored == nil {
			stored = map[string]any{"env": []any{}, "database": false, "protected": false}
			fake.demos[applicationID] = stored
		}
		var env []any
		_ = json.Unmarshal(body["env"], &env)
		if env == nil {
			env = []any{}
		}
		stored["env"] = env
		var database bool
		_ = json.Unmarshal(body["database"], &database)
		stored["database"] = database
		for _, key := range []string{"base_stack", "migrate_command", "database_extensions"} {
			if raw, present := body[key]; present {
				var value any
				_ = json.Unmarshal(raw, &value)
				stored[key] = value
			}
		}
		generated := ""
		if raw, present := body["protected"]; present {
			var protected bool
			_ = json.Unmarshal(raw, &protected)
			if protected && stored["protected"] != true {
				generated = "minted-" + applicationID
			}
			stored["protected"] = protected
		}
		view := fake.demoView(applicationID)
		if generated != "" {
			view["generated_password"] = generated
		}
		writeJSON(t, writer, http.StatusOK, view)
	})
}

func (fake *applicationFake) settingsView() map[string]any {
	if fake.settingsLabel == nil {
		return map[string]any{"ci_runner_label": "ubuntu-latest", "is_default": true, "updated_at": nil, "auto_merge_setup_pull_requests": fake.settingsAutoMerge}
	}
	return map[string]any{"ci_runner_label": *fake.settingsLabel, "is_default": false, "updated_at": "2026-10-17T00:00:00Z", "auto_merge_setup_pull_requests": fake.settingsAutoMerge}
}

func (fake *applicationFake) aiConfigView(applicationID string) map[string]any {
	application := fake.applications[applicationID]
	lanes := []any{}
	for _, lane := range applicationLaneOrder {
		effective := map[string]any{
			"model": "", "autonomy": "comment", "branches": []any{}, "ignore_paths": []any{"**/go.sum"},
			"skip_drafts": true, "skill_ids": []any{}, "custom_instructions": "",
			"max_runs_per_day": 0, "max_input_characters": 0, "max_reviews_per_pull_request": 0,
		}
		if lane == "pull_request" {
			effective["max_reviews_per_pull_request"] = 5
		}
		enabled := false
		override := fake.aiOverrides[applicationID][lane]
		if override != nil {
			if override.enabled != nil {
				enabled = *override.enabled
			}
			for key, value := range override.settings {
				if key == "budget" {
					budget, _ := value.(map[string]any)
					for budgetKey, budgetValue := range budget {
						effective[budgetKey] = budgetValue
					}
					continue
				}
				effective[key] = value
			}
		}
		source := "organisation"
		if override != nil {
			source = "application"
		}
		lanes = append(lanes, map[string]any{
			"lane": lane, "capability": lane, "enabled": enabled, "organisation_enabled": false, "source": source,
			"effective": effective, "organisation_default": map[string]any{}, "overridden_for_application": override != nil,
		})
	}
	return map[string]any{
		"binding": map[string]any{"connected": true, "provider": "github", "binding_external_id": "1",
			"repo_full_name": fmt.Sprint(application["app_repo_owner"]) + "/" + fmt.Sprint(application["app_repo_name"]), "reason": ""},
		"lanes": lanes,
	}
}

func (fake *applicationFake) demoView(applicationID string) map[string]any {
	stored := fake.demos[applicationID]
	view := map[string]any{"env": []any{}, "database": false, "protected": false}
	if stored == nil {
		return view
	}
	view["env"] = stored["env"]
	view["database"] = stored["database"]
	view["protected"] = stored["protected"]
	if stored["protected"] == true {
		view["protected_username"] = "demo"
	}
	if baseStack, ok := stored["base_stack"].(map[string]any); ok {
		view["base_stack"] = map[string]any{"id": baseStack["id"], "name": "shared", "status": "up"}
	}
	if command, ok := stored["migrate_command"].(string); ok && command != "" {
		view["migrate_command"] = command
	}
	if extensions, ok := stored["database_extensions"].([]any); ok && len(extensions) > 0 {
		view["database_extensions"] = extensions
	}
	return view
}

func (fake *applicationFake) registerApplicationDeploymentRoutes() {
	t := fake.t
	fake.handle("GET /api/v1/org/applications/{application_id}/installations", func(writer http.ResponseWriter, request *http.Request) {
		applicationID := request.PathValue("application_id")
		installations := fake.installations[applicationID]
		if installations == nil {
			installations = []map[string]any{}
		}
		writeJSON(t, writer, http.StatusOK, map[string]any{"installations": installations})
	})
	fake.handle("POST /api/v1/org/applications/{application_id}/deploy", func(writer http.ResponseWriter, request *http.Request) {
		applicationID, application := fake.application(writer, request)
		if application == nil {
			return
		}
		body := fake.record("deploy", request)
		clusterID := rawString(body["cluster_id"])
		if clusterID == "" {
			writeJSON(t, writer, http.StatusUnprocessableEntity, map[string]any{"detail": "cluster_id is required"})
			return
		}
		namespace := strings.TrimSpace(rawString(body["namespace"]))
		if namespace == "" {
			namespace = fmt.Sprint(application["name"])
		}
		var installation map[string]any
		for _, existing := range fake.installations[applicationID] {
			if existing["cluster_id"] == clusterID && existing["namespace"] == namespace {
				installation = existing
			}
		}
		if installation != nil && installation["status"] == "removing" {
			writeJSON(t, writer, http.StatusConflict, map[string]any{"detail": "being removed"})
			return
		}
		if installation == nil {
			installation = map[string]any{
				"id": fake.nextID("installation"), "application_id": applicationID, "cluster_id": clusterID, "namespace": namespace,
				"deploy_mode": "quick", "desired_chart_version": nil, "stack_name": namespace + "-stack", "app_resource_id": nil,
				"status": "pending", "error_message": nil, "failure_condition": "not_applicable", "last_reconciled_at": nil,
				"created_at": "2026-10-17T00:00:00Z", "updated_at": "2026-10-17T00:00:00Z", "inputs": map[string]any{},
			}
			fake.installations[applicationID] = append(fake.installations[applicationID], installation)
		}
		if mode := rawString(body["deploy_mode"]); mode != "" {
			installation["deploy_mode"] = mode
		}
		stored, _ := installation["inputs"].(map[string]any)
		if stored == nil {
			stored = map[string]any{}
			installation["inputs"] = stored
		}
		var resets []string
		_ = json.Unmarshal(body["reset_inputs"], &resets)
		for _, name := range resets {
			delete(stored, name)
		}
		var inputs map[string]any
		_ = json.Unmarshal(body["inputs"], &inputs)
		for name, value := range inputs {
			stored[name] = value
		}
		installation["status"] = "deploying"
		writeJSON(t, writer, http.StatusOK, map[string]any{"installation_id": installation["id"], "operation_id": fake.nextID("operation"),
			"status": "queued", "message": "Deployment queued"})
	})
	fake.handle("POST /api/v1/org/applications/{application_id}/deployments/remove", func(writer http.ResponseWriter, request *http.Request) {
		applicationID, application := fake.application(writer, request)
		if application == nil {
			return
		}
		body := fake.record("remove deployment", request)
		for index, existing := range fake.installations[applicationID] {
			if existing["cluster_id"] == rawString(body["cluster_id"]) && existing["namespace"] == rawString(body["namespace"]) {
				if existing["status"] == "removing" {
					writeJSON(t, writer, http.StatusConflict, map[string]any{"detail": "This application's deployment on that cluster and namespace is already being removed."})
					return
				}
				installations := fake.installations[applicationID]
				fake.installations[applicationID] = append(installations[:index], installations[index+1:]...)
				writeJSON(t, writer, http.StatusAccepted, map[string]any{"cluster_id": existing["cluster_id"], "namespace": existing["namespace"],
					"installation_id": existing["id"], "operation_id": "operation-remove", "status": "removing"})
				return
			}
		}
		writeJSON(t, writer, http.StatusUnprocessableEntity, map[string]any{"detail": "This application is not deployed to that cluster and namespace, so there is nothing to remove."})
	})
}

// canonicalSpec is the fake's stand-in for pipelinespec.Render: the stored
// text is a canonical form, not the bytes the client sent.
func canonicalSpec(specYAML string) string {
	lines := []string{}
	for _, line := range strings.Split(specYAML, "\n") {
		trimmed := strings.TrimRight(line, " \t")
		if trimmed == "" || strings.HasPrefix(strings.TrimSpace(trimmed), "#") {
			continue
		}
		lines = append(lines, trimmed)
	}
	return strings.Join(lines, "\n") + "\n"
}

func canonicalSpecHash(specYAML string) string {
	sum := sha256.Sum256([]byte(canonicalSpec(specYAML)))
	return hex.EncodeToString(sum[:])
}

// pipelineRepository resolves the path's selector to a connected repository,
// through an application's linked repository or by repository id.
func (fake *applicationFake) pipelineRepository(writer http.ResponseWriter, request *http.Request) string {
	if applicationID := request.PathValue("application_id"); applicationID != "" {
		for repositoryID, repository := range fake.repositories {
			if repository["application_id"] == applicationID {
				return repositoryID
			}
		}
		fake.notFound(writer, "This application is not connected to Ankra Pipelines")
		return ""
	}
	repositoryID := request.PathValue("repository_id")
	if _, exists := fake.repositories[repositoryID]; !exists {
		fake.notFound(writer, "Pipeline repository not found")
		return ""
	}
	return repositoryID
}

func (fake *applicationFake) registerPipelineRoutes() {
	t := fake.t
	fake.handle("POST /api/v1/org/pipelines/repositories", func(writer http.ResponseWriter, request *http.Request) {
		body := fake.record("connect repository", request)
		for repositoryID, existing := range fake.repositories {
			if existing["provider"] == rawString(body["provider"]) && strings.EqualFold(fmt.Sprint(existing["owner"]), rawString(body["owner"])) &&
				strings.EqualFold(fmt.Sprint(existing["name"]), rawString(body["name"])) {
				writeJSON(t, writer, http.StatusConflict, map[string]any{"detail": "already connected", "repository_id": repositoryID})
				return
			}
		}
		branch := rawString(body["default_branch"])
		if branch == "" {
			branch = "main"
		}
		repositoryID := fake.nextID("repository")
		var workspacesOnly bool
		_ = json.Unmarshal(body["workspaces_only"], &workspacesOnly)
		repository := map[string]any{
			"id": repositoryID, "organisation_id": "org", "provider": rawString(body["provider"]), "owner": rawString(body["owner"]),
			"name": rawString(body["name"]), "credential_name": rawString(body["credential_name"]), "default_branch": branch,
			"application_id": nil, "cluster_id": nil, "created_at": "2026-10-17T00:00:00Z", "updated_at": "2026-10-17T00:00:00Z",
			"last_trigger": nil, "merge_train_enabled": false, "merge_train_max_cars": 1, "workspaces_only": workspacesOnly,
		}
		if applicationID := rawString(body["application_id"]); applicationID != "" {
			repository["application_id"] = applicationID
		}
		if clusterID := rawString(body["cluster_id"]); clusterID != "" {
			repository["cluster_id"] = clusterID
		}
		fake.repositories[repositoryID] = repository
		response := map[string]any{"definition": map[string]any{"status": "absent", "detail": "", "definition_id": nil, "spec_hash": nil, "violations": []any{}, "read_error": nil}}
		for key, value := range repository {
			response[key] = value
		}
		writeJSON(t, writer, http.StatusCreated, response)
	})
	fake.handle("GET /api/v1/org/pipelines/repositories", func(writer http.ResponseWriter, request *http.Request) {
		identifiers := []string{}
		for repositoryID, repository := range fake.repositories {
			if provider := request.URL.Query().Get("provider"); provider != "" && repository["provider"] != provider {
				continue
			}
			identifiers = append(identifiers, repositoryID)
		}
		sort.Strings(identifiers)
		start := 0
		if cursor := request.URL.Query().Get("cursor"); cursor != "" {
			_, _ = fmt.Sscan(cursor, &start)
		}
		page := []any{}
		var nextCursor any
		if start < len(identifiers) {
			page = append(page, fake.repositories[identifiers[start]])
			if start+1 < len(identifiers) {
				nextCursor = fmt.Sprint(start + 1)
			}
		}
		writeJSON(t, writer, http.StatusOK, map[string]any{"repositories": page, "next_cursor": nextCursor})
	})
	fake.handle("GET /api/v1/org/pipelines/repositories/{repository_id}", func(writer http.ResponseWriter, request *http.Request) {
		repository, exists := fake.repositories[request.PathValue("repository_id")]
		if !exists {
			fake.notFound(writer, "Pipeline repository not found")
			return
		}
		writeJSON(t, writer, http.StatusOK, repository)
	})
	fake.handle("PATCH /api/v1/org/pipelines/repositories/{repository_id}", func(writer http.ResponseWriter, request *http.Request) {
		repository, exists := fake.repositories[request.PathValue("repository_id")]
		if !exists {
			fake.notFound(writer, "Pipeline repository not found")
			return
		}
		body := fake.record("update repository", request)
		if raw, present := body["cluster_id"]; present {
			if string(raw) == "null" {
				repository["cluster_id"] = nil
			} else {
				repository["cluster_id"] = rawString(raw)
			}
		}
		if raw, present := body["workspaces_only"]; present {
			var workspacesOnly bool
			_ = json.Unmarshal(raw, &workspacesOnly)
			repository["workspaces_only"] = workspacesOnly
		}
		writeJSON(t, writer, http.StatusOK, repository)
	})
	fake.handle("DELETE /api/v1/org/pipelines/repositories/{repository_id}", func(writer http.ResponseWriter, request *http.Request) {
		repositoryID := request.PathValue("repository_id")
		if _, exists := fake.repositories[repositoryID]; !exists {
			fake.notFound(writer, "Pipeline repository not found")
			return
		}
		delete(fake.repositories, repositoryID)
		writer.WriteHeader(http.StatusNoContent)
	})
	for _, prefix := range []string{"/api/v1/org/applications/{application_id}", "/api/v1/org/pipeline-repositories/{repository_id}"} {
		fake.registerScopedPipelineRoutes(prefix)
	}
}

func (fake *applicationFake) registerScopedPipelineRoutes(prefix string) {
	t := fake.t
	fake.handle("GET "+prefix+"/pipeline", func(writer http.ResponseWriter, request *http.Request) {
		repositoryID := fake.pipelineRepository(writer, request)
		if repositoryID == "" {
			return
		}
		definition, exists := fake.definitions[repositoryID]
		if !exists {
			fake.notFound(writer, "No pipeline definition is recorded for this repository")
			return
		}
		writeJSON(t, writer, http.StatusOK, definition)
	})
	fake.handle("PUT "+prefix+"/pipeline", func(writer http.ResponseWriter, request *http.Request) {
		repositoryID := fake.pipelineRepository(writer, request)
		if repositoryID == "" {
			return
		}
		body := fake.record("put definition", request)
		spec := rawString(body["spec_yaml"])
		if strings.TrimSpace(spec) == "" {
			writeJSON(t, writer, http.StatusUnprocessableEntity, map[string]any{"detail": "The pipeline spec is not valid"})
			return
		}
		fake.definitions[repositoryID] = map[string]any{
			"source": "generated", "spec_yaml": canonicalSpec(spec), "spec_hash": canonicalSpecHash(spec),
			"protected_hash": "protected-" + canonicalSpecHash(spec)[:8], "stages": []any{}, "violations": []any{},
			"repository": map[string]any{"id": repositoryID},
		}
		writeJSON(t, writer, http.StatusOK, fake.definitions[repositoryID])
	})
	fake.handle("GET "+prefix+"/pipeline/schedules", func(writer http.ResponseWriter, request *http.Request) {
		repositoryID := fake.pipelineRepository(writer, request)
		if repositoryID == "" {
			return
		}
		identifiers := []string{}
		for scheduleID := range fake.schedules[repositoryID] {
			identifiers = append(identifiers, scheduleID)
		}
		sort.Strings(identifiers)
		schedules := []any{}
		for _, scheduleID := range identifiers {
			schedules = append(schedules, fake.schedules[repositoryID][scheduleID])
		}
		writeJSON(t, writer, http.StatusOK, map[string]any{"schedules": schedules})
	})
	fake.handle("POST "+prefix+"/pipeline/schedules", func(writer http.ResponseWriter, request *http.Request) {
		repositoryID := fake.pipelineRepository(writer, request)
		if repositoryID == "" {
			return
		}
		body := fake.record("create schedule", request)
		timezone := rawString(body["timezone"])
		if timezone == "" {
			timezone = "UTC"
		}
		enabled := true
		if raw, present := body["enabled"]; present {
			_ = json.Unmarshal(raw, &enabled)
		}
		var inputs map[string]any
		_ = json.Unmarshal(body["inputs"], &inputs)
		if inputs == nil {
			inputs = map[string]any{}
		}
		scheduleID := fake.nextID("schedule")
		schedule := map[string]any{
			"id": scheduleID, "repository_id": repositoryID, "cron": strings.Join(strings.Fields(rawString(body["cron"])), " "),
			"timezone": timezone, "ref": rawString(body["ref"]), "inputs": inputs, "enabled": enabled,
			"next_fire_at": "2026-10-18T00:00:00Z", "last_fired_at": nil, "last_run_id": nil,
			"created_at": "2026-10-17T00:00:00Z", "updated_at": "2026-10-17T00:00:00Z",
		}
		if fake.schedules[repositoryID] == nil {
			fake.schedules[repositoryID] = map[string]map[string]any{}
		}
		fake.schedules[repositoryID][scheduleID] = schedule
		writeJSON(t, writer, http.StatusCreated, schedule)
	})
	fake.handle("PUT "+prefix+"/pipeline/schedules/{schedule_id}", func(writer http.ResponseWriter, request *http.Request) {
		repositoryID := fake.pipelineRepository(writer, request)
		if repositoryID == "" {
			return
		}
		schedule, exists := fake.schedules[repositoryID][request.PathValue("schedule_id")]
		if !exists {
			fake.notFound(writer, "Schedule not found")
			return
		}
		body := fake.record("update schedule", request)
		for key, raw := range body {
			var value any
			_ = json.Unmarshal(raw, &value)
			if key == "cron" {
				value = strings.Join(strings.Fields(rawString(raw)), " ")
			}
			schedule[key] = value
		}
		writeJSON(t, writer, http.StatusOK, schedule)
	})
	fake.handle("DELETE "+prefix+"/pipeline/schedules/{schedule_id}", func(writer http.ResponseWriter, request *http.Request) {
		repositoryID := fake.pipelineRepository(writer, request)
		if repositoryID == "" {
			return
		}
		scheduleID := request.PathValue("schedule_id")
		if _, exists := fake.schedules[repositoryID][scheduleID]; !exists {
			fake.notFound(writer, "Schedule not found")
			return
		}
		delete(fake.schedules[repositoryID], scheduleID)
		writer.WriteHeader(http.StatusNoContent)
	})
	fake.handle("GET "+prefix+"/merge-train", func(writer http.ResponseWriter, request *http.Request) {
		repositoryID := fake.pipelineRepository(writer, request)
		if repositoryID == "" {
			return
		}
		writeJSON(t, writer, http.StatusOK, map[string]any{"repository_id": repositoryID, "settings": fake.trainSettings(repositoryID), "entries": []any{}, "recent": []any{}})
	})
	fake.handle("PUT "+prefix+"/merge-train/settings", func(writer http.ResponseWriter, request *http.Request) {
		repositoryID := fake.pipelineRepository(writer, request)
		if repositoryID == "" {
			return
		}
		body := fake.record("set merge train", request)
		if _, present := body["enabled"]; !present {
			writeJSON(t, writer, http.StatusUnprocessableEntity, map[string]any{"detail": "enabled is required"})
			return
		}
		settings := fake.trainSettings(repositoryID)
		var enabled bool
		_ = json.Unmarshal(body["enabled"], &enabled)
		settings["enabled"] = enabled
		if raw, present := body["max_cars"]; present {
			var maxCars float64
			_ = json.Unmarshal(raw, &maxCars)
			settings["max_cars"] = maxCars
		}
		fake.trains[repositoryID] = settings
		writeJSON(t, writer, http.StatusOK, settings)
	})
}

func (fake *applicationFake) trainSettings(repositoryID string) map[string]any {
	if settings, exists := fake.trains[repositoryID]; exists {
		return settings
	}
	return map[string]any{"enabled": false, "max_cars": float64(1)}
}
