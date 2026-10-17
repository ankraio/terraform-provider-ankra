data "ankra_proxmox_bridges" "pve1" {
  credential_id = ankra_proxmox_credential.lab.id
  node          = "pve1"
}

output "bridges" {
  value = data.ankra_proxmox_bridges.pve1.bridges[*].name
}
