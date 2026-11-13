resource "ankra_pipeline_workspace_profiles" "web" {
  repository_id = ankra_pipeline_repository.web.id
  profiles_json = jsonencode({
    default = {
      image       = "ghcr.io/acme/workspace:1.4.0"
      volume_size = "20Gi"
      resources   = { cpu = "2", memory = "4Gi" }
      setup       = ["make deps"]
    }
  })
}
