data "ankra_cluster_stack_variables" "platform" {
  cluster_id = ankra_cluster.production.id
  stack_name = "platform"
}
