resource "ankra_ankracloud_credential" "main" {
  name      = "ankracloud-main"
  api_token = var.ankracloud_token
}

variable "ankracloud_token" {
  type      = string
  sensitive = true
}
