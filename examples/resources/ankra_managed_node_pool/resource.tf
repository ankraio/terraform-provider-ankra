resource "ankra_managed_node_pool" "batch" {
  provider_name = "aks"
  cluster_id    = ankra_managed_cluster.example.cluster_id
  name          = "batch"
  size          = "Standard_D8s_v5"
  node_count    = 2

  labels = {
    workload = "batch"
  }
  taints = [{
    key    = "batch"
    effect = "NoSchedule"
  }]
}
