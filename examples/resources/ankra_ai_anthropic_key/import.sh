# The Anthropic key is a singleton. The key cannot be read back, so the first
# apply after an import stores the configured key again.
terraform import ankra_ai_anthropic_key.own ai_anthropic_key
