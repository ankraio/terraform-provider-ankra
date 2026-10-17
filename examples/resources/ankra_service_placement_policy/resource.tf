resource "ankra_service_placement_policy" "prod" {
  cluster_id    = "9f4c2d1e-5a6b-4c7d-8e9f-0a1b2c3d4e5f"
  region        = "eu-north-1"
  data_boundary = "eu"
}
