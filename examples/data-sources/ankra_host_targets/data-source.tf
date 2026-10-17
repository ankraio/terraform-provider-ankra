data "ankra_host_targets" "edge" {
  environment = "edge"
}

output "online_hosts" {
  value = [for target in data.ankra_host_targets.edge.host_targets : target.name if target.status == "online"]
}
