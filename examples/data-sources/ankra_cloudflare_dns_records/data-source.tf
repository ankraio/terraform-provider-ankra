data "ankra_cloudflare_dns_records" "mail" {
  zone_id         = "023e105f4ecef8ad9ca31a8372d0c353"
  credential_name = "cloudflare-main"
  record_type     = "MX"
}

output "mail_exchangers" {
  value = [for record in data.ankra_cloudflare_dns_records.mail.records : "${record.priority} ${record.content}"]
}
