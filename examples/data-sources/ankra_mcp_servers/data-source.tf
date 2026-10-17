data "ankra_mcp_servers" "all" {}

output "enabled_servers" {
  value = [for server in data.ankra_mcp_servers.all.servers : server.name if server.enabled]
}
