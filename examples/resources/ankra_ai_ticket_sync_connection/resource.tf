# Terraform 1.11 or later: the credential is write-only and never stored
# in state. Bump credential_wo_version to send a new one.
resource "ankra_ai_ticket_sync_connection" "linear" {
  name                  = "ops-board"
  provider_name         = "linear"
  config                = { team_key = "OPS" }
  credential_wo         = { api_key = var.linear_api_key }
  credential_wo_version = 1
  sync_min_priority     = "high"
}

variable "linear_api_key" {
  type      = string
  sensitive = true
  ephemeral = true
}
