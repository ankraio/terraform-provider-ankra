resource "ankra_service_token" "ci" {
  name      = "ci-deployer"
  role_slug = "member"
  label     = "pipelines"
}
