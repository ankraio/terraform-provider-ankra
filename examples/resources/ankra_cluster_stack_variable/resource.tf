# Sets one key of the "platform" stack's variables; the stack's other keys
# are left as they are. The stack itself stays owned by ankra_cluster or the
# GitOps repository.
resource "ankra_cluster_stack_variable" "ingress_domain" {
  cluster_id = ankra_cluster.production.id
  stack_name = "platform"
  name       = "ingress_domain"
  value      = "apps.example.com"
}
