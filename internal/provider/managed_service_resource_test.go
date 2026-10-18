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
	"time"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

const (
	managedServicePackageID  = "66666666-6666-4666-8666-666666666666"
	managedServiceClusterID  = "77777777-7777-4777-8777-777777777777"
	managedServiceConsumerID = "88888888-8888-4888-8888-888888888888"
	managedServiceLateID     = "99999999-9999-4999-8999-999999999999"
)

// managedServiceFake holds managed services the way the platform does: a
// strict review body, a digest-checked confirm, an install that reaches
// serving after a read, and a retirement that settles after a read. A
// released service keeps answering, with state "released".
type managedServiceFake struct {
	*routeFake
	reviews            map[string]map[string]any
	instances          map[string]map[string]any
	retirements        map[string]map[string]any
	reviewBodies       []map[string]json.RawMessage
	retirementBodies   []map[string]any
	confirmedDigests   []string
	installOutcome     string
	retirementOutcome  string
	reviewCount        int
	isApplicationsOff  bool
	retirementRequests int
	// isStillDeploying keeps a new install deploying, for the wait timeout.
	isStillDeploying bool
	// isConfirmAnswerLost confirms the review but answers 502, as when the
	// answer is lost on the way back.
	isConfirmAnswerLost bool
	// isConfirmRefused refuses the confirm with nothing set up.
	isConfirmRefused bool
}

