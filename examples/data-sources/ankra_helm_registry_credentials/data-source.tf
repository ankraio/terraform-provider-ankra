data "ankra_helm_registry_credentials" "all" {}

output "credential_names" {
  value = [for credential in data.ankra_helm_registry_credentials.all.credentials : credential.name]
}
