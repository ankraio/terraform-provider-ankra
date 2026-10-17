resource "ankra_cost_autopilot_override" "production_freeze" {
  cluster_id = "11111111-1111-1111-1111-111111111111"
  tier       = "hands-off"
  reason     = "Quarter-end change freeze"
  expires_at = "2027-01-05T00:00:00Z"
}
