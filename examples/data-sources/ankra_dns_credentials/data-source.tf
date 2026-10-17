data "ankra_dns_credentials" "all" {}

output "dns_credential_names" {
  value = data.ankra_dns_credentials.all.credentials[*].name
}
