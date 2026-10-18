data "ankra_morpheus_layouts" "all" {
  credential_id = ankra_morpheus_credential.main.id
}

output "morpheus_layouts" {
  value = { for layout in data.ankra_morpheus_layouts.all.layouts : layout.name => layout.id }
}
