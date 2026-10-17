data "ankra_services" "serving" {
  state = "serving"
}

output "serving_services" {
  value = [for service in data.ankra_services.serving.services : service.name]
}
