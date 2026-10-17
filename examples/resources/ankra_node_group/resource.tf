resource "ankra_node_group" "gpu" {
  cluster_id    = ankra_hetzner_cluster.example.cluster_id
  name          = "gpu"
  instance_type = "cx42"
  node_count    = 2

  labels = {
    accelerator = "gpu"
  }
  taints = [{
    key    = "gpu"
    effect = "NoSchedule"
  }]

  # Building or draining nodes takes minutes; each change is waited for.
  timeouts {
    create = "30m"
    update = "30m"
  }
}
