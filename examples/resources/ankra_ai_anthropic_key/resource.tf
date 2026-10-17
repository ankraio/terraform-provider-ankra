resource "ankra_ai_anthropic_key" "own" {
  api_key = var.anthropic_api_key
}

variable "anthropic_api_key" {
  type      = string
  sensitive = true
}
