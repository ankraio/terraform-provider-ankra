resource "ankra_alert_ingest_credential" "alertmanager" {
  name       = "prod-alertmanager"
  cluster_id = ankra_cluster.prod.id
  scope      = "mixed"
}

output "alertmanager_webhook_path" {
  value = ankra_alert_ingest_credential.alertmanager.webhook_path
}
