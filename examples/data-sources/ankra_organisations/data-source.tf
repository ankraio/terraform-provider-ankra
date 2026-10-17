data "ankra_organisations" "all" {}

output "organisation_names" {
  value = [for organisation in data.ankra_organisations.all.organisations : organisation.name]
}
