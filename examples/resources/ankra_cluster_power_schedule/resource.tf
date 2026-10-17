data "ankra_cluster" "staging" {
  name = "staging"
}

resource "ankra_cluster_power_schedule" "evening_stop" {
  cluster_id      = data.ankra_cluster.staging.id
  action          = "stop"
  stop_mode       = "delete_resources"
  schedule_kind   = "cron"
  cron_expression = "0 19 * * 1-5"
  timezone        = "Europe/Stockholm"
}

resource "ankra_cluster_power_schedule" "morning_start" {
  cluster_id      = data.ankra_cluster.staging.id
  action          = "start"
  schedule_kind   = "cron"
  cron_expression = "0 7 * * 1-5"
  timezone        = "Europe/Stockholm"
}
