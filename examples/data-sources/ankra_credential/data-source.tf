# Reference a credential created outside Terraform by name.
data "ankra_credential" "hetzner" {
  credential_provider = "hetzner"
  name                = "hetzner-main"
}

data "ankra_credential" "ssh_key" {
  credential_provider = "ssh-key"
  name                = "laptop"
}

output "hetzner_credential_id" {
  value = data.ankra_credential.hetzner.id
}
