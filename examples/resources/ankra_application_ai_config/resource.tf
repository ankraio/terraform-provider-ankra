resource "ankra_application_ai_config" "web" {
  application_id = ankra_application.web.id

  lanes = {
    pull_request = {
      enabled          = true
      autonomy         = "propose_fix"
      branches         = ["main"]
      max_runs_per_day = 50
    }
    previews = {
      enabled = true
    }
  }
}
