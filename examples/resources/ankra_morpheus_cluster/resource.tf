resource "ankra_morpheus_cluster" "example" {
  name                  = "morpheus-dc1"
  credential_id         = "61262ddf-72db-4745-9a77-2c19961cf2d5"
  ssh_key_credential_id = "0f0f4d7e-2f0e-4f5a-9d6b-8c1a2b3c4d5e"

  # Numeric ids from `ankra cluster morpheus groups|clouds|networks|layouts|plans`.
  group_id              = 1
  cloud_id              = 2
  network_id            = 14
  layout_id             = 207
  bastion_plan_id       = 101
  control_plane_plan_id = 102
  worker_plan_id        = 103
  worker_count          = 2
}
