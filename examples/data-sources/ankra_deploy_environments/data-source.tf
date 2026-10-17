data "ankra_deploy_environments" "all" {}

output "environment_names" {
  value = data.ankra_deploy_environments.all.environments[*].name
}
