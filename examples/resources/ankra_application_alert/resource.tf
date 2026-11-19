# Pages on-call when the application's 95th percentile response time on
# production stays above 800 ms for 5 minutes in a row.
resource "ankra_application_alert" "slow_responses" {
  application_id = ankra_application.web.id
  environment_id = var.production_environment_id
  name           = "Slow responses"
  severity       = "critical"

  condition = {
    metric       = "p95_ms"
    threshold    = 800
    hold_minutes = 5
  }

  send_to = [ankra_alert_destination.pagerduty.id]
}

# Without severity the platform stores warning; leaving it out later keeps
# whatever severity the alert has.
resource "ankra_application_alert" "failed_requests" {
  application_id = ankra_application.web.id
  environment_id = var.production_environment_id
  name           = "Failed requests"

  condition = {
    metric       = "failed_share_pct"
    threshold    = 5
    hold_minutes = 10
  }

  send_to = [ankra_alert_destination.platform_channel.id]
}

# `ankra application environments <application>` lists the ids.
variable "production_environment_id" {
  type = string
}
