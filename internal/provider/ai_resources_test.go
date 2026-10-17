// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

// aiFake holds the organisation's AI settings the way the platform does:
// keys are write-only (only masked previews come back), removing the active
// provider's key falls back to ankra, the model catalogue is virtual until
// its first change materialises the built-ins, deleting an endpoint cascades
// its models, and an existing board identity keeps its role.
type aiFake struct {
	*routeFake
	provider              string
	anthropicKey          string
	openRouterKey         string
	compatible            map[string]string
	catalogueMaterialised bool
	models                []map[string]any
	endpoints             []map[string]any
	endpointKeys          map[string]string
	laneSelections        map[string]string
	boardRole             string
	modelBodies           []map[string]json.RawMessage
	endpointBodies        []map[string]json.RawMessage
	created               int
}

func aiFakeMask(key string) string {
	if len(key) < 8 {
		return "****"
	}
	return key[:3] + "..." + key[len(key)-4:]
}

func aiFakeBuiltins() []map[string]any {
	builtins := []map[string]any{}
	for index, key := range []string{"expert", "think", "quick"} {
		builtins = append(builtins, map[string]any{
			"id": nil, "key": key, "display_name": strings.ToUpper(key[:1]) + key[1:], "description": "built-in " + key,
			"provider": "ankra", "endpoint_id": nil, "model_id": "claude-" + key, "tier": key,
			"context_window_tokens": 200000, "max_output_tokens": 8192, "supports_tools": true,
			"supports_thinking": false, "supports_images": true, "is_enabled": true, "sort_order": index, "is_default": true,
		})
	}
	return builtins
}

