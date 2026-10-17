data "ankra_morpheus_networks" "all" {
  credential_id = ankra_morpheus_credential.main.id
}

output "morpheus_networks" {
  value = { for network in data.ankra_morpheus_networks.all.networks : network.name => network.id }
}
