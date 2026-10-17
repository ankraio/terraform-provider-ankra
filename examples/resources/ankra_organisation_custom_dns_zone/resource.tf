resource "ankra_dns_credential" "webhook" {
  name                 = "corporate-dns"
  webhook_provider_url = var.dns_webhook_url
}

resource "ankra_organisation_custom_dns_zone" "corporate" {
  zone            = "apps.example.com"
  credential_name = ankra_dns_credential.webhook.name
}

variable "dns_webhook_url" {
  type      = string
  sensitive = true
}
