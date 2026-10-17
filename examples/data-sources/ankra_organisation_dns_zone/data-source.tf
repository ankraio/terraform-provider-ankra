data "ankra_organisation_dns_zone" "this" {}

output "zone" {
  value = data.ankra_organisation_dns_zone.this.fqdn
}
