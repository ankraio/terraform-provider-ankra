## 0.2.0 (Unreleased)

BREAKING CHANGES:

* resource/ankra_*_cluster: `distribution` now defaults to `kubeadm` and `cni` to `cilium` (the platform default; kubeadm clusters only run Cilium). A configuration that relied on the implicit `k3s`/`flannel` defaults must set them explicitly - both attributes force replacement, so add them before upgrading the provider to keep existing clusters in place.
* provider: Migrated from `terraform-plugin-sdk/v2` to `terraform-plugin-framework` (Terraform protocol v6).
* resource/ankra_cluster: A bare-string entry in `parents` must name a manifest or addon declared in the resource's stacks (its kind is inferred); reference a member defined elsewhere as `"manifest:<name>"` or `"addon:<name>"`. `job_configuration` must be a JSON object of integer timeouts (`jsonencode({ create_job_timeout = 600 })`). Both are checked at plan time.

FEATURES:

* **New Resource:** `ankra_hetzner_cluster` — provisions a Hetzner-backed cluster (`POST /clusters/hetzner`) and deprovisions it on destroy (`DELETE /clusters/hetzner/{id}?force=true`). All arguments force replacement, since provisioning parameters are immutable after creation.
* provider: Add provider-level `token` and `base_url` configuration with `ANKRA_TOKEN` and `ANKRA_BASE_URL` environment variable fallbacks.
* resource/ankra_cluster: Support `terraform import` by cluster id.
* resource/ankra_cluster: `Read` now detects out-of-band deletion and removes the resource from state.
* data-source/ankra_clusters: The `ankra_clusters` data source is now registered and usable.
* resource/ankra_cluster: Addons take `registry_url`, `registry_name` and `registry_credential_name`. `registry_name` defaults to a name derived from the URL, which the platform accepts when a registry with that URL is connected.
* resource/ankra_cluster: `timeouts` gains `update`; `create` now bounds the whole create, including the import call.

BUG FIXES:

* resource/ankra_cluster: Create and update work against the current import API again. The provider now calls `POST /api/v1/clusters/import?wait=true`; without `wait` the platform answers `202 {"status":"accepted"}` and imports in the background, so 0.1.x failed with "missing cluster_id" while the cluster was registered outside Terraform state. A response without a cluster id (a queued import) is resolved by cluster name, and a create that fails after the platform registered the cluster records it in state (tainted) instead of orphaning it.
* resource/ankra_cluster: Stack payloads use the shapes the platform requires: `parents` are sent as `{name, kind}` objects, addons send `registry_name` and `registry_url` (0.1.x sent only `repository_url`), `job_configuration` is sent as an object and `configuration` as `{values_base64}` (plain YAML is encoded for you).
* resource/ankra_cluster: A rejected specification is reported per member and field, from the platform's `errors` list, instead of as a missing id; the platform's advisory `warnings` are surfaced as Terraform warnings.
* resource/ankra_cluster: An update no longer overwrites `helm_command` with the empty command the platform returns on a re-import.
* resource/ankra_cluster: Changing `ankra_token` (e.g. rotating it) is an in-place update that makes no API call; 0.1.x forced a destroy and re-create of the cluster.

IMPROVEMENTS:

* resource/ankra_cluster: API calls now surface the HTTP status and response body on failure.
* provider: All HTTP traffic goes through a shared, typed API client with a versioned `User-Agent`.

DEPRECATIONS:

* resource/ankra_cluster, data-source/ankra_clusters: The per-resource `ankra_token` attribute is deprecated in favour of provider-level `token` / `ANKRA_TOKEN`. It still works and overrides the provider token when set.
* resource/ankra_cluster: Addon `repository_url` is deprecated in favour of `registry_url` (it is still accepted as an alias), and `configuration_type` is ignored.

## 0.1.0

FEATURES:
