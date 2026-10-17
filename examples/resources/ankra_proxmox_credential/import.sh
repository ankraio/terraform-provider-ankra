# Credentials are imported by id. The platform never returns a credential's secrets,
# so the first apply after an import adopts the configured values without replacing it.
terraform import ankra_proxmox_credential.lab 00000000-0000-4000-8000-000000000000
