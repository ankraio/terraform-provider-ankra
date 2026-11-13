resource "ankra_ai_ticket_project_channels" "payments" {
  project_id       = ankra_project.payments.id
  slack_channel_id = "C0987654321"
}
