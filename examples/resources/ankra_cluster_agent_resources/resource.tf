resource "ankra_cluster_agent_resources" "agent" {
  cluster_id     = "0b6f1d2e-1111-4222-8333-944445555666"
  memory_request = "256Mi"
  memory_limit   = "1Gi"
  cpu_request    = "100m"
}
