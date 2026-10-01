## 0.2.0 (2026-10-01)

0.2.0 is the first release since 0.1.6. It moves the provider to terraform-plugin-framework, makes `ankra_cluster` work against the current import API again (0.1.6 cannot create or update a cluster), and adds resources for the clouds Ankra provisions on. Read the upgrade notes before running `terraform init -upgrade`.

UPGRADE NOTES:

* **Terraform 1.0 or newer.** The provider now serves Terraform protocol v6. CI tests Terraform 1.6 through 1.12.
* **Provisioned clusters default to kubeadm and Cilium.** On the `ankra_*_cluster` resources, `distribution` now defaults to `kubeadm` and `cni` to `cilium`. Both force replacement. If a configuration relied on the old implicit `k3s`/`flannel`, set `distribution = "k3s"` and `cni = "flannel"` explicitly *before* upgrading, or the next plan replaces the cluster.
* **`ankra_cluster` addons.** Set `registry_url` (and ideally `registry_name`, the name of the Helm registry connected in Ankra). `repository_url` still works as a deprecated alias. `configuration` takes YAML or base64-encoded YAML. `job_configuration` must be a JSON object, for example `jsonencode({ create_job_timeout = 600 })`.
* **`ankra_cluster` parents.** Write `"manifest:<name>"` or `"addon:<name>"`. A bare name still works when exactly one manifest or addon of that name is declared in the resource.
* **Tokens.** Configure `token` on the provider block or set `ANKRA_TOKEN`. The per-resource `ankra_token` still works but is deprecated. Changing it is now an in-place update (0.1.x replaced the cluster).
* **Rotate agent tokens printed by 0.1.x.** `helm_command` embeds a live cluster agent token and is now sensitive. If earlier plans or CI logs printed it, rotate those tokens.

BREAKING CHANGES:

