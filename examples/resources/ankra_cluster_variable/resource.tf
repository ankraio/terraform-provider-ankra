resource "ankra_cluster_variable" "ingress_domain" {
  cluster_id  = ankra_cluster.production.id
  name        = "ingress_domain"
  value       = "apps.example.com"
  description = "Base domain for ingress hostnames on this cluster"
}
