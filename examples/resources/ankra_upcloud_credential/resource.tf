resource "ankra_upcloud_credential" "main" {
  name      = "upcloud-main"
  api_token = var.upcloud_token
}

variable "upcloud_token" {
  type      = string
  sensitive = true
}
