resource "ankra_hetzner_cluster" "example" {
  name          = "hetzner-dev"
  credential_id = "61262ddf-72db-4745-9a77-2c19961cf2d5"
  location      = "fsn1"

  # Server types must be available in the chosen location — check
  # `ankra cluster hetzner server-types --credential-id <id> --location <loc>`.
  control_plane_count       = 1
  control_plane_server_type = "cpx22"
  worker_count              = 1
  worker_server_type        = "cpx22"
  bastion_server_type       = "cpx12"
}

output "hetzner_cluster_id" {
  value = ankra_hetzner_cluster.example.cluster_id
}
