data "ankra_clusters" "all" {}

output "cluster_names" {
  value = [for cluster in data.ankra_clusters.all.clusters : cluster.name]
}
