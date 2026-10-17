data "ankra_organisation_variables" "all" {}

output "variable_names" {
  value = [for variable in data.ankra_organisation_variables.all.variables : variable.name]
}
