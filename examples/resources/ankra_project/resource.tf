# Projects belong to the organisation and need no provider project_id. Once
# the organisation has a second project, resources that are scoped to a
# project (and most others) do: set the provider's project_id, for example to
# "default".
provider "ankra" {
  project_id = "default"
}

resource "ankra_project" "platform" {
  name        = "Platform team"
  slug        = "platform"
  description = "Shared infrastructure clusters"
}

# Pin a CI token to the new project.
resource "ankra_service_token" "platform_ci" {
  name       = "platform-ci"
  role_slug  = "member"
  project_id = ankra_project.platform.id
}
