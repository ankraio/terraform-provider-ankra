# Acknowledge a finding on the one workload it was seen in.
resource "ankra_security_disposition" "frontend_net" {
  occurrence_id = "8f0c5a1e-2d4b-4c7e-9a31-5b6d7e8f9a0b"
  scope         = "workload"
  disposition   = "acknowledged"
  reason        = "Not reachable from outside the cluster"
}

# Accept the risk of a finding across every cluster running the add-on.
# An accepted risk needs a review deadline within a year.
resource "ankra_security_disposition" "ingress_cve" {
  occurrence_id             = "3c2b1a09-8f7e-4d6c-b5a4-938271605f4e"
  disposition               = "accepted_risk"
  reason                    = "Mitigated by the WAF rule set"
  expires_at                = "2027-03-31"
  expire_when_fix_available = true
  revoke_reason             = "WAF rule removed"
}
