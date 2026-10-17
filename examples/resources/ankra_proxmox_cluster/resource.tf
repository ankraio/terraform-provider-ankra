resource "ankra_proxmox_cluster" "example" {
  name                  = "proxmox-lab"
  credential_id         = "61262ddf-72db-4745-9a77-2c19961cf2d5"
  ssh_key_credential_id = "0f0f4d7e-2f0e-4f5a-9d6b-8c1a2b3c4d5e"

  # Hosts, bridges, storages and templates:
  # `ankra cluster proxmox hosts|bridges|storages|templates --credential-id <id>`.
  node    = "pve1"
  bridge  = "vmbr0"
  storage = "local-lvm"

  control_plane_count         = 3
  control_plane_instance_type = "px-medium"
  worker_count                = 2
  worker_instance_type        = "px-large"
}
