data "ankra_projects" "all" {}

output "project_ids_by_slug" {
  value = { for project in data.ankra_projects.all.projects : project.slug => project.id }
}
