resource "ankra_ai_endpoint" "litellm" {
  name     = "litellm"
  base_url = "https://litellm.example.com/v1"
  api_key  = var.litellm_api_key
}

resource "ankra_ai_model" "gpt" {
  key                   = "gpt-4o"
  display_name          = "GPT-4o"
  model_id              = "gpt-4o"
  endpoint_id           = ankra_ai_endpoint.litellm.id
  context_window_tokens = 128000
  supports_images       = true
}

variable "litellm_api_key" {
  type      = string
  sensitive = true
}
