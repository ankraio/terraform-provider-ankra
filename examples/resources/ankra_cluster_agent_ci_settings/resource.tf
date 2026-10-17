resource "ankra_cluster_agent_ci_settings" "ci" {
  cluster_id            = "0b6f1d2e-1111-4222-8333-944445555666"
  ci_worker_count       = 8
  ci_storage_class      = "fast-ssd"
  ci_run_reservation    = "on"
  ci_image_prepull      = "on"
  ci_max_admitting_runs = 4
  ci_reservation_sizing = "measured"

  # Run every pipeline pod on the "ci" node group. The platform adds the
  # group's node selector and tolerations.
  ci_placement = {
    node_group = "ci"
    mode       = "required"
  }
}
