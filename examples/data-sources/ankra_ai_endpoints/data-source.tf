data "ankra_ai_endpoints" "all" {}

output "endpoint_ids" {
  value = { for endpoint in data.ankra_ai_endpoints.all.endpoints : endpoint.name => endpoint.id }
}
