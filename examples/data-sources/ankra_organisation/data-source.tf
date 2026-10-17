data "ankra_organisation" "current" {}

output "admins" {
  value = [for member in data.ankra_organisation.current.members : member.email if member.role == "admin"]
}
