resource "ankra_cluster_dns_zone" "prod" {
  cluster_id = "0b6f1d2e-1111-4222-8333-944445555666"
}

output "cluster_domain" {
  value = ankra_cluster_dns_zone.prod.fqdn
}
