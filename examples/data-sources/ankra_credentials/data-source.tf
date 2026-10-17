data "ankra_credentials" "hetzner" {
  credential_provider = "hetzner"
}

output "hetzner_credential_names" {
  value = data.ankra_credentials.hetzner.credentials[*].name
}
