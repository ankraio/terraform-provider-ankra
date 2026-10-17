resource "ankra_azure_credential" "main" {
  name            = "azure-main"
  subscription_id = "00000000-0000-0000-0000-000000000000"
  tenant_id       = "00000000-0000-0000-0000-000000000000"
  client_id       = "00000000-0000-0000-0000-000000000000"
  client_secret   = var.azure_client_secret
}

variable "azure_client_secret" {
  type      = string
  sensitive = true
}
