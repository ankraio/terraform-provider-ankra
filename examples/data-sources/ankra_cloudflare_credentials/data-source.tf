data "ankra_cloudflare_credentials" "all" {}

output "needs_reconnect" {
  value = [for credential in data.ankra_cloudflare_credentials.all.credentials : credential.name if credential.needs_reconnect]
}
