data "ankra_alert_routes" "all" {}

output "route_count" {
  value = length(data.ankra_alert_routes.all.routes)
}