func newManagedServiceFake(t *testing.T) *managedServiceFake {
	previous := managedServicePollInterval
	managedServicePollInterval = 10 * time.Millisecond
	t.Cleanup(func() { managedServicePollInterval = previous })

	fake := &managedServiceFake{
		routeFake: newRouteFake(t), reviews: map[string]map[string]any{}, instances: map[string]map[string]any{},
		retirements: map[string]map[string]any{}, installOutcome: "serving", retirementOutcome: "retired",
	}
	gated := func(pattern string, handler http.HandlerFunc) {
		fake.handle(pattern, func(writer http.ResponseWriter, request *http.Request) {
			if fake.isApplicationsOff {
				writeJSON(t, writer, http.StatusNotFound, map[string]any{"detail": "Not Found"})
				return
			}
			handler(writer, request)
		})
	}

	gated("POST /api/v1/org/service-admission/reviews", func(writer http.ResponseWriter, request *http.Request) {
		var body map[string]json.RawMessage
		decodeJSON(t, request, &body)
		fake.reviewBodies = append(fake.reviewBodies, body)
		for _, key := range []string{"package_version_id", "name", "mode", "region", "data_boundary", "cluster_id", "consumer_ids", "parameters", "secret_references"} {
			raw, present := body[key]
			if !present || string(raw) == "null" {
				writeJSON(t, writer, http.StatusUnprocessableEntity, map[string]any{"detail": key + " is required"})
				return
			}
		}
		if len(body) != 9 {
			writeJSON(t, writer, http.StatusUnprocessableEntity, map[string]any{"detail": "unknown field"})
			return
		}
		var name string
		_ = json.Unmarshal(body["name"], &name)
		for _, instance := range fake.instances {
			if instance["name"] == name && instance["state"] != "released" {
				writeJSON(t, writer, http.StatusConflict, map[string]any{"detail": "Service name is already in use"})
				return
			}
		}
		fake.reviewCount++
		identifier := fmt.Sprintf("aaaaaaaa-0000-4000-8000-%012d", fake.reviewCount)
		review := map[string]any{"id": identifier, "digest": fmt.Sprintf("sha256:%064d", fake.reviewCount), "body": body,
			"package_version_id": managedServicePackageID, "execution_id": nil, "confirmed_at": nil}
		fake.reviews[identifier] = review
		writeJSON(t, writer, http.StatusOK, review)
	})
	gated("POST /api/v1/org/service-admission/reviews/{review_id}/confirm", func(writer http.ResponseWriter, request *http.Request) {
		review, exists := fake.reviews[request.PathValue("review_id")]
		if !exists {
			writeJSON(t, writer, http.StatusNotFound, map[string]any{"detail": "Service review not found"})
			return
		}
		var body map[string]string
		decodeJSON(t, request, &body)
		fake.confirmedDigests = append(fake.confirmedDigests, body["digest"])
		if body["digest"] != review["digest"] || fake.isConfirmRefused {
			writeJSON(t, writer, http.StatusConflict, map[string]any{"detail": "Service review has changed"})
			return
		}
		reviewBody, _ := review["body"].(map[string]json.RawMessage)
		text := func(key string) string {
			var value string
			_ = json.Unmarshal(reviewBody[key], &value)
			return value
		}
		var consumerIDs []string
		_ = json.Unmarshal(reviewBody["consumer_ids"], &consumerIDs)
		consumers := []map[string]any{}
		for _, consumerID := range consumerIDs {
			consumers = append(consumers, map[string]any{"id": consumerID})
		}
		fake.instances[fmt.Sprint(review["id"])] = map[string]any{
			"id": review["id"], "name": text("name"), "package_version_id": text("package_version_id"), "generation": 1,
			"mode": text("mode"), "cluster_id": text("cluster_id"), "cluster_name": "prod", "region": text("region"),
			"data_boundary": text("data_boundary"), "consumers": consumers, "state": "deploying", "deployment_state": "running",
			"readiness": "pending", "created_at": "2026-10-18T09:00:00Z", "released_at": nil, "retirement": nil,
		}
		review["execution_id"] = "bbbbbbbb-0000-4000-8000-000000000001"
		review["confirmed_at"] = "2026-10-18T09:00:00Z"
		if fake.isConfirmAnswerLost {
			writeJSON(t, writer, http.StatusBadGateway, map[string]any{"detail": "upstream connection reset"})
			return
		}
		writeJSON(t, writer, http.StatusOK, review)
	})
	gated("GET /api/v1/org/service-admission/instances/{instance_id}", func(writer http.ResponseWriter, request *http.Request) {
		instance, exists := fake.instances[request.PathValue("instance_id")]
		if !exists {
			writeJSON(t, writer, http.StatusNotFound, map[string]any{"detail": "Service instance not found"})
			return
		}
		answer := map[string]any{}
		for key, value := range instance {
			answer[key] = value
		}
		writeJSON(t, writer, http.StatusOK, answer)
		if instance["state"] == "deploying" && !fake.isStillDeploying {
			instance["state"] = fake.installOutcome
			instance["readiness"] = "ready"
		}
	})
	gated("POST /api/v1/org/service-admission/instances/{instance_id}/retirements", func(writer http.ResponseWriter, request *http.Request) {
		fake.retirementRequests++
		instance, exists := fake.instances[request.PathValue("instance_id")]
		if !exists {
			writeJSON(t, writer, http.StatusNotFound, map[string]any{"detail": "Service instance not found"})
			return
		}
		var body map[string]any
		decodeJSON(t, request, &body)
		fake.retirementBodies = append(fake.retirementBodies, body)
		if body["acknowledge_data_loss"] != true {
			writeJSON(t, writer, http.StatusUnprocessableEntity, map[string]any{"detail": "acknowledge_data_loss must be true"})
			return
		}
		if fmt.Sprint(body["expected_generation"]) != fmt.Sprint(instance["generation"]) {
			writeJSON(t, writer, http.StatusConflict, map[string]any{"detail": "Service changed"})
			return
		}
		expected := []string{}
		consumers, _ := instance["consumers"].([]map[string]any)
		for _, consumer := range consumers {
			expected = append(expected, fmt.Sprint(consumer["id"]))
		}
		sent := []string{}
		disconnected, _ := body["disconnect_consumer_ids"].([]any)
		for _, consumerID := range disconnected {
			sent = append(sent, fmt.Sprint(consumerID))
		}
		if strings.Join(expected, ",") != strings.Join(sent, ",") {
			writeJSON(t, writer, http.StatusConflict, map[string]any{"detail": "Retirement consumers do not match"})
			return
		}
		identifier := fmt.Sprintf("cccccccc-0000-4000-8000-%012d", len(fake.retirements)+1)
		retirement := map[string]any{"id": identifier, "instance_id": instance["id"], "digest": "sha256:retire-" + identifier,
			"state": "pending", "phase": nil, "outcome": nil, "reason": nil}
		fake.retirements[identifier] = retirement
		writeJSON(t, writer, http.StatusOK, retirement)
	})
	gated("POST /api/v1/org/service-admission/instances/{instance_id}/retirements/{retirement_id}/confirm", func(writer http.ResponseWriter, request *http.Request) {
		retirement, exists := fake.retirements[request.PathValue("retirement_id")]
		if !exists {
			writeJSON(t, writer, http.StatusNotFound, map[string]any{"detail": "Retirement not found"})
			return
		}
		var body map[string]string
		decodeJSON(t, request, &body)
		if body["digest"] != retirement["digest"] {
			writeJSON(t, writer, http.StatusConflict, map[string]any{"detail": "Retirement review has changed"})
			return
		}
		retirement["state"] = "in_progress"
		phase := "retire_stack"
		retirement["phase"] = phase
		fake.instances[request.PathValue("instance_id")]["state"] = "retiring"
		writeJSON(t, writer, http.StatusOK, retirement)
	})
	gated("GET /api/v1/org/service-admission/instances/{instance_id}/retirements/{retirement_id}", func(writer http.ResponseWriter, request *http.Request) {
		retirement, exists := fake.retirements[request.PathValue("retirement_id")]
		if !exists {
			writeJSON(t, writer, http.StatusNotFound, map[string]any{"detail": "Retirement not found"})
			return
		}
		writeJSON(t, writer, http.StatusOK, retirement)
		if retirement["state"] == "in_progress" {
			retirement["state"] = "settled"
			retirement["outcome"] = fake.retirementOutcome
			if fake.retirementOutcome == "retired" {
				instance := fake.instances[request.PathValue("instance_id")]
				instance["state"] = "released"
				instance["released_at"] = "2026-10-18T10:00:00Z"
			} else {
				retirement["reason"] = "the stack could not be removed"
			}
		}
	})
	return fake
}

