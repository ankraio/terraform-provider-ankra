resource "ankra_pipeline_definition" "platform" {
  repository_id = ankra_pipeline_repository.platform.id
  spec_yaml     = file("${path.module}/pipeline.yaml")
}
