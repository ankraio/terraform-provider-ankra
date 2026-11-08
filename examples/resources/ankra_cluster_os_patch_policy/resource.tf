resource "ankra_cluster_os_patch_policy" "prod" {
  cluster_id            = "0b6f1d2e-1111-4222-8333-944445555666"
  enabled               = true
  window_days           = ["sat", "sun"]
  window_start_hour_utc = 1
  window_duration_hours = 4
  cadence_days          = 7
}
