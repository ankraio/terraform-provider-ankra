data "ankra_proxmox_hosts" "lab" {
  credential_id = ankra_proxmox_credential.lab.id
}

output "online_hosts" {
  value = [for host in data.ankra_proxmox_hosts.lab.hosts : host.node if host.status == "online"]
}
