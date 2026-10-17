resource "ankra_dns_credential" "webhook" {
  name                 = "corporate-dns"
  webhook_provider_url = var.dns_webhook_url
}

variable "dns_webhook_url" {
  type      = string
  sensitive = true
}
