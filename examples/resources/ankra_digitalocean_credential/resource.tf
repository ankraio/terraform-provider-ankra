resource "ankra_digitalocean_credential" "main" {
  name      = "digitalocean-main"
  api_token = var.digitalocean_token
}

variable "digitalocean_token" {
  type      = string
  sensitive = true
}
