// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"fmt"
	"net/http"
	"sort"
	"testing"
)

// lanesFake stands in for the platform behind every resource of the cluster
// lanes domain. It answers with the status codes and bodies the backend
// sends (cluster go/internal/providerapi, managedk8sapi, clusterapi
// playground handlers):
//
//   - a provider create answers 201 on the token surface; deprovision 200 or
//     404 when the cluster is gone;
//   - node-group writes refuse anything but wait=false, answer 202
//     {"status":"accepted"}, and the listing derives groups from what was
//     submitted; a write naming a missing group is a 409;
//   - managed creates and node-pool writes answer 200; Kapsule and Ankra
//     Cloud Kubernetes are only deleted through POST
//     .../delete-provider-cluster, their bare DELETE is a disconnect;
//   - the playground status keeps answering 200 for a destroyed environment,
//     with phase "deprovisioning"; resizing from or to the trial is a 400.
type lanesFake struct {
	*routeFake
	clusters map[string]*lanesFakeCluster
	created  int

	createBodies     map[string][]map[string]any
	deprovisionQuery []string
	nodeGroupWrites  []string
	nodeGroupBodies  map[string]map[string]any
	poolPatches      []map[string]any
	managedDeletes   []string
	playgroundCalls  []string
}

type lanesFakeCluster struct {
	id     string
	name   string
	kind   string
	state  string
	groups map[string]map[string]any
	pools  map[string]map[string]any
	// playground fields
	phase     string
	plan      string
	orderedAt any
}

func newLanesFake(t *testing.T) *lanesFake {
	fake := &lanesFake{
		routeFake:       newRouteFake(t),
		clusters:        map[string]*lanesFakeCluster{},
		createBodies:    map[string][]map[string]any{},
		nodeGroupBodies: map[string]map[string]any{},
	}

	fake.handle("GET /api/v1/clusters", func(writer http.ResponseWriter, request *http.Request) {
		wanted := request.URL.Query().Get("cluster_id")
		rows := []map[string]any{}
		for _, id := range fake.sortedIDs() {
			cluster := fake.clusters[id]
			if cluster.kind == "playground" || (wanted != "" && wanted != id) {
				continue
			}
			rows = append(rows, map[string]any{"id": id, "name": cluster.name, "kind": cluster.kind, "state": cluster.state})
		}
		writeJSON(t, writer, http.StatusOK, map[string]any{
			"result":     rows,
			"pagination": map[string]any{"total_count": len(rows), "total_pages": 1, "page": 1, "page_size": 100},
		})
	})

	fake.handle("POST /api/v1/clusters/{lane}", func(writer http.ResponseWriter, request *http.Request) {
		var body map[string]any
		decodeJSON(t, request, &body)
		lane := request.PathValue("lane")
		fake.createBodies[lane] = append(fake.createBodies[lane], body)
		cluster := fake.addCluster(lane, fmt.Sprint(body["name"]), "running")
		cluster.groups["workers"] = map[string]any{
			"name": "workers", "instance_type": "default", "count": body["worker_count"],
			"min": 1, "max": 100, "autoscaling_enabled": false, "labels": map[string]any{}, "taints": []any{},
		}
		writeJSON(t, writer, http.StatusCreated, map[string]any{"cluster_id": cluster.id, "name": cluster.name})
	})
	fake.handle("DELETE /api/v1/clusters/{lane}/{id}", func(writer http.ResponseWriter, request *http.Request) {
		cluster := fake.clusters[request.PathValue("id")]
		if cluster == nil || cluster.kind != request.PathValue("lane") {
			writeJSON(t, writer, http.StatusNotFound, map[string]any{"detail": "Cluster not found"})
			return
		}
		fake.deprovisionQuery = append(fake.deprovisionQuery, request.URL.RawQuery)
		delete(fake.clusters, cluster.id)
		writeJSON(t, writer, http.StatusOK, map[string]any{"success": true, "cluster_id": cluster.id})
	})

	fake.registerNodeGroupRoutes(t)
	fake.registerManagedRoutes(t)
	fake.registerPlaygroundRoutes(t)

	for _, distribution := range []string{"k3s", "kubeadm"} {
		version := "v1.33.4"
		if distribution == "k3s" {
			version = "v1.33.4+k3s1"
		}
		fake.handle("GET /api/v1/clusters/"+distribution+"-versions", func(writer http.ResponseWriter, _ *http.Request) {
			writeJSON(t, writer, http.StatusOK, map[string]any{
				"stable_version": version,
				"versions": []any{
					map[string]any{"version": version, "channel": "v1.33", "published_at": "2026-09-01T00:00:00Z"},
					map[string]any{"version": "v1.32.8", "channel": "v1.32", "published_at": nil},
				},
			})
		})
	}
	return fake
}

