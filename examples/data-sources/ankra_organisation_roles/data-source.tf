data "ankra_organisation_roles" "all" {}

output "custom_role_slugs" {
  value = [for role in data.ankra_organisation_roles.all.roles : role.slug if !role.builtin]
}
