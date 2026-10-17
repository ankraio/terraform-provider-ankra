resource "ankra_ssh_key_credential" "laptop" {
  name           = "laptop"
  ssh_public_key = file("~/.ssh/id_ed25519.pub")
}

# Or let the platform generate the keypair; the private key is returned once.
resource "ankra_ssh_key_credential" "generated" {
  name     = "generated"
  generate = true
}

output "generated_private_key" {
  value     = ankra_ssh_key_credential.generated.private_key
  sensitive = true
}
