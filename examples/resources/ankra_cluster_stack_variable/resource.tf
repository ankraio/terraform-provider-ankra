resource "ankra_cluster_stack_variable" "replicas" {
  cluster_id = ankra_cluster.production.id
  stack_name = "platform"
  name       = "replicas"
  value      = "3"
}
