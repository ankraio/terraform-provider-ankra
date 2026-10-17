data "ankra_cluster" "prod" {
  name = "prod"
}

resource "ankra_cluster_access_grant" "oncall" {
  cluster_id = data.ankra_cluster.prod.id
  user_email = "oncall@example.com"
  role       = "edit"
  expires_at = "2026-12-31T23:00:00Z"
  reason     = "Migration window"
}

resource "ankra_cluster_access_grant" "team_view" {
  cluster_id = data.ankra_cluster.prod.id
  user_email = "developer@example.com"
  scope      = "namespace"
  namespace  = "payments"
  role       = "view"
}
