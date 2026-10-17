data "ankra_proxmox_templates" "pve1" {
  credential_id = ankra_proxmox_credential.lab.id
  node          = "pve1"
}

output "templates" {
  value = data.ankra_proxmox_templates.pve1.templates[*].name
}