func newAIFake(t *testing.T) *aiFake {
	fake := &aiFake{
		routeFake:      newRouteFake(t),
		provider:       "ankra",
		endpointKeys:   map[string]string{},
		laneSelections: map[string]string{},
	}
	nextID := func() string {
		fake.created++
		return fmt.Sprintf("00000000-0000-4000-8000-%012d", fake.created)
	}
	keyStatus := func(key string) map[string]any {
		if key == "" {
			return map[string]any{"configured": false, "key_preview": nil}
		}
		return map[string]any{"configured": true, "key_preview": aiFakeMask(key)}
	}
	materialise := func() {
		if fake.catalogueMaterialised {
			return
		}
		fake.catalogueMaterialised = true
		fake.models = aiFakeBuiltins()
		for _, model := range fake.models {
			model["id"] = nextID()
		}
	}
	findModel := func(reference string) int {
		for index, model := range fake.models {
			if model["id"] == reference || model["key"] == reference {
				return index
			}
		}
		return -1
	}
	modelFromBody := func(body map[string]json.RawMessage) map[string]any {
		model := map[string]any{}
		for key, raw := range body {
			var value any
			_ = json.Unmarshal(raw, &value)
			model[key] = value
		}
		model["provider"] = "ankra"
		if model["endpoint_id"] != nil {
			model["provider"] = "openai_compatible"
		}
		if model["tier"] == "" {
			model["tier"] = nil
		}
		if model["context_window_tokens"] == float64(0) {
			model["context_window_tokens"] = 200000
		}
		if model["max_output_tokens"] == float64(0) {
			model["max_output_tokens"] = 8192
		}
		key, _ := model["key"].(string)
		model["is_default"] = key == "expert" || key == "think" || key == "quick"
		return model
	}

	fake.handle("GET /api/v1/org/ai-settings", func(writer http.ResponseWriter, _ *http.Request) {
		compatible := map[string]any{"configured": false, "base_url": nil, "model": nil, "key_preview": nil}
		if fake.compatible != nil {
			compatible = map[string]any{"configured": true, "base_url": fake.compatible["base_url"],
				"model": fake.compatible["model"], "key_preview": aiFakeMask(fake.compatible["api_key"])}
		}
		writeJSON(t, writer, http.StatusOK, map[string]any{
			"provider": fake.provider, "anthropic": keyStatus(fake.anthropicKey),
			"openai_compatible": compatible, "openrouter": keyStatus(fake.openRouterKey),
		})
	})
	fake.handle("PUT /api/v1/org/ai-settings/provider", func(writer http.ResponseWriter, request *http.Request) {
		var body map[string]string
		decodeJSON(t, request, &body)
		if body["provider"] == "anthropic" && fake.anthropicKey == "" {
			writeJSON(t, writer, http.StatusBadRequest, map[string]any{"detail": "Save an Anthropic API key before activating this provider."})
			return
		}
		fake.provider = body["provider"]
		writeJSON(t, writer, http.StatusOK, map[string]any{"provider": fake.provider, "anthropic": keyStatus(fake.anthropicKey),
			"openai_compatible": map[string]any{"configured": false}, "openrouter": keyStatus(fake.openRouterKey)})
	})
	fake.handle("POST /api/v1/org/ai-settings/anthropic", func(writer http.ResponseWriter, request *http.Request) {
		var body map[string]string
		decodeJSON(t, request, &body)
		if !strings.HasPrefix(body["api_key"], "sk-ant-") {
			writeJSON(t, writer, http.StatusBadRequest, map[string]any{"detail": "Invalid Anthropic API key format (must start with sk-ant-)"})
			return
		}
		fake.anthropicKey = body["api_key"]
		writeJSON(t, writer, http.StatusCreated, keyStatus(fake.anthropicKey))
	})
	fake.handle("DELETE /api/v1/org/ai-settings/anthropic", func(writer http.ResponseWriter, _ *http.Request) {
		fake.anthropicKey = ""
		if fake.provider == "anthropic" {
			fake.provider = "ankra"
		}
		writeJSON(t, writer, http.StatusOK, keyStatus(""))
	})
	fake.handle("POST /api/v1/org/ai-settings/openrouter", func(writer http.ResponseWriter, request *http.Request) {
		var body map[string]string
		decodeJSON(t, request, &body)
		fake.openRouterKey = body["api_key"]
		writeJSON(t, writer, http.StatusCreated, keyStatus(fake.openRouterKey))
	})
	fake.handle("DELETE /api/v1/org/ai-settings/openrouter", func(writer http.ResponseWriter, _ *http.Request) {
		fake.openRouterKey = ""
		if fake.provider == "openrouter" {
			fake.provider = "ankra"
		}
		writeJSON(t, writer, http.StatusOK, keyStatus(""))
	})
	fake.handle("POST /api/v1/org/ai-settings/openai-compatible", func(writer http.ResponseWriter, request *http.Request) {
		var body map[string]string
		decodeJSON(t, request, &body)
		body["base_url"] = strings.TrimRight(body["base_url"], "/")
		fake.compatible = body
		writeJSON(t, writer, http.StatusCreated, map[string]any{"configured": true, "base_url": body["base_url"],
			"model": body["model"], "key_preview": aiFakeMask(body["api_key"])})
	})
	fake.handle("DELETE /api/v1/org/ai-settings/openai-compatible", func(writer http.ResponseWriter, _ *http.Request) {
		fake.compatible = nil
		writeJSON(t, writer, http.StatusOK, map[string]any{"configured": false, "base_url": nil, "model": nil, "key_preview": nil})
	})

	fake.handle("GET /api/v1/org/ai-settings/models", func(writer http.ResponseWriter, _ *http.Request) {
		models := fake.models
		if !fake.catalogueMaterialised {
			models = aiFakeBuiltins()
		}
		writeJSON(t, writer, http.StatusOK, map[string]any{"models": models})
	})
	fake.handle("POST /api/v1/org/ai-settings/models", func(writer http.ResponseWriter, request *http.Request) {
		var body map[string]json.RawMessage
		decodeJSON(t, request, &body)
		fake.modelBodies = append(fake.modelBodies, body)
		materialise()
		model := modelFromBody(body)
		if findModel(platformServicesText(model["key"])) >= 0 {
			writeJSON(t, writer, http.StatusBadRequest, map[string]any{"detail": "A model with this key already exists"})
			return
		}
		model["id"] = nextID()
		fake.models = append(fake.models, model)
		writeJSON(t, writer, http.StatusCreated, model)
	})
	fake.handle("PUT /api/v1/org/ai-settings/models/{reference}", func(writer http.ResponseWriter, request *http.Request) {
		var body map[string]json.RawMessage
		decodeJSON(t, request, &body)
		fake.modelBodies = append(fake.modelBodies, body)
		materialise()
		index := findModel(request.PathValue("reference"))
		if index < 0 {
			writeJSON(t, writer, http.StatusNotFound, map[string]any{"detail": "Model not found"})
			return
		}
		model := modelFromBody(body)
		model["id"] = fake.models[index]["id"]
		fake.models[index] = model
		writeJSON(t, writer, http.StatusOK, model)
	})
	fake.handle("DELETE /api/v1/org/ai-settings/models/{reference}", func(writer http.ResponseWriter, request *http.Request) {
		materialise()
		index := findModel(request.PathValue("reference"))
		if index < 0 {
			writeJSON(t, writer, http.StatusNotFound, map[string]any{"detail": "Model not found"})
			return
		}
		fake.models = append(fake.models[:index], fake.models[index+1:]...)
		writeJSON(t, writer, http.StatusOK, map[string]any{"deleted": true})
	})

	fake.handle("GET /api/v1/org/ai-settings/endpoints", func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(t, writer, http.StatusOK, map[string]any{"endpoints": fake.endpoints})
	})
	fake.handle("POST /api/v1/org/ai-settings/endpoints", func(writer http.ResponseWriter, request *http.Request) {
		var body map[string]json.RawMessage
		decodeJSON(t, request, &body)
		fake.endpointBodies = append(fake.endpointBodies, body)
		var name, baseURL, apiKey string
		_ = json.Unmarshal(body["name"], &name)
		_ = json.Unmarshal(body["base_url"], &baseURL)
		_ = json.Unmarshal(body["api_key"], &apiKey)
		identifier := nextID()
		fake.endpointKeys[identifier] = apiKey
		endpoint := map[string]any{"id": identifier, "name": name, "provider": "openai_compatible",
			"base_url": strings.TrimRight(baseURL, "/"), "key_preview": aiFakeMask(apiKey),
			"created_at": "2026-10-17T00:00:00Z", "updated_at": "2026-10-17T00:00:00Z"}
		fake.endpoints = append(fake.endpoints, endpoint)
		writeJSON(t, writer, http.StatusCreated, endpoint)
	})
	fake.handle("PUT /api/v1/org/ai-settings/endpoints/{id}", func(writer http.ResponseWriter, request *http.Request) {
		var body map[string]json.RawMessage
		decodeJSON(t, request, &body)
		fake.endpointBodies = append(fake.endpointBodies, body)
		for _, endpoint := range fake.endpoints {
			if endpoint["id"] != request.PathValue("id") {
				continue
			}
			var name, baseURL, apiKey string
			_ = json.Unmarshal(body["name"], &name)
			_ = json.Unmarshal(body["base_url"], &baseURL)
			if raw, present := body["api_key"]; present {
				_ = json.Unmarshal(raw, &apiKey)
				fake.endpointKeys[request.PathValue("id")] = apiKey
			}
			endpoint["name"] = name
			endpoint["base_url"] = strings.TrimRight(baseURL, "/")
			endpoint["key_preview"] = aiFakeMask(fake.endpointKeys[request.PathValue("id")])
			writeJSON(t, writer, http.StatusOK, endpoint)
			return
		}
		writeJSON(t, writer, http.StatusNotFound, map[string]any{"detail": "Endpoint not found"})
	})
	fake.handle("DELETE /api/v1/org/ai-settings/endpoints/{id}", func(writer http.ResponseWriter, request *http.Request) {
		if !fake.deleteEndpoint(request.PathValue("id")) {
			writeJSON(t, writer, http.StatusNotFound, map[string]any{"detail": "Endpoint not found"})
			return
		}
		writeJSON(t, writer, http.StatusOK, map[string]any{"deleted": true, "cascaded_model_count": 0})
	})

	lanesListing := func() []map[string]any {
		listing := []map[string]any{}
		for _, lane := range []string{"code_review", "stack_readme"} {
			selected := fake.laneSelections[lane]
			listing = append(listing, map[string]any{"lane": lane, "display_name": lane, "description": "lane " + lane,
				"default_tier": "think", "selected_model_key": selected, "effective_model_id": "claude-think",
				"effective_model_name": "Think", "is_selection_stale": false})
		}
		return listing
	}
	fake.handle("GET /api/v1/org/ai-settings/lane-models", func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(t, writer, http.StatusOK, map[string]any{"lanes": lanesListing()})
	})
	fake.handle("PUT /api/v1/org/ai-settings/lane-models/{lane}", func(writer http.ResponseWriter, request *http.Request) {
		lane := request.PathValue("lane")
		if lane != "code_review" && lane != "stack_readme" {
			writeJSON(t, writer, http.StatusNotFound, map[string]any{"detail": "unknown lane"})
			return
		}
		var body map[string]*string
		decodeJSON(t, request, &body)
		if body["model_key"] == nil || *body["model_key"] == "" {
			delete(fake.laneSelections, lane)
		} else {
			fake.laneSelections[lane] = *body["model_key"]
		}
		writeJSON(t, writer, http.StatusOK, map[string]any{"lanes": lanesListing()})
	})

	identity := func() map[string]any {
		if fake.boardRole == "" {
			return map[string]any{"provisioned": false}
		}
		return map[string]any{"provisioned": true, "subject": "board-agent", "role_slug": fake.boardRole,
			"ankra_user_id": "11111111-1111-4111-8111-111111111111"}
	}
	fake.handle("GET /api/v1/org/ai-tasks/board-identity", func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(t, writer, http.StatusOK, identity())
	})
	fake.handle("POST /api/v1/org/ai-tasks/board-identity", func(writer http.ResponseWriter, request *http.Request) {
		var body map[string]string
		decodeJSON(t, request, &body)
		if fake.boardRole == "" {
			fake.boardRole = body["role_slug"]
		}
		writeJSON(t, writer, http.StatusCreated, identity())
	})
	fake.handle("DELETE /api/v1/org/ai-tasks/board-identity", func(writer http.ResponseWriter, _ *http.Request) {
		fake.boardRole = ""
		writeJSON(t, writer, http.StatusOK, map[string]any{"provisioned": false})
	})
	return fake
}

