data "ankra_teams_channels" "tenants" {}

locals {
  teams_oncall_channel = one([for channel in data.ankra_teams_channels.tenants.channels : channel if channel.team_name == "Platform" && channel.name == "On-call"])
}

resource "ankra_alert_destination" "teams_oncall" {
  name             = "teams-oncall"
  integration_type = "teams"
  channel_id       = local.teams_oncall_channel.id
  channel_name     = local.teams_oncall_channel.name
  teams_tenant_id  = local.teams_oncall_channel.tenant_id
}
