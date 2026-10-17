data "ankra_clusters" "all" {}

output "cluster_names" {
  value = [for cluster in data.ankra_clusters.all.clusters : cluster.name]
}

# The clusters of another project, by slug or ID, without changing the
# provider's own project.
data "ankra_clusters" "staging" {
  project_id = "staging"
}
