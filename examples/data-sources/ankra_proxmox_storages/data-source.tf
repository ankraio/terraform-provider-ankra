data "ankra_proxmox_storages" "pve1" {
  credential_id = ankra_proxmox_credential.lab.id
  node          = "pve1"
}

locals {
  image_storages = [for storage in data.ankra_proxmox_storages.pve1.storages : storage.storage if contains(storage.content, "images")]
}
