resource "ankra_application" "web" {
  name                     = "web"
  app_repo_credential_name = "github-main"
  app_repo_owner           = "acme"
  app_repo_name            = "web"
  app_repo_branch          = "main"

  # Publish to the organisation's own Harbor instead of Ankra's registry.
  image_registry = {
    url                   = "oci://harbor.acme.dev/apps"
    credential_name       = "harbor-push"
    admin_credential_name = "harbor-admin"
  }
}
