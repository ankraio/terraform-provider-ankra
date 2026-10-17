# A repository with no application, such as a library or a values repository.
resource "ankra_pipeline_repository" "platform" {
  provider_name   = "github"
  owner           = "acme"
  name            = "platform"
  credential_name = "github-main"
  default_branch  = "main"
}
