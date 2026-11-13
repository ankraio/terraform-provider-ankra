resource "ankra_service_credential" "billing_database" {
  name             = "billing-db"
  value_wo         = var.billing_database_url
  value_wo_version = 1
}

variable "billing_database_url" {
  type      = string
  sensitive = true
  ephemeral = true
}