func (fake *lanesFake) sortedIDs() []string {
	ids := make([]string, 0, len(fake.clusters))
	for id := range fake.clusters {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func (fake *lanesFake) addCluster(kind, name, state string) *lanesFakeCluster {
	fake.created++
	cluster := &lanesFakeCluster{
		id:     fmt.Sprintf("00000000-0000-0000-0000-%012d", fake.created),
		name:   name,
		kind:   kind,
		state:  state,
		groups: map[string]map[string]any{},
		pools:  map[string]map[string]any{},
	}
	fake.clusters[cluster.id] = cluster
	return cluster
}

func (fake *lanesFake) registerNodeGroupRoutes(t *testing.T) {
	cluster := func(writer http.ResponseWriter, request *http.Request) *lanesFakeCluster {
		found := fake.clusters[request.PathValue("id")]
		if found == nil || found.kind != request.PathValue("lane") {
			writeJSON(t, writer, http.StatusNotFound, map[string]any{"detail": "Cluster not found"})
			return nil
		}
		return found
	}
	accepted := func(request *http.Request, summary string) {
		if request.URL.Query().Get("wait") != "false" {
			t.Errorf("%s %s sent without wait=false", request.Method, request.URL.Path)
		}
		fake.nodeGroupWrites = append(fake.nodeGroupWrites, summary)
	}
	group := func(writer http.ResponseWriter, request *http.Request, found *lanesFakeCluster) map[string]any {
		row := found.groups[request.PathValue("group")]
		if row == nil {
			writeJSON(t, writer, http.StatusConflict, map[string]any{"detail": "Node group not found"})
		}
		return row
	}

	fake.handle("GET /api/v1/clusters/{lane}/{id}/node-groups", func(writer http.ResponseWriter, request *http.Request) {
		found := cluster(writer, request)
		if found == nil {
			return
		}
		names := make([]string, 0, len(found.groups))
		for name := range found.groups {
			names = append(names, name)
		}
		sort.Strings(names)
		rows := make([]any, 0, len(names))
		for _, name := range names {
			rows = append(rows, found.groups[name])
		}
		writeJSON(t, writer, http.StatusOK, map[string]any{"node_groups": rows})
	})
	fake.handle("POST /api/v1/clusters/{lane}/{id}/node-groups", func(writer http.ResponseWriter, request *http.Request) {
		found := cluster(writer, request)
		if found == nil {
			return
		}
		var body map[string]any
		decodeJSON(t, request, &body)
		name := fmt.Sprint(body["name"])
		if _, exists := found.groups[name]; exists {
			writeJSON(t, writer, http.StatusConflict, map[string]any{"detail": "Node group already exists"})
			return
		}
		accepted(request, "add "+name)
		fake.nodeGroupBodies["add "+name] = body
		count := body["count"]
		if count == nil {
			count = 1
		}
		row := map[string]any{
			"name": name, "instance_type": body["instance_type"], "count": count,
			"min": 1, "max": 100, "autoscaling_enabled": false, "labels": map[string]any{}, "taints": []any{},
		}
		if labels, ok := body["labels"].(map[string]any); ok {
			row["labels"] = labels
		}
		if taints, ok := body["taints"].([]any); ok {
			row["taints"] = taints
		}
		if autoscaling, ok := body["autoscaling"].(map[string]any); ok {
			row["autoscaling_enabled"] = autoscaling["enabled"]
			row["min"] = autoscaling["min_count"]
			row["max"] = autoscaling["max_count"]
		}
		found.groups[name] = row
		writeJSON(t, writer, http.StatusAccepted, map[string]any{"status": "accepted"})
	})
	put := func(suffix string, apply func(row map[string]any, body map[string]any)) {
		fake.handle("PUT /api/v1/clusters/{lane}/{id}/node-groups/{group}/"+suffix, func(writer http.ResponseWriter, request *http.Request) {
			found := cluster(writer, request)
			if found == nil {
				return
			}
			row := group(writer, request, found)
			if row == nil {
				return
			}
			var body map[string]any
			decodeJSON(t, request, &body)
			accepted(request, suffix+" "+request.PathValue("group"))
			fake.nodeGroupBodies[suffix+" "+request.PathValue("group")] = body
			apply(row, body)
			writeJSON(t, writer, http.StatusAccepted, map[string]any{"status": "accepted"})
		})
	}
	put("scale", func(row, body map[string]any) { row["count"] = body["count"] })
	put("instance-type", func(row, body map[string]any) { row["instance_type"] = body["instance_type"] })
	put("labels", func(row, body map[string]any) { row["labels"] = body["labels"] })
	put("taints", func(row, body map[string]any) { row["taints"] = body["taints"] })
	put("autoscaling", func(row, body map[string]any) {
		row["autoscaling_enabled"] = body["enabled"]
		row["min"] = body["min_count"]
		row["max"] = body["max_count"]
	})
	fake.handle("DELETE /api/v1/clusters/{lane}/{id}/node-groups/{group}", func(writer http.ResponseWriter, request *http.Request) {
		found := cluster(writer, request)
		if found == nil {
			return
		}
		if group(writer, request, found) == nil {
			return
		}
		accepted(request, "delete "+request.PathValue("group"))
		delete(found.groups, request.PathValue("group"))
		writeJSON(t, writer, http.StatusAccepted, map[string]any{"status": "accepted"})
	})
}

func (fake *lanesFake) registerManagedRoutes(t *testing.T) {
	managedCluster := func(writer http.ResponseWriter, request *http.Request) *lanesFakeCluster {
		found := fake.clusters[request.PathValue("id")]
		if found == nil || found.kind != request.PathValue("provider") {
			writeJSON(t, writer, http.StatusNotFound, map[string]any{"detail": "Managed cluster not found"})
			return nil
		}
		return found
	}
	poolRow := func(body map[string]any) map[string]any {
		row := map[string]any{
			"name": body["name"], "size": body["size"], "count": body["count"], "status": "running",
			"labels": map[string]any{}, "taints": []any{}, "autoscaling": nil,
			"zone": body["zone"], "root_volume_type": body["root_volume_type"], "root_volume_size_gb": body["root_volume_size_gb"],
			"externally_managed": false,
		}
		if labels, ok := body["labels"].(map[string]any); ok {
			row["labels"] = labels
		}
		if taints, ok := body["taints"].([]any); ok {
			row["taints"] = taints
		}
		if autoscaling, ok := body["autoscaling"].(map[string]any); ok {
			row["autoscaling"] = autoscaling
		}
		return row
	}

	fake.handle("POST /api/v1/clusters/managed/{provider}", func(writer http.ResponseWriter, request *http.Request) {
		var body map[string]any
		decodeJSON(t, request, &body)
		provider := request.PathValue("provider")
		fake.createBodies[provider] = append(fake.createBodies[provider], body)
		cluster := fake.addCluster(provider, fmt.Sprint(body["name"]), "provider_without_access")
		pools, _ := body["node_pools"].([]any)
		for _, rawPool := range pools {
			pool, _ := rawPool.(map[string]any)
			cluster.pools[fmt.Sprint(pool["name"])] = poolRow(pool)
		}
		writeJSON(t, writer, http.StatusOK, map[string]any{"cluster_id": cluster.id, "name": cluster.name, "provenance": map[string]any{}})
	})
	deleteCluster := func(writer http.ResponseWriter, request *http.Request) {
		found := managedCluster(writer, request)
		if found == nil {
			return
		}
		fake.managedDeletes = append(fake.managedDeletes, request.Method+" "+request.URL.Path+"?"+request.URL.RawQuery)
		provider := request.PathValue("provider")
		isExplicit := provider == "kapsule" || provider == "ankracloud_k8s"
		if isExplicit && request.Method == http.MethodDelete {
			t.Errorf("%s cluster sent to the bare DELETE, which only disconnects it", provider)
		}
		delete(fake.clusters, found.id)
		writeJSON(t, writer, http.StatusOK, map[string]any{"success": true, "cluster_id": found.id, "resources_marked": 1, "errors": []any{}})
	}
	fake.handle("DELETE /api/v1/clusters/managed/{provider}/{id}", deleteCluster)
	fake.handle("POST /api/v1/clusters/managed/{provider}/{id}/delete-provider-cluster", deleteCluster)

	fake.handle("GET /api/v1/clusters/managed/{provider}/{id}/node-pools", func(writer http.ResponseWriter, request *http.Request) {
		found := managedCluster(writer, request)
		if found == nil {
			return
		}
		names := make([]string, 0, len(found.pools))
		for name := range found.pools {
			names = append(names, name)
		}
		sort.Strings(names)
		rows := make([]any, 0, len(names))
		for _, name := range names {
			rows = append(rows, found.pools[name])
		}
		writeJSON(t, writer, http.StatusOK, map[string]any{"node_pools": rows})
	})
	fake.handle("POST /api/v1/clusters/managed/{provider}/{id}/node-pools", func(writer http.ResponseWriter, request *http.Request) {
		found := managedCluster(writer, request)
		if found == nil {
			return
		}
		var body map[string]any
		decodeJSON(t, request, &body)
		name := fmt.Sprint(body["name"])
		if _, exists := found.pools[name]; exists {
			writeJSON(t, writer, http.StatusConflict, map[string]any{"detail": "Node pool already exists"})
			return
		}
		fake.createBodies["pool "+name] = append(fake.createBodies["pool "+name], body)
		found.pools[name] = poolRow(body)
		writeJSON(t, writer, http.StatusOK, map[string]any{"cluster_id": found.id, "node_pool_name": name, "count": body["count"]})
	})
	fake.handle("PATCH /api/v1/clusters/managed/{provider}/{id}/node-pools/{pool}", func(writer http.ResponseWriter, request *http.Request) {
		found := managedCluster(writer, request)
		if found == nil {
			return
		}
		row := found.pools[request.PathValue("pool")]
		if row == nil {
			writeJSON(t, writer, http.StatusNotFound, map[string]any{"detail": "Cluster or node pool not found"})
			return
		}
		var body map[string]any
		decodeJSON(t, request, &body)
		if len(body) == 0 {
			writeJSON(t, writer, http.StatusUnprocessableEntity, map[string]any{"detail": "At least one managed pool update field is required"})
			return
		}
		body["pool"] = request.PathValue("pool")
		fake.poolPatches = append(fake.poolPatches, body)
		if count, ok := body["count"]; ok {
			row["count"] = count
		}
		if enabled, ok := body["autoscaling_enabled"]; ok {
			row["autoscaling"] = map[string]any{"enabled": enabled, "min_count": body["autoscaling_min"], "max_count": body["autoscaling_max"]}
		}
		writeJSON(t, writer, http.StatusOK, map[string]any{"cluster_id": found.id, "node_pool_name": request.PathValue("pool")})
	})
	fake.handle("DELETE /api/v1/clusters/managed/{provider}/{id}/node-pools/{pool}", func(writer http.ResponseWriter, request *http.Request) {
		found := managedCluster(writer, request)
		if found == nil {
			return
		}
		if found.pools[request.PathValue("pool")] == nil {
			writeJSON(t, writer, http.StatusNotFound, map[string]any{"detail": "Cluster or node pool not found"})
			return
		}
		delete(found.pools, request.PathValue("pool"))
		writeJSON(t, writer, http.StatusOK, map[string]any{"cluster_id": found.id, "node_pool_name": request.PathValue("pool")})
	})
}

// lanesFakePlaygroundPlans are the orderable sizes of the fake.
var lanesFakePlaygroundPlans = []string{"trial", "starter", "pro"}

func (fake *lanesFake) registerPlaygroundRoutes(t *testing.T) {
	status := func(cluster *lanesFakeCluster) map[string]any {
		return map[string]any{
			"cluster_id": cluster.id, "phase": cluster.phase, "status_message": nil,
			"expires_at": "2026-10-24T00:00:00Z", "starter_url": nil,
			"plan": map[string]any{"id": cluster.plan, "display_name": cluster.plan, "ordered_at": cluster.orderedAt},
		}
	}
	known := func(plan string) bool {
		for _, candidate := range lanesFakePlaygroundPlans {
			if candidate == plan {
				return true
			}
		}
		return false
	}
	playground := func(writer http.ResponseWriter, request *http.Request) *lanesFakeCluster {
		found := fake.clusters[request.PathValue("id")]
		if found == nil || found.kind != "playground" {
			writeJSON(t, writer, http.StatusNotFound, map[string]any{"detail": "Playground not found"})
			return nil
		}
		return found
	}

	fake.handle("GET /api/v1/org/clusters/playground/plans", func(writer http.ResponseWriter, _ *http.Request) {
		plans := []any{}
		for index, plan := range lanesFakePlaygroundPlans {
			plans = append(plans, map[string]any{
				"id": plan, "display_name": plan, "summary": plan + " size", "vcpus": index + 1, "memory_gb": float64(index) + 0.5,
				"storage_gb": 10 * (index + 1), "max_pods": 50, "price_monthly_cents": 1000 * index, "currency": "EUR", "available": true,
			})
		}
		writeJSON(t, writer, http.StatusOK, map[string]any{
			"plans": plans, "default_plan_id": "trial", "currency": "EUR",
			"organisation_has_paid_plan": true, "organisation_has_payment_card": true,
			"quota": map[string]any{"limit_mib": 8192, "used_mib": 512},
		})
	})
	fake.handle("POST /api/v1/org/clusters/playground", func(writer http.ResponseWriter, request *http.Request) {
		var body map[string]any
		decodeJSON(t, request, &body)
		plan := fmt.Sprint(body["plan"])
		if !known(plan) {
			writeJSON(t, writer, http.StatusBadRequest, map[string]any{"detail": "Unknown playground plan."})
			return
		}
		fake.playgroundCalls = append(fake.playgroundCalls, "create "+plan)
		cluster := fake.addCluster("playground", "playground", "running")
		cluster.phase = "ready"
		cluster.plan = plan
		if plan != "trial" {
			cluster.orderedAt = "2026-10-17T00:00:00Z"
		}
		writeJSON(t, writer, http.StatusOK, map[string]any{"cluster_id": cluster.id, "success": true})
	})
	fake.handle("GET /api/v1/org/clusters/playground/{id}/status", func(writer http.ResponseWriter, request *http.Request) {
		if found := playground(writer, request); found != nil {
			writeJSON(t, writer, http.StatusOK, status(found))
		}
	})
	fake.handle("POST /api/v1/org/clusters/playground/{id}/resize", func(writer http.ResponseWriter, request *http.Request) {
		found := playground(writer, request)
		if found == nil {
			return
		}
		var body map[string]any
		decodeJSON(t, request, &body)
		plan := fmt.Sprint(body["plan"])
		switch {
		case !known(plan):
			writeJSON(t, writer, http.StatusBadRequest, map[string]any{"detail": "Unknown playground plan."})
		case plan == "trial" || found.orderedAt == nil:
			writeJSON(t, writer, http.StatusBadRequest, map[string]any{"detail": "The free trial cannot be resized. Order a paid size instead."})
		case plan == found.plan:
			writeJSON(t, writer, http.StatusBadRequest, map[string]any{"detail": "The playground is already this size."})
		default:
			fake.playgroundCalls = append(fake.playgroundCalls, "resize "+plan)
			found.plan = plan
			writeJSON(t, writer, http.StatusOK, map[string]any{"cluster_id": found.id, "plan": map[string]any{"id": plan}})
		}
	})
	fake.handle("DELETE /api/v1/org/clusters/playground/{id}", func(writer http.ResponseWriter, request *http.Request) {
		found := playground(writer, request)
		if found == nil {
			return
		}
		fake.playgroundCalls = append(fake.playgroundCalls, "destroy")
		found.phase = "deprovisioning"
		writeJSON(t, writer, http.StatusOK, map[string]any{"cluster_id": found.id, "phase": "deprovisioning"})
	})
}
