resource "ankra_security_report_schedule" "production" {
  cluster_id = ankra_cluster.production.id
  frequency  = "weekly"
  recipients = ["security@example.com", "platform-team@example.com"]
}
