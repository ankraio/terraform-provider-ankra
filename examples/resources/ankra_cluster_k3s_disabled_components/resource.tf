resource "ankra_cluster_k3s_disabled_components" "prod" {
  cluster_id = "0b6f1d2e-1111-4222-8333-944445555666"
  components = ["traefik", "servicelb"]
}
