data "ankra_cluster_metrics_source" "prod" {
  cluster_id = "0b6f1d2e-1111-4222-8333-944445555666"
}

output "metrics_backend" {
  value = data.ankra_cluster_metrics_source.prod.metrics.endpoint
}
