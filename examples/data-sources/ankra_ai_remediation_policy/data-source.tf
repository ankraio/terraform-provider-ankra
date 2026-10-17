data "ankra_ai_remediation_policy" "this" {}

output "ai_remediation_autonomy" {
  value = data.ankra_ai_remediation_policy.this.enabled ? data.ankra_ai_remediation_policy.this.autonomy_level : "off"
}
