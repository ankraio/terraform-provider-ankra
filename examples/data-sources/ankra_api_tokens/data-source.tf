data "ankra_api_tokens" "mine" {}

output "live_token_names" {
  value = [for token in data.ankra_api_tokens.mine.tokens : token.name if !token.revoked]
}
