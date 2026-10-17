resource "ankra_morpheus_credential" "main" {
  name         = "morpheus-main"
  api_url      = "https://morpheus.example"
  access_token = var.morpheus_access_token
}

variable "morpheus_access_token" {
  type      = string
  sensitive = true
}
