data "ankra_applications" "web" {
  search = "web"
}

output "application_ids" {
  value = [for application in data.ankra_applications.web.applications : application.id]
}
