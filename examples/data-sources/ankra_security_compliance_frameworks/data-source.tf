data "ankra_security_compliance_frameworks" "all" {}

output "available_frameworks" {
  value = [for framework in data.ankra_security_compliance_frameworks.all.frameworks : framework.key]
}
