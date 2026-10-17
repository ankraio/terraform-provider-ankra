data "ankra_alert_destinations" "all" {}

output "destination_ids" {
  value = { for destination in data.ankra_alert_destinations.all.destinations : destination.name => destination.id }
}