func (fake *managedServiceFake) live() int {
	count := 0
	for _, instance := range fake.instances {
		if instance["state"] != "released" {
			count++
		}
	}
	return count
}

func managedServiceConfig(fake *managedServiceFake, allowDataLoss bool) string {
	return fake.providerBlock() + fmt.Sprintf(`
resource "ankra_managed_service" "orders" {
  package_version_id         = %q
  name                       = "orders-db"
  cluster_id                 = %q
  region                     = "eu-north-1"
  data_boundary              = "eu"
  consumer_ids               = [%q]
  allow_data_loss_on_destroy = %t
}
`, managedServicePackageID, managedServiceClusterID, managedServiceConsumerID, allowDataLoss)
}

func TestAccManagedServiceLifecycle(t *testing.T) {
	fake := newManagedServiceFake(t)
	var retirementsBeforeGuard int

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy: func(_ *terraform.State) error {
			if fake.live() != 0 {
				return fmt.Errorf("a service is still live after destroy: %v", fake.instances)
			}
			body := fake.retirementBodies[len(fake.retirementBodies)-1]
			sent, _ := body["disconnect_consumer_ids"].([]any)
			if len(sent) != 2 {
				return fmt.Errorf("retirement did not disconnect every current consumer: %v", body)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: managedServiceConfig(fake, false),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ankra_managed_service.orders", "id", "aaaaaaaa-0000-4000-8000-000000000001"),
					resource.TestCheckResourceAttr("ankra_managed_service.orders", "state", "serving"),
					resource.TestCheckResourceAttr("ankra_managed_service.orders", "mode", "customer"),
					resource.TestCheckResourceAttr("ankra_managed_service.orders", "cluster_name", "prod"),
					resource.TestCheckResourceAttr("ankra_managed_service.orders", "connected_consumer_ids.#", "1"),
					func(_ *terraform.State) error {
						body := fake.reviewBodies[0]
						if string(body["parameters"]) != "{}" || string(body["secret_references"]) != "{}" {
							return fmt.Errorf("empty maps must be sent as {}, got %s and %s", body["parameters"], body["secret_references"])
						}
						if len(fake.confirmedDigests) != 1 || fake.confirmedDigests[0] != fake.reviews["aaaaaaaa-0000-4000-8000-000000000001"]["digest"] {
							return fmt.Errorf("confirm did not carry the review's digest: %v", fake.confirmedDigests)
						}
						return nil
					},
				),
			},
			{
				// A consumer connected outside Terraform shows, and plans no replacement.
				PreConfig: func() {
					instance := fake.instances["aaaaaaaa-0000-4000-8000-000000000001"]
					consumers, _ := instance["consumers"].([]map[string]any)
					instance["consumers"] = append(consumers, map[string]any{"id": managedServiceLateID})
					instance["generation"] = 2
					retirementsBeforeGuard = fake.retirementRequests
				},
				Config: managedServiceConfig(fake, false),
				Check:  resource.TestCheckResourceAttr("ankra_managed_service.orders", "connected_consumer_ids.#", "2"),
			},
			{
				Config:      managedServiceConfig(fake, false),
				Destroy:     true,
				ExpectError: regexp.MustCompile("Retiring the managed service would delete its data"),
				Check: func(_ *terraform.State) error {
					if fake.retirementRequests != retirementsBeforeGuard || fake.live() != 1 {
						return fmt.Errorf("the guarded destroy reached the platform")
					}
					return nil
				},
			},
			{
				Config: managedServiceConfig(fake, true),
				Check: func(_ *terraform.State) error {
					if fake.retirementRequests != retirementsBeforeGuard || fake.reviewCount != 1 {
						return fmt.Errorf("turning the guard off retired or replaced the service")
					}
					return nil
				},
			},
			{
				ResourceName:            "ankra_managed_service.orders",
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"consumer_ids", "allow_data_loss_on_destroy"},
			},
		},
	})
}

