data "ankra_cluster" "prod" {
  name = "prod"
}

output "prod_kind" {
  value = data.ankra_cluster.prod.kind
}
