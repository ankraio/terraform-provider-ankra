resource "ankra_ai_environment" "this" {
  staging_cluster_id   = "1e2d3c4b-0000-0000-0000-000000000000"
  workspace_ttl_hours  = 8
  demo_ttl_hours       = 48
  default_gateway_mode = "ask"
  demo_base_domain     = "previews.example.com"
}
