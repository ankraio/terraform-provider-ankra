data "ankra_service_tokens" "all" {}

# Service accounts holding no role act with read-only access.
output "accounts_without_role" {
  value = distinct([for token in data.ankra_service_tokens.all.tokens : token.account_name if token.role_slug == ""])
}
