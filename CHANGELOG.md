## 0.2.0 (Unreleased)

BREAKING CHANGES:

* resource/ankra_*_cluster: `distribution` now defaults to `kubeadm` and `cni` to `cilium` (the platform default; kubeadm clusters only run Cilium). A configuration that relied on the implicit `k3s`/`flannel` defaults must set them explicitly - both attributes force replacement, so add them before upgrading the provider to keep existing clusters in place.
* provider: Migrated from `terraform-plugin-sdk/v2` to `terraform-plugin-framework` (Terraform protocol v6).

FEATURES:

* **New Resource:** `ankra_hetzner_cluster` — provisions a Hetzner-backed cluster (`POST /clusters/hetzner`) and deprovisions it on destroy (`DELETE /clusters/hetzner/{id}?force=true`). All arguments force replacement, since provisioning parameters are immutable after creation.
* provider: Add provider-level `token` and `base_url` configuration with `ANKRA_TOKEN` and `ANKRA_BASE_URL` environment variable fallbacks.
* resource/ankra_cluster: Support `terraform import` by cluster id.
* resource/ankra_cluster: `Read` now detects out-of-band deletion and removes the resource from state.
* data-source/ankra_clusters: The `ankra_clusters` data source is now registered and usable.

IMPROVEMENTS:

* resource/ankra_cluster: API calls now surface the HTTP status and response body on failure.
* provider: All HTTP traffic goes through a shared, typed API client with a versioned `User-Agent`.

DEPRECATIONS:

* resource/ankra_cluster, data-source/ankra_clusters: The per-resource `ankra_token` attribute is deprecated in favour of provider-level `token` / `ANKRA_TOKEN`. It still works and overrides the provider token when set.

## 0.1.0

FEATURES:
