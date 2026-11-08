data "ankra_project" "default" {
  project = "default"
}

output "default_project_id" {
  value = data.ankra_project.default.id
}
