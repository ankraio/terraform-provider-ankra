data "ankra_cloudflare_domains" "main" {
  credential_name = "cloudflare-main"
}

output "zone_ids" {
  value = { for domain in data.ankra_cloudflare_domains.main.domains : domain.name => domain.id }
}
