data "ankra_morpheus_groups" "all" {
  credential_id = ankra_morpheus_credential.main.id
}

output "morpheus_groups" {
  value = { for group in data.ankra_morpheus_groups.all.groups : group.name => group.id }
}
