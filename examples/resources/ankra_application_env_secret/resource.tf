# Terraform 1.11 or later: the value is write-only and never stored in state.
# Bump value_wo_version to write a new value.
resource "ankra_application_env_secret" "database_url" {
  application_id   = ankra_application.web.id
  key              = "DATABASE_URL"
  value_wo         = var.database_url
  value_wo_version = 1
}

# Older Terraform: value is stored in state in plain text, marked sensitive.
resource "ankra_application_env_secret" "api_token" {
  application_id = ankra_application.web.id
  key            = "API_TOKEN"
  value          = var.api_token
}

variable "database_url" {
  type      = string
  sensitive = true
  ephemeral = true
}

variable "api_token" {
  type      = string
  sensitive = true
}