// A guarded destroy sends nothing at all, not even a read.
func TestAccManagedServiceGuardedDestroySendsNothing(t *testing.T) {
	fake := newManagedServiceFake(t)
	var servedBefore int

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{Config: managedServiceConfig(fake, false)},
			{
				PreConfig:   func() { servedBefore = len(fake.served()) },
				Config:      managedServiceConfig(fake, false),
				Destroy:     true,
				ExpectError: regexp.MustCompile("Nothing was changed"),
				Check: func(_ *terraform.State) error {
					for _, served := range fake.served()[servedBefore:] {
						if !strings.HasPrefix(served, "GET ") {
							return fmt.Errorf("a guarded destroy wrote to the platform: %s", served)
						}
					}
					return nil
				},
			},
			{
				// Let the test case clean up.
				Config: managedServiceConfig(fake, true),
			},
		},
	})
}

func TestAccManagedServiceReleasedIsGone(t *testing.T) {
	fake := newManagedServiceFake(t)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{Config: managedServiceConfig(fake, true)},
			{
				PreConfig: func() {
					instance := fake.instances["aaaaaaaa-0000-4000-8000-000000000001"]
					instance["state"] = "released"
				},
				Config:             managedServiceConfig(fake, true),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
			{
				Config: managedServiceConfig(fake, true),
				Check: func(_ *terraform.State) error {
					if fake.reviewCount != 2 || fake.live() != 1 {
						return fmt.Errorf("a released service was not set up again: %d reviews, %d live", fake.reviewCount, fake.live())
					}
					return nil
				},
			},
		},
	})
}

