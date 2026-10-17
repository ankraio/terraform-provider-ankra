resource "ankra_cost_settings" "pricing" {
  currency                        = "eur"
  effective_discount_pct          = 12.5
  include_network_egress_estimate = true
  analysed_cluster_limit          = 20
}
