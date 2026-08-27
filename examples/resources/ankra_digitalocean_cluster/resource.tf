resource "ankra_digitalocean_cluster" "example" {
  name                  = "digitalocean-dev"
  credential_id         = "61262ddf-72db-4745-9a77-2c19961cf2d5"
  region                = "fra1"
  ssh_key_credential_id = "0f0f4d7e-2f0e-4f5a-9d6b-8c1a2b3c4d5e"

  # Droplet sizes must be available in the chosen region — check
  # `ankra cluster digitalocean sizes --credential-id <id> --region <region>`.
  control_plane_count = 1
  control_plane_size  = "s-2vcpu-4gb"
  worker_count        = 2
  worker_size         = "s-2vcpu-4gb"
  bastion_size        = "s-1vcpu-1gb"

  # Provisioning is asynchronous; the apply blocks until the platform reports
  # the cluster running, so anything downstream gets a live cluster.
  timeouts {
    create = "60m"
  }
}

output "digitalocean_cluster_id" {
  value = ankra_digitalocean_cluster.example.cluster_id
}
