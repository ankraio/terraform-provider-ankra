data "ankra_morpheus_clouds" "all" {
  credential_id = ankra_morpheus_credential.main.id
}

output "morpheus_clouds" {
  value = { for cloud in data.ankra_morpheus_clouds.all.clouds : cloud.name => cloud.id }
}