* provider: Migrated from `terraform-plugin-sdk/v2` to `terraform-plugin-framework` (Terraform protocol v6), so Terraform 1.0 or newer is required. ([#14](https://github.com/ankraio/terraform-provider-ankra/pull/14))
* resource/ankra_*_cluster: `distribution` now defaults to `kubeadm` and `cni` to `cilium` (the platform default; kubeadm clusters only run Cilium). A configuration that relied on the implicit `k3s`/`flannel` defaults must set them explicitly - both attributes force replacement, so add them before upgrading the provider to keep existing clusters in place. ([#17](https://github.com/ankraio/terraform-provider-ankra/pull/17))
* resource/ankra_cluster: A bare-string entry in `parents` must name a manifest or addon declared in the resource's stacks (its kind is inferred); reference a member defined elsewhere as `"manifest:<name>"` or `"addon:<name>"`. `job_configuration` must be a JSON object of integer timeouts (`jsonencode({ create_job_timeout = 600 })`). Both are checked at plan time. ([#18](https://github.com/ankraio/terraform-provider-ankra/pull/18))

FEATURES:

* **New Resource:** `ankra_hetzner_cluster` - provisions a Hetzner-backed cluster and deprovisions it on destroy. Provisioning arguments force replacement, since they are immutable after creation. ([#14](https://github.com/ankraio/terraform-provider-ankra/pull/14))
* **New Resources:** `ankra_digitalocean_cluster`, `ankra_ovh_cluster`, `ankra_scaleway_cluster` and `ankra_upcloud_cluster` - provision on each cloud lane and deprovision on destroy. Each keeps its cloud's own words for capacity and placement (`size` / `plan` / `flavor_id` / `server_type`, `region` / `zone` / `location`). ([#16](https://github.com/ankraio/terraform-provider-ankra/pull/16))
* provider: Add provider-level `token` and `base_url` configuration with `ANKRA_TOKEN` and `ANKRA_BASE_URL` environment variable fallbacks. The token is validated once at configure time instead of producing a raw 401 later. ([#14](https://github.com/ankraio/terraform-provider-ankra/pull/14), [#15](https://github.com/ankraio/terraform-provider-ankra/pull/15))
* resource/ankra_*_cluster: Create waits until the platform reports the cluster `running`, bounded by `timeouts { create }`. `wait_for_ready = false` opts out. The id is saved to state before the wait, so a timeout never loses the cluster. ([#16](https://github.com/ankraio/terraform-provider-ankra/pull/16))
* resource/ankra_*_cluster: Add `force_destroy` (default `true`, the previous behaviour); set it to `false` to take the platform's guarded delete path. Changing it never replaces the cluster. ([#15](https://github.com/ankraio/terraform-provider-ankra/pull/15), [#16](https://github.com/ankraio/terraform-provider-ankra/pull/16))
* All cluster resources expose the platform-reported `state` and `kind`, refreshed on every read, so a change made outside Terraform shows up in the plan. ([#16](https://github.com/ankraio/terraform-provider-ankra/pull/16))
* resource/ankra_cluster: Support `terraform import` by cluster id, and remove the resource from state when the cluster was deleted out of band. ([#14](https://github.com/ankraio/terraform-provider-ankra/pull/14))
* resource/ankra_cluster: Add `wait_for_online` (default `false`) to wait for the cluster agent to check in. ([#16](https://github.com/ankraio/terraform-provider-ankra/pull/16))
* resource/ankra_cluster: Addons take `registry_url`, `registry_name` and `registry_credential_name`. `registry_name` defaults to a name derived from the URL, which the platform accepts when a registry with that URL is connected. ([#18](https://github.com/ankraio/terraform-provider-ankra/pull/18))
* resource/ankra_cluster: `timeouts` gains `update`; `create` now bounds the whole create, including the import call. ([#18](https://github.com/ankraio/terraform-provider-ankra/pull/18))
* data-source/ankra_clusters: The `ankra_clusters` data source is now registered and usable. ([#14](https://github.com/ankraio/terraform-provider-ankra/pull/14))

BUG FIXES:

* resource/ankra_cluster: Create and update work against the current import API again. The provider now calls `POST /api/v1/clusters/import?wait=true`; without `wait` the platform answers `202 {"status":"accepted"}` and imports in the background, so 0.1.x failed with "missing cluster_id" while the cluster was registered outside Terraform state. A response without a cluster id (a queued import) is resolved by cluster name, and a create that fails after the platform registered the cluster records it in state (tainted) instead of orphaning it. ([#18](https://github.com/ankraio/terraform-provider-ankra/pull/18))
* resource/ankra_cluster: Stack payloads use the shapes the platform requires: `parents` are sent as `{name, kind}` objects, addons send `registry_name` and `registry_url` (0.1.x sent only `repository_url`), `job_configuration` is sent as an object and `configuration` as `{values_base64}` (plain YAML is encoded for you). ([#18](https://github.com/ankraio/terraform-provider-ankra/pull/18))
* resource/ankra_cluster: A rejected specification is reported per member and field, from the platform's `errors` list, instead of as a missing id; the platform's advisory `warnings` are surfaced as Terraform warnings. ([#18](https://github.com/ankraio/terraform-provider-ankra/pull/18))
* resource/ankra_cluster: An update no longer overwrites `helm_command` with the empty command the platform returns on a re-import. ([#18](https://github.com/ankraio/terraform-provider-ankra/pull/18))
* resource/ankra_cluster: Changing `ankra_token` (e.g. rotating it) is an in-place update that makes no API call; 0.1.x forced a destroy and re-create of the cluster. ([#18](https://github.com/ankraio/terraform-provider-ankra/pull/18))
* client: The cluster listing is read from the contract's `result` key and paged to the end. Before this, every cluster read as deleted and Terraform planned to rebuild healthy clusters, and the `ankra_clusters` data source returned an empty list. ([#15](https://github.com/ankraio/terraform-provider-ankra/pull/15))
* client: Idempotent requests retry on 429 and gateway errors with backoff and honour the platform's `retry_after` hint. A create is never retried on a server error, so a retry cannot provision a second cluster. ([#15](https://github.com/ankraio/terraform-provider-ankra/pull/15))
* resource/ankra_cluster: `helm_command` and addon `configuration` / `job_configuration` are marked sensitive, so the agent token and Helm values no longer appear in plan output. ([#15](https://github.com/ankraio/terraform-provider-ankra/pull/15))
* resource/ankra_*_cluster: A create that times out mid-request reports the last state the cluster reached instead of a bare "context deadline exceeded". ([#16](https://github.com/ankraio/terraform-provider-ankra/pull/16))

IMPROVEMENTS:

* resource/ankra_cluster: API calls now surface the HTTP status and response body on failure.
* provider: All HTTP traffic goes through a shared, typed API client with a versioned `User-Agent`.

DEPRECATIONS:

* resource/ankra_cluster, data-source/ankra_clusters: The per-resource `ankra_token` attribute is deprecated in favour of provider-level `token` / `ANKRA_TOKEN`. It still works and overrides the provider token when set.
* resource/ankra_cluster: Addon `repository_url` is deprecated in favour of `registry_url` (it is still accepted as an alias), and `configuration_type` is ignored.

KNOWN LIMITS:

* resource/ankra_cluster: Changing `github_repository` or `github_branch` on an existing cluster fails with a 409: the platform refuses a GitOps repoint unless it is acknowledged with `allow_repoint`, which the provider does not expose yet.
* resource/ankra_cluster: `helm_command` is issued only when a cluster is first registered. Adopting an existing cluster, or a queued import, leaves it empty.
* resource/ankra_*_cluster: Out-of-band resizes are not detected: the cluster listing carries no sizing.

## 0.1.0

FEATURES:
