resource "ankra_cluster_agent_ci_settings" "ci" {
  cluster_id         = "0b6f1d2e-1111-4222-8333-944445555666"
  ci_worker_count    = 8
  ci_storage_class   = "fast-ssd"
  ci_run_reservation = "on"
  ci_image_prepull   = "on"
}
