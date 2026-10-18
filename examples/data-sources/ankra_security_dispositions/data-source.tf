data "ankra_security_dispositions" "accepted_risks" {
  statuses    = ["active", "expiring"]
  disposition = "accepted_risk"
}

output "accepted_risk_reasons" {
  value = { for policy in data.ankra_security_dispositions.accepted_risks.dispositions : policy.id => policy.reason }
}
