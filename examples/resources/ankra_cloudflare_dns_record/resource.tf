data "ankra_cloudflare_domains" "main" {
  credential_name = ankra_cloudflare_credential.main.name
}

locals {
  example_zone_id = one([for domain in data.ankra_cloudflare_domains.main.domains : domain.id if domain.name == "example.com"])
}

resource "ankra_cloudflare_dns_record" "www" {
  credential_name = ankra_cloudflare_credential.main.name
  zone_id         = local.example_zone_id
  name            = "www"
  record_type     = "CNAME"
  content         = "ingress.k8s.example.com"
  proxied         = true
}
