# The platform refuses a deploy until the application's setup pull request is
# merged and its first build has published an image. Create retries for up to
# the create timeout; with a new application, merge the setup pull request in
# that window or apply the deployment in a second run.
resource "ankra_application_deployment" "production" {
  application_id = ankra_application.web.id
  cluster_id     = "9d2f6a3b-7c1e-4f0a-b5d8-2e4c6a8b0f13"
  namespace      = "web"
  deploy_mode    = "high_availability"

  inputs = {
    ingress_host = "web.acme.dev"
  }

  timeouts {
    create = "45m"
  }
}
