resource "ankra_aws_cluster" "example" {
  name                  = "aws-dev"
  credential_id         = "61262ddf-72db-4745-9a77-2c19961cf2d5"
  ssh_key_credential_id = "0f0f4d7e-2f0e-4f5a-9d6b-8c1a2b3c4d5e"
  region                = "eu-central-1"
  bastion_allowed_ips   = ["203.0.113.0/24"]

  # Instance types available in the region:
  # `ankra cluster aws instance-types --credential-id <id> --region <region>`.
  control_plane_count = 1
  control_plane_type  = "t3.medium"
  worker_count        = 2
  worker_type         = "t3.medium"

  # Destroying a cluster that still holds persistent volumes needs consent.
  accept_volume_data_loss = false

  timeouts {
    create = "60m"
  }
}
