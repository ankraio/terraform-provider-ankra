data "ankra_slack_channels" "workspace" {}

locals {
  slack_alerts_channel = one([for channel in data.ankra_slack_channels.workspace.channels : channel if channel.name == "alerts"])
}

resource "ankra_alert_destination" "slack_alerts" {
  name         = "slack-alerts"
  channel_id   = local.slack_alerts_channel.id
  channel_name = local.slack_alerts_channel.name
}
