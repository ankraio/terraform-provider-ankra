resource "ankra_ci_settings" "this" {
  ci_cluster_id             = "1e2d3c4b-0000-0000-0000-000000000000"
  ci_build_fallback         = "none"
  ci_max_parallel_runs      = 8
  ci_allowed_image_prefixes = ["ghcr.io/acme/", "docker.io/library/"]
  ci_image_gate             = "app"
}