// deleteEndpoint removes an endpoint and cascades its catalogue models, as
// the platform does.
func (fake *aiFake) deleteEndpoint(identifier string) bool {
	for index, endpoint := range fake.endpoints {
		if endpoint["id"] != identifier {
			continue
		}
		fake.endpoints = append(fake.endpoints[:index], fake.endpoints[index+1:]...)
		kept := fake.models[:0]
		for _, model := range fake.models {
			if model["endpoint_id"] != identifier {
				kept = append(kept, model)
			}
		}
		fake.models = kept
		return true
	}
	return false
}

func TestAccAIProviderAndKeys(t *testing.T) {
	fake := newAIFake(t)

	configuration := func(anthropicKey string) string {
		return fake.providerBlock() + fmt.Sprintf(`
resource "ankra_ai_anthropic_key" "own" {
  api_key = %q
}

resource "ankra_ai_openrouter_key" "own" {
  api_key = "sk-or-v1-abcdefgh"
}

resource "ankra_ai_openai_compatible" "legacy" {
  base_url = "https://llm.example.com/v1/"
  model    = "gpt-4o"
  api_key  = "sk-legacy-123456"
}

resource "ankra_ai_provider" "active" {
  name       = "anthropic"
  depends_on = [ankra_ai_anthropic_key.own]
}
`, anthropicKey)
	}

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: configuration("sk-ant-first-key-0001"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ankra_ai_anthropic_key.own", "key_preview", "sk-...0001"),
					resource.TestCheckResourceAttr("ankra_ai_provider.active", "name", "anthropic"),
					// The configured spelling with a trailing slash is kept: the platform trims it.
					resource.TestCheckResourceAttr("ankra_ai_openai_compatible.legacy", "base_url", "https://llm.example.com/v1/"),
					resource.TestCheckResourceAttr("ankra_ai_openai_compatible.legacy", "key_preview", "sk-...3456"),
					func(_ *terraform.State) error {
						if fake.anthropicKey != "sk-ant-first-key-0001" || fake.compatible["model"] != "gpt-4o" {
							return fmt.Errorf("platform holds anthropic %q, compatible %v", fake.anthropicKey, fake.compatible)
						}
						return nil
					},
				),
			},
			{
				Config: configuration("sk-ant-second-key-0002"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ankra_ai_anthropic_key.own", "key_preview", "sk-...0002"),
					func(_ *terraform.State) error {
						if fake.anthropicKey != "sk-ant-second-key-0002" {
							return fmt.Errorf("key was not rotated: %q", fake.anthropicKey)
						}
						return nil
					},
				),
			},
			{
				ResourceName:            "ankra_ai_anthropic_key.own",
				ImportState:             true,
				ImportStateId:           "ai_anthropic_key",
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"api_key"},
			},
			{
				ResourceName:      "ankra_ai_provider.active",
				ImportState:       true,
				ImportStateId:     "ai_provider",
				ImportStateVerify: true,
			},
			{
				// Removed outside Terraform: the platform also falls back to
				// ankra, and the next apply restores both.
				PreConfig: func() {
					fake.anthropicKey = ""
					fake.provider = "ankra"
				},
				Config: configuration("sk-ant-second-key-0002"),
				Check: func(_ *terraform.State) error {
					if fake.anthropicKey != "sk-ant-second-key-0002" || fake.provider != "anthropic" {
						return fmt.Errorf("not restored: key %q, provider %q", fake.anthropicKey, fake.provider)
					}
					return nil
				},
			},
		},
		CheckDestroy: func(_ *terraform.State) error {
			if fake.anthropicKey != "" || fake.openRouterKey != "" || fake.compatible != nil || fake.provider != "ankra" {
				return fmt.Errorf("destroy left anthropic %q, openrouter %q, compatible %v, provider %q",
					fake.anthropicKey, fake.openRouterKey, fake.compatible, fake.provider)
			}
			return nil
		},
	})
}

func TestAccAIEndpointModelAndLane(t *testing.T) {
	fake := newAIFake(t)

	configuration := func(endpointName, tierLine string) string {
		return fake.providerBlock() + fmt.Sprintf(`
resource "ankra_ai_endpoint" "litellm" {
  name     = %q
  base_url = "https://litellm.example.com/v1"
  api_key  = "sk-endpoint-0001"
}

resource "ankra_ai_model" "gpt" {
  key          = "gpt-4o"
  display_name = "GPT-4o"
  model_id     = "gpt-4o"
  endpoint_id  = ankra_ai_endpoint.litellm.id
  %s
}

resource "ankra_ai_lane_model" "review" {
  lane      = "code_review"
  model_key = ankra_ai_model.gpt.key
}

data "ankra_ai_models" "all" {
  depends_on = [ankra_ai_model.gpt]
}

data "ankra_ai_endpoints" "all" {
  depends_on = [ankra_ai_endpoint.litellm]
}

data "ankra_ai_lanes" "all" {
  depends_on = [ankra_ai_lane_model.review]
}
`, endpointName, tierLine)
	}

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: configuration("litellm", `tier = "expert"`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("ankra_ai_endpoint.litellm", "id"),
					resource.TestCheckResourceAttr("ankra_ai_endpoint.litellm", "key_preview", "sk-...0001"),
					resource.TestCheckResourceAttr("ankra_ai_model.gpt", "provider_kind", "openai_compatible"),
					resource.TestCheckResourceAttr("ankra_ai_model.gpt", "tier", "expert"),
					resource.TestCheckResourceAttr("ankra_ai_model.gpt", "context_window_tokens", "200000"),
					resource.TestCheckResourceAttr("ankra_ai_model.gpt", "is_default", "false"),
					resource.TestCheckResourceAttr("ankra_ai_lane_model.review", "model_key", "gpt-4o"),
					// The first change materialised the three built-ins next to the new entry.
					resource.TestCheckResourceAttr("data.ankra_ai_models.all", "models.#", "4"),
					resource.TestCheckResourceAttr("data.ankra_ai_endpoints.all", "endpoints.#", "1"),
					resource.TestCheckResourceAttr("data.ankra_ai_lanes.all", "lanes.#", "2"),
					resource.TestCheckResourceAttr("data.ankra_ai_lanes.all", "lanes.0.selected_model_key", "gpt-4o"),
					resource.TestCheckNoResourceAttr("data.ankra_ai_lanes.all", "lanes.1.selected_model_key"),
					func(_ *terraform.State) error {
						body := fake.modelBodies[0]
						if string(body["context_window_tokens"]) != "0" || string(body["description"]) != `""` {
							return fmt.Errorf("create sent context window %s, description %s", body["context_window_tokens"], body["description"])
						}
						return nil
					},
				),
			},
			{
				// Renaming the endpoint keeps its key; removing the tier
				// sends an empty tier, and the whole entry is replaced.
				Config: configuration("litellm-proxy", ""),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ankra_ai_endpoint.litellm", "name", "litellm-proxy"),
					resource.TestCheckNoResourceAttr("ankra_ai_model.gpt", "tier"),
					func(_ *terraform.State) error {
						endpointUpdate := fake.endpointBodies[len(fake.endpointBodies)-1]
						if _, present := endpointUpdate["api_key"]; present {
							return fmt.Errorf("endpoint update re-sent an unchanged key")
						}
						modelUpdate := fake.modelBodies[len(fake.modelBodies)-1]
						if string(modelUpdate["tier"]) != `""` || string(modelUpdate["context_window_tokens"]) != "200000" {
							return fmt.Errorf("model update sent tier %s, context window %s", modelUpdate["tier"], modelUpdate["context_window_tokens"])
						}
						return nil
					},
				),
			},
			{
				ResourceName:            "ankra_ai_endpoint.litellm",
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"api_key"},
			},
			{
				ResourceName:      "ankra_ai_model.gpt",
				ImportState:       true,
				ImportStateVerify: true,
			},
			{
				ResourceName:      "ankra_ai_lane_model.review",
				ImportState:       true,
				ImportStateId:     "code_review",
				ImportStateVerify: true,
			},
			{
				// The endpoint deleted outside Terraform takes its model
				// with it, and the lane selection is cleared: all three are
				// recreated.
				PreConfig: func() {
					fake.deleteEndpoint(platformServicesText(fake.endpoints[0]["id"]))
					delete(fake.laneSelections, "code_review")
				},
				Config: configuration("litellm-proxy", ""),
				Check: func(_ *terraform.State) error {
					if len(fake.endpoints) != 1 || fake.laneSelections["code_review"] != "gpt-4o" {
						return fmt.Errorf("not recreated: endpoints %v, lanes %v", fake.endpoints, fake.laneSelections)
					}
					return nil
				},
			},
		},
		CheckDestroy: func(_ *terraform.State) error {
			if len(fake.endpoints) != 0 || len(fake.laneSelections) != 0 {
				return fmt.Errorf("destroy left endpoints %v, lanes %v", fake.endpoints, fake.laneSelections)
			}
			return nil
		},
	})
}

func TestAccAIModelBuiltinImport(t *testing.T) {
	fake := newAIFake(t)

	configuration := fake.providerBlock() + `
resource "ankra_ai_model" "expert" {
  key             = "expert"
  display_name    = "Expert"
  description     = "built-in expert"
  model_id        = "claude-opus-5"
  tier            = "expert"
  supports_images = true
}
`
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				// A built-in key is managed by importing it, not creating it.
				Config:             configuration,
				ResourceName:       "ankra_ai_model.expert",
				ImportState:        true,
				ImportStateId:      "expert",
				ImportStatePersist: true,
				ImportStateCheck: func(states []*terraform.InstanceState) error {
					if states[0].Attributes["model_id"] != "claude-expert" || states[0].Attributes["id"] != "" {
						return fmt.Errorf("imported %v", states[0].Attributes)
					}
					return nil
				},
			},
			{
				Config: configuration,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ankra_ai_model.expert", "model_id", "claude-opus-5"),
					resource.TestCheckResourceAttrSet("ankra_ai_model.expert", "id"),
					func(_ *terraform.State) error {
						if !fake.catalogueMaterialised || len(fake.models) != 3 {
							return fmt.Errorf("editing a built-in should materialise the catalogue, got %v", fake.models)
						}
						return nil
					},
				),
			},
		},
	})
}

