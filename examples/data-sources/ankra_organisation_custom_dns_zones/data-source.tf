data "ankra_organisation_custom_dns_zones" "all" {}

output "zones" {
  value = data.ankra_organisation_custom_dns_zones.all.zones[*].zone
}
