data "ankra_registry_projects" "all" {}

output "registry_projects" {
  value = [for project in data.ankra_registry_projects.all.projects : project.name]
}
