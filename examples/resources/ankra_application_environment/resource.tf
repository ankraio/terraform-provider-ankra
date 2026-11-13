resource "ankra_application_environment" "production" {
  application_id = ankra_application.web.id
  environment_id = "8f1d2c3b-4a5e-4f60-9b7a-1c2d3e4f5a6b"
  name           = "production"
  level          = "you_run_it"

  operating_expectations = {
    availability_percent = 99.9
    data_region          = "eu"
    recovery_minutes     = 60
    data_loss_minutes    = 15
    retention_days       = 30
    monthly_budget_cents = 50000
  }
}
