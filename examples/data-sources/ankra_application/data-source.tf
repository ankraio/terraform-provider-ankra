data "ankra_application" "web" {
  id = "5b0c4a9e-2f43-4b8e-9a51-1f0d8e7c2a10"
}

output "web_is_running" {
  value = data.ankra_application.web.deployment_state == "running"
}
