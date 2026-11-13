resource "ankra_pipeline_policy" "ceiling" {
  allowed_network_tiers  = ["egress-https"]
  allowed_node_selectors = { "ankra.io/node-group" = ["ci"] }
  max_cpu                = "4"
  max_memory             = "8Gi"
  webhook_allowed_hosts  = ["hooks.slack.com"]
}
