data "ankra_cloud_dns_zones" "all" {}

output "zone_names" {
  value = [for zone in data.ankra_cloud_dns_zones.all.zones : zone.name]
}
