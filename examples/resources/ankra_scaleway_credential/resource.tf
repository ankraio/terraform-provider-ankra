resource "ankra_scaleway_credential" "main" {
  name       = "scaleway-main"
  access_key = "SCWXXXXXXXXXXXXXXXXX"
  secret_key = var.scaleway_secret_key
  project_id = "11111111-2222-3333-4444-555555555555"
}

variable "scaleway_secret_key" {
  type      = string
  sensitive = true
}
