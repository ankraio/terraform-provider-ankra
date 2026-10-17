resource "ankra_ai_openai_compatible" "legacy" {
  base_url = "https://llm.example.com/v1"
  model    = "gpt-4o"
  api_key  = var.llm_api_key
}

variable "llm_api_key" {
  type      = string
  sensitive = true
}
