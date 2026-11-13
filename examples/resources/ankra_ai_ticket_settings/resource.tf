resource "ankra_ai_ticket_settings" "desk" {
  slack_channel_id             = "C0123456789"
  require_agent_review         = true
  auto_file_insight_severities = ["critical", "high"]
}
