data "ankra_cluster_dns_zones" "all" {}

output "cluster_domains" {
  value = { for zone in data.ankra_cluster_dns_zones.all.zones : zone.cluster_name => zone.fqdn }
}

output "failing_cluster_domains" {
  value = [for zone in data.ankra_cluster_dns_zones.all.zones : zone.cluster_name if zone.last_error != null]
}
