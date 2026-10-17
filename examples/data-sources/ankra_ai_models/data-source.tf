data "ankra_ai_models" "all" {}

output "enabled_model_keys" {
  value = [for model in data.ankra_ai_models.all.models : model.key if model.is_enabled]
}