func TestAccAIBoardIdentity(t *testing.T) {
	fake := newAIFake(t)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: fake.providerBlock() + `
resource "ankra_ai_board_identity" "board" {}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ankra_ai_board_identity.board", "role_slug", "operator"),
					resource.TestCheckResourceAttr("ankra_ai_board_identity.board", "subject", "board-agent"),
				),
			},
			{
				ResourceName:      "ankra_ai_board_identity.board",
				ImportState:       true,
				ImportStateId:     "ai_board_identity",
				ImportStateVerify: true,
			},
			{
				// Changing the role stands the identity down and provisions it again.
				Config: fake.providerBlock() + `
resource "ankra_ai_board_identity" "board" {
  role_slug = "viewer"
}
`,
				Check: func(_ *terraform.State) error {
					if fake.boardRole != "viewer" {
						return fmt.Errorf("role is %q", fake.boardRole)
					}
					return nil
				},
			},
			{
				PreConfig: func() { fake.boardRole = "" },
				Config: fake.providerBlock() + `
resource "ankra_ai_board_identity" "board" {
  role_slug = "viewer"
}
`,
				Check: func(_ *terraform.State) error {
					if fake.boardRole != "viewer" {
						return fmt.Errorf("revoked identity was not provisioned again: %q", fake.boardRole)
					}
					return nil
				},
			},
		},
		CheckDestroy: func(_ *terraform.State) error {
			if fake.boardRole != "" {
				return fmt.Errorf("destroy left the identity with role %q", fake.boardRole)
			}
			return nil
		},
	})
}

func TestAccAIBoardIdentityExistingRole(t *testing.T) {
	fake := newAIFake(t)
	fake.boardRole = "member"

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: fake.providerBlock() + `
resource "ankra_ai_board_identity" "board" {}
`,
				ExpectError: regexp.MustCompile(`already exists with role "member"`),
			},
		},
	})
}

func TestAccAIPlanTimeValidation(t *testing.T) {
	fake := newAIFake(t)

	for name, configuration := range map[string]string{
		"provider": `resource "ankra_ai_provider" "bad" {
name = "gemini"
}`,
		"key": `resource "ankra_ai_anthropic_key" "bad" {
api_key = "sk-or-wrong"
}`,
		"base url": `resource "ankra_ai_endpoint" "bad" {
name = "x"
base_url = "ftp://x"
api_key = "k"
}`,
		"model key": `resource "ankra_ai_model" "bad" {
key = "auto"
display_name = "x"
model_id = "x"
}`,
		"tier": `resource "ankra_ai_model" "bad" {
key = "x"
display_name = "x"
model_id = "x"
tier = "fast"
}`,
		"role": `resource "ankra_ai_board_identity" "bad" {
role_slug = "owner"
}`,
		"lane": `resource "ankra_ai_lane_model" "bad" {
lane = "a/b"
model_key = "x"
}`,
	} {
		t.Run(name, func(t *testing.T) {
			resource.Test(t, resource.TestCase{
				ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
				Steps: []resource.TestStep{{
					Config:      fake.providerBlock() + configuration,
					PlanOnly:    true,
					ExpectError: regexp.MustCompile(`(Invalid|invalid)`),
				}},
			})
		})
	}
	if served := fake.served(); len(served) != 0 {
		t.Errorf("a refused plan sent %v", served)
	}
}
