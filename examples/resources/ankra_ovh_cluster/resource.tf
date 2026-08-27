resource "ankra_ovh_cluster" "example" {
  name                  = "ovh-dev"
  credential_id         = "61262ddf-72db-4745-9a77-2c19961cf2d5"
  region                = "GRA11"
  ssh_key_credential_id = "0f0f4d7e-2f0e-4f5a-9d6b-8c1a2b3c4d5e"

  control_plane_count     = 1
  control_plane_flavor_id = "b3-16"
  worker_count            = 2
  worker_flavor_id        = "b3-16"
  gateway_flavor_id       = "c3-4"

  # 3-AZ regions only: spread the cluster across availability zones.
  availability_zones = ["eu-west-par-a", "eu-west-par-b", "eu-west-par-c"]
}

output "ovh_cluster_id" {
  value = ankra_ovh_cluster.example.cluster_id
}
