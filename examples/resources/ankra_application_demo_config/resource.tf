resource "ankra_application_demo_config" "web" {
  application_id      = ankra_application.web.id
  database            = true
  database_extensions = ["pgcrypto"]
  migrate_command     = "npm run migrate"
  protected           = true

  env = [
    { name = "MODE", value = "demo" },
    { name = "STRIPE_KEY", value = "$${SECRET_SLOT:0f8f1c1e-9d0b-4c55-8a39-3c1e3b4f7a21}", secret = true },
  ]
}

output "demo_password" {
  value     = ankra_application_demo_config.web.generated_password
  sensitive = true
}
