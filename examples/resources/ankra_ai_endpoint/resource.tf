resource "ankra_ai_endpoint" "litellm" {
  name     = "litellm"
  base_url = "https://litellm.example.com/v1"
  api_key  = var.litellm_api_key
}

variable "litellm_api_key" {
  type      = string
  sensitive = true
}
