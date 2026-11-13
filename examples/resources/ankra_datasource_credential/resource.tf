# Terraform 1.11 or later: the password is write-only and never stored in
# state. Bump secret_wo_version to store a new one.
resource "ankra_datasource_credential" "mimir" {
  name              = "mimir-reader"
  auth_scheme       = "basic"
  username          = "reader"
  password_wo       = var.mimir_password
  secret_wo_version = 1
}

variable "mimir_password" {
  type      = string
  sensitive = true
  ephemeral = true
}