func TestAccManagedServiceFailures(t *testing.T) {
	t.Run("failed install", func(t *testing.T) {
		fake := newManagedServiceFake(t)
		fake.installOutcome = "failed"
		resource.Test(t, resource.TestCase{
			ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{Config: managedServiceConfig(fake, true), ExpectError: regexp.MustCompile("Managed service install failed")},
			},
		})
	})
	t.Run("retirement not completed", func(t *testing.T) {
		fake := newManagedServiceFake(t)
		resource.Test(t, resource.TestCase{
			ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{Config: managedServiceConfig(fake, true)},
				{
					PreConfig:   func() { fake.retirementOutcome = "removal_failed" },
					Config:      managedServiceConfig(fake, true),
					Destroy:     true,
					ExpectError: regexp.MustCompile(`outcome\s+"removal_failed"`),
				},
				{
					PreConfig: func() { fake.retirementOutcome = "retired" },
					Config:    managedServiceConfig(fake, true),
				},
			},
		})
	})
	t.Run("feature off", func(t *testing.T) {
		fake := newManagedServiceFake(t)
		fake.isApplicationsOff = true
		resource.Test(t, resource.TestCase{
			ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{Config: managedServiceConfig(fake, true), ExpectError: regexp.MustCompile("Managed services not available")},
			},
		})
	})
	t.Run("refused confirm", func(t *testing.T) {
		fake := newManagedServiceFake(t)
		fake.isConfirmRefused = true
		resource.Test(t, resource.TestCase{
			ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{Config: managedServiceConfig(fake, true), ExpectError: regexp.MustCompile("Unable to confirm managed service")},
			},
		})
		if len(fake.instances) != 0 {
			t.Errorf("a refused confirm left a service: %v", fake.instances)
		}
	})
}

// A wait that runs out only warns: the resource stays untainted, so the next
// plan neither replaces nor retires a service that is still coming up.
func TestAccManagedServiceSlowInstallIsKept(t *testing.T) {
	fake := newManagedServiceFake(t)
	fake.isStillDeploying = true
	withTimeout := func(allowDataLoss bool) string {
		return strings.Replace(managedServiceConfig(fake, allowDataLoss), fmt.Sprintf("= %t\n}\n", allowDataLoss), fmt.Sprintf("= %t\n\n  timeouts {\n    create = \"50ms\"\n  }\n}\n", allowDataLoss), 1)
	}

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: withTimeout(false),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ankra_managed_service.orders", "state", "deploying"),
					func(state *terraform.State) error {
						if state.RootModule().Resources["ankra_managed_service.orders"].Primary.Tainted {
							return fmt.Errorf("a slow install tainted the service")
						}
						return nil
					},
				),
			},
			{
				// The install finishes: refresh reads it, and nothing is replaced.
				PreConfig: func() {
					fake.isStillDeploying = false
					fake.instances["aaaaaaaa-0000-4000-8000-000000000001"]["state"] = "serving"
				},
				Config: withTimeout(false),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ankra_managed_service.orders", "state", "serving"),
					func(_ *terraform.State) error {
						if fake.reviewCount != 1 || fake.retirementRequests != 0 {
							return fmt.Errorf("a slow install was replaced: %d reviews, %d retirements", fake.reviewCount, fake.retirementRequests)
						}
						return nil
					},
				),
			},
			{Config: withTimeout(true)},
		},
	})
}

// A confirm that reached the platform but whose answer was lost adopts the
// service instead of orphaning it.
func TestAccManagedServiceLostConfirmAnswerIsAdopted(t *testing.T) {
	fake := newManagedServiceFake(t)
	fake.isConfirmAnswerLost = true
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: managedServiceConfig(fake, true),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ankra_managed_service.orders", "id", "aaaaaaaa-0000-4000-8000-000000000001"),
					resource.TestCheckResourceAttr("ankra_managed_service.orders", "state", "serving"),
					func(_ *terraform.State) error {
						if fake.reviewCount != 1 {
							return fmt.Errorf("the lost answer led to a second setup")
						}
						return nil
					},
				),
			},
		},
	})
}
