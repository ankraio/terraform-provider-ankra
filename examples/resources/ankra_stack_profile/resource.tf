resource "ankra_stack_profile" "platform" {
  name              = "platform-base"
  description       = "Ingress, certificates and monitoring"
  category          = "platform"
  tags              = ["ingress", "observability"]
  source_cluster_id = "3f0c6c1e-8a4b-4f39-9d55-1a2b3c4d5e6f"
  stack_name        = "platform"
}
