resource "ankra_upcloud_cluster" "example" {
  name                  = "upcloud-dev"
  credential_id         = "61262ddf-72db-4745-9a77-2c19961cf2d5"
  zone                  = "de-fra1"
  ssh_key_credential_id = "0f0f4d7e-2f0e-4f5a-9d6b-8c1a2b3c4d5e"

  control_plane_count = 1
  control_plane_plan  = "2xCPU-4GB"
  worker_count        = 2
  worker_plan         = "2xCPU-4GB"
  bastion_plan        = "1xCPU-1GB"
}

output "upcloud_cluster_id" {
  value = ankra_upcloud_cluster.example.cluster_id
}
