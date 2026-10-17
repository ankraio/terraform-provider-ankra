resource "ankra_ai_anthropic_key" "own" {
  api_key = var.anthropic_api_key
}

# Activate the stored key for the organisation.
resource "ankra_ai_provider" "active" {
  name       = "anthropic"
  depends_on = [ankra_ai_anthropic_key.own]
}

variable "anthropic_api_key" {
  type      = string
  sensitive = true
}
