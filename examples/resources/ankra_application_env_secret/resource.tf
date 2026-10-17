resource "ankra_application_env_secret" "database_url" {
  application_id = ankra_application.web.id
  key            = "DATABASE_URL"
  value          = var.database_url
}

variable "database_url" {
  type      = string
  sensitive = true
}
