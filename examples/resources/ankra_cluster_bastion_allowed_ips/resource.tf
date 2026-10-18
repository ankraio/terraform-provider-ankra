resource "ankra_cluster_bastion_allowed_ips" "prod" {
  cluster_id = ankra_hetzner_cluster.prod.id
  cidrs = [
    "203.0.113.0/24",
    "198.51.100.7/32",
  ]
}
