resource "ankra_application_autonomy" "web" {
  application_id = ankra_application.web.id
  level          = "together"
  areas          = { keep_it_up = "ankra_runs_it" }

  limits = {
    production_downtime_seconds = 0
    may_change_data             = false
  }

  auto_fix_builds               = true
  setup_pull_request_auto_merge = "organisation"
}
