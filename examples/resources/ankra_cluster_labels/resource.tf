resource "ankra_cluster_labels" "prod" {
  cluster_id = "0b6f1d2e-1111-4222-8333-944445555666"
  labels = {
    team   = "payments"
    region = "eu-north"
  }
}
