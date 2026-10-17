data "ankra_alert_ingest_credentials" "all" {}

# Alertmanager webhook paths, by credential name. Append each to the Ankra API
# URL; the credential's token is only shown when it is created.
output "alertmanager_webhook_paths" {
  value = { for credential in data.ankra_alert_ingest_credentials.all.credentials : credential.name => credential.webhook_path }
}
