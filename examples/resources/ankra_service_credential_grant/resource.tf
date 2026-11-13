resource "ankra_service_credential_grant" "billing_database" {
  family_id      = ankra_service_credential.billing_database.id
  value_id       = ankra_service_credential.billing_database.active_value_id
  package_digest = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
  input_name     = "DATABASE_URL"
  mode           = "hosted"
}
