resource "ankra_upcloud_cluster_settings" "prod" {
  cluster_id         = "0b6f1d2e-1111-4222-8333-944445555666"
  load_balancer_plan = "production-small"
  zones              = ["fi-hel1", "fi-hel2", "de-fra1"]
}
