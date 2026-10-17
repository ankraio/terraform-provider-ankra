data "ankra_node_groups" "example" {
  cluster_id = "ef8cf93e-1d97-4494-9de4-12ced709ffde"
}

output "node_group_sizes" {
  value = { for group in data.ankra_node_groups.example.node_groups : group.name => group.count }
}
