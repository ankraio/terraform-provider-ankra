resource "ankra_application_deployment" "production" {
  application_id = ankra_application.web.id
  cluster_id     = "9d2f6a3b-7c1e-4f0a-b5d8-2e4c6a8b0f13"
  namespace      = "web"
  deploy_mode    = "high_availability"

  inputs = {
    ingress_host = "web.acme.dev"
  }
}
