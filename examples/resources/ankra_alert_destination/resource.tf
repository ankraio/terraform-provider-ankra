resource "ankra_alert_destination" "pagerduty" {
  name             = "on-call"
  integration_type = "pagerduty"
  url              = var.pagerduty_events_url
  description      = "Pages the on-call engineer"
}

resource "ankra_alert_destination" "platform_channel" {
  name             = "platform-alerts"
  integration_type = "slack"
  channel_id       = "C0123456789"
  channel_name     = "platform-alerts"
}

variable "pagerduty_events_url" {
  type      = string
  sensitive = true
}
