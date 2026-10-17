data "ankra_service_packages" "all" {}

resource "ankra_service_placement_policy" "prod" {
  cluster_id    = "9f4c2d1e-5a6b-4c7d-8e9f-0a1b2c3d4e5f"
  region        = "eu-north-1"
  data_boundary = "eu"
}

resource "ankra_service_consumer" "orders" {
  application_id = "3c2b1a0f-9e8d-4c7b-a6f5-e4d3c2b1a0f9"
  cluster_id     = ankra_service_placement_policy.prod.cluster_id
  namespace      = "orders"
}

resource "ankra_managed_service" "orders_db" {
  package_version_id = one([for package in data.ankra_service_packages.all.packages : package.id if package.name == "postgres" && package.version == "1.0.0"])
  name               = "orders-db"
  cluster_id         = ankra_service_placement_policy.prod.cluster_id
  region             = ankra_service_placement_policy.prod.region
  data_boundary      = ankra_service_placement_policy.prod.data_boundary
  consumer_ids       = [ankra_service_consumer.orders.id]

  # Retiring the service deletes its data. Destroy (and replacement) refuse
  # until this is set to true.
  allow_data_loss_on_destroy = false
}
