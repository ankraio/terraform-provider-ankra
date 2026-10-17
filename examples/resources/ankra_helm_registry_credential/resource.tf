resource "ankra_helm_registry_credential" "acme" {
  name     = "acme-charts"
  username = "robot$acme"
  password = var.acme_registry_token
}

variable "acme_registry_token" {
  type      = string
  sensitive = true
}
