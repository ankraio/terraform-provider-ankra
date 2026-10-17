resource "ankra_ankracloud_cluster" "example" {
  name = "cloud-dev"

  # Without credential_id the organisation's built-in Ankra Cloud credential
  # is used. Zones and plans: `ankra cluster ankracloud zones|plans`.
  zone               = "se-sto1"
  bastion_plan       = "small"
  control_plane_plan = "medium"
  worker_plan        = "medium"
  worker_count       = 2
}
