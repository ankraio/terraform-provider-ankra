resource "ankra_hetzner_credential" "main" {
  name      = "hetzner-main"
  api_token = var.hcloud_token
}

variable "hcloud_token" {
  type      = string
  sensitive = true
}
