resource "ankra_scaleway_cluster" "example" {
  name                  = "scaleway-dev"
  credential_id         = "61262ddf-72db-4745-9a77-2c19961cf2d5"
  region                = "fr-par"
  zone                  = "fr-par-1"
  ssh_key_credential_id = "0f0f4d7e-2f0e-4f5a-9d6b-8c1a2b3c4d5e"

  control_plane_count = 1
  control_plane_type  = "DEV1-M"
  worker_count        = 2
  worker_type         = "DEV1-M"

  gateway_type        = "VPC-GW-S"
  gateway_allowed_ips = ["203.0.113.0/24"]

  # `retain` leaves the Scaleway resources in place when the cluster is
  # deprovisioned; `delete` releases them.
  retention_policy = "retain"
}

output "scaleway_cluster_id" {
  value = ankra_scaleway_cluster.example.cluster_id
}
