resource "ankra_cloudflare_credential" "main" {
  name      = "cloudflare-main"
  api_token = var.cloudflare_api_token
}

variable "cloudflare_api_token" {
  type      = string
  sensitive = true
}
