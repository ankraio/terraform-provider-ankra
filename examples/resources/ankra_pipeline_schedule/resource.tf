resource "ankra_pipeline_schedule" "nightly" {
  application_id = ankra_application.web.id
  cron           = "0 3 * * *"
  timezone       = "Europe/Oslo"

  inputs = {
    suite = "full"
  }
}
